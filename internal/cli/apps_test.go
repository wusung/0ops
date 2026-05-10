package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	serverpkg "github.com/winshare/zeroops/internal/server"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
)

type cliFakeStore struct {
	token   db.CliToken
	team    db.Team
	role    string
	apps    []db.App
	members bool
}

func (f cliFakeStore) FindCliTokenByHash(ctx context.Context, tokenHash string) (db.CliToken, error) {
	if tokenHash != f.token.TokenHash {
		return db.CliToken{}, errors.New("not found")
	}
	return f.token, nil
}

func (f cliFakeStore) ResolveTeamBySlug(ctx context.Context, slug string) (db.Team, error) {
	if slug != f.team.Slug {
		return db.Team{}, errors.New("not found")
	}
	return f.team, nil
}

func (f cliFakeStore) CheckTeamMembership(ctx context.Context, teamID string, userID string) (bool, error) {
	return f.members && teamID == f.team.ID && userID == f.token.OwnerUserID, nil
}

func (f cliFakeStore) GetTeamMembershipRole(ctx context.Context, teamID string, userID string) (string, error) {
	if !f.members || teamID != f.team.ID || userID != f.token.OwnerUserID {
		return "", errors.New("not found")
	}
	return f.role, nil
}

func (f cliFakeStore) ListTeamApps(ctx context.Context, teamID string, limit int32, afterID *string) ([]db.App, error) {
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

func (f cliFakeStore) HasAnyOwner(ctx context.Context) (bool, error) { return false, nil }

func (f cliFakeStore) BootstrapOwner(ctx context.Context, params db.BootstrapOwnerParams) (string, string, error) {
	return "team-bootstrap", "user-bootstrap", nil
}

func (f cliFakeStore) ListTeamMembers(ctx context.Context, teamID string) ([]db.Member, error) {
	return []db.Member{{UserID: "user-1", GithubLogin: strPtr("owner"), Role: "owner"}}, nil
}

func (f cliFakeStore) CreatePreview(ctx context.Context, teamID, actorUserID, action string, args json.RawMessage, summary string) (db.Preview, error) {
	return db.Preview{
		ID:          "preview-1",
		TeamID:      teamID,
		ActorUserID: actorUserID,
		Action:      action,
		Args:        args,
		ExpiresAt:   time.Now().UTC().Add(time.Minute),
	}, nil
}

func (f cliFakeStore) GetPreview(ctx context.Context, previewID string) (db.Preview, error) {
	return db.Preview{
		ID:          previewID,
		TeamID:      f.team.ID,
		ActorUserID: f.token.OwnerUserID,
		Action:      "invite_member",
		Args:        []byte(`{"github_login":"newbie","role":"member"}`),
		ExpiresAt:   time.Now().UTC().Add(time.Minute),
	}, nil
}

func (f cliFakeStore) ConsumePreview(ctx context.Context, previewID string) error { return nil }

func (f cliFakeStore) InviteMember(ctx context.Context, params db.InviteMemberParams) (db.Member, error) {
	now := time.Now().UTC()
	return db.Member{
		UserID:      "user-new",
		GithubLogin: params.GithubLogin,
		Email:       params.Email,
		Role:        params.Role,
		InvitedAt:   &now,
		JoinedAt:    &now,
	}, nil
}

func (f cliFakeStore) RemoveMember(ctx context.Context, teamID, actorUserID, targetUserID string) error {
	return nil
}

func TestAppsListCommand(t *testing.T) {
	store, token := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{
		"apps", "list",
		"--team", store.team.Slug,
		"--host", srv.URL,
		"--token", token,
		"--output", "json",
	})

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	items, ok := out["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("unexpected items payload: %#v", out["items"])
	}
}

func TestAppsListCommandUsesAuthConfig(t *testing.T) {
	store, token := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)

	t.Setenv("OPS_HOST", "")
	t.Setenv("OPS_BEARER_TOKEN", "")
	t.Setenv("OPS_TEAM", "")
	writeAuthFile(t, srv.URL, token, store.team.Slug)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"apps", "list", "--output", "json"})

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	items, ok := out["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("unexpected items payload: %#v", out["items"])
	}
}

func TestMembersListCommand(t *testing.T) {
	store, token := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{
		"members", "list",
		"--team", store.team.Slug,
		"--host", srv.URL,
		"--token", token,
		"--output", "json",
	})

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	items, ok := out["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("unexpected items payload: %#v", out["items"])
	}
}

func TestAdminBootstrapOwnerCommand(t *testing.T) {
	store, _ := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{
		"admin", "bootstrap-owner",
		"--host", srv.URL,
		"--team-slug", "bootstrap-acme",
		"--team-name", "Bootstrap Acme",
		"--github-login", "owner-login",
		"--output", "json",
	})

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if out["team_id"] == nil || out["user_id"] == nil {
		t.Fatalf("unexpected bootstrap payload: %#v", out)
	}
}

func newCLIFakeStore() (cliFakeStore, string) {
	token := "dev-token"
	store := cliFakeStore{
		token: db.CliToken{
			ID:          "token-1",
			OwnerUserID: "user-1",
			TeamID:      "team-1",
			TokenHash:   auth.HashBearerToken(token),
			Scopes:      []string{"apps:read", "members:manage"},
		},
		team: db.Team{
			ID:   "team-1",
			Slug: "acme",
			Name: "Acme",
		},
		role:    "admin",
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

func writeAuthFile(t *testing.T, host, token, team string) {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	path := filepath.Join(dir, "0ops")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	payload := map[string]any{
		"version": 1,
		"tokens": []map[string]any{
			{
				"host":              host,
				"default_team_slug": team,
				"bearer_token":      token,
			},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "auth.json"), data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
