package createapp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared/dto"
)

type fakeStore struct {
	preview      db.Preview
	app          db.App
	createCalls  int
	deleteCalls  int
	deletedAppID string
	consumeArgs  []json.RawMessage
}

func (f *fakeStore) GetTeamAppBySlug(context.Context, string, string) (db.App, error) {
	if f.app.Slug != "" {
		return f.app, nil
	}
	return db.App{}, pgx.ErrNoRows
}

func (f *fakeStore) GetPreview(context.Context, string) (db.Preview, error) {
	if f.preview.ID == "" {
		return db.Preview{}, db.ErrPreviewNotFound
	}
	return f.preview, nil
}

func (f *fakeStore) ConsumePreviewWithResult(_ context.Context, _ string, result json.RawMessage) error {
	f.consumeArgs = append(f.consumeArgs, append(json.RawMessage(nil), result...))
	now := time.Now().UTC()
	f.preview.ConsumedAt = &now
	f.preview.LastResult = append(json.RawMessage(nil), result...)
	return nil
}

func (f *fakeStore) CreateApp(_ context.Context, params db.AppCreateParams) (db.AppCreateResult, error) {
	f.createCalls++
	f.app = db.App{
		ID:     "app-1",
		TeamID: params.TeamID,
		Slug:   params.Slug,
	}
	return db.AppCreateResult{
		AppID:       "app-1",
		AppSlug:     params.Slug,
		DeployRunID: "deploy-1",
	}, nil
}

func (f *fakeStore) DeleteAppByID(_ context.Context, appID string) error {
	f.deleteCalls++
	f.deletedAppID = appID
	return nil
}

type noopK3s struct{}

func (noopK3s) EnsureTeamIsolation(context.Context, string, string, string) (string, error) {
	return "team-free", nil
}

type failingK3s struct {
	err error
}

func (f failingK3s) EnsureTeamIsolation(context.Context, string, string, string) (string, error) {
	return "", f.err
}

type noopCF struct{}

func (noopCF) RouteAppToDomain(context.Context, string, string, string) (string, error) {
	return "nextdemo.winshare.tw", nil
}

func (noopCF) CreateTunnelRoute(context.Context, string, string, string) error {
	return nil
}

type failingCF struct {
	err error
}

func (f failingCF) RouteAppToDomain(context.Context, string, string, string) (string, error) {
	return "", f.err
}

func (f failingCF) CreateTunnelRoute(context.Context, string, string, string) error {
	return nil
}

type routeTrackingCF struct {
	createTunnelRouteCalled bool
	createTunnelRouteErr    error
	calls                   []string
	teamID                  string
	appSlug                 string
	backendURL              string
}

func (r *routeTrackingCF) RouteAppToDomain(_ context.Context, teamID, _ string, appSlug string) (string, error) {
	r.calls = append(r.calls, "route")
	r.teamID = teamID
	r.appSlug = appSlug
	return "nextdemo.winshare.tw", nil
}

func (r *routeTrackingCF) CreateTunnelRoute(_ context.Context, teamID, appSlug, backendURL string) error {
	r.calls = append(r.calls, "tunnel")
	r.createTunnelRouteCalled = true
	r.teamID = teamID
	r.appSlug = appSlug
	r.backendURL = backendURL
	return r.createTunnelRouteErr
}

func TestConfirmReplayReturnsStoredResult(t *testing.T) {
	now := time.Now().UTC()
	stored := dto.AppCreateResponse{
		AppID:         "app-1",
		AppSlug:       "nextdemo",
		DeployRunID:   "deploy-1",
		TraceID:       "trace-1",
		SubdomainURL:  "https://nextdemo.winshare.tw",
		InitialDeploy: true,
	}
	storedJSON, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal stored result: %v", err)
	}
	store := &fakeStore{
		preview: db.Preview{
			ID:          "preview-1",
			TeamID:      "team-1",
			ActorUserID: "user-1",
			Action:      previewAction,
			LastResult:  storedJSON,
			ConsumedAt:  &now,
			ExpiresAt:   now.Add(10 * time.Minute),
		},
	}

	svc := New(store, noopK3s{}, noopCF{}, nil, nil, nil, "")
	result, err := svc.Confirm(context.Background(), "team-1", "user-1", "team-slug", "preview-1", "trace-ignored")
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if !result.Replayed {
		t.Fatal("expected replayed result")
	}
	if result.Response.AppSlug != stored.AppSlug {
		t.Fatalf("AppSlug = %q, want %q", result.Response.AppSlug, stored.AppSlug)
	}
	if result.Response.SubdomainURL != "https://nextdemo.winshare.tw" {
		t.Fatalf("SubdomainURL = %q, want https://nextdemo.winshare.tw", result.Response.SubdomainURL)
	}
}

