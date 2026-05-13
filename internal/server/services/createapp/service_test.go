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
	preview     db.Preview
	app         db.App
	createCalls int
	consumeArgs []json.RawMessage
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

type noopK3s struct{}

func (noopK3s) EnsureNamespace(context.Context, string, string, string) (string, error) {
	return "team-free", nil
}

type noopCF struct{}

func (noopCF) RouteAppToDomain(context.Context, string, string, string) (string, error) {
	return "nextdemo.winshare.tw", nil
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

func TestCreateAppLifecycle(t *testing.T) {
	got := CreateAppLifecycle()
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
