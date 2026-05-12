package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	serverpkg "github.com/winshare/zeroops/internal/server"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/githuboauth"
	"github.com/winshare/zeroops/internal/shared/authconfig"
)

type cliFakeStore struct {
	token   db.CliToken
	tokens  map[string]db.CliToken
	team    db.Team
	role    string
	apps    []db.App
	domains []db.DomainBinding
	deploys []db.DeployRun
	members bool
}

type mockGitHubOAuthClient struct {
	challenge githuboauth.DeviceAuthorization
	user      githuboauth.UserProfile
}

func (m mockGitHubOAuthClient) StartDeviceAuthorization(context.Context) (githuboauth.DeviceAuthorization, error) {
	return m.challenge, nil
}

func (m mockGitHubOAuthClient) ExchangeDeviceCode(context.Context, string) (githuboauth.AccessTokenResponse, error) {
	return githuboauth.AccessTokenResponse{AccessToken: "test-access-token", TokenType: "bearer", Scope: "user:email"}, nil
}

func (m mockGitHubOAuthClient) FetchUser(context.Context, string) (githuboauth.UserProfile, error) {
	return m.user, nil
}

func (f *cliFakeStore) FindCliTokenByID(ctx context.Context, tokenID string) (db.CliToken, error) {
	if tokenID == f.token.ID {
		return f.token, nil
	}
	if tok, ok := f.tokens[tokenID]; ok {
		return tok, nil
	}
	return db.CliToken{}, errors.New("not found")
}
func (f *cliFakeStore) ResolveTeamBySlug(ctx context.Context, slug string) (db.Team, error) {
	if slug != f.team.Slug {
		return db.Team{}, errors.New("not found")
	}
	return f.team, nil
}
func (f *cliFakeStore) CheckTeamMembership(ctx context.Context, teamID string, userID string) (bool, error) {
	return f.members && teamID == f.team.ID && userID == f.token.OwnerUserID, nil
}
func (f *cliFakeStore) GetTeamMembershipRole(ctx context.Context, teamID string, userID string) (string, error) {
	if !f.members || teamID != f.team.ID || userID != f.token.OwnerUserID {
		return "", errors.New("not found")
	}
	return f.role, nil
}
func (f *cliFakeStore) ListUserTeams(ctx context.Context, userID string, limit int32, afterSlug *string) ([]db.TeamMembership, error) {
	return []db.TeamMembership{{Team: f.team, UserID: f.token.OwnerUserID, Role: f.role}}, nil
}
func (f *cliFakeStore) ListTeamApps(ctx context.Context, teamID string, limit int32, afterID *string) ([]db.App, error) {
	return f.apps, nil
}
func (f *cliFakeStore) GetTeamAppBySlug(ctx context.Context, teamID string, slug string) (db.App, error) {
	for _, a := range f.apps {
		if a.Slug == slug {
			return a, nil
		}
	}
	return db.App{}, pgx.ErrNoRows
}
func (f *cliFakeStore) ListDomainsByAppSlug(ctx context.Context, teamID string, appSlug string) ([]db.DomainBinding, error) {
	out := make([]db.DomainBinding, 0)
	for _, item := range f.domains {
		if item.AppSlug == appSlug {
			out = append(out, item)
		}
	}
	return out, nil
}
func (f *cliFakeStore) GetLatestDeployByAppSlug(ctx context.Context, teamID string, appSlug string) (db.DeployRun, error) {
	for _, row := range f.deploys {
		if row.AppSlug == appSlug {
			return row, nil
		}
	}
	return db.DeployRun{}, pgx.ErrNoRows
}
func (f *cliFakeStore) ListDeployLogLines(ctx context.Context, teamID string, appSlug string, limit int) ([]db.DeployLogLine, error) {
	row, err := f.GetLatestDeployByAppSlug(ctx, teamID, appSlug)
	if err != nil {
		return nil, err
	}
	return append([]db.DeployLogLine(nil), row.LogLines...), nil
}
func (f *cliFakeStore) HasAnyOwner(ctx context.Context) (bool, error) { return false, nil }
func (f *cliFakeStore) BootstrapOwner(ctx context.Context, params db.BootstrapOwnerParams) (string, string, error) {
	return "team-bootstrap", "user-bootstrap", nil
}
func (f *cliFakeStore) ListTeamMembers(ctx context.Context, teamID string) ([]db.Member, error) {
	return []db.Member{{UserID: "user-1", GithubLogin: strPtr("owner"), Role: "owner"}}, nil
}

