package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/reconciler"
)

type stubIncidentService struct {
	listResult db.IncidentListResult
	getRow     db.IncidentRow
	getErr     error
	closeRow   db.IncidentRow
	closeErr   error
	closedWith reconciler.CloseParams
}

func (s *stubIncidentService) List(_ context.Context, _ db.IncidentListFilter) (db.IncidentListResult, error) {
	return s.listResult, nil
}

func (s *stubIncidentService) Get(_ context.Context, _, _ string) (db.IncidentRow, error) {
	if s.getErr != nil {
		return db.IncidentRow{}, s.getErr
	}
	return s.getRow, nil
}

func (s *stubIncidentService) Close(_ context.Context, in reconciler.CloseParams) (db.IncidentRow, error) {
	s.closedWith = in
	if s.closeErr != nil {
		return db.IncidentRow{}, s.closeErr
	}
	return s.closeRow, nil
}

func upgradeStoreForIncidents(store *fakeStore) {
	tok := store.token
	tok.Scopes = append([]string(nil), tok.Scopes...)
	tok.Scopes = append(tok.Scopes, "incidents:read", "incidents:write")
	store.token = tok
	store.tokens[tok.ID] = tok
}

func TestListIncidentsHandlerReturnsRows(t *testing.T) {
	store, token := newFakeStore()
	upgradeStoreForIncidents(store)
	opened := time.Date(2026, 5, 15, 12, 30, 0, 0, time.UTC)
	svc := &stubIncidentService{
		listResult: db.IncidentListResult{
			Items: []db.IncidentRow{
				{ID: "inc-1", TeamID: "team-1", SubjectType: "deploy_run", SubjectID: "deploy-1",
					Kind: "failed_permanently", Severity: "medium", OpenedAt: opened},
			},
		},
	}
	srv := httptest.NewServer(NewRouterWithReconciler(store, nil, nil, nil, nil, nil, svc))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/teams/acme/incidents?status=open", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status = %d: %s", res.StatusCode, string(body))
	}
	var out struct {
		Items []struct {
			ID       string `json:"id"`
			Severity string `json:"severity"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "inc-1" {
		t.Fatalf("items = %+v", out.Items)
	}
}

func TestGetIncidentHandlerNotFound(t *testing.T) {
	store, token := newFakeStore()
	upgradeStoreForIncidents(store)
	svc := &stubIncidentService{getErr: reconciler.ErrNotFound}
	srv := httptest.NewServer(NewRouterWithReconciler(store, nil, nil, nil, nil, nil, svc))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/teams/acme/incidents/00000000-0000-0000-0000-000000000099", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, _ := http.DefaultClient.Do(req)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", res.StatusCode)
	}
}

func TestCloseIncidentHandlerAcceptsNote(t *testing.T) {
	store, token := newFakeStore()
	upgradeStoreForIncidents(store)
	closedAt := time.Date(2026, 5, 16, 8, 0, 0, 0, time.UTC)
	svc := &stubIncidentService{
		closeRow: db.IncidentRow{
			ID:       "inc-1",
			TeamID:   "team-1",
			Kind:     "failed_permanently",
			Severity: "medium",
			OpenedAt: closedAt.Add(-time.Hour),
			ClosedAt: &closedAt,
			ClosedBy: strPtr("user-1"),
		},
	}
	srv := httptest.NewServer(NewRouterWithReconciler(store, nil, nil, nil, nil, nil, svc))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]any{"note": "root cause: GitHub outage"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/acme/incidents/inc-1:close", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, _ := http.DefaultClient.Do(req)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("status = %d: %s", res.StatusCode, string(raw))
	}
	if svc.closedWith.Note != "root cause: GitHub outage" {
		t.Fatalf("note not forwarded: %q", svc.closedWith.Note)
	}
	if svc.closedWith.ActorID != "user-1" {
		t.Fatalf("actor id = %q, want user-1", svc.closedWith.ActorID)
	}
	if svc.closedWith.TeamID != "team-1" {
		t.Fatalf("team id = %q, want team-1", svc.closedWith.TeamID)
	}
}

func TestCloseIncidentHandlerAlreadyClosedReturns409(t *testing.T) {
	store, token := newFakeStore()
	upgradeStoreForIncidents(store)
	svc := &stubIncidentService{closeErr: reconciler.ErrAlreadyClosed}
	srv := httptest.NewServer(NewRouterWithReconciler(store, nil, nil, nil, nil, nil, svc))
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/acme/incidents/inc-1:close", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+token)
	res, _ := http.DefaultClient.Do(req)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", res.StatusCode)
	}
}

func TestCloseIncidentHandlerRequiresAdmin(t *testing.T) {
	store, token := newFakeStore()
	upgradeStoreForIncidents(store)
	store.role = "member"
	svc := &stubIncidentService{}
	srv := httptest.NewServer(NewRouterWithReconciler(store, nil, nil, nil, nil, nil, svc))
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/acme/incidents/inc-1:close", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+token)
	res, _ := http.DefaultClient.Do(req)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", res.StatusCode)
	}
}

func TestListIncidentsHandlerForbiddenWithoutScope(t *testing.T) {
	store, token := newFakeStore()
	// Do not call upgradeStoreForIncidents — token lacks incidents:read.
	svc := &stubIncidentService{}
	srv := httptest.NewServer(NewRouterWithReconciler(store, nil, nil, nil, nil, nil, svc))
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/teams/acme/incidents", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, _ := http.DefaultClient.Do(req)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", res.StatusCode)
	}
}

func TestEncodeDecodeIncidentCursor(t *testing.T) {
	ts := time.Date(2026, 5, 16, 12, 34, 56, 789, time.UTC)
	id := "00000000-0000-0000-0000-000000000abc"
	cur := encodeIncidentCursor(ts, id)
	if cur == "" {
		t.Fatalf("encode returned empty cursor")
	}
	gotTS, gotID, err := decodeIncidentCursor(cur)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !gotTS.Equal(ts) || gotID != id {
		t.Fatalf("roundtrip = (%s, %s), want (%s, %s)", gotTS, gotID, ts, id)
	}
	if _, _, err := decodeIncidentCursor("not-base64-!"); err == nil {
		t.Fatalf("expected error for bad cursor, got nil")
	}
}

