package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
)

func TestNewRouterListTeams(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).ListTeams(context.Background())
	if err != nil {
		t.Fatalf("ListTeams() error = %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(out.Items))
	}
	if out.Items[0].TeamSlug != store.team.Slug {
		t.Fatalf("team_slug = %q, want %q", out.Items[0].TeamSlug, store.team.Slug)
	}
	if out.Items[0].Role != store.role {
		t.Fatalf("role = %q, want %q", out.Items[0].Role, store.role)
	}
	if out.Items[0].Plan != store.team.Plan {
		t.Fatalf("plan = %q, want %q", out.Items[0].Plan, store.team.Plan)
	}
}

func TestNewRouterListTeamsRequiresTeamsReadScope(t *testing.T) {
	store, token := newFakeStore()
	store.token.Scopes = []string{"apps:read"}
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/me/teams", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", res.StatusCode)
	}
}

func TestListTeamsResponseJSONShape(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).ListTeams(context.Background())
	if err != nil {
		t.Fatalf("ListTeams() error = %v", err)
	}

	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !json.Valid(raw) {
		t.Fatal("response is not valid json")
	}

	var decoded dto.ListTeamsResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
}

func TestNewRouterListTeamsReturnsAllPages(t *testing.T) {
	baseStore, token := newFakeStore()
	store := &paginatedTeamsStore{
		fakeStore: *baseStore,
	}
	for i := 1; i <= 205; i++ {
		store.teams = append(store.teams, db.TeamMembership{
			Team: db.Team{
				ID:   fmt.Sprintf("team-%03d", i),
				Slug: fmt.Sprintf("team-%03d", i),
				Name: fmt.Sprintf("Team %03d", i),
				Plan: "starter",
			},
			UserID: baseStore.token.OwnerUserID,
			Role:   "viewer",
		})
	}

	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).ListTeams(context.Background())
	if err != nil {
		t.Fatalf("ListTeams() error = %v", err)
	}
	if len(out.Items) != 205 {
		t.Fatalf("len(items) = %d, want 205", len(out.Items))
	}
	if out.Items[0].TeamSlug != "team-001" || out.Items[204].TeamSlug != "team-205" {
		t.Fatalf("unexpected items: first=%q last=%q", out.Items[0].TeamSlug, out.Items[204].TeamSlug)
	}
	if store.calls != 2 {
		t.Fatalf("ListUserTeams() calls = %d, want 2", store.calls)
	}
}

type paginatedTeamsStore struct {
	fakeStore
	teams []db.TeamMembership
	calls int
}

func (f *paginatedTeamsStore) ListUserTeams(_ context.Context, userID string, limit int32, afterSlug *string) ([]db.TeamMembership, error) {
	if userID != f.token.OwnerUserID || !f.members {
		return nil, nil
	}

	f.calls++

	out := make([]db.TeamMembership, 0, limit)
	for _, team := range f.teams {
		if afterSlug != nil && team.Team.Slug <= *afterSlug {
			continue
		}
		out = append(out, team)
		if int32(len(out)) >= limit { //nolint:gosec // len() fits in int32
			break
		}
	}

	return out, nil
}
