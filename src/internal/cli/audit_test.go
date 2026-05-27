package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/shared/dto"
)

func TestAuditListRendersTableAndForwardsFilters(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		actor := "alice"
		traceID := "trace-1"
		out := dto.ListAuditResponse{
			Items: []dto.AuditLogEntry{{
				ID:        42,
				Time:      time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC),
				Source:    "user",
				Actor:     &actor,
				Action:    "create_app",
				Outcome:   "success",
				TraceID:   &traceID,
			}},
			PageSize: 50,
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"audit", "list",
		"--team", "acme",
		"--host", srv.URL,
		"--token", "test-token",
		"--action", "create_",
		"--actor", "alice",
		"--page-size", "25",
		"--output", "table",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !strings.Contains(stdout.String(), "create_app") {
		t.Fatalf("table output missing action row: %s", stdout.String())
	}
	if !strings.Contains(capturedURL, "/v1/teams/acme/audit") {
		t.Fatalf("URL mismatch: %s", capturedURL)
	}
	if !strings.Contains(capturedURL, "action=create_") {
		t.Fatalf("action filter not forwarded: %s", capturedURL)
	}
	if !strings.Contains(capturedURL, "actor=alice") {
		t.Fatalf("actor filter not forwarded: %s", capturedURL)
	}
	if !strings.Contains(capturedURL, "page_size=25") {
		t.Fatalf("page_size not forwarded: %s", capturedURL)
	}
}

func TestAuditListJSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(dto.ListAuditResponse{
			Items:    []dto.AuditLogEntry{},
			PageSize: 50,
		})
	}))
	t.Cleanup(srv.Close)

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"audit", "list",
		"--team", "acme",
		"--host", srv.URL,
		"--token", "test-token",
		"--output", "json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var got dto.ListAuditResponse
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json output not parseable: %v\n%s", err, stdout.String())
	}
	if got.PageSize != 50 {
		t.Fatalf("page_size lost in roundtrip: %d", got.PageSize)
	}
}

func TestAuditListSinceAcceptsDuration(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		_ = json.NewEncoder(w).Encode(dto.ListAuditResponse{Items: []dto.AuditLogEntry{}, PageSize: 50})
	}))
	t.Cleanup(srv.Close)

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"audit", "list",
		"--team", "acme",
		"--host", srv.URL,
		"--token", "test-token",
		"--since", "24h",
		"--output", "json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(capturedURL, "since=") {
		t.Fatalf("since not normalised: %s", capturedURL)
	}
	// Should be RFC3339 not '24h'.
	if strings.Contains(capturedURL, "since=24h") {
		t.Fatalf("since=24h not normalised: %s", capturedURL)
	}
}

func TestAuditGetReturnsJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(dto.AuditLogEntry{
			ID:      77,
			Action:  "delete_app",
			Outcome: "success",
		})
	}))
	t.Cleanup(srv.Close)

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"audit", "get", "77",
		"--team", "acme",
		"--host", srv.URL,
		"--token", "test-token",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "delete_app") {
		t.Fatalf("output missing action: %s", stdout.String())
	}
}

func TestAuditListBadOutputFmt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(dto.ListAuditResponse{})
	}))
	t.Cleanup(srv.Close)

	cmd := NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"audit", "list",
		"--team", "acme",
		"--host", srv.URL,
		"--token", "test-token",
		"--output", "weird",
	})
	err := cmd.Execute()
	if err == nil || !strings.Contains(fmt.Sprint(err), "unsupported output format") {
		t.Fatalf("expected unsupported output format error, got %v", err)
	}
}
