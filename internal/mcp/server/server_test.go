package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	serverpkg "github.com/winshare/zeroops/internal/server"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared"
)

func TestImplementationUsesSharedVersion(t *testing.T) {
	impl := Implementation()
	if impl.Name != "0ops-mcp" || impl.Version != shared.Version {
		t.Fatalf("implementation mismatch: %#v", impl)
	}
}

func TestListAppsAndGetAppTools(t *testing.T) {
	store, token := newMCPFakeStore()
	backend := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(backend.Close)
	t.Setenv("OPS_HOST", backend.URL)
	t.Setenv("OPS_BEARER_TOKEN", token)

	srv := New(slog.Default())
	sTransport, cTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx, sTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	session, err := client.Connect(ctx, cTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "list_apps", Arguments: map[string]any{"team_slug": store.team.Slug}})
	if err != nil {
		t.Fatalf("list_apps: %v", err)
	}
	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "get_app", Arguments: map[string]any{"team_slug": store.team.Slug, "app_slug": "alpha"}})
	if err != nil {
		t.Fatalf("get_app: %v", err)
	}
	cancel()
	<-errCh
}

func TestListMembersTool(t *testing.T) {
	store, token := newMCPFakeStore()
	backend := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(backend.Close)
	t.Setenv("OPS_HOST", backend.URL)
	t.Setenv("OPS_BEARER_TOKEN", token)

	srv := New(slog.Default())
	sTransport, cTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx, sTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	session, err := client.Connect(ctx, cTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "list_members", Arguments: map[string]any{"team_slug": store.team.Slug}})
	if err != nil {
		t.Fatalf("list_members: %v", err)
	}
	cancel()
	<-errCh
}

func TestReadVerticalSliceTools(t *testing.T) {
	store, token := newMCPFakeStore()
	backend := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(backend.Close)
	t.Setenv("OPS_HOST", backend.URL)
	t.Setenv("OPS_BEARER_TOKEN", token)

	srv := New(slog.Default())
	sTransport, cTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx, sTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	session, err := client.Connect(ctx, cTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "inspect_repo", Arguments: map[string]any{"team_slug": store.team.Slug, "app_slug": "alpha"}})
	if err != nil {
		t.Fatalf("inspect_repo: %v", err)
	}
	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "get_deploy_status", Arguments: map[string]any{"team_slug": store.team.Slug, "app_slug": "alpha"}})
	if err != nil {
		t.Fatalf("get_deploy_status: %v", err)
	}
	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "tail_logs", Arguments: map[string]any{"team_slug": store.team.Slug, "app_slug": "alpha", "limit": 10}})
	if err != nil {
		t.Fatalf("tail_logs: %v", err)
	}
	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "list_domains", Arguments: map[string]any{"team_slug": store.team.Slug, "app_slug": "alpha"}})
	if err != nil {
		t.Fatalf("list_domains: %v", err)
	}
	cancel()
	<-errCh
}

type mcpFakeStore struct {
	token      db.CliToken
	team       db.Team
	role       string
	apps       []db.App
	domains    []db.DomainBinding
	deploys    []db.DeployRun
	memberRows []db.Member
	members    bool
}

