package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	serverpkg "github.com/winshare/zeroops/internal/server"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
)

type cliFakeStore struct {
	token   db.CliToken
	team    db.Team
	role    string
	apps    []db.App
	domains []db.DomainBinding
	deploys []db.DeployRun
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
func (f cliFakeStore) ListUserTeams(ctx context.Context, userID string, limit int32, afterSlug *string) ([]db.TeamMembership, error) {
	return []db.TeamMembership{{Team: f.team, UserID: f.token.OwnerUserID, Role: f.role}}, nil
}
func (f cliFakeStore) ListTeamApps(ctx context.Context, teamID string, limit int32, afterID *string) ([]db.App, error) {
	return f.apps, nil
}
func (f cliFakeStore) GetTeamAppBySlug(ctx context.Context, teamID string, slug string) (db.App, error) {
	for _, a := range f.apps {
		if a.Slug == slug {
			return a, nil
		}
	}
	return db.App{}, pgx.ErrNoRows
}
func (f cliFakeStore) ListDomainsByAppSlug(ctx context.Context, teamID string, appSlug string) ([]db.DomainBinding, error) {
	out := make([]db.DomainBinding, 0)
	for _, item := range f.domains {
		if item.AppSlug == appSlug {
			out = append(out, item)
		}
	}
	return out, nil
}
func (f cliFakeStore) GetLatestDeployByAppSlug(ctx context.Context, teamID string, appSlug string) (db.DeployRun, error) {
	for _, row := range f.deploys {
		if row.AppSlug == appSlug {
			return row, nil
		}
	}
	return db.DeployRun{}, pgx.ErrNoRows
}
func (f cliFakeStore) ListDeployLogLines(ctx context.Context, teamID string, appSlug string, limit int) ([]db.DeployLogLine, error) {
	row, err := f.GetLatestDeployByAppSlug(ctx, teamID, appSlug)
	if err != nil {
		return nil, err
	}
	return append([]db.DeployLogLine(nil), row.LogLines...), nil
}
func (f cliFakeStore) HasAnyOwner(ctx context.Context) (bool, error) { return false, nil }
func (f cliFakeStore) BootstrapOwner(ctx context.Context, params db.BootstrapOwnerParams) (string, string, error) {
	return "team-bootstrap", "user-bootstrap", nil
}
func (f cliFakeStore) ListTeamMembers(ctx context.Context, teamID string) ([]db.Member, error) {
	return []db.Member{{UserID: "user-1", GithubLogin: strPtr("owner"), Role: "owner"}}, nil
}
func (f cliFakeStore) CreatePreview(ctx context.Context, teamID, actorUserID, action string, args json.RawMessage, summary string) (db.Preview, error) {
	return db.Preview{ID: "preview-1", TeamID: teamID, ActorUserID: actorUserID, Action: action, Args: args, ExpiresAt: time.Now().UTC().Add(time.Minute)}, nil
}
func (f cliFakeStore) GetPreview(ctx context.Context, previewID string) (db.Preview, error) {
	return db.Preview{ID: previewID, TeamID: f.team.ID, ActorUserID: f.token.OwnerUserID, Action: "invite_member", Args: []byte(`{"github_login":"newbie","role":"member"}`), ExpiresAt: time.Now().UTC().Add(time.Minute)}, nil
}
func (f cliFakeStore) ConsumePreview(ctx context.Context, previewID string) error { return nil }
func (f cliFakeStore) InviteMember(ctx context.Context, params db.InviteMemberParams) (db.Member, error) {
	now := time.Now().UTC()
	return db.Member{UserID: "user-new", GithubLogin: params.GithubLogin, Email: params.Email, Role: params.Role, InvitedAt: &now, JoinedAt: &now}, nil
}
func (f cliFakeStore) RemoveMember(ctx context.Context, teamID, actorUserID, targetUserID string) error {
	return nil
}

func TestAppsListCommand(t *testing.T) {
	store, token := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"apps", "list", "--team", store.team.Slug, "--host", srv.URL, "--token", token, "--output", "json"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestAppsGetCommand(t *testing.T) {
	store, token := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"apps", "get", "alpha", "--team", store.team.Slug, "--host", srv.URL, "--token", token, "--output", "json"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRepoInspectCommand(t *testing.T) {
	store, token := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"repo", "inspect", "alpha", "--team", store.team.Slug, "--host", srv.URL, "--token", token, "--output", "json"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestDeploysStatusAndLogsCommand(t *testing.T) {
	store, token := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)

	statusCmd := NewRootCommand()
	statusCmd.SetArgs([]string{"deploys", "status", "alpha", "--team", store.team.Slug, "--host", srv.URL, "--token", token, "--output", "json"})
	var statusOut bytes.Buffer
	statusCmd.SetOut(&statusOut)
	statusCmd.SetErr(&statusOut)
	if err := statusCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("status Execute() error = %v", err)
	}

	logsCmd := NewRootCommand()
	logsCmd.SetArgs([]string{"deploys", "logs", "alpha", "--team", store.team.Slug, "--host", srv.URL, "--token", token, "--output", "json"})
	var logsOut bytes.Buffer
	logsCmd.SetOut(&logsOut)
	logsCmd.SetErr(&logsOut)
	if err := logsCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("logs Execute() error = %v", err)
	}
}

func TestDomainsListCommand(t *testing.T) {
	store, token := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"domains", "list", "alpha", "--team", store.team.Slug, "--host", srv.URL, "--token", token, "--output", "json"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestMembersListCommand(t *testing.T) {
	store, token := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"members", "list", "--team", store.team.Slug, "--host", srv.URL, "--token", token, "--output", "json"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestAdminBootstrapOwnerCommand(t *testing.T) {
	store, _ := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"admin", "bootstrap-owner", "--host", srv.URL, "--team-slug", "bootstrap-acme", "--team-name", "Bootstrap Acme", "--github-login", "owner-login", "--output", "json"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func newCLIFakeStore() (cliFakeStore, string) {
	token := "dev-token"
	return cliFakeStore{
		token: db.CliToken{ID: "token-1", OwnerUserID: "user-1", TeamID: "team-1", TokenHash: auth.HashBearerToken(token), Scopes: []string{"apps:read", "teams:read", "members:manage"}},
		team:  db.Team{ID: "team-1", Slug: "acme", Name: "Acme", Plan: "starter"},
		role:  "admin", members: true,
		apps: []db.App{
			{ID: "1", TeamID: "team-1", Slug: "alpha", Name: strPtr("Alpha"), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
			{ID: "2", TeamID: "team-1", Slug: "beta", Name: strPtr("Beta"), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		},
		domains: []db.DomainBinding{
			{ID: "d1", TeamID: "team-1", AppID: "1", AppSlug: "alpha", Hostname: "alpha.example.com", Kind: strPtr("primary"), Verified: true},
		},
		deploys: []db.DeployRun{
			{
				ID:      "deploy-1",
				TeamID:  "team-1",
				AppID:   "1",
				AppSlug: "alpha",
				Status:  "succeeded",
				LogLines: []db.DeployLogLine{
					{Timestamp: time.Now().UTC().Add(-time.Minute), Message: "build started"},
					{Timestamp: time.Now().UTC(), Message: "deploy succeeded"},
				},
			},
		},
	}, token
}

func strPtr(v string) *string { return &v }
