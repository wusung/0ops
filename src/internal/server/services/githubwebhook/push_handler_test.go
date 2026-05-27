package githubwebhook_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/githubwebhook"
	"github.com/winshare/zeroops/internal/server/services/redeploy"
)

// fakeStore is an in-memory PushHandlerStore for the push-handler unit
// tests. Each method is purposely simple: the tests below are about the
// handler's branching logic, not DB semantics.
type fakeStore struct {
	mu            sync.Mutex
	team          *db.Team
	apps          []db.App
	inFlight      map[string]bool
	insertedRuns  []db.InsertRedeployRunParams
	auditEntries  []auditEntry
	teamLookupErr error
}

type auditEntry struct {
	teamID string
	action string
	args   map[string]any
	result map[string]any
}

func (f *fakeStore) FindTeamByGitHubInstallID(_ context.Context, _ int64) (db.Team, error) {
	if f.teamLookupErr != nil {
		return db.Team{}, f.teamLookupErr
	}
	if f.team == nil {
		return db.Team{}, db.ErrTeamNotFound
	}
	return *f.team, nil
}

func (f *fakeStore) FindLiveAppsByRepoAndBranch(_ context.Context, _, _, _ string) ([]db.App, error) {
	return append([]db.App(nil), f.apps...), nil
}

func (f *fakeStore) HasInFlightDeployRun(_ context.Context, appID string) (bool, error) {
	return f.inFlight[appID], nil
}

func (f *fakeStore) InsertRedeployRun(_ context.Context, params db.InsertRedeployRunParams) (db.InsertRedeployRunResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.insertedRuns = append(f.insertedRuns, params)
	return db.InsertRedeployRunResult{DeployRunID: "run-" + params.AppID}, nil
}

func (f *fakeStore) AppendWebhookAudit(_ context.Context, teamID, action string, args, result map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auditEntries = append(f.auditEntries, auditEntry{teamID, action, args, result})
	return nil
}

type fakeTrigger struct {
	calls []redeploy.TriggerArgs
}

func (f *fakeTrigger) Trigger(_ context.Context, args redeploy.TriggerArgs) (redeploy.TriggerResult, error) {
	f.calls = append(f.calls, args)
	return redeploy.TriggerResult{DeployRunID: "run-" + args.AppID, CommitSHA: args.CommitSHA, TraceID: args.TraceID}, nil
}

func mustEncodePush(t *testing.T, payload githubwebhook.PushPayload) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode payload err = %v", err)
	}
	return body
}

func newLiveApp(slug, repoURL, branch string) db.App {
	status := "live"
	repo := repoURL
	br := branch
	return db.App{
		ID:                "app-" + slug,
		TeamID:            "team-1",
		Slug:              slug,
		Status:            &status,
		RepoURL:           &repo,
		RepoDefaultBranch: &br,
	}
}

func basePush() githubwebhook.PushPayload {
	p := githubwebhook.PushPayload{Ref: "refs/heads/main", After: "abc123"}
	p.Repository.HTMLURL = "https://github.com/foo/bar"
	p.Repository.DefaultBranch = "main"
	p.Installation.ID = 99
	return p
}

func TestPushHandlerTriggersForMatchingLiveApp(t *testing.T) {
	store := &fakeStore{
		team:     &db.Team{ID: "team-1", Slug: "acme"},
		apps:     []db.App{newLiveApp("alpha", "https://github.com/foo/bar", "main")},
		inFlight: map[string]bool{},
	}
	trigger := &fakeTrigger{}
	h := githubwebhook.NewPushHandler(store, trigger)

	body := mustEncodePush(t, basePush())
	out, err := h.Handle(context.Background(), "delivery-1", "trace-1", body)
	if err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if !out.Acted || len(out.Triggered) != 1 {
		t.Fatalf("unexpected outcome = %+v", out)
	}
	if len(trigger.calls) != 1 {
		t.Fatalf("trigger calls = %d, want 1", len(trigger.calls))
	}
	got := trigger.calls[0]
	if got.Source != redeploy.SourceWebhook || got.WebhookDeliveryID != "delivery-1" || got.CommitSHA != "abc123" || got.TraceID != "trace-1" {
		t.Fatalf("trigger args = %+v", got)
	}
	if len(store.auditEntries) == 0 || store.auditEntries[0].action != "github_webhook_push_triggered" {
		t.Fatalf("audit not recorded: %+v", store.auditEntries)
	}
}

