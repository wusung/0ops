package redeploy_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/redeploy"
	workflowdispatch "github.com/winshare/zeroops/internal/server/services/workflowdispatch"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// fakeStore is a deterministic in-memory store used to drive the redeploy
// service through every spec acceptance scenario without spinning a DB.
type fakeStore struct {
	apps         map[string]db.App
	previews     map[string]db.Preview
	inFlightApps map[string]bool
	redeployRuns []db.InsertRedeployRunParams
	insertErr    error
	createErr    error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		apps:         map[string]db.App{},
		previews:     map[string]db.Preview{},
		inFlightApps: map[string]bool{},
	}
}

func (f *fakeStore) GetTeamAppBySlug(_ context.Context, _, slug string) (db.App, error) {
	app, ok := f.apps[slug]
	if !ok {
		return db.App{}, pgx.ErrNoRows
	}
	return app, nil
}

func (f *fakeStore) HasInFlightDeployRun(_ context.Context, appID string) (bool, error) {
	return f.inFlightApps[appID], nil
}

func (f *fakeStore) CreatePreview(_ context.Context, teamID, actorUserID, action string, args json.RawMessage, _ string) (db.Preview, error) {
	if f.createErr != nil {
		return db.Preview{}, f.createErr
	}
	p := db.Preview{
		ID:          "preview-rd-1",
		TeamID:      teamID,
		ActorUserID: actorUserID,
		Action:      action,
		Args:        append(json.RawMessage(nil), args...),
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(10 * time.Minute),
	}
	f.previews[p.ID] = p
	return p, nil
}

func (f *fakeStore) GetPreview(_ context.Context, previewID string) (db.Preview, error) {
	p, ok := f.previews[previewID]
	if !ok {
		return db.Preview{}, db.ErrPreviewNotFound
	}
	return p, nil
}

func (f *fakeStore) ConsumePreviewWithResult(_ context.Context, previewID string, result json.RawMessage) error {
	p, ok := f.previews[previewID]
	if !ok {
		return db.ErrPreviewNotFound
	}
	if p.ConsumedAt != nil {
		return db.ErrPreviewConsumed
	}
	now := time.Now().UTC()
	p.ConsumedAt = &now
	p.LastResult = append(json.RawMessage(nil), result...)
	f.previews[previewID] = p
	return nil
}

func (f *fakeStore) InsertRedeployRun(_ context.Context, params db.InsertRedeployRunParams) (db.InsertRedeployRunResult, error) {
	if f.insertErr != nil {
		return db.InsertRedeployRunResult{}, f.insertErr
	}
	f.redeployRuns = append(f.redeployRuns, params)
	return db.InsertRedeployRunResult{DeployRunID: "deploy-rd-" + params.AppID}, nil
}

type recordingDispatcher struct {
	calls []workflowdispatch.ClientPayload
	err   error
}

func (r *recordingDispatcher) Dispatch(_ context.Context, p workflowdispatch.ClientPayload) error {
	r.calls = append(r.calls, p)
	return r.err
}

type recordingSigner struct {
	token string
	err   error
}

func (r recordingSigner) Issue(_, _ string, _ []string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	if r.token == "" {
		return "signed-ops-token", nil
	}
	return r.token, nil
}

func newService(store *fakeStore, dispatcher redeploy.Dispatcher, signer redeploy.OpsTokenSigner) *redeploy.Service {
	trigger := redeploy.NewTrigger(store, dispatcher, signer, "http://localhost:8080")
	return redeploy.New(store, trigger)
}

func liveApp() db.App {
	status := "live"
	repo := "https://github.com/foo/bar"
	branch := "main"
	return db.App{
		ID:                "app-alpha",
		TeamID:            "team-1",
		Slug:              "alpha",
		Status:            &status,
		RepoURL:           &repo,
		RepoDefaultBranch: &branch,
	}
}