func (f *cliFakeStore) ListTeamTokens(ctx context.Context, teamID string) ([]db.CliToken, error) {
	out := make([]db.CliToken, 0, len(f.tokens))
	for _, tok := range f.tokens {
		if tok.TeamID == teamID && tok.Kind == "pat" {
			out = append(out, tok)
		}
	}
	return out, nil
}
func (f *cliFakeStore) CreatePreview(ctx context.Context, teamID, actorUserID, action string, args json.RawMessage, summary string) (db.Preview, error) {
	return db.Preview{ID: "preview-1", TeamID: teamID, ActorUserID: actorUserID, Action: action, Args: args, ExpiresAt: time.Now().UTC().Add(time.Minute)}, nil
}
func (f *cliFakeStore) GetPreview(ctx context.Context, previewID string) (db.Preview, error) {
	return db.Preview{ID: previewID, TeamID: f.team.ID, ActorUserID: f.token.OwnerUserID, Action: "invite_member", Args: []byte(`{"github_login":"newbie","role":"member"}`), ExpiresAt: time.Now().UTC().Add(time.Minute)}, nil
}
func (f *cliFakeStore) ConsumePreview(ctx context.Context, previewID string) error { return nil }
func (f *cliFakeStore) InviteMember(ctx context.Context, params db.InviteMemberParams) (db.Member, error) {
	now := time.Now().UTC()
	return db.Member{UserID: "user-new", GithubLogin: params.GithubLogin, Email: params.Email, Role: params.Role, InvitedAt: &now, JoinedAt: &now}, nil
}
func (f *cliFakeStore) RemoveMember(ctx context.Context, teamID, actorUserID, targetUserID string) error {
	return nil
}

func (f *cliFakeStore) ResolveUserDefaultTeamByGithubLogin(ctx context.Context, githubLogin string) (string, string, string, error) {
	if githubLogin != "owner" {
		return "", "", "", pgx.ErrNoRows
	}
	return f.token.OwnerUserID, f.team.ID, f.team.Slug, nil
}

func (f *cliFakeStore) CreateCLIToken(ctx context.Context, ownerUserID, teamID string, scopes []string) (string, error) {
	token, err := auth.NewBearerToken("device", "token-issued")
	if err != nil {
		return "", err
	}
	parsed, err := auth.ParseBearerToken(token)
	if err != nil {
		return "", err
	}
	f.tokens[parsed.ID] = db.CliToken{
		ID:          parsed.ID,
		OwnerUserID: ownerUserID,
		TeamID:      teamID,
		TokenHash:   auth.HashBearerToken(parsed.Secret),
		Scopes:      append([]string(nil), scopes...),
	}
	return token, nil
}

func (f *cliFakeStore) CreatePAT(ctx context.Context, ownerUserID, teamID, name string, scopes []string, expiresAt time.Time) (string, error) {
	token, err := auth.NewBearerToken("pat", "token-pat")
	if err != nil {
		return "", err
	}
	parsed, err := auth.ParseBearerToken(token)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	f.tokens[parsed.ID] = db.CliToken{
		ID:          parsed.ID,
		OwnerUserID: ownerUserID,
		TeamID:      teamID,
		Kind:        "pat",
		Name:        name,
		TokenHash:   auth.HashBearerToken(parsed.Secret),
		Scopes:      append([]string(nil), scopes...),
		CreatedAt:   now,
		ExpiresAt:   &expiresAt,
	}
	return token, nil
}