func TestConfirmCreatesAppAndConsumesPreview(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeStore{
		preview: db.Preview{
			ID:          "preview-1",
			TeamID:      "team-1",
			ActorUserID: "user-1",
			Action:      previewAction,
			Args:        mustJSON(t, dto.AppCreateRequest{Slug: "nextdemo", RepoURL: "https://github.com/example/nextdemo", Ref: "main"}),
			ExpiresAt:   now.Add(10 * time.Minute),
		},
	}

	svc := New(store, noopK3s{}, noopCF{}, nil, nil, nil, "")
	result, err := svc.Confirm(context.Background(), "team-1", "user-1", "team-slug", "preview-1", "trace-1")
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if result.Replayed {
		t.Fatal("expected fresh result")
	}
	if result.Response.AppSlug != "nextdemo" {
		t.Fatalf("AppSlug = %q, want nextdemo", result.Response.AppSlug)
	}
	if store.createCalls != 1 {
		t.Fatalf("CreateApp calls = %d, want 1", store.createCalls)
	}
	if got := store.preview.LastResult; len(got) == 0 {
		t.Fatal("expected stored last result")
	}
	if store.deleteCalls != 0 {
		t.Fatalf("DeleteAppByID calls = %d, want 0", store.deleteCalls)
	}
}

func TestConfirmRejectsExpiredPreview(t *testing.T) {
	store := &fakeStore{
		preview: db.Preview{
			ID:          "preview-1",
			TeamID:      "team-1",
			ActorUserID: "user-1",
			Action:      previewAction,
			Args:        mustJSON(t, dto.AppCreateRequest{Slug: "nextdemo", RepoURL: "https://github.com/example/nextdemo", Ref: "main"}),
			ExpiresAt:   time.Now().UTC().Add(-time.Minute),
		},
	}

	svc := New(store, noopK3s{}, noopCF{}, nil, nil, nil, "")
	_, err := svc.Confirm(context.Background(), "team-1", "user-1", "team-slug", "preview-1", "trace-1")
	if !errors.Is(err, ErrPreviewExpired) {
		t.Fatalf("err = %v, want ErrPreviewExpired", err)
	}
}

func TestConfirmRollsBackAppOnCloudflareFailure(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeStore{
		preview: db.Preview{
			ID:          "preview-1",
			TeamID:      "team-1",
			ActorUserID: "user-1",
			Action:      previewAction,
			Args:        mustJSON(t, dto.AppCreateRequest{Slug: "nextdemo", RepoURL: "https://github.com/example/nextdemo", Ref: "main"}),
			ExpiresAt:   now.Add(10 * time.Minute),
		},
	}

	svc := New(store, noopK3s{}, failingCF{err: errors.New("cloudflare route missing")}, nil, nil, nil, "")
	_, err := svc.Confirm(context.Background(), "team-1", "user-1", "team-slug", "preview-1", "trace-1")
	if err == nil {
		t.Fatal("Confirm() error = nil, want route failure")
	}
	if store.createCalls != 1 {
		t.Fatalf("CreateApp calls = %d, want 1", store.createCalls)
	}
	if store.deleteCalls != 1 {
		t.Fatalf("DeleteAppByID calls = %d, want 1", store.deleteCalls)
	}
	if store.deletedAppID != "app-1" {
		t.Fatalf("DeletedAppID = %q, want app-1", store.deletedAppID)
	}
	if store.preview.ConsumedAt != nil {
		t.Fatal("preview should not be consumed on route failure")
	}
}

func TestConfirmCreatesTunnelRouteAfterDomainRouting(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeStore{
		preview: db.Preview{
			ID:          "preview-1",
			TeamID:      "team-1",
			ActorUserID: "user-1",
			Action:      previewAction,
			Args:        mustJSON(t, dto.AppCreateRequest{Slug: "nextdemo", RepoURL: "https://github.com/example/nextdemo", Ref: "main"}),
			ExpiresAt:   now.Add(10 * time.Minute),
		},
	}
	cf := &routeTrackingCF{}

	svc := New(store, noopK3s{}, cf, nil, nil, nil, "")
	_, err := svc.Confirm(context.Background(), "team-1", "user-1", "team-slug", "preview-1", "trace-1")
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if !cf.createTunnelRouteCalled {
		t.Fatal("expected CreateTunnelRoute to be called")
	}
	if len(cf.calls) != 2 || cf.calls[0] != "route" || cf.calls[1] != "tunnel" {
		t.Fatalf("calls = %#v, want [route tunnel]", cf.calls)
	}
	if cf.teamID != "team-1" {
		t.Fatalf("teamID = %q, want team-1", cf.teamID)
	}
	if cf.appSlug != "nextdemo" {
		t.Fatalf("appSlug = %q, want nextdemo", cf.appSlug)
	}
	if cf.backendURL != tunnelBackendURL {
		t.Fatalf("backendURL = %q, want %q", cf.backendURL, tunnelBackendURL)
	}
}

