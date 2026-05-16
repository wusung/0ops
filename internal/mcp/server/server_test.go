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
	preview, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "create_app_preview",
		Arguments: map[string]any{
			"team_slug": store.team.Slug,
			"slug":      "nextdemo",
			"repo_url":  "https://github.com/example/nextdemo",
			"ref":       "main",
		},
	})
	if err != nil {
		t.Fatalf("create_app_preview: %v", err)
	}
	var previewPayload struct {
		PreviewID string `json:"preview_id"`
	}
	if err := json.Unmarshal([]byte(preview.Content[0].(*mcp.TextContent).Text), &previewPayload); err != nil {
		t.Fatalf("decode create_app_preview payload: %v", err)
	}
	if previewPayload.PreviewID == "" {
		t.Fatal("expected preview_id from create_app_preview")
	}
	_, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "create_app",
		Arguments: map[string]any{
			"team_slug":  store.team.Slug,
			"preview_id": previewPayload.PreviewID,
		},
	})
	if err != nil {
		t.Fatalf("create_app: %v", err)
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
	tokens     map[string]db.CliToken
	team       db.Team
	role       string
	apps       []db.App
	domains    []db.DomainBinding
	deploys    []db.DeployRun
	memberRows []db.Member
	members    bool
	previews   map[string]db.Preview
}

