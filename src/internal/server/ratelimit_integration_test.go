package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	ratelimit "github.com/winshare/zeroops/internal/server/middleware/ratelimit"
)

// TestRouterEnforcesPerTokenRateLimit wires NewRouterWithRateLimit with a
// tiny quota table so the test can drain the bucket and observe a 429
// response that includes the spec § 5.1 envelope shape and `Retry-After`
// header.
func TestRouterEnforcesPerTokenRateLimit(t *testing.T) {
	store, token := newFakeStore()
	limiter := ratelimit.New(ratelimit.Config{
		Quotas: map[ratelimit.Plan]ratelimit.PlanQuotas{
			ratelimit.PlanStarter: {
				PerTokenRead:          ratelimit.Quota{PerMinute: 2},
				PerTokenWrite:         ratelimit.Quota{PerMinute: 1},
				PerTokenPreviewCreate: ratelimit.Quota{PerMinute: 1},
				PerTeamWrite:          ratelimit.Quota{PerMinute: 5},
				PerTeamPreviewCreate:  ratelimit.Quota{PerMinute: 5},
			},
			// Fallback when plan unrecognized.
			ratelimit.PlanFree: {
				PerTokenRead:          ratelimit.Quota{PerMinute: 2},
				PerTokenWrite:         ratelimit.Quota{PerMinute: 1},
				PerTokenPreviewCreate: ratelimit.Quota{PerMinute: 1},
				PerTeamWrite:          ratelimit.Quota{PerMinute: 5},
				PerTeamPreviewCreate:  ratelimit.Quota{PerMinute: 5},
			},
		},
	})

	srv := httptest.NewServer(NewRouterWithRateLimit(store, nil, nil, limiter, nil))
	t.Cleanup(srv.Close)

	doListApps := func() *http.Response {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/teams/"+store.team.Slug+"/apps", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	// Per-token read = 2 → first two succeed.
	for i := 0; i < 2; i++ {
		res := doListApps()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("ListApps #%d: status = %d, want 200", i+1, res.StatusCode)
		}
		_ = res.Body.Close()
	}

	// Third call must hit 429.
	res := doListApps()
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("third ListApps: status = %d, want 429", res.StatusCode)
	}
	if got := res.Header.Get("Retry-After"); got == "" {
		t.Fatalf("missing Retry-After header")
	} else if n, _ := strconv.Atoi(got); n < 1 {
		t.Fatalf("Retry-After = %q, want >= 1", got)
	}
	body := readBody(t, res)
	for _, frag := range []string{
		`"code":"rate_limited"`,
		`"scope":"per_token"`,
		`"category":"read"`,
		`"plan":"starter"`,
	} {
		if !strings.Contains(body, frag) {
			t.Fatalf("body missing %q: %s", frag, body)
		}
	}
}

func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	buf := make([]byte, 0, 1024)
	chunk := make([]byte, 512)
	for {
		n, err := res.Body.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if err != nil {
			break
		}
	}
	return string(buf)
}
