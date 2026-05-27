package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/shared/authconfig"
	"github.com/winshare/zeroops/internal/shared/dto"
)

func TestIncidentsListRendersTable(t *testing.T) {
	opened := time.Date(2026, 5, 15, 12, 30, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/incidents") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("status") != "open" {
			http.Error(w, "bad status", http.StatusBadRequest)
			return
		}
		payload := dto.ListIncidentsResponse{
			Items: []dto.Incident{
				{ID: "inc-1", TeamID: "team-1", SubjectType: "deploy_run", SubjectID: "run-1",
					Kind: "failed_permanently", Severity: "medium", OpenedAt: opened},
			},
			PageSize: 50,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)

	cfg, _ := authconfig.Load()
	t.Cleanup(func() { _ = authconfig.Save(cfg) })

	root := NewRootCommand()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"incidents", "list", "--team", "acme", "--host", srv.URL, "--token", "fake-token", "--status", "open", "--output", "table"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "inc-1") || !strings.Contains(got, "medium") {
		t.Fatalf("table missing fields:\n%s", got)
	}
}

func TestIncidentsCloseSendsNote(t *testing.T) {
	var (
		gotBody []byte
		gotPath string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = body
		closed := time.Now().UTC()
		closer := "user-1"
		payload := dto.Incident{
			ID:       "inc-1",
			TeamID:   "team-1",
			Kind:     "failed_permanently",
			Severity: "medium",
			OpenedAt: closed.Add(-time.Hour),
			ClosedAt: &closed,
			ClosedBy: &closer,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)

	root := NewRootCommand()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"incidents", "close", "inc-1", "--team", "acme", "--host", srv.URL, "--token", "fake-token", "--note", "root cause: outage", "--output", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/incidents/inc-1:close") {
		t.Fatalf("path = %s, want /incidents/inc-1:close", gotPath)
	}
	if !strings.Contains(string(gotBody), "root cause: outage") {
		t.Fatalf("body did not carry note: %s", gotBody)
	}
	var parsed dto.Incident
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if parsed.ID != "inc-1" {
		t.Fatalf("response id = %s", parsed.ID)
	}
}