func TestConfirmRollsBackAppWhenK3sIsolationFails(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeStore{
		preview: db.Preview{
			ID:          "preview-1",
			TeamID:      "team-1",
			ActorUserID: "user-1",
			Action:      previewAction,
			Args:        mustJSON(t, dto.AppCreateRequest{Slug: "nextdemo", RepoURL: "https://github.com/example/nextdemo", Ref: "main"}),
			ExpiresAt:   now.Add(10 * time.Minute),
		},
	}

	svc := New(store, failingK3s{err: errors.New("quota apply failed")}, noopCF{}, nil, nil, nil, "")
	_, err := svc.Confirm(context.Background(), "team-1", "user-1", "team-slug", "preview-1", "trace-1")
	if err == nil {
		t.Fatal("Confirm() error = nil, want k3s isolation failure")
	}
	if store.deleteCalls != 1 {
		t.Fatalf("DeleteAppByID calls = %d, want 1", store.deleteCalls)
	}
	if store.preview.ConsumedAt != nil {
		t.Fatal("preview should not be consumed when k3s isolation fails")
	}
}

func TestConfirmRollsBackWhenCreateTunnelRouteFails(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeStore{
		preview: db.Preview{
			ID:          "preview-1",
			TeamID:      "team-1",
			ActorUserID: "user-1",
			Action:      previewAction,
			Args:        mustJSON(t, dto.AppCreateRequest{Slug: "nextdemo", RepoURL: "https://github.com/example/nextdemo", Ref: "main"}),
			ExpiresAt:   now.Add(10 * time.Minute),
		},
	}
	cf := &routeTrackingCF{createTunnelRouteErr: errors.New("tunnel route failed")}

	svc := New(store, noopK3s{}, cf, nil, nil, nil, "")
	_, err := svc.Confirm(context.Background(), "team-1", "user-1", "team-slug", "preview-1", "trace-1")
	if err == nil {
		t.Fatal("Confirm() error = nil, want tunnel route failure")
	}
	if store.deleteCalls != 1 {
		t.Fatalf("DeleteAppByID calls = %d, want 1", store.deleteCalls)
	}
	if store.preview.ConsumedAt != nil {
		t.Fatal("preview should not be consumed when tunnel route creation fails")
	}
}

func TestCreateAppLifecycle(t *testing.T) {
	got := Lifecycle()
	if len(got) != 7 {
		t.Fatalf("len = %d, want 7", len(got))
	}
	if got[0] != DeployRunQueued || got[len(got)-1] != DeployRunLive {
		t.Fatalf("unexpected lifecycle: %#v", got)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// fakeRecordingInspector is a minimal Inspector stub that records calls.
type fakeRecordingInspector struct {
	calls    int
	lastURL  string
	lastRef  string
	returnMd RepoMetadata
	returnErr error
}

func (f *fakeRecordingInspector) Inspect(_ context.Context, repoURL, ref string) (RepoMetadata, error) {
	f.calls++
	f.lastURL = repoURL
	f.lastRef = ref
	return f.returnMd, f.returnErr
}

// TestService_InspectorWireUp verifies the builder pattern (T12).
func TestService_InspectorWireUp(t *testing.T) {
	// Default construction: inspector must be nil.
	s := New(nil, nil, nil, nil, nil, nil, "")
	if s.Inspector() != nil {
		t.Fatalf("expected nil inspector by default, got %v", s.Inspector())
	}

	// WithInspector installs the inspector and returns the same receiver.
	fake := &fakeRecordingInspector{}
	s2 := New(nil, nil, nil, nil, nil, nil, "").WithInspector(fake)
	if s2.Inspector() != fake {
		t.Fatalf("expected installed inspector, got %v", s2.Inspector())
	}

	// WithInspector is chainable and returns the same pointer.
	s3 := New(nil, nil, nil, nil, nil, nil, "")
	got := s3.WithInspector(fake)
	if got != s3 {
		t.Fatal("WithInspector must return the receiver")
	}
}
