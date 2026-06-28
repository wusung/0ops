package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// stubExportService implements both the query and export surfaces so the router
// registers the export route.
type stubExportService struct {
	got audit.ExportRequest
	res audit.ExportResult
	err error
}

func (s *stubExportService) Query(context.Context, audit.QueryFilter) (audit.QueryResult, error) {
	return audit.QueryResult{}, nil
}
func (s *stubExportService) Get(context.Context, string, int64, audit.QueryScope, string) (audit.Row, error) {
	return audit.Row{}, audit.ErrNotFound
}
func (s *stubExportService) Export(_ context.Context, req audit.ExportRequest) (audit.ExportResult, error) {
	s.got = req
	if s.err != nil {
		return audit.ExportResult{}, s.err
	}
	return s.res, nil
}

func exportTestStore(t *testing.T, scopes []string, role string) (*fakeStore, string) {
	t.Helper()
	store, token := newFakeStore()
	cur := store.tokens["token-1"]
	cur.Scopes = scopes
	store.tokens["token-1"] = cur
	store.token = cur
	store.role = role
	return store, token
}

func getWithToken(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func TestExportAuditHandlerJSONReturnsManifestAndEntries(t *testing.T) {
	store, token := exportTestStore(t, []string{"audit:export"}, "admin")
	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	svc := &stubExportService{res: audit.ExportResult{
		Since: created.Add(-time.Hour), Until: created.Add(time.Hour),
		Rows: []audit.ExportRow{{
			Row:      audit.Row{ID: 1, TeamID: store.team.ID, Action: "create_app", Source: "user", Outcome: "success", CreatedAt: created},
			PrevHash: []byte{0xaa, 0xbb},
			RowHash:  []byte{0xcc, 0xdd},
		}},
		Chains: []audit.ChainHead{{
			PartitionMonth: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			GenesisHash:    []byte{0x01}, TipHash: []byte{0xcc, 0xdd}, RowCount: 1,
		}},
	}}
	srv := newExportTestServer(t, store, svc)

	resp := getWithToken(t, srv+"/v1/teams/"+store.team.Slug+"/audit/export?format=json&since=2026-06-01T00:00:00Z&until=2026-06-30T00:00:00Z", token)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	var env dto.AuditExportEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Manifest.TeamSlug != store.team.Slug || env.Manifest.TeamID != store.team.ID {
		t.Fatalf("manifest team = %q/%q, want %q/%q", env.Manifest.TeamSlug, env.Manifest.TeamID, store.team.Slug, store.team.ID)
	}
	if env.Manifest.RowCount != 1 || len(env.Entries) != 1 {
		t.Fatalf("row_count=%d entries=%d, want 1/1", env.Manifest.RowCount, len(env.Entries))
	}
	if len(env.Manifest.Chains) != 1 || env.Manifest.Chains[0].Month != "2026-06" {
		t.Fatalf("chains = %+v", env.Manifest.Chains)
	}
	if env.Manifest.Chains[0].TipHash != "ccdd" {
		t.Fatalf("tip hash hex = %q, want ccdd", env.Manifest.Chains[0].TipHash)
	}
	if env.Entries[0].RowHash != "ccdd" || env.Entries[0].PrevHash != "aabb" {
		t.Fatalf("entry hashes = %q/%q, want ccdd/aabb", env.Entries[0].RowHash, env.Entries[0].PrevHash)
	}
	if env.Manifest.Generator == "" || env.Manifest.GeneratedAt.IsZero() {
		t.Fatal("manifest provenance not populated")
	}
}

func TestExportAuditHandlerCSVPutsManifestInHeader(t *testing.T) {
	store, token := exportTestStore(t, []string{"audit:export"}, "admin")
	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	svc := &stubExportService{res: audit.ExportResult{
		Since: created, Until: created,
		Rows: []audit.ExportRow{{
			Row:     audit.Row{ID: 7, TeamID: store.team.ID, Action: "delete_app", Source: "user", Outcome: "success", CreatedAt: created},
			RowHash: []byte{0xcc, 0xdd},
		}},
		Chains: []audit.ChainHead{{PartitionMonth: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), GenesisHash: []byte{0x01}, TipHash: []byte{0xcc, 0xdd}, RowCount: 1}},
	}}
	srv := newExportTestServer(t, store, svc)

	resp := getWithToken(t, srv+"/v1/teams/"+store.team.Slug+"/audit/export?format=csv&since=2026-06-01T00:00:00Z", token)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/csv" {
		t.Fatalf("content-type = %q, want text/csv", ct)
	}
	hdr := resp.Header.Get("X-0ops-Audit-Integrity")
	if hdr == "" {
		t.Fatal("missing X-0ops-Audit-Integrity header (hard rule #7)")
	}
	raw, err := base64.StdEncoding.DecodeString(hdr)
	if err != nil {
		t.Fatalf("manifest header not base64: %v", err)
	}
	var manifest dto.IntegritySummary
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("manifest header not json: %v", err)
	}
	if manifest.RowCount != 1 {
		t.Fatalf("manifest row_count = %d, want 1", manifest.RowCount)
	}
	body, _ := io.ReadAll(resp.Body)
	if !containsAll(string(body), "id", "row_hash", "delete_app") {
		t.Fatalf("csv body missing expected columns/data:\n%s", body)
	}
}

func TestExportAuditHandlerRequiresSince(t *testing.T) {
	store, token := exportTestStore(t, []string{"audit:export"}, "admin")
	svc := &stubExportService{}
	srv := newExportTestServer(t, store, svc)

	resp := getWithToken(t, srv+"/v1/teams/"+store.team.Slug+"/audit/export?format=json", token)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for missing since", resp.StatusCode)
	}
}

func TestExportAuditHandlerRangeTooLargeIs422(t *testing.T) {
	store, token := exportTestStore(t, []string{"audit:export"}, "admin")
	svc := &stubExportService{err: audit.ErrExportRangeTooLarge}
	srv := newExportTestServer(t, store, svc)

	resp := getWithToken(t, srv+"/v1/teams/"+store.team.Slug+"/audit/export?format=json&since=2024-01-01T00:00:00Z&until=2026-06-01T00:00:00Z", token)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for range > 13 months", resp.StatusCode)
	}
}

func TestExportAuditHandlerForbidsMissingScope(t *testing.T) {
	store, token := exportTestStore(t, []string{"audit:read"}, "admin") // read but not export
	svc := &stubExportService{}
	srv := newExportTestServer(t, store, svc)

	resp := getWithToken(t, srv+"/v1/teams/"+store.team.Slug+"/audit/export?format=json&since=2026-06-01T00:00:00Z", token)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 without audit:export scope (hard rule #6)", resp.StatusCode)
	}
}

func TestExportAuditHandlerForbidsViewerRole(t *testing.T) {
	store, token := exportTestStore(t, []string{"audit:export"}, "viewer")
	svc := &stubExportService{}
	srv := newExportTestServer(t, store, svc)

	resp := getWithToken(t, srv+"/v1/teams/"+store.team.Slug+"/audit/export?format=json&since=2026-06-01T00:00:00Z", token)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for viewer role", resp.StatusCode)
	}
}

func newExportTestServer(t *testing.T, store routerStore, svc auditQueryService) string {
	t.Helper()
	s := httptest.NewServer(NewRouterWithAudit(store, nil, nil, svc))
	t.Cleanup(s.Close)
	return s.URL
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