func TestPreviewCreatesRow(t *testing.T) {
	store := newFakeStore()
	store.apps["alpha"] = liveApp()
	svc := newService(store, nil, nil)

	preview, err := svc.Preview(context.Background(), "team-1", "user-1", "acme", "alpha", dto.RedeployRequest{})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if preview.PreviewID == "" || preview.Summary == "" {
		t.Fatalf("unexpected preview = %+v", preview)
	}
	if len(preview.SideEffects) < 3 {
		t.Fatalf("side_effects too short = %d", len(preview.SideEffects))
	}
	stored, ok := store.previews[preview.PreviewID]
	if !ok || stored.Action != redeploy.PreviewAction {
		t.Fatalf("preview not persisted: %+v", stored)
	}
	var args struct {
		AppSlug   string `json:"app_slug"`
		AppID     string `json:"app_id"`
		Ref       string `json:"ref"`
		CommitSHA string `json:"commit_sha"`
	}
	if err := json.Unmarshal(stored.Args, &args); err != nil {
		t.Fatalf("decode args = %v", err)
	}
	if args.AppSlug != "alpha" || args.AppID != "app-alpha" || args.Ref != "main" {
		t.Fatalf("persisted args wrong: %+v", args)
	}
}

func TestPreviewPausedAppRejected(t *testing.T) {
	store := newFakeStore()
	app := liveApp()
	status := "paused"
	app.Status = &status
	store.apps["alpha"] = app
	svc := newService(store, nil, nil)

	_, err := svc.Preview(context.Background(), "team-1", "user-1", "acme", "alpha", dto.RedeployRequest{})
	if !errors.Is(err, redeploy.ErrAppPaused) {
		t.Fatalf("err = %v, want ErrAppPaused", err)
	}
}

func TestPreviewAppMissingReturnsNotFound(t *testing.T) {
	store := newFakeStore()
	svc := newService(store, nil, nil)

	_, err := svc.Preview(context.Background(), "team-1", "user-1", "acme", "alpha", dto.RedeployRequest{})
	if !errors.Is(err, redeploy.ErrAppNotFound) {
		t.Fatalf("err = %v, want ErrAppNotFound", err)
	}
}

func TestConfirmTriggersDispatcherAndStoresLastResult(t *testing.T) {
	store := newFakeStore()
	store.apps["alpha"] = liveApp()
	disp := &recordingDispatcher{}
	signer := recordingSigner{}
	svc := newService(store, disp, signer)

	preview, err := svc.Preview(context.Background(), "team-1", "user-1", "acme", "alpha", dto.RedeployRequest{})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	result, err := svc.Confirm(context.Background(), "team-1", "user-1", "acme", preview.PreviewID, "trace-1")
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if result.Replayed {
		t.Fatalf("first confirm should not be a replay")
	}
	if result.Response.DeployRunID == "" || result.Response.AppSlug != "alpha" || result.Response.Source != "user" {
		t.Fatalf("response = %+v", result.Response)
	}
	if len(disp.calls) != 1 {
		t.Fatalf("dispatcher calls = %d, want 1", len(disp.calls))
	}
	if disp.calls[0].OpsToken == "" || !strings.Contains(disp.calls[0].CallbackURL, result.Response.DeployRunID) {
		t.Fatalf("dispatch payload = %+v", disp.calls[0])
	}
	if len(store.redeployRuns) != 1 || store.redeployRuns[0].Source != "user" || store.redeployRuns[0].ActorUserID != "user-1" {
		t.Fatalf("redeploy run = %+v", store.redeployRuns)
	}
}