func (f *mcpFakeStore) FindCliTokenByID(_ context.Context, tokenID string) (db.CliToken, error) {
	if tokenID == f.token.ID {
		return f.token, nil
	}
	if tok, ok := f.tokens[tokenID]; ok {
		return tok, nil
	}
	return db.CliToken{}, os.ErrNotExist
}
func (f *mcpFakeStore) ResolveTeamBySlug(_ context.Context, slug string) (db.Team, error) {
	if slug != f.team.Slug {
		return db.Team{}, os.ErrNotExist
	}
	return f.team, nil
}
func (f *mcpFakeStore) CheckTeamMembership(_ context.Context, teamID string, userID string) (bool, error) {
	return f.members && teamID == f.team.ID && userID == f.token.OwnerUserID, nil
}
func (f *mcpFakeStore) GetTeamMembershipRole(_ context.Context, _ string, _ string) (string, error) {
	return f.role, nil
}
func (f *mcpFakeStore) ListUserTeams(_ context.Context, _ string, _ int32, _ *string) ([]db.TeamMembership, error) {
	return []db.TeamMembership{{Team: f.team, UserID: f.token.OwnerUserID, Role: f.role}}, nil
}
func (f *mcpFakeStore) ListTeamApps(_ context.Context, _ string, _ int32, _ *string) ([]db.App, error) {
	return f.apps, nil
}
func (f *mcpFakeStore) GetTeamAppBySlug(_ context.Context, _ string, slug string) (db.App, error) {
	for _, a := range f.apps {
		if a.Slug == slug {
			return a, nil
		}
	}
	return db.App{}, pgx.ErrNoRows
}
func (f *mcpFakeStore) ListDomainsByAppSlug(_ context.Context, _ string, appSlug string) ([]db.DomainBinding, error) {
	out := make([]db.DomainBinding, 0)
	for _, item := range f.domains {
		if item.AppSlug == appSlug {
			out = append(out, item)
		}
	}
	return out, nil
}
func (f *mcpFakeStore) GetLatestDeployByAppSlug(_ context.Context, _ string, appSlug string) (db.DeployRun, error) {
	for _, row := range f.deploys {
		if row.AppSlug == appSlug {
			return row, nil
		}
	}
	return db.DeployRun{}, pgx.ErrNoRows
}
func (f *mcpFakeStore) ListDeployLogLines(ctx context.Context, teamID string, appSlug string, _ int) ([]db.DeployLogLine, error) {
	row, err := f.GetLatestDeployByAppSlug(ctx, teamID, appSlug)
	if err != nil {
		return nil, err
	}
	return append([]db.DeployLogLine(nil), row.LogLines...), nil
}
func (f *mcpFakeStore) HasAnyOwner(_ context.Context) (bool, error) { return false, nil }
func (f *mcpFakeStore) BootstrapOwner(_ context.Context, _ db.BootstrapOwnerParams) (string, string, error) {
	return "team-bootstrap", "user-bootstrap", nil
}
func (f *mcpFakeStore) ListTeamMembers(_ context.Context, _ string) ([]db.Member, error) {
	return f.memberRows, nil
}
func (f *mcpFakeStore) ListTeamTokens(_ context.Context, teamID string) ([]db.CliToken, error) {
	out := make([]db.CliToken, 0, len(f.tokens))
	for _, tok := range f.tokens {
		if tok.TeamID == teamID && tok.Kind == "pat" {
			out = append(out, tok)
		}
	}
	return out, nil
}
func (f *mcpFakeStore) CreatePreview(_ context.Context, teamID, actorUserID, action string, args json.RawMessage, _ string) (db.Preview, error) {
	preview := db.Preview{ID: "preview-1", TeamID: teamID, ActorUserID: actorUserID, Action: action, Args: args, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	f.previews[preview.ID] = preview
	return preview, nil
}
func (f *mcpFakeStore) GetPreview(_ context.Context, previewID string) (db.Preview, error) {
	if preview, ok := f.previews[previewID]; ok {
		return preview, nil
	}
	return db.Preview{}, db.ErrPreviewNotFound
}
func (f *mcpFakeStore) ConsumePreview(_ context.Context, _ string) error { return nil }
func (f *mcpFakeStore) ConsumePreviewWithResult(_ context.Context, previewID string, result json.RawMessage) error {
	preview, ok := f.previews[previewID]
	if !ok {
		return db.ErrPreviewNotFound
	}
	if preview.ConsumedAt != nil {
		return db.ErrPreviewConsumed
	}
	now := time.Now().UTC()
	preview.ConsumedAt = &now
	preview.LastResult = append(json.RawMessage(nil), result...)
	f.previews[previewID] = preview
	return nil
}
func (f *mcpFakeStore) InviteMember(_ context.Context, params db.InviteMemberParams) (db.Member, error) {
	now := time.Now().UTC()
	return db.Member{UserID: "user-new", Role: params.Role, InvitedAt: &now, JoinedAt: &now}, nil
}
func (f *mcpFakeStore) RemoveMember(_ context.Context, _, _, _ string) error {
	return nil
}
func (f *mcpFakeStore) CreateApp(_ context.Context, params db.AppCreateParams) (db.AppCreateResult, error) {
	appID := "app-nextdemo"
	deployRunID := "deploy-nextdemo"
	now := time.Now().UTC()
	f.apps = append(f.apps, db.App{
		ID:                appID,
		TeamID:            params.TeamID,
		Slug:              params.Slug,
		RepoURL:           &params.RepoURL,
		RepoDefaultBranch: &params.Ref,
		Builder:           params.Builder,
		Status:            strPtr("queued"),
		CreatedAt:         now,
		UpdatedAt:         now,
	})
	f.deploys = append(f.deploys, db.DeployRun{
		ID:      deployRunID,
		TeamID:  params.TeamID,
		AppID:   appID,
		AppSlug: params.Slug,
		Status:  "queued",
	})
	return db.AppCreateResult{
		AppID:       appID,
		AppSlug:     params.Slug,
		DeployRunID: deployRunID,
	}, nil
}

func (f *mcpFakeStore) DeleteAppByID(_ context.Context, appID string) error {
	filteredApps := f.apps[:0]
	for _, app := range f.apps {
		if app.ID != appID {
			filteredApps = append(filteredApps, app)
		}
	}
	f.apps = filteredApps
	return nil
}

func (f *mcpFakeStore) RegisterWebhookDelivery(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}

func (f *mcpFakeStore) GetTeamByID(_ context.Context, _ string) (db.Team, error) {
	return f.team, nil
}

func (f *mcpFakeStore) FindTeamByGitHubInstallID(_ context.Context, _ int64) (db.Team, error) {
	return db.Team{}, db.ErrTeamNotFound
}

func (f *mcpFakeStore) SetTeamGitHubInstall(_ context.Context, _, _ string, _ *int64, _ string, _ map[string]any, _ map[string]any) error {
	return nil
}

func (f *mcpFakeStore) PauseTeamApps(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (f *mcpFakeStore) ApplyDeployCallback(_ context.Context, _ db.DeployCallbackParams) error {
	return nil
}

func (f *mcpFakeStore) FindLiveAppsByRepoAndBranch(_ context.Context, _, _, _ string) ([]db.App, error) {
	return nil, nil
}

func (f *mcpFakeStore) HasInFlightDeployRun(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (f *mcpFakeStore) InsertRedeployRun(_ context.Context, _ db.InsertRedeployRunParams) (db.InsertRedeployRunResult, error) {
	return db.InsertRedeployRunResult{}, nil
}

func (f *mcpFakeStore) AppendWebhookAudit(_ context.Context, _ string, _ string, _, _ map[string]any) error {
	return nil
}

func (f *mcpFakeStore) ListAppDomainBindings(_ context.Context, _ string) ([]db.AppDomainBinding, error) {
	return nil, nil
}
func (f *mcpFakeStore) DeleteAppDomainBindings(_ context.Context, _ string) error { return nil }
func (f *mcpFakeStore) ListInFlightDeployRuns(_ context.Context, _ string) ([]db.InFlightDeployRun, error) {
	return nil, nil
}
func (f *mcpFakeStore) CancelDeployRun(_ context.Context, _, _ string) error { return nil }
func (f *mcpFakeStore) UpdateAppStatus(_ context.Context, _, _ string) error { return nil }
func (f *mcpFakeStore) EnqueueReconciliationJob(_ context.Context, _ db.ReconciliationJobInsert) (string, error) {
	return "", nil
}
func (f *mcpFakeStore) AppendAuditLog(_ context.Context, _ db.AuditLogInsert) error { return nil }
func (f *mcpFakeStore) MarkReconciliationJobAttempt(_ context.Context, _, _ string, _ *time.Time, _ bool) error {
	return nil
}

func (f mcpFakeStore) IsToolGranted(_ context.Context, _ string, _ string, _ string) (bool, error) {
	return true, nil
}
func (f mcpFakeStore) ListGrantedTools(_ context.Context, _ string, _ string) ([]string, error) {
	return []string{}, nil
}
func (f mcpFakeStore) UpsertToolGrant(_ context.Context, _ string, _ string, _ string, _ bool, _ *string) error {
	return nil
}
func (f mcpFakeStore) RevokeToolGrant(_ context.Context, _ string, _ string, _ string) error {
	return nil
}
func (f mcpFakeStore) ListAllUserGrants(_ context.Context, _ string, _ string) ([]db.ToolGrant, error) {
	return []db.ToolGrant{}, nil
}

func (f *mcpFakeStore) ResolveUserDefaultTeamByGithubLogin(_ context.Context, githubLogin string) (string, string, string, error) {
	if githubLogin != "owner" {
		return "", "", "", pgx.ErrNoRows
	}
	return f.token.OwnerUserID, f.team.ID, f.team.Slug, nil
}

func (f *mcpFakeStore) GetOrCreateUserAndPersonalTeam(ctx context.Context, githubLogin string) (string, string, string, error) {
	return f.ResolveUserDefaultTeamByGithubLogin(ctx, githubLogin)
}

func (f *mcpFakeStore) CreateCLIToken(_ context.Context, ownerUserID, teamID string, scopes []string) (string, error) {
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

func (f *mcpFakeStore) CreatePAT(_ context.Context, ownerUserID, teamID, name string, scopes []string, expiresAt time.Time) (string, error) {
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

func (f *mcpFakeStore) RevokeCLITokenByID(_ context.Context, tokenID string) error {
	tok, ok := f.tokens[tokenID]
	if !ok {
		return pgx.ErrNoRows
	}
	now := time.Now().UTC()
	tok.RevokedAt = &now
	f.tokens[tokenID] = tok
	return nil
}

func (f *mcpFakeStore) RevokePATByName(_ context.Context, teamID, name string) error {
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

func newMCPFakeStore() (*mcpFakeStore, string) {
	token, err := auth.NewBearerToken("device", "token-1")
	if err != nil {
		panic(err)
	}
	parsed, err := auth.ParseBearerToken(token)
	if err != nil {
		panic(err)
	}
	baseToken := db.CliToken{ID: "token-1", OwnerUserID: "user-1", TeamID: "team-1", TokenHash: auth.HashBearerToken(parsed.Secret), Scopes: []string{"apps:read", "apps:write", "teams:read", "members:manage"}}
	defaultInstallID := int64(99999)
	return &mcpFakeStore{
		token:  baseToken,
		tokens: map[string]db.CliToken{baseToken.ID: baseToken},
		team:   db.Team{ID: "team-1", Slug: "acme", Name: "Acme", Plan: "starter", GithubInstallID: &defaultInstallID},
		role:   "admin", members: true,
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
		previews:   map[string]db.Preview{},
	}, token
}

func strPtr(v string) *string { return &v }
