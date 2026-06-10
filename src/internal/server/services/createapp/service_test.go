package createapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/createapp/ingestion"
	"github.com/winshare/zeroops/internal/server/services/workflowdispatch"
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
	createParams db.AppCreateParams
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
	f.createParams = params
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
	return "nextdemo.jesontech.com", nil
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
	return "nextdemo.jesontech.com", nil
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
		SubdomainURL:  "https://nextdemo.jesontech.com",
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
	if result.Response.SubdomainURL != "https://nextdemo.jesontech.com" {
		t.Fatalf("SubdomainURL = %q, want https://nextdemo.jesontech.com", result.Response.SubdomainURL)
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
			Args:        mustJSON(t, dto.AppCreateRequest{Slug: "nextdemo", Source: &dto.Source{Type: dto.SourceKindGitHub, GitHub: &dto.SourceGitHub{URL: "https://github.com/example/nextdemo", Ref: "main"}}}),
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
			Args:        mustJSON(t, dto.AppCreateRequest{Slug: "nextdemo", Source: &dto.Source{Type: dto.SourceKindGitHub, GitHub: &dto.SourceGitHub{URL: "https://github.com/example/nextdemo", Ref: "main"}}}),
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
			Args:        mustJSON(t, dto.AppCreateRequest{Slug: "nextdemo", Source: &dto.Source{Type: dto.SourceKindGitHub, GitHub: &dto.SourceGitHub{URL: "https://github.com/example/nextdemo", Ref: "main"}}}),
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
			Args:        mustJSON(t, dto.AppCreateRequest{Slug: "nextdemo", Source: &dto.Source{Type: dto.SourceKindGitHub, GitHub: &dto.SourceGitHub{URL: "https://github.com/example/nextdemo", Ref: "main"}}}),
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
			Args:        mustJSON(t, dto.AppCreateRequest{Slug: "nextdemo", Source: &dto.Source{Type: dto.SourceKindGitHub, GitHub: &dto.SourceGitHub{URL: "https://github.com/example/nextdemo", Ref: "main"}}}),
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
			Args:        mustJSON(t, dto.AppCreateRequest{Slug: "nextdemo", Source: &dto.Source{Type: dto.SourceKindGitHub, GitHub: &dto.SourceGitHub{URL: "https://github.com/example/nextdemo", Ref: "main"}}}),
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

// TestValidateRequest_RejectsLegacyGitHubRepoURL guards M8: the github-via-repo_url
// alias is removed at the service layer too; only the dev file:// legacy path
// remains for Source=nil requests.
func TestValidateRequest_RejectsLegacyGitHubRepoURL(t *testing.T) {
	req := dto.AppCreateRequest{
		Slug:    "nextdemo",
		RepoURL: "https://github.com/example/nextdemo",
		Ref:     "main",
	}
	err := validateRequest(req)
	if err == nil {
		t.Fatal("validateRequest() error = nil, want unsupported repo_url (github via repo_url removed in M8)")
	}
	if err.Error() != "unsupported repo_url" {
		t.Fatalf("err = %q, want %q", err.Error(), "unsupported repo_url")
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

// ----- T14 service tests -----

// recordingDispatcher records the last payload passed to Dispatch.
type recordingDispatcher struct {
	called  bool
	payload workflowdispatch.ClientPayload
	err     error
}

func (r *recordingDispatcher) Dispatch(_ context.Context, p workflowdispatch.ClientPayload) error {
	r.called = true
	r.payload = p
	return r.err
}

// stubOpsTokenSigner always returns a fixed token.
type stubOpsTokenSigner struct{}

func (stubOpsTokenSigner) Issue(_, _ string, _ []string) (string, error) {
	return "ops-token-stub", nil
}

// newTestUploadSigner returns a TokenSigner with a deterministic secret.
func newTestUploadSigner() *ingestion.TokenSigner {
	return &ingestion.TokenSigner{
		Secret: []byte("test-secret-32-bytes-long-enough"),
		TTL:    15 * time.Minute,
	}
}

// mustJSONNoT marshals v to JSON and panics on error.
// Used in helper constructors where no *testing.T is in scope.
func mustJSONNoT(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// newUploadPreview builds a fakeStore with a preview for an upload-source request.
func newUploadPreview(uploadID string) *fakeStore {
	now := time.Now().UTC()
	return &fakeStore{
		preview: db.Preview{
			ID:          "preview-1",
			TeamID:      "team-1",
			ActorUserID: "user-1",
			Action:      previewAction,
			Args: mustJSONNoT(dto.AppCreateRequest{
				Slug: "nextdemo",
				Source: &dto.Source{
					Type:   dto.SourceKindUpload,
					Upload: &dto.SourceUpload{UploadID: uploadID},
				},
			}),
			ExpiresAt: now.Add(10 * time.Minute),
		},
	}
}

// TestConfirm_UploadSourceSignsTokenAndPopulatesPayload verifies that when
// Service.Confirm is called with an upload source and a token signer installed,
// the dispatch payload contains source_kind="upload", upload_id, fetch_token
// (a valid JWT), and fetch_url.
func TestConfirm_UploadSourceSignsTokenAndPopulatesPayload(t *testing.T) {
	store := newUploadPreview("upl_abc123")
	disp := &recordingDispatcher{}
	signer := newTestUploadSigner()

	svc := New(store, noopK3s{}, noopCF{}, nil, disp, stubOpsTokenSigner{}, "https://ops.example").
		WithUploadTokenSigner(signer, "")

	_, err := svc.Confirm(context.Background(), "team-1", "user-1", "team-slug", "preview-1", "trace-1")
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if !disp.called {
		t.Fatal("dispatcher not called")
	}
	p := disp.payload
	if p.SourceKind != "upload" {
		t.Fatalf("source_kind = %q, want upload", p.SourceKind)
	}
	if p.UploadID != "upl_abc123" {
		t.Fatalf("upload_id = %q, want upl_abc123", p.UploadID)
	}
	if p.FetchToken == "" {
		t.Fatal("fetch_token must be non-empty")
	}
	// Verify the token is a valid JWT for this signer.
	claims, err := signer.Verify(p.FetchToken)
	if err != nil {
		t.Fatalf("fetch_token is not a valid JWT: %v", err)
	}
	if claims.Scope != ingestion.ScopeDownloadUpload {
		t.Fatalf("Scope: got %q want %q", claims.Scope, ingestion.ScopeDownloadUpload)
	}
	if claims.UploadID != "upl_abc123" {
		t.Fatalf("token upload_id = %q, want upl_abc123", claims.UploadID)
	}
	if claims.TeamID != "team-1" {
		t.Fatalf("token team_id = %q, want team-1", claims.TeamID)
	}
	// fetch_url must contain the upload ID path.
	if !strings.Contains(p.FetchURL, "upl_abc123") {
		t.Fatalf("fetch_url = %q, want URL containing upl_abc123", p.FetchURL)
	}
	if !strings.HasPrefix(p.FetchURL, "https://ops.example") {
		t.Fatalf("fetch_url = %q, want prefix https://ops.example", p.FetchURL)
	}
}

// TestConfirm_UploadSourceWithoutSignerSkipsTokenFields verifies that when
// no token signer is installed, fetch_token and fetch_url are empty but
// source_kind and upload_id are still populated.
func TestConfirm_UploadSourceWithoutSignerSkipsTokenFields(t *testing.T) {
	store := newUploadPreview("upl_nosign")
	disp := &recordingDispatcher{}

	svc := New(store, noopK3s{}, noopCF{}, nil, disp, stubOpsTokenSigner{}, "https://ops.example")
	// WithUploadTokenSigner not called — signer is nil.

	_, err := svc.Confirm(context.Background(), "team-1", "user-1", "team-slug", "preview-1", "trace-1")
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	p := disp.payload
	if p.SourceKind != "upload" {
		t.Fatalf("source_kind = %q, want upload", p.SourceKind)
	}
	if p.UploadID != "upl_nosign" {
		t.Fatalf("upload_id = %q, want upl_nosign", p.UploadID)
	}
	if p.FetchToken != "" {
		t.Fatalf("fetch_token must be empty without signer, got %q", p.FetchToken)
	}
	if p.FetchURL != "" {
		t.Fatalf("fetch_url must be empty without signer, got %q", p.FetchURL)
	}
}

// TestConfirm_GitHubSourceSetsKindOnly verifies that a GitHub source sets
// source_kind="github", leaves upload fields empty, and (M8) flows the github
// URL + ref from Source.GitHub into the stored app and the dispatch payload —
// the legacy top-level RepoURL/Ref are empty for a Source-only github request.
func TestConfirm_GitHubSourceSetsKindOnly(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeStore{
		preview: db.Preview{
			ID:          "preview-1",
			TeamID:      "team-1",
			ActorUserID: "user-1",
			Action:      previewAction,
			Args: mustJSONNoT(dto.AppCreateRequest{
				Slug: "nextdemo",
				Source: &dto.Source{
					Type:   dto.SourceKindGitHub,
					GitHub: &dto.SourceGitHub{URL: "https://github.com/example/nextdemo", Ref: "main"},
				},
			}),
			ExpiresAt: now.Add(10 * time.Minute),
		},
	}
	disp := &recordingDispatcher{}

	svc := New(store, noopK3s{}, noopCF{}, nil, disp, stubOpsTokenSigner{}, "https://ops.example")

	_, err := svc.Confirm(context.Background(), "team-1", "user-1", "team-slug", "preview-1", "trace-1")
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	p := disp.payload
	if p.SourceKind != "github" {
		t.Fatalf("source_kind = %q, want github", p.SourceKind)
	}
	if p.UploadID != "" || p.FetchToken != "" || p.FetchURL != "" {
		t.Fatalf("upload fields must be empty for github source: upload_id=%q fetch_token=%q fetch_url=%q",
			p.UploadID, p.FetchToken, p.FetchURL)
	}
	// M8: github URL + ref must reach the build/deploy pipeline via Source.GitHub.
	if p.Ref != "main" {
		t.Fatalf("dispatch ref = %q, want main (derived from Source.GitHub.Ref)", p.Ref)
	}
	if p.CommitSHA != "main" {
		t.Fatalf("dispatch commit_sha = %q, want main (defaults to ref when gitops nil)", p.CommitSHA)
	}
	if store.createParams.RepoURL != "https://github.com/example/nextdemo" {
		t.Fatalf("stored repo_url = %q, want github URL from Source.GitHub.URL", store.createParams.RepoURL)
	}
	if store.createParams.Ref != "main" {
		t.Fatalf("stored ref = %q, want main from Source.GitHub.Ref", store.createParams.Ref)
	}
}

// TestConfirm_LegacyFileURLSetsLocalKind verifies that a legacy repo_url
// with file:// scheme sets source_kind="local".
// Requires LOCAL_FILE_REPO_ENABLED + LOCAL_FILE_REPO_ROOT so validateRequest
// accepts the file:// path.
func TestConfirm_LegacyFileURLSetsLocalKind(t *testing.T) {
	// Enable the local-repo gate and point root at /tmp (always exists).
	t.Setenv("LOCAL_FILE_REPO_ENABLED", "true")
	t.Setenv("LOCAL_FILE_REPO_ROOT", "/tmp")

	now := time.Now().UTC()
	store := &fakeStore{
		preview: db.Preview{
			ID:          "preview-1",
			TeamID:      "team-1",
			ActorUserID: "user-1",
			Action:      previewAction,
			Args: mustJSONNoT(dto.AppCreateRequest{
				Slug:    "nextdemo",
				RepoURL: "file:///tmp",
				Ref:     "main",
			}),
			ExpiresAt: now.Add(10 * time.Minute),
		},
	}
	disp := &recordingDispatcher{}

	svc := New(store, noopK3s{}, noopCF{}, nil, disp, stubOpsTokenSigner{}, "https://ops.example")

	_, err := svc.Confirm(context.Background(), "team-1", "user-1", "team-slug", "preview-1", "trace-1")
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	p := disp.payload
	if p.SourceKind != "local" {
		t.Fatalf("source_kind = %q, want local", p.SourceKind)
	}
}

// TestConfirm_TokenSignFailureRollsBack verifies that when the upload token
// signer returns an error, the confirm rolls back (DeleteAppByID called) and
// returns an error.
func TestConfirm_TokenSignFailureRollsBack(t *testing.T) {
	store := newUploadPreview("upl_failsign")
	disp := &recordingDispatcher{}

	// Use a signer with empty secret — Sign() returns errEmptySecret.
	badSigner := &ingestion.TokenSigner{Secret: nil, TTL: 15 * time.Minute}

	svc := New(store, noopK3s{}, noopCF{}, nil, disp, stubOpsTokenSigner{}, "https://ops.example").
		WithUploadTokenSigner(badSigner, "")

	_, err := svc.Confirm(context.Background(), "team-1", "user-1", "team-slug", "preview-1", "trace-1")
	if err == nil {
		t.Fatal("Confirm() must fail when token signing fails")
	}
	if !strings.Contains(err.Error(), "sign upload fetch token") {
		t.Fatalf("error should mention 'sign upload fetch token', got: %v", err)
	}
	// Rollback: app must be deleted.
	if store.deleteCalls != 1 {
		t.Fatalf("DeleteAppByID calls = %d, want 1 (rollback)", store.deleteCalls)
	}
	// Dispatcher must NOT have been called (token sign failure is before dispatch).
	if disp.called {
		t.Fatal("dispatcher should not be called after token sign failure")
	}
	// Preview must not be consumed.
	if store.preview.ConsumedAt != nil {
		t.Fatal("preview should not be consumed after token sign failure")
	}
}

// TestService_WithUploadTokenSignerBuilder verifies the builder pattern (T14).
func TestService_WithUploadTokenSignerBuilder(t *testing.T) {
	// Default: uploadTokenSigner is nil.
	s := New(nil, nil, nil, nil, nil, nil, "")
	if s.uploadTokenSigner != nil {
		t.Fatal("expected nil uploadTokenSigner by default")
	}

	// Install a signer; receiver returned.
	signer := newTestUploadSigner()
	s2 := New(nil, nil, nil, nil, nil, nil, "").WithUploadTokenSigner(signer, "https://base.example")
	if s2.uploadTokenSigner != signer {
		t.Fatalf("expected installed signer, got %v", s2.uploadTokenSigner)
	}
	if s2.uploadFetchBaseURL != "https://base.example" {
		t.Fatalf("uploadFetchBaseURL = %q, want https://base.example", s2.uploadFetchBaseURL)
	}

	// Chainable and returns same pointer.
	s3 := New(nil, nil, nil, nil, nil, nil, "")
	got := s3.WithUploadTokenSigner(signer, "")
	if got != s3 {
		t.Fatal("WithUploadTokenSigner must return the receiver")
	}
}