func (f mcpFakeStore) FindCliTokenByHash(ctx context.Context, tokenHash string) (db.CliToken, error) {
	if tokenHash != f.token.TokenHash {
		return db.CliToken{}, os.ErrNotExist
	}
	return f.token, nil
}
func (f mcpFakeStore) ResolveTeamBySlug(ctx context.Context, slug string) (db.Team, error) {
	if slug != f.team.Slug {
		return db.Team{}, os.ErrNotExist
	}
	return f.team, nil
}
func (f mcpFakeStore) CheckTeamMembership(ctx context.Context, teamID string, userID string) (bool, error) {
	return f.members && teamID == f.team.ID && userID == f.token.OwnerUserID, nil
}
func (f mcpFakeStore) GetTeamMembershipRole(ctx context.Context, teamID string, userID string) (string, error) {
	return f.role, nil
}
func (f mcpFakeStore) ListUserTeams(ctx context.Context, userID string, limit int32, afterSlug *string) ([]db.TeamMembership, error) {
	return []db.TeamMembership{{Team: f.team, UserID: f.token.OwnerUserID, Role: f.role}}, nil
}
func (f mcpFakeStore) ListTeamApps(ctx context.Context, teamID string, limit int32, afterID *string) ([]db.App, error) {
	return f.apps, nil
}
func (f mcpFakeStore) GetTeamAppBySlug(ctx context.Context, teamID string, slug string) (db.App, error) {
	for _, a := range f.apps {
		if a.Slug == slug {
			return a, nil
		}
	}
	return db.App{}, pgx.ErrNoRows
}
func (f mcpFakeStore) ListDomainsByAppSlug(ctx context.Context, teamID string, appSlug string) ([]db.DomainBinding, error) {
	out := make([]db.DomainBinding, 0)
	for _, item := range f.domains {
		if item.AppSlug == appSlug {
			out = append(out, item)
		}
	}
	return out, nil
}
func (f mcpFakeStore) GetLatestDeployByAppSlug(ctx context.Context, teamID string, appSlug string) (db.DeployRun, error) {
	for _, row := range f.deploys {
		if row.AppSlug == appSlug {
			return row, nil
		}
	}
	return db.DeployRun{}, pgx.ErrNoRows
}
func (f mcpFakeStore) ListDeployLogLines(ctx context.Context, teamID string, appSlug string, limit int) ([]db.DeployLogLine, error) {
	row, err := f.GetLatestDeployByAppSlug(ctx, teamID, appSlug)
	if err != nil {
		return nil, err
	}
	return append([]db.DeployLogLine(nil), row.LogLines...), nil
}
func (f mcpFakeStore) HasAnyOwner(ctx context.Context) (bool, error) { return false, nil }
func (f mcpFakeStore) BootstrapOwner(ctx context.Context, params db.BootstrapOwnerParams) (string, string, error) {
	return "team-bootstrap", "user-bootstrap", nil
}
func (f mcpFakeStore) ListTeamMembers(ctx context.Context, teamID string) ([]db.Member, error) {
	return f.memberRows, nil
}
func (f mcpFakeStore) CreatePreview(ctx context.Context, teamID, actorUserID, action string, args json.RawMessage, summary string) (db.Preview, error) {
	return db.Preview{ID: "preview-1", TeamID: teamID, ActorUserID: actorUserID, Action: action, Args: args, ExpiresAt: time.Now().UTC().Add(time.Minute)}, nil
}
func (f mcpFakeStore) GetPreview(ctx context.Context, previewID string) (db.Preview, error) {
	return db.Preview{ID: previewID, TeamID: f.team.ID, ActorUserID: f.token.OwnerUserID, Action: "invite_member", Args: []byte(`{"github_login":"newbie","role":"member"}`), ExpiresAt: time.Now().UTC().Add(time.Minute)}, nil
}
func (f mcpFakeStore) ConsumePreview(ctx context.Context, previewID string) error { return nil }
func (f mcpFakeStore) InviteMember(ctx context.Context, params db.InviteMemberParams) (db.Member, error) {
	now := time.Now().UTC()
	return db.Member{UserID: "user-new", Role: params.Role, InvitedAt: &now, JoinedAt: &now}, nil
}
func (f mcpFakeStore) RemoveMember(ctx context.Context, teamID, actorUserID, targetUserID string) error {
	return nil
}

func newMCPFakeStore() (mcpFakeStore, string) {
	token := "dev-token"
	return mcpFakeStore{
		token: db.CliToken{ID: "token-1", OwnerUserID: "user-1", TeamID: "team-1", TokenHash: auth.HashBearerToken(token), Scopes: []string{"apps:read", "teams:read", "members:manage"}},
		team:  db.Team{ID: "team-1", Slug: "acme", Name: "Acme", Plan: "starter"},
		role:  "admin", members: true,
		apps:    []db.App{{ID: "1", TeamID: "team-1", Slug: "alpha"}, {ID: "2", TeamID: "team-1", Slug: "beta"}},
		domains: []db.DomainBinding{{ID: "d1", TeamID: "team-1", AppID: "1", AppSlug: "alpha", Hostname: "alpha.example.com", Kind: strPtr("primary"), Verified: true}},
		deploys: []db.DeployRun{{
			ID:      "deploy-1",
			TeamID:  "team-1",
			AppID:   "1",
			AppSlug: "alpha",
			Status:  "succeeded",
			LogLines: []db.DeployLogLine{
				{Timestamp: time.Now().UTC().Add(-time.Minute), Message: "build started"},
				{Timestamp: time.Now().UTC(), Message: "deploy succeeded"},
			},
		}},
		memberRows: []db.Member{{UserID: "user-1", GithubLogin: strPtr("owner"), Role: "owner"}},
	}, token
}

func strPtr(v string) *string { return &v }