func (f *cliFakeStore) RevokeCLITokenByID(ctx context.Context, tokenID string) error {
	tok, ok := f.tokens[tokenID]
	if !ok {
		return pgx.ErrNoRows
	}
	now := time.Now().UTC()
	tok.RevokedAt = &now
	f.tokens[tokenID] = tok
	return nil
}

func (f *cliFakeStore) RevokePATByName(ctx context.Context, teamID, name string) error {
	for id, tok := range f.tokens {
		if tok.TeamID == teamID && tok.Kind == "pat" && tok.Name == name {
			now := time.Now().UTC()
			tok.RevokedAt = &now
			f.tokens[id] = tok
			return nil
		}
	}
	return pgx.ErrNoRows
}

func (f *cliFakeStore) GetOrCreateUserAndPersonalTeam(ctx context.Context, githubLogin string) (string, string, string, error) {
	return f.ResolveUserDefaultTeamByGithubLogin(ctx, githubLogin)
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

func TestAuthTokensCreateListRevokeCommand(t *testing.T) {
	store, token := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)

	createCmd := NewRootCommand()
	createCmd.SetArgs([]string{"auth", "tokens", "create", "--team", store.team.Slug, "--host", srv.URL, "--token", token, "--name", "ci", "--scopes", "apps:read,teams:read", "--expires", "30d", "--output", "json"})
	var createOut bytes.Buffer
	createCmd.SetOut(&createOut)
	createCmd.SetErr(&createOut)
	if err := createCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create Execute() error = %v", err)
	}
	if !bytes.Contains(createOut.Bytes(), []byte("op_pat_")) {
		t.Fatalf("create output missing token: %s", createOut.String())
	}

	listCmd := NewRootCommand()
	listCmd.SetArgs([]string{"auth", "tokens", "list", "--team", store.team.Slug, "--host", srv.URL, "--token", token, "--output", "json"})
	var listOut bytes.Buffer
	listCmd.SetOut(&listOut)
	listCmd.SetErr(&listOut)
	if err := listCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list Execute() error = %v", err)
	}
	if !bytes.Contains(listOut.Bytes(), []byte(`"name": "ci"`)) {
		t.Fatalf("list output missing token name: %s", listOut.String())
	}

	revokeCmd := NewRootCommand()
	revokeCmd.SetArgs([]string{"auth", "tokens", "revoke", "ci", "--team", store.team.Slug, "--host", srv.URL, "--token", token, "--yes"})
	var revokeOut bytes.Buffer
	revokeCmd.SetOut(&revokeOut)
	revokeCmd.SetErr(&revokeOut)
	if err := revokeCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("revoke Execute() error = %v", err)
	}
	for _, tok := range store.tokens {
		if tok.Kind == "pat" && tok.Name == "ci" {
			if tok.RevokedAt == nil {
				t.Fatal("expected revoked PAT")
			}
		}
	}
}

