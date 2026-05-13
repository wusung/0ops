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

type fakeCreateAppStore struct {
	team             db.Team
	apps             []db.App
	previews         map[string]db.Preview
	nextPreviewID    string
	nextAppResult    db.AppCreateResult
	createAppCalls   int
	deleteAppCalls   []string
	consumeCalls     []string
	lastConsumedJSON json.RawMessage
}

func (f *fakeCreateAppStore) ResolveTeamBySlug(_ context.Context, slug string) (db.Team, error) {
	if slug != f.team.Slug {
		return db.Team{}, pgx.ErrNoRows
	}
	return f.team, nil
}

func (f *fakeCreateAppStore) GetTeamAppBySlug(_ context.Context, teamID, slug string) (db.App, error) {
	if teamID != f.team.ID {
		return db.App{}, pgx.ErrNoRows
	}
	for _, app := range f.apps {
		if app.Slug == slug {
			return app, nil
		}
	}
	return db.App{}, pgx.ErrNoRows
}

func (f *fakeCreateAppStore) CreatePreview(_ context.Context, teamID, actorUserID, action string, args json.RawMessage, summary string) (db.Preview, error) {
	id := f.nextPreviewID
	if id == "" {
		id = "preview-1"
	}
	out := db.Preview{
		ID:          id,
		TeamID:      teamID,
		ActorUserID: actorUserID,
		Action:      action,
		Args:        args,
		ExpiresAt:   time.Now().UTC().Add(10 * time.Minute),
	}
	if f.previews == nil {
		f.previews = map[string]db.Preview{}
	}
	f.previews[id] = out
	_ = summary
	return out, nil
}

func (f *fakeCreateAppStore) GetPreview(_ context.Context, previewID string) (db.Preview, error) {
	if preview, ok := f.previews[previewID]; ok {
		return preview, nil
	}
	return db.Preview{}, pgx.ErrNoRows
}

func (f *fakeCreateAppStore) ConsumePreviewWithResult(_ context.Context, previewID string, result json.RawMessage) error {
	preview, ok := f.previews[previewID]
	if !ok {
		return db.ErrPreviewNotFound
	}
	now := time.Now().UTC()
	preview.ConsumedAt = &now
	preview.LastResult = append(json.RawMessage(nil), result...)
	f.previews[previewID] = preview
	f.consumeCalls = append(f.consumeCalls, previewID)
	f.lastConsumedJSON = append(json.RawMessage(nil), result...)
	return nil
}

func (f *fakeCreateAppStore) CreateApp(_ context.Context, params db.AppCreateParams) (db.AppCreateResult, error) {
	f.createAppCalls++
	if f.nextAppResult.AppID != "" {
		f.apps = append(f.apps, db.App{ID: f.nextAppResult.AppID, TeamID: params.TeamID, Slug: params.Slug})
		return f.nextAppResult, nil
	}
	result := db.AppCreateResult{AppID: "app-1", AppSlug: params.Slug, DeployRunID: "deploy-1"}
	f.apps = append(f.apps, db.App{ID: result.AppID, TeamID: params.TeamID, Slug: params.Slug})
	return result, nil
}

func (f *fakeCreateAppStore) DeleteAppByID(_ context.Context, appID string) error {
	f.deleteAppCalls = append(f.deleteAppCalls, appID)
	filtered := f.apps[:0]
	for _, app := range f.apps {
		if app.ID != appID {
			filtered = append(filtered, app)
		}
	}
	f.apps = filtered
	return nil
}

type fakeK3sClient struct {
	namespaceErr error
	quotaErr     error
	limitErr     error
	networkErr   error
	psaErr       error
}

func (f *fakeK3sClient) EnsureNamespace(_ context.Context, _, teamSlug, _ string) (string, error) {
	if f.namespaceErr != nil {
		return "", f.namespaceErr
	}
	return "team-" + teamSlug, nil
}

