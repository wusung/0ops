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

type fakePinCall struct {
	teamID    string
	uploadID  string
	expiresAt time.Time
}

type fakeStore struct {
	preview      db.Preview
	app          db.App
	createCalls  int
	deleteCalls  int
	deletedAppID string
	consumeArgs  []json.RawMessage
	pinCalls     []fakePinCall
	pinErr       error
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

func (f *fakeStore) PinUpload(_ context.Context, teamID, id string, expiresAt time.Time) error {
	f.pinCalls = append(f.pinCalls, fakePinCall{teamID, id, expiresAt})
	return f.pinErr
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

// TestConfirm_PinsUploadOnSuccess verifies that a successful confirm with an
// upload-source payload calls PinUpload exactly once with the correct args.
func TestConfirm_PinsUploadOnSuccess(t *testing.T) {
	now := time.Now().UTC()
	fixedNow := now
	store := &fakeStore{
		preview: db.Preview{
			ID:          "preview-1",
			TeamID:      "team-1",
			ActorUserID: "user-1",
			Action:      previewAction,
			Args: mustJSON(t, dto.AppCreateRequest{
				Slug: "nextdemo",
				Source: &dto.Source{
					Type:   dto.SourceKindUpload,
					Upload: &dto.SourceUpload{UploadID: "upl_x"},
				},
			}),
			ExpiresAt: now.Add(10 * time.Minute),
		},
	}

	svc := New(store, noopK3s{}, noopCF{}, nil, nil, nil, "")
	svc.now = func() time.Time { return fixedNow }
	result, err := svc.Confirm(context.Background(), "team-1", "user-1", "team-slug", "preview-1", "trace-1")
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if result.Replayed {
		t.Fatal("expected fresh result, not replay")
	}
	if len(store.pinCalls) != 1 {
		t.Fatalf("PinUpload calls = %d, want 1", len(store.pinCalls))
	}
	got := store.pinCalls[0]
	if got.teamID != "team-1" {
		t.Fatalf("pin teamID = %q, want team-1", got.teamID)
	}
	if got.uploadID != "upl_x" {
		t.Fatalf("pin uploadID = %q, want upl_x", got.uploadID)
	}
	wantExpiry := fixedNow.UTC().Add(uploadPinTTL)
	if !got.expiresAt.Equal(wantExpiry) {
		t.Fatalf("pin expiresAt = %v, want %v", got.expiresAt, wantExpiry)
	}
}

// TestConfirm_NoPinForGitHubSource verifies that a GitHub-source confirm
// does NOT call PinUpload.
func TestConfirm_NoPinForGitHubSource(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeStore{
		preview: db.Preview{
			ID:          "preview-1",
			TeamID:      "team-1",
			ActorUserID: "user-1",
			Action:      previewAction,
			Args: mustJSON(t, dto.AppCreateRequest{
				Slug: "nextdemo",
				Source: &dto.Source{
					Type:   dto.SourceKindGitHub,
					GitHub: &dto.SourceGitHub{URL: "https://github.com/example/nextdemo", Ref: "main"},
				},
			}),
			ExpiresAt: now.Add(10 * time.Minute),
		},
	}

	svc := New(store, noopK3s{}, noopCF{}, nil, nil, nil, "")
	_, err := svc.Confirm(context.Background(), "team-1", "user-1", "team-slug", "preview-1", "trace-1")
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if len(store.pinCalls) != 0 {
		t.Fatalf("PinUpload calls = %d, want 0 for github source", len(store.pinCalls))
	}
}

// TestConfirm_NoPinForLegacyRepoURL verifies that a legacy repo_url confirm
// (Source=nil) does NOT call PinUpload.
func TestConfirm_NoPinForLegacyRepoURL(t *testing.T) {
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
	_, err := svc.Confirm(context.Background(), "team-1", "user-1", "team-slug", "preview-1", "trace-1")
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if len(store.pinCalls) != 0 {
		t.Fatalf("PinUpload calls = %d, want 0 for legacy repo_url", len(store.pinCalls))
	}
}

// TestConfirm_PinFailureDoesNotRollback verifies that a PinUpload failure
// does NOT cause the confirm to fail — the deploy_run continues and the
// response is returned normally.
func TestConfirm_PinFailureDoesNotRollback(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeStore{
		preview: db.Preview{
			ID:          "preview-1",
			TeamID:      "team-1",
			ActorUserID: "user-1",
			Action:      previewAction,
			Args: mustJSON(t, dto.AppCreateRequest{
				Slug: "nextdemo",
				Source: &dto.Source{
					Type:   dto.SourceKindUpload,
					Upload: &dto.SourceUpload{UploadID: "upl_fail"},
				},
			}),
			ExpiresAt: now.Add(10 * time.Minute),
		},
		pinErr: errors.New("simulated db down"),
	}

	svc := New(store, noopK3s{}, noopCF{}, nil, nil, nil, "")
	result, err := svc.Confirm(context.Background(), "team-1", "user-1", "team-slug", "preview-1", "trace-1")
	if err != nil {
		t.Fatalf("Confirm() must succeed despite pin failure, got error = %v", err)
	}
	if result.Response.DeployRunID == "" {
		t.Fatal("expected non-empty DeployRunID in response")
	}
	if store.createCalls != 1 {
		t.Fatalf("CreateApp calls = %d, want 1", store.createCalls)
	}
	if store.deleteCalls != 0 {
		t.Fatalf("DeleteAppByID calls = %d, want 0 (no rollback)", store.deleteCalls)
	}
	if len(store.pinCalls) != 1 {
		t.Fatalf("PinUpload calls = %d, want 1 (attempted despite failure)", len(store.pinCalls))
	}
}

// TestValidateRequest_AcceptsSourceUploadOnly verifies that a request with
// Source set to upload and an empty RepoURL passes validation.
func TestValidateRequest_AcceptsSourceUploadOnly(t *testing.T) {
	req := dto.AppCreateRequest{
		Slug: "nextdemo",
		Source: &dto.Source{
			Type:   dto.SourceKindUpload,
			Upload: &dto.SourceUpload{UploadID: "upl_abc"},
		},
	}
	if err := validateRequest(req); err != nil {
		t.Fatalf("validateRequest() error = %v, want nil", err)
	}
}

// TestValidateRequest_AcceptsSourceGitHub verifies that a request with
// Source set to github passes validation.
func TestValidateRequest_AcceptsSourceGitHub(t *testing.T) {
	req := dto.AppCreateRequest{
		Slug: "nextdemo",
		Source: &dto.Source{
			Type:   dto.SourceKindGitHub,
			GitHub: &dto.SourceGitHub{URL: "https://github.com/example/nextdemo", Ref: "main"},
		},
	}
	if err := validateRequest(req); err != nil {
		t.Fatalf("validateRequest() error = %v, want nil", err)
	}
}

// TestValidateRequest_RejectsSourceInvalid verifies that a github source with
// a nil GitHub payload fails validation.
func TestValidateRequest_RejectsSourceInvalid(t *testing.T) {
	req := dto.AppCreateRequest{
		Slug: "nextdemo",
		Source: &dto.Source{
			Type:   dto.SourceKindGitHub,
			GitHub: nil,
		},
	}
	err := validateRequest(req)
	if err == nil {
		t.Fatal("validateRequest() error = nil, want github source incomplete")
	}
	if err.Error() != "github source incomplete" {
		t.Fatalf("err = %q, want %q", err.Error(), "github source incomplete")
	}
}

// TestValidateRequest_AcceptsLegacyRepoURL verifies that the legacy
// repo_url + ref path still passes when Source is nil.
func TestValidateRequest_AcceptsLegacyRepoURL(t *testing.T) {
	req := dto.AppCreateRequest{
		Slug:    "nextdemo",
		RepoURL: "https://github.com/example/nextdemo",
		Ref:     "main",
	}
	if err := validateRequest(req); err != nil {
		t.Fatalf("validateRequest() error = %v, want nil", err)
	}
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