func TestAuthTokensListShowsExpiryWarning(t *testing.T) {
	store, token := newCLIFakeStore()
	now := time.Now().UTC()
	store.tokens["token-soon"] = db.CliToken{
		ID:          "token-soon",
		OwnerUserID: store.token.OwnerUserID,
		TeamID:      store.team.ID,
		Kind:        "pat",
		Name:        "soon",
		TokenHash:   auth.HashBearerToken("dummy"),
		Scopes:      []string{"apps:read"},
		CreatedAt:   now.Add(-80 * 24 * time.Hour),
		ExpiresAt:   ptrTime(now.Add(10 * 24 * time.Hour)),
	}
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)

	listCmd := NewRootCommand()
	listCmd.SetArgs([]string{"auth", "tokens", "list", "--team", store.team.Slug, "--host", srv.URL, "--token", token, "--output", "table"})
	var listOut bytes.Buffer
	listCmd.SetOut(&listOut)
	listCmd.SetErr(&listOut)
	if err := listCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list Execute() error = %v", err)
	}
	if !bytes.Contains(listOut.Bytes(), []byte("expiring_soon")) {
		t.Fatalf("expected expiry warning in output: %s", listOut.String())
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

func TestAuthLoginThenAppsListWithoutTokenFlag(t *testing.T) {
	store, _ := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouterWithGitHubOAuth(store, mockGitHubOAuthClient{
		challenge: githuboauth.DeviceAuthorization{
			DeviceCode:       "device-abc",
			UserCode:         "ABCD-EFGH",
			VerificationURI:  "https://github.com/login/device",
			ExpiresInSeconds: 600,
			IntervalSeconds:  1,
		},
		user: githuboauth.UserProfile{Login: "owner", Email: "owner@example.com"},
	}))
	t.Cleanup(srv.Close)

	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)

	loginCmd := NewRootCommand()
	loginCmd.SetArgs([]string{"auth", "login", "--host", srv.URL, "--github-login", "owner"})
	var loginOut bytes.Buffer
	loginCmd.SetOut(&loginOut)
	loginCmd.SetErr(&loginOut)
	if err := loginCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("auth login Execute() error = %v", err)
	}

	authPath, err := authconfig.Path()
	if err != nil {
		t.Fatalf("authconfig.Path() error = %v", err)
	}
	if _, err := os.Stat(authPath); err != nil {
		t.Fatalf("auth file missing: %v", err)
	}

	appsCmd := NewRootCommand()
	appsCmd.SetArgs([]string{"apps", "list", "--team", store.team.Slug, "--host", srv.URL, "--output", "json"})
	var appsOut bytes.Buffer
	appsCmd.SetOut(&appsOut)
	appsCmd.SetErr(&appsOut)
	if err := appsCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("apps list Execute() error = %v", err)
	}
}

func TestAuthLoginThenTeamsListWithoutTokenFlag(t *testing.T) {
	store, _ := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouterWithGitHubOAuth(store, mockGitHubOAuthClient{
		challenge: githuboauth.DeviceAuthorization{
			DeviceCode:       "device-abc",
			UserCode:         "ABCD-EFGH",
			VerificationURI:  "https://github.com/login/device",
			ExpiresInSeconds: 600,
			IntervalSeconds:  1,
		},
		user: githuboauth.UserProfile{Login: "owner", Email: "owner@example.com"},
	}))
	t.Cleanup(srv.Close)

	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)

	loginCmd := NewRootCommand()
	loginCmd.SetArgs([]string{"auth", "login", "--host", srv.URL, "--github-login", "owner"})
	var loginOut bytes.Buffer
	loginCmd.SetOut(&loginOut)
	loginCmd.SetErr(&loginOut)
	if err := loginCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("auth login Execute() error = %v", err)
	}

	teamsCmd := NewRootCommand()
	teamsCmd.SetArgs([]string{"teams", "list", "--host", srv.URL, "--output", "json"})
	var teamsOut bytes.Buffer
	teamsCmd.SetOut(&teamsOut)
	teamsCmd.SetErr(&teamsOut)
	if err := teamsCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("teams list Execute() error = %v", err)
	}
	if teamsOut.Len() == 0 {
		t.Fatal("expected teams output")
	}
}