func (f *fakeK3sClient) EnsureResourceQuota(_ context.Context, _ string, _ string) error {
	return f.quotaErr
}

func (f *fakeK3sClient) EnsureLimitRange(_ context.Context, _ string) error {
	return f.limitErr
}

func (f *fakeK3sClient) EnsureNetworkPolicy(_ context.Context, _ string) error {
	return f.networkErr
}

func (f *fakeK3sClient) PatchNamespacePSA(_ context.Context, _ string) error {
	return f.psaErr
}

func TestPreviewCreateAppReturnsSummaryAndPreview(t *testing.T) {
	store := &fakeCreateAppStore{
		team: db.Team{ID: "team-1", Slug: "acme", Plan: "starter"},
	}
	svc := New(store, nil)

	preview, summary, err := svc.PreviewCreateApp(context.Background(), "acme", "user-1", dto.AppCreateRequest{
		Slug:    "nextdemo",
		RepoURL: "https://github.com/example/nextdemo",
		Ref:     "main",
	})
	if err != nil {
		t.Fatalf("PreviewCreateApp() error = %v", err)
	}
	if summary == "" || preview.ID == "" {
		t.Fatalf("expected summary and preview id, got summary=%q preview=%q", summary, preview.ID)
	}
}

func TestConfirmCreateAppReplaysConsumedPreview(t *testing.T) {
	now := time.Now().UTC()
	response := dto.AppCreateResponse{
		AppID:         "app-1",
		AppSlug:       "nextdemo",
		DeployRunID:   "deploy-1",
		TraceID:       "preview-1",
		SubdomainURL:  "https://nextdemo.winshare.tw",
		InitialDeploy: true,
	}
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	store := &fakeCreateAppStore{
		team: db.Team{ID: "team-1", Slug: "acme", Plan: "starter"},
		previews: map[string]db.Preview{
			"preview-1": {
				ID:          "preview-1",
				TeamID:      "team-1",
				ActorUserID: "user-1",
				Action:      previewAction,
				Args:        json.RawMessage(`{"slug":"nextdemo","repo_url":"https://github.com/example/nextdemo","ref":"main"}`),
				LastResult:  payload,
				ExpiresAt:   now.Add(1 * time.Minute),
				ConsumedAt:  &now,
			},
		},
	}
	svc := New(store, nil)

	out, replayed, err := svc.ConfirmCreateApp(context.Background(), "acme", "user-1", "preview-1", "trace-1")
	if err != nil {
		t.Fatalf("ConfirmCreateApp() error = %v", err)
	}
	if !replayed {
		t.Fatal("expected replayed=true")
	}
	if out.AppID != response.AppID || out.DeployRunID != response.DeployRunID {
		t.Fatalf("unexpected replay response: %+v", out)
	}
	if store.createAppCalls != 0 {
		t.Fatalf("create app should not be called on replay, got %d", store.createAppCalls)
	}
}

func TestConfirmCreateAppCompensatesOnK3sFailure(t *testing.T) {
	store := &fakeCreateAppStore{
		team: db.Team{ID: "team-1", Slug: "acme", Plan: "starter"},
		previews: map[string]db.Preview{
			"preview-1": {
				ID:          "preview-1",
				TeamID:      "team-1",
				ActorUserID: "user-1",
				Action:      previewAction,
				Args:        json.RawMessage(`{"slug":"nextdemo","repo_url":"https://github.com/example/nextdemo","ref":"main"}`),
				ExpiresAt:   time.Now().UTC().Add(1 * time.Minute),
			},
		},
	}
	svc := New(store, &fakeK3sClient{namespaceErr: errors.New("boom")})

	_, _, err := svc.ConfirmCreateApp(context.Background(), "acme", "user-1", "preview-1", "trace-1")
	if err == nil {
		t.Fatal("expected error")
	}
	if len(store.deleteAppCalls) != 1 {
		t.Fatalf("expected compensation delete, got %v", store.deleteAppCalls)
	}
}
