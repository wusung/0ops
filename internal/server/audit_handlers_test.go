package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/shared/backendclient"
)

// stubAuditService captures the most recent filter the handler forwarded
// so tests can assert RBAC scope, defaults, and pagination wiring.
type stubAuditService struct {
	got   audit.QueryFilter
	rows  []audit.Row
	gotID int64
	getRow audit.Row
	getErr error
}

func (s *stubAuditService) Query(_ context.Context, f audit.QueryFilter) (audit.QueryResult, error) {
	s.got = f
	return audit.QueryResult{Items: s.rows}, nil
}

func (s *stubAuditService) Get(_ context.Context, _ string, id int64, _ audit.QueryScope, _ string) (audit.Row, error) {
	s.gotID = id
	if s.getErr != nil {
		return audit.Row{}, s.getErr
	}
	return s.getRow, nil
}

func TestListAuditHandlerEnforcesAdminForTeamScope(t *testing.T) {
	store, token := newFakeStore()
	store.tokens["token-1"] = db.CliToken{
		ID:          "token-1",
		OwnerUserID: store.tokens["token-1"].OwnerUserID,
		TeamID:      store.tokens["token-1"].TeamID,
		TokenHash:   store.tokens["token-1"].TokenHash,
		Scopes:      []string{"audit:read"},
	}
	store.token = store.tokens["token-1"]
	store.role = "viewer"

	auditSvc := &stubAuditService{}
	srv := httptest.NewServer(NewRouterWithAudit(store, nil, nil, auditSvc))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	client.NoRetry = true
	_, err := client.ListAudit(context.Background(), store.team.Slug, backendclient.AuditListParams{})
	if err == nil || !strings.Contains(err.Error(), "forbidden_role") {
		t.Fatalf("expected forbidden_role error, got %v", err)
	}
}

func TestListAuditHandlerAllowsViewerSelfScope(t *testing.T) {
	store, token := newFakeStore()
	store.tokens["token-1"] = db.CliToken{
		ID:          "token-1",
		OwnerUserID: "user-1",
		TeamID:      "team-1",
		TokenHash:   store.tokens["token-1"].TokenHash,
		Scopes:      []string{"audit:read"},
	}
	store.token = store.tokens["token-1"]
	store.role = "viewer"

	auditSvc := &stubAuditService{}
	srv := httptest.NewServer(NewRouterWithAudit(store, nil, nil, auditSvc))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	client.NoRetry = true
	_, err := client.ListAudit(context.Background(), store.team.Slug, backendclient.AuditListParams{Actor: "me"})
	if err != nil {
		t.Fatalf("ListAudit() error = %v", err)
	}
	if auditSvc.got.Scope != audit.QueryScopeSelf {
		t.Fatalf("expected QueryScopeSelf, got %q", auditSvc.got.Scope)
	}
	if auditSvc.got.ActorUserID != "user-1" {
		t.Fatalf("expected actor=user-1, got %q", auditSvc.got.ActorUserID)
	}
}

func TestListAuditHandlerReturnsItems(t *testing.T) {
	store, token := newFakeStore()
	store.tokens["token-1"] = db.CliToken{
		ID:          "token-1",
		OwnerUserID: "user-1",
		TeamID:      "team-1",
		TokenHash:   store.tokens["token-1"].TokenHash,
		Scopes:      []string{"audit:read"},
	}
	store.token = store.tokens["token-1"]
	store.role = "admin"

	created := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	actor := "user-1"
	login := "alice"
	auditSvc := &stubAuditService{rows: []audit.Row{{
		ID:          1,
		TeamID:      "team-1",
		ActorUserID: &actor,
		ActorLogin:  &login,
		Source:      "user",
		SubjectType: "app",
		Action:      "create_app",
		Args:        json.RawMessage(`{"slug":"nextdemo"}`),
		Result:      json.RawMessage(`null`),
		TraceID:     "trace-1",
		Outcome:     "success",
		CreatedAt:   created,
	}}}
	srv := httptest.NewServer(NewRouterWithAudit(store, nil, nil, auditSvc))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	out, err := client.ListAudit(context.Background(), store.team.Slug, backendclient.AuditListParams{Action: "create_", PageSize: 25})
	if err != nil {
		t.Fatalf("ListAudit() error = %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(out.Items))
	}
	item := out.Items[0]
	if item.Action != "create_app" {
		t.Fatalf("action mismatch: %q", item.Action)
	}
	if item.Actor == nil || *item.Actor != "alice" {
		t.Fatalf("actor not populated: %#v", item.Actor)
	}
	if item.TraceID == nil || *item.TraceID != "trace-1" {
		t.Fatalf("trace_id not propagated: %#v", item.TraceID)
	}
	if string(item.Args) == "" {
		t.Fatalf("args lost in marshalling")
	}
	if auditSvc.got.Action != "create_" {
		t.Fatalf("action filter not forwarded: %q", auditSvc.got.Action)
	}
	if auditSvc.got.PageSize != 25 {
		t.Fatalf("page_size not forwarded: %d", auditSvc.got.PageSize)
	}
}

func TestGetAuditHandlerReturnsNotFound(t *testing.T) {
	store, token := newFakeStore()
	store.tokens["token-1"] = db.CliToken{
		ID:          "token-1",
		OwnerUserID: "user-1",
		TeamID:      "team-1",
		TokenHash:   store.tokens["token-1"].TokenHash,
		Scopes:      []string{"audit:read"},
	}
	store.token = store.tokens["token-1"]
	store.role = "admin"

	auditSvc := &stubAuditService{getErr: audit.ErrNotFound}
	srv := httptest.NewServer(NewRouterWithAudit(store, nil, nil, auditSvc))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	client.NoRetry = true
	_, err := client.GetAudit(context.Background(), store.team.Slug, 9999)
	if err == nil || !strings.Contains(err.Error(), "audit_not_found") {
		t.Fatalf("expected audit_not_found, got %v", err)
	}
	if auditSvc.gotID != 9999 {
		t.Fatalf("expected id=9999 passed to service, got %d", auditSvc.gotID)
	}
}

func TestListAuditHandlerRejectsBadScope(t *testing.T) {
	store, token := newFakeStore()
	store.tokens["token-1"] = db.CliToken{
		ID:          "token-1",
		OwnerUserID: "user-1",
		TeamID:      "team-1",
		TokenHash:   store.tokens["token-1"].TokenHash,
		Scopes:      []string{},
	}
	store.token = store.tokens["token-1"]
	store.role = "admin"

	auditSvc := &stubAuditService{}
	srv := httptest.NewServer(NewRouterWithAudit(store, nil, nil, auditSvc))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	client.NoRetry = true
	_, err := client.ListAudit(context.Background(), store.team.Slug, backendclient.AuditListParams{})
	if err == nil || !strings.Contains(err.Error(), "forbidden_scope") {
		t.Fatalf("expected forbidden_scope, got %v", err)
	}
}
