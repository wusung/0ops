package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
)

type fakeStore struct {
	token   db.CliToken
	team    db.Team
	role    string
	apps    []db.App
	members bool
}

func (f fakeStore) FindCliTokenByHash(ctx context.Context, tokenHash string) (db.CliToken, error) {
	if tokenHash != f.token.TokenHash {
		return db.CliToken{}, errors.New("not found")
	}
	return f.token, nil
}

func (f fakeStore) ResolveTeamBySlug(ctx context.Context, slug string) (db.Team, error) {
	if slug != f.team.Slug {
		return db.Team{}, errors.New("not found")
	}
	return f.team, nil
}

func (f fakeStore) CheckTeamMembership(ctx context.Context, teamID string, userID string) (bool, error) {
	return f.members && teamID == f.team.ID && userID == f.token.OwnerUserID, nil
}

func (f fakeStore) GetTeamMembershipRole(ctx context.Context, teamID string, userID string) (string, error) {
	if !f.members || teamID != f.team.ID || userID != f.token.OwnerUserID {
		return "", errors.New("not found")
	}
	return f.role, nil
}

func (f fakeStore) ListTeamApps(ctx context.Context, teamID string, limit int32, afterSlug string) ([]db.App, error) {
	if teamID != f.team.ID {
		return nil, errors.New("team mismatch")
	}
	out := make([]db.App, 0, len(f.apps))
	for _, app := range f.apps {
		if afterSlug != "" && app.Slug <= afterSlug {
			continue
		}
		out = append(out, app)
		if int32(len(out)) >= limit {
			break
		}
	}
	return out, nil
}

func TestNewRouterListApps(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	out, err := client.ListApps(context.Background(), store.team.Slug, 50, "")
	if err != nil {
		t.Fatalf("ListApps() error = %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(out.Items))
	}
	if out.Items[0].Slug != "alpha" || out.Items[1].Slug != "beta" {
		t.Fatalf("unexpected items: %#v", out.Items)
	}
}

func TestNewRouterListAppsRejectsWrongTeam(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/teams/wrong/apps", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}

func TestListAppsPagination(t *testing.T) {
	store, token := newFakeStore()
	store.apps = append(store.apps, db.App{
		ID:        "4",
		TeamID:    store.team.ID,
		Slug:      "gamma",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})

	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	out, err := client.ListApps(context.Background(), store.team.Slug, 2, "")
	if err != nil {
		t.Fatalf("ListApps() error = %v", err)
	}
	if out.NextCursor == nil || *out.NextCursor != "beta" {
		t.Fatalf("next_cursor = %#v, want beta", out.NextCursor)
	}
}

func newFakeStore() (fakeStore, string) {
	token := "dev-token"
	store := fakeStore{
		token: db.CliToken{
			ID:          "token-1",
			OwnerUserID: "user-1",
			TeamID:      "team-1",
			TokenHash:   auth.HashBearerToken(token),
			Scopes:      []string{"apps:read"},
		},
		team: db.Team{
			ID:   "team-1",
			Slug: "acme",
			Name: "Acme",
		},
		role:    "viewer",
		members: true,
		apps: []db.App{
			{
				ID:        "1",
				TeamID:    "team-1",
				Slug:      "alpha",
				Name:      strPtr("Alpha"),
				Status:    strPtr("ready"),
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			},
			{
				ID:        "2",
				TeamID:    "team-1",
				Slug:      "beta",
				Name:      strPtr("Beta"),
				Status:    strPtr("ready"),
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			},
		},
	}
	return store, token
}

func strPtr(v string) *string { return &v }

func TestRouterJSONShape(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).ListApps(context.Background(), store.team.Slug, 50, "")
	if err != nil {
		t.Fatalf("ListApps() error = %v", err)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !json.Valid(raw) {
		t.Fatal("response is not valid json")
	}
	var decoded dto.ListAppsResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
}
