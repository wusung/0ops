package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
)

type fakeStore struct {
	token      db.CliToken
	team       db.Team
	role       string
	apps       []db.App
	memberRows []db.Member
	members    bool
	hasOwner   bool
	previews   map[string]db.Preview
}

func (f *fakeStore) FindCliTokenByHash(ctx context.Context, tokenHash string) (db.CliToken, error) {
	if tokenHash != f.token.TokenHash {
		return db.CliToken{}, errors.New("not found")
	}
	return f.token, nil
}

func (f *fakeStore) ResolveTeamBySlug(ctx context.Context, slug string) (db.Team, error) {
	if slug != f.team.Slug {
		return db.Team{}, errors.New("not found")
	}
	return f.team, nil
}

func (f *fakeStore) CheckTeamMembership(ctx context.Context, teamID string, userID string) (bool, error) {
	return f.members && teamID == f.team.ID && userID == f.token.OwnerUserID, nil
}

func (f *fakeStore) GetTeamMembershipRole(ctx context.Context, teamID string, userID string) (string, error) {
	if !f.members || teamID != f.team.ID || userID != f.token.OwnerUserID {
		return "", errors.New("not found")
	}
	return f.role, nil
}

func (f *fakeStore) ListUserTeams(ctx context.Context, userID string, limit int32, afterSlug *string) ([]db.TeamMembership, error) {
	if userID != f.token.OwnerUserID || !f.members {
		return nil, nil
	}
	return []db.TeamMembership{{
		Team:   db.Team{ID: f.team.ID, Slug: f.team.Slug, Name: f.team.Name, Plan: f.team.Plan},
		UserID: f.token.OwnerUserID,
		Role:   f.role,
	}}, nil
}

func (f *fakeStore) ListTeamApps(ctx context.Context, teamID string, limit int32, afterID *string) ([]db.App, error) {
	if teamID != f.team.ID {
		return nil, errors.New("team mismatch")
	}
	out := make([]db.App, 0, len(f.apps))
	for _, app := range f.apps {
		if afterID != nil && app.ID <= *afterID {
			continue
		}
		out = append(out, app)
		if int32(len(out)) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeStore) GetTeamAppBySlug(ctx context.Context, teamID string, slug string) (db.App, error) {
	if teamID != f.team.ID {
		return db.App{}, errors.New("team mismatch")
	}
	for _, app := range f.apps {
		if app.Slug == slug {
			return app, nil
		}
	}
	return db.App{}, pgx.ErrNoRows
}

func (f *fakeStore) HasAnyOwner(ctx context.Context) (bool, error) { return f.hasOwner, nil }

func (f *fakeStore) BootstrapOwner(ctx context.Context, params db.BootstrapOwnerParams) (string, string, error) {
	if f.hasOwner {
		return "", "", db.ErrBootstrapAlreadyDone
	}
	f.hasOwner = true
	return "team-bootstrap", "user-bootstrap", nil
}

func (f *fakeStore) ListTeamMembers(ctx context.Context, teamID string) ([]db.Member, error) {
	return append([]db.Member(nil), f.memberRows...), nil
}

func (f *fakeStore) CreatePreview(ctx context.Context, teamID, actorUserID, action string, args json.RawMessage, summary string) (db.Preview, error) {
	p := db.Preview{
		ID:          "preview-1",
		TeamID:      teamID,
		ActorUserID: actorUserID,
		Action:      action,
		Args:        args,
		ExpiresAt:   time.Now().UTC().Add(10 * time.Minute),
	}
	f.previews[p.ID] = p
	return p, nil
}

func (f *fakeStore) GetPreview(ctx context.Context, previewID string) (db.Preview, error) {
	p, ok := f.previews[previewID]
	if !ok {
		return db.Preview{}, db.ErrPreviewNotFound
	}
	return p, nil
}

func (f *fakeStore) ConsumePreview(ctx context.Context, previewID string) error { return nil }

func (f *fakeStore) InviteMember(ctx context.Context, params db.InviteMemberParams) (db.Member, error) {
	now := time.Now().UTC()
	member := db.Member{
		UserID:      "user-new",
		GithubLogin: params.GithubLogin,
		Email:       params.Email,
		Role:        params.Role,
		InvitedAt:   &now,
		JoinedAt:    &now,
	}
	f.memberRows = append(f.memberRows, member)
	return member, nil
}

func (f *fakeStore) RemoveMember(ctx context.Context, teamID, actorUserID, targetUserID string) error {
	return nil
}

func (f *fakeStore) IsToolGranted(ctx context.Context, teamID, userID, toolID string) (bool, error) {
	return false, nil
}

func (f *fakeStore) ListGrantedTools(ctx context.Context, teamID, userID string) ([]string, error) {
	return []string{}, nil
}

func (f *fakeStore) UpsertToolGrant(ctx context.Context, teamID, userID, toolID string, allowed bool, grantedByActorID *string) error {
	return nil
}

func (f *fakeStore) RevokeToolGrant(ctx context.Context, teamID, userID, toolID string) error {
	return nil
}

func (f *fakeStore) ListAllUserGrants(ctx context.Context, teamID, userID string) ([]db.ToolGrant, error) {
	return []db.ToolGrant{}, nil
}

func TestNewRouterListApps(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).ListApps(context.Background(), store.team.Slug, 50, "")
	if err != nil {
		t.Fatalf("ListApps() error = %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(out.Items))
	}
}

func TestNewRouterGetApp(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).GetApp(context.Background(), store.team.Slug, "alpha")
	if err != nil {
		t.Fatalf("GetApp() error = %v", err)
	}
	if out.Slug != "alpha" {
		t.Fatalf("Slug = %q, want alpha", out.Slug)
	}
}

