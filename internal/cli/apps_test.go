package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/server/auth"
	serverpkg "github.com/winshare/zeroops/internal/server"
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

func (f cliFakeStore) ListTeamApps(ctx context.Context, teamID string, limit int32, afterSlug string) ([]db.App, error) {
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
	if !strings.Contains(stdout.String(), `"slug":"alpha"`) {
		t.Fatalf("stdout = %s", stdout.String())
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