func TestAuthLoginUsesDefaultHostAndGithubLoginFromEnv(t *testing.T) {
	store, _ := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouterWithGitHubOAuth(store, mockGitHubOAuthClient{
		challenge: githuboauth.DeviceAuthorization{
			DeviceCode:       "device-abc",
			UserCode:         "ABCD-EFGH",
			VerificationURI:  "https://github.com/login/device",
			ExpiresInSeconds: 600,
			IntervalSeconds:  1,
		},
		user: githuboauth.UserProfile{Login: "owner", Email: "owner@example.com"},
	}))
	t.Cleanup(srv.Close)

	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("OPS_HOST", srv.URL)
	t.Setenv("OPS_GITHUB_LOGIN", "owner")

	loginCmd := NewRootCommand()
	loginCmd.SetArgs([]string{"auth", "login"})
	var loginOut bytes.Buffer
	loginCmd.SetOut(&loginOut)
	loginCmd.SetErr(&loginOut)
	if err := loginCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("auth login Execute() error = %v", err)
	}

	cfg, err := authconfig.Load()
	if err != nil {
		t.Fatalf("authconfig.Load() error = %v", err)
	}
	token, ok := cfg.TokenForHost(srv.URL)
	if !ok {
		t.Fatalf("expected token entry for host %q", srv.URL)
	}
	if token.GitHubLogin != "owner" {
		t.Fatalf("github_login = %q, want owner", token.GitHubLogin)
	}
}

func TestAuthCommandRunsLoginFlowByDefault(t *testing.T) {
	store, _ := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouterWithGitHubOAuth(store, mockGitHubOAuthClient{
		challenge: githuboauth.DeviceAuthorization{
			DeviceCode:       "device-abc",
			UserCode:         "ABCD-EFGH",
			VerificationURI:  "https://github.com/login/device",
			ExpiresInSeconds: 600,
			IntervalSeconds:  1,
		},
		user: githuboauth.UserProfile{Login: "owner", Email: "owner@example.com"},
	}))
	t.Cleanup(srv.Close)

	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("OPS_HOST", srv.URL)
	t.Setenv("OPS_GITHUB_LOGIN", "owner")

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"auth"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("auth Execute() error = %v", err)
	}

	cfg, err := authconfig.Load()
	if err != nil {
		t.Fatalf("authconfig.Load() error = %v", err)
	}
	if _, ok := cfg.TokenForHost(srv.URL); !ok {
		t.Fatalf("expected token entry for host %q", srv.URL)
	}
}

func TestAuthLoginUsesLocalhostDefaultGithubLogin(t *testing.T) {
	store, _ := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouterWithGitHubOAuth(store, mockGitHubOAuthClient{
		challenge: githuboauth.DeviceAuthorization{
			DeviceCode:       "device-abc",
			UserCode:         "ABCD-EFGH",
			VerificationURI:  "https://github.com/login/device",
			ExpiresInSeconds: 600,
			IntervalSeconds:  1,
		},
		user: githuboauth.UserProfile{Login: "owner", Email: "owner@example.com"},
	}))
	t.Cleanup(srv.Close)

	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("OPS_HOST", srv.URL)
	t.Setenv("OPS_GITHUB_LOGIN", "")
	t.Setenv("GITHUB_LOGIN", "")

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"auth", "login"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("auth login Execute() error = %v", err)
	}

	cfg, err := authconfig.Load()
	if err != nil {
		t.Fatalf("authconfig.Load() error = %v", err)
	}
	token, ok := cfg.TokenForHost(srv.URL)
	if !ok {
		t.Fatalf("expected token entry for host %q", srv.URL)
	}
	if token.GitHubLogin != "owner" {
		t.Fatalf("github_login = %q, want owner", token.GitHubLogin)
	}
}

func newCLIFakeStore() (*cliFakeStore, string) {
	token, err := auth.NewBearerToken("device", "token-1")
	if err != nil {
		panic(err)
	}
	parsed, err := auth.ParseBearerToken(token)
	if err != nil {
		panic(err)
	}
	baseToken := db.CliToken{ID: "token-1", OwnerUserID: "user-1", TeamID: "team-1", TokenHash: auth.HashBearerToken(parsed.Secret), Scopes: []string{"apps:read", "teams:read", "members:manage"}}
	return &cliFakeStore{
		token: baseToken,
		tokens: map[string]db.CliToken{
			baseToken.ID: baseToken,
		},
		team: db.Team{ID: "team-1", Slug: "acme", Name: "Acme", Plan: "starter"},
		role: "admin", members: true,
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

func ptrTime(v time.Time) *time.Time { return &v }