func TestBootstrapOwnerOneShot(t *testing.T) {
	store, _ := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, "")
	_, err := client.BootstrapOwner(context.Background(), dto.BootstrapOwnerRequest{
		TeamSlug:    "acme-bootstrap",
		TeamName:    "Acme Bootstrap",
		GithubLogin: "owner-login",
	})
	if err != nil {
		t.Fatalf("BootstrapOwner() first call error = %v", err)
	}
	_, err = client.BootstrapOwner(context.Background(), dto.BootstrapOwnerRequest{
		TeamSlug:    "acme-bootstrap",
		TeamName:    "Acme Bootstrap",
		GithubLogin: "owner-login",
	})
	if err == nil || !strings.Contains(err.Error(), "bootstrap_already_done") {
		t.Fatalf("second bootstrap error = %v, want bootstrap_already_done", err)
	}
}

func TestMembersPreviewInviteAndInvite(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	preview, err := client.PreviewInviteMember(context.Background(), store.team.Slug, dto.InviteMemberRequest{
		GithubLogin: strPtr("new-member"),
		Role:        "member",
	})
	if err != nil {
		t.Fatalf("PreviewInviteMember() error = %v", err)
	}
	if preview.PreviewID == "" {
		t.Fatal("expected preview id")
	}
	_, err = client.InviteMember(context.Background(), store.team.Slug, dto.ConfirmInviteMemberRequest{
		PreviewID: preview.PreviewID,
	})
	if err != nil {
		t.Fatalf("InviteMember() error = %v", err)
	}
}

func newFakeStore() (*fakeStore, string) {
	token := "dev-token"
	return &fakeStore{
		token: db.CliToken{
			ID:          "token-1",
			OwnerUserID: "user-1",
			TeamID:      "team-1",
			TokenHash:   auth.HashBearerToken(token),
			Scopes:      []string{"apps:read", "teams:read", "members:manage"},
		},
		team:    db.Team{ID: "team-1", Slug: "acme", Name: "Acme", Plan: "starter"},
		role:    "admin",
		members: true,
		apps: []db.App{
			{ID: "1", TeamID: "team-1", Slug: "alpha", Name: strPtr("Alpha"), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
			{ID: "2", TeamID: "team-1", Slug: "beta", Name: strPtr("Beta"), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		},
		memberRows: []db.Member{{UserID: "user-1", GithubLogin: strPtr("owner"), Role: "owner"}},
		previews:   map[string]db.Preview{},
	}, token
}

func strPtr(v string) *string { return &v }