func TestPushHandlerSkipsPausedApp(t *testing.T) {
	app := newLiveApp("alpha", "https://github.com/foo/bar", "main")
	paused := "paused"
	app.Status = &paused
	store := &fakeStore{
		team:     &db.Team{ID: "team-1", Slug: "acme"},
		apps:     []db.App{app},
		inFlight: map[string]bool{},
	}
	// We still expect FindLiveAppsByRepoAndBranch to filter to live, but
	// for unit testing we keep the paused app in the slice to exercise
	// the inline guard.
	trigger := &fakeTrigger{}
	h := githubwebhook.NewPushHandler(store, trigger)
	body := mustEncodePush(t, basePush())
	out, err := h.Handle(context.Background(), "delivery-1", "", body)
	if err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if len(trigger.calls) != 0 {
		t.Fatalf("trigger should not run for paused app")
	}
	if len(out.Skipped) != 1 || out.Skipped[0].Reason != githubwebhook.SkipReasonPaused {
		t.Fatalf("skipped = %+v", out.Skipped)
	}
}

func TestPushHandlerSkipsInFlight(t *testing.T) {
	store := &fakeStore{
		team:     &db.Team{ID: "team-1", Slug: "acme"},
		apps:     []db.App{newLiveApp("alpha", "https://github.com/foo/bar", "main")},
		inFlight: map[string]bool{"app-alpha": true},
	}
	trigger := &fakeTrigger{}
	h := githubwebhook.NewPushHandler(store, trigger)
	body := mustEncodePush(t, basePush())
	out, err := h.Handle(context.Background(), "delivery-1", "", body)
	if err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if len(trigger.calls) != 0 {
		t.Fatalf("trigger should not run when app is in-flight")
	}
	if len(out.Skipped) != 1 || out.Skipped[0].Reason != githubwebhook.SkipReasonInFlight {
		t.Fatalf("skipped = %+v", out.Skipped)
	}
}

func TestPushHandlerIgnoresDeletedBranchAndTagPush(t *testing.T) {
	store := &fakeStore{team: &db.Team{ID: "team-1", Slug: "acme"}}
	trigger := &fakeTrigger{}
	h := githubwebhook.NewPushHandler(store, trigger)

	deleted := basePush()
	deleted.Deleted = true
	out, _ := h.Handle(context.Background(), "delivery-del", "", mustEncodePush(t, deleted))
	if out.Acted || out.Reason != "branch_deleted" {
		t.Fatalf("deleted branch outcome = %+v", out)
	}

	tag := basePush()
	tag.Ref = "refs/tags/v1"
	out, _ = h.Handle(context.Background(), "delivery-tag", "", mustEncodePush(t, tag))
	if out.Acted || out.Reason != "non_branch_ref" {
		t.Fatalf("tag push outcome = %+v", out)
	}

	if len(trigger.calls) != 0 {
		t.Fatalf("trigger must not run for deleted/tag pushes")
	}
}

func TestPushHandlerMultiAppFanOut(t *testing.T) {
	store := &fakeStore{
		team: &db.Team{ID: "team-1", Slug: "acme"},
		apps: []db.App{
			newLiveApp("alpha", "https://github.com/foo/bar", "main"),
			newLiveApp("beta", "https://github.com/foo/bar", "main"),
		},
		inFlight: map[string]bool{},
	}
	trigger := &fakeTrigger{}
	h := githubwebhook.NewPushHandler(store, trigger)
	out, err := h.Handle(context.Background(), "delivery-fan", "trace-fan", mustEncodePush(t, basePush()))
	if err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if !out.Acted || len(out.Triggered) != 2 {
		t.Fatalf("expected fan-out, got %+v", out)
	}
	if len(trigger.calls) != 2 {
		t.Fatalf("trigger calls = %d, want 2", len(trigger.calls))
	}
}

func TestPushHandlerNoTeamForInstallation(t *testing.T) {
	store := &fakeStore{}
	trigger := &fakeTrigger{}
	h := githubwebhook.NewPushHandler(store, trigger)
	out, _ := h.Handle(context.Background(), "delivery-1", "", mustEncodePush(t, basePush()))
	if out.Reason != "team_not_found" || len(trigger.calls) != 0 {
		t.Fatalf("expected team_not_found, got %+v", out)
	}
}