func TestConfirmIdempotentReplay(t *testing.T) {
	store := newFakeStore()
	store.apps["alpha"] = liveApp()
	disp := &recordingDispatcher{}
	signer := recordingSigner{}
	svc := newService(store, disp, signer)

	preview, err := svc.Preview(context.Background(), "team-1", "user-1", "acme", "alpha", dto.RedeployRequest{})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	first, err := svc.Confirm(context.Background(), "team-1", "user-1", "acme", preview.PreviewID, "trace-1")
	if err != nil {
		t.Fatalf("Confirm() first error = %v", err)
	}
	second, err := svc.Confirm(context.Background(), "team-1", "user-1", "acme", preview.PreviewID, "trace-2")
	if err != nil {
		t.Fatalf("Confirm() second error = %v", err)
	}
	if !second.Replayed {
		t.Fatalf("second confirm should be flagged Replayed")
	}
	if second.Response.DeployRunID != first.Response.DeployRunID {
		t.Fatalf("replay returned different deploy run id: %s vs %s", second.Response.DeployRunID, first.Response.DeployRunID)
	}
	if len(disp.calls) != 1 {
		t.Fatalf("replay must not re-dispatch; dispatcher calls = %d", len(disp.calls))
	}
	if len(store.redeployRuns) != 1 {
		t.Fatalf("replay must not re-insert deploy_run; rows = %d", len(store.redeployRuns))
	}
}

func TestConfirmRejectsCrossActor(t *testing.T) {
	store := newFakeStore()
	store.apps["alpha"] = liveApp()
	svc := newService(store, nil, nil)

	preview, _ := svc.Preview(context.Background(), "team-1", "user-1", "acme", "alpha", dto.RedeployRequest{})
	_, err := svc.Confirm(context.Background(), "team-1", "other-user", "acme", preview.PreviewID, "")
	if !errors.Is(err, redeploy.ErrPreviewNotFound) {
		t.Fatalf("err = %v, want ErrPreviewNotFound (cross-actor isolation)", err)
	}
}

func TestConfirmRejectsCrossTeam(t *testing.T) {
	store := newFakeStore()
	store.apps["alpha"] = liveApp()
	svc := newService(store, nil, nil)

	preview, _ := svc.Preview(context.Background(), "team-1", "user-1", "acme", "alpha", dto.RedeployRequest{})
	_, err := svc.Confirm(context.Background(), "team-2", "user-1", "acme", preview.PreviewID, "")
	if !errors.Is(err, redeploy.ErrPreviewNotFound) {
		t.Fatalf("err = %v, want ErrPreviewNotFound (cross-team isolation)", err)
	}
}

func TestConfirmInFlightRejected(t *testing.T) {
	store := newFakeStore()
	store.apps["alpha"] = liveApp()
	store.inFlightApps["app-alpha"] = true
	disp := &recordingDispatcher{}
	signer := recordingSigner{}
	svc := newService(store, disp, signer)

	preview, _ := svc.Preview(context.Background(), "team-1", "user-1", "acme", "alpha", dto.RedeployRequest{})
	_, err := svc.Confirm(context.Background(), "team-1", "user-1", "acme", preview.PreviewID, "")
	if !errors.Is(err, redeploy.ErrAppBusy) {
		t.Fatalf("err = %v, want ErrAppBusy", err)
	}
	if len(disp.calls) != 0 {
		t.Fatalf("dispatcher must not run when app is busy")
	}
}

func TestConfirmHandlesPreviewExpired(t *testing.T) {
	store := newFakeStore()
	store.apps["alpha"] = liveApp()
	svc := newService(store, &recordingDispatcher{}, recordingSigner{})

	preview, _ := svc.Preview(context.Background(), "team-1", "user-1", "acme", "alpha", dto.RedeployRequest{})
	// Force expiry by mutating the persisted row.
	p := store.previews[preview.PreviewID]
	p.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	store.previews[preview.PreviewID] = p

	_, err := svc.Confirm(context.Background(), "team-1", "user-1", "acme", preview.PreviewID, "")
	if !errors.Is(err, redeploy.ErrPreviewExpired) {
		t.Fatalf("err = %v, want ErrPreviewExpired", err)
	}
}
