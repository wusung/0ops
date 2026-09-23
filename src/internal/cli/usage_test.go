package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wusung/0ops/internal/shared/authconfig"
	"github.com/wusung/0ops/internal/shared/dto"
)

func usageFixture() dto.UsageResponse {
	return dto.UsageResponse{
		From: "2026-09-01",
		To:   "2026-09-22",
		Apps: []dto.AppUsage{{
			AppID: "a1", AppSlug: "web",
			Totals: dto.UsageTotals{
				CPUMillicoreSeconds: 7_200_000,
				MemoryByteSeconds:   "1152921504606846976",
				GPUCountSeconds:     7200,
				PodSeconds:          72000,
				CPUCoreHours:        2,
				MemoryGiBHours:      298.26,
				GPUHours:            2,
				PodHours:            20,
			},
		}},
		Totals: dto.UsageTotals{
			CPUCoreHours: 2, MemoryGiBHours: 298.26, GPUHours: 2, PodHours: 20,
			PodSeconds: 72000,
		},
		BillingDisclaimer: dto.UsageBillingDisclaimer,
	}
}

func usageServer(t *testing.T, capture *http.Request, payload dto.UsageResponse) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*capture = *r
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runUsage(t *testing.T, srvURL string, args ...string) string {
	t.Helper()
	cfg, _ := authconfig.Load()
	t.Cleanup(func() { _ = authconfig.Save(cfg) })

	root := NewRootCommand()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(io.Discard)
	root.SetArgs(append([]string{"usage", "--team", "acme", "--host", srvURL, "--token", "fake-token"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return out.String()
}

func TestUsageRendersHoursTable(t *testing.T) {
	var got http.Request
	srv := usageServer(t, &got, usageFixture())

	out := runUsage(t, srv.URL)

	for _, want := range []string{"web", "CPU CORE-HOURS", "2.00", "298.26", "20.00", "TOTAL"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
	if got.URL.Path != "/v1/teams/acme/usage" {
		t.Errorf("path = %q", got.URL.Path)
	}
}

// Every rendering has to say it is not a bill (spec § 14 rule #10).
func TestUsageTableCarriesBillingDisclaimer(t *testing.T) {
	var got http.Request
	srv := usageServer(t, &got, usageFixture())

	out := runUsage(t, srv.URL)
	if !strings.Contains(out, "not a bill") {
		t.Errorf("output must state this is not a bill:\n%s", out)
	}
}

// A window built partly from inferred end times must say so, or the
// reader will take a conservative number as exact.
func TestUsageFlagsEstimatedWindows(t *testing.T) {
	payload := usageFixture()
	payload.Apps[0].Totals.EstimatedRatio = 0.2
	payload.Totals.EstimatedRatio = 0.2
	var got http.Request
	srv := usageServer(t, &got, payload)

	out := runUsage(t, srv.URL)
	if !strings.Contains(out, "*") || !strings.Contains(out, "inferred") {
		t.Errorf("estimated window not flagged:\n%s", out)
	}
	if !strings.Contains(out, "at least this much") {
		t.Errorf("output must say the estimate is conservative, not approximate:\n%s", out)
	}
}

func TestUsageAppFlagHitsAppEndpoint(t *testing.T) {
	var got http.Request
	srv := usageServer(t, &got, usageFixture())

	runUsage(t, srv.URL, "--app", "web")

	if got.URL.Path != "/v1/teams/acme/apps/web/usage" {
		t.Errorf("path = %q, want the app-scoped endpoint", got.URL.Path)
	}
}

func TestUsageForwardsWindowAndDetail(t *testing.T) {
	var got http.Request
	srv := usageServer(t, &got, usageFixture())

	runUsage(t, srv.URL, "--from", "2026-09-01", "--to", "2026-09-07", "--interval")

	q := got.URL.Query()
	if q.Get("from") != "2026-09-01" || q.Get("to") != "2026-09-07" {
		t.Errorf("window not forwarded: %v", q)
	}
	if q.Get("detail") != "interval" {
		t.Errorf("detail = %q, want interval", q.Get("detail"))
	}
}

func TestUsageJSONOutputKeepsExactMemorySeconds(t *testing.T) {
	var got http.Request
	srv := usageServer(t, &got, usageFixture())

	out := runUsage(t, srv.URL, "--output", "json")

	// The exact figure must survive as a string; a float round-trip
	// would quietly lose the low digits.
	if !strings.Contains(out, `"memory_byte_seconds": "1152921504606846976"`) {
		t.Errorf("exact memory-seconds lost in JSON output:\n%s", out)
	}
}

func TestUsageRendersEmptyWindowExplicitly(t *testing.T) {
	payload := usageFixture()
	payload.Apps = nil
	var got http.Request
	srv := usageServer(t, &got, payload)

	out := runUsage(t, srv.URL)
	if !strings.Contains(out, "no allocation recorded") {
		t.Errorf("empty window should say so rather than print a bare header:\n%s", out)
	}
}

func TestUsageShowsAllocatedAndUsedInSeparateColumns(t *testing.T) {
	payload := usageFixture()
	payload.Apps[0].Observed = &dto.ObservedUsage{
		CPUMillicoresAvg: 42, CPUMillicoresMax: 310,
		MemoryBytesAvg: 100 << 20, MemoryBytesMax: 180 << 20, SampleCount: 288,
	}
	var got http.Request
	srv := usageServer(t, &got, payload)

	out := runUsage(t, srv.URL, "--observed")

	if got.URL.Query().Get("include") != "observed" {
		t.Errorf("include = %q, want observed", got.URL.Query().Get("include"))
	}
	for _, want := range []string{"CPU USED avg/peak", "42m/310m", "100Mi/180Mi", "what they consumed"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// The allocation figure must still be there, in its own column.
	if !strings.Contains(out, "2.00") {
		t.Errorf("allocation column lost:\n%s", out)
	}
}

// An app with no measurements must render a dash, not a zero: those are
// different claims.
func TestUsageRendersUnmeasuredAppAsDash(t *testing.T) {
	payload := usageFixture()
	payload.Apps = append(payload.Apps, dto.AppUsage{AppID: "a2", AppSlug: "quiet"})
	payload.Apps[0].Observed = &dto.ObservedUsage{CPUMillicoresAvg: 10, CPUMillicoresMax: 20, SampleCount: 5}
	var got http.Request
	srv := usageServer(t, &got, payload)

	out := runUsage(t, srv.URL, "--observed")

	if !strings.Contains(out, "quiet") || !strings.Contains(out, "-") {
		t.Errorf("unmeasured app should render a dash:\n%s", out)
	}
}

func TestUsageOmitsUsedColumnsWithoutObservation(t *testing.T) {
	var got http.Request
	srv := usageServer(t, &got, usageFixture())

	out := runUsage(t, srv.URL)

	if strings.Contains(out, "CPU USED") {
		t.Errorf("used columns must not appear when nothing was measured:\n%s", out)
	}
	if got.URL.Query().Get("include") != "" {
		t.Errorf("include forwarded without the flag: %q", got.URL.Query().Get("include"))
	}
}
