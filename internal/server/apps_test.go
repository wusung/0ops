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
	"github.com/winshare/zeroops/internal/server/services/githuboauth"
	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
)

type fakeStore struct {
	token      db.CliToken
	tokens     map[string]db.CliToken
	team       db.Team
	role       string
	apps       []db.App
	domains    []db.DomainBinding
	deploys    []db.DeployRun
	memberRows []db.Member
	members    bool
	hasOwner   bool
	previews   map[string]db.Preview
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

func (f *fakeStore) FindCliTokenByID(_ context.Context, tokenID string) (db.CliToken, error) {
	if tokenID == f.token.ID {
		return f.token, nil
	}
	if tok, ok := f.tokens[tokenID]; ok {
		return tok, nil
	}
	return db.CliToken{}, errors.New("not found")
}

func (f *fakeStore) ResolveTeamBySlug(_ context.Context, slug string) (db.Team, error) {
	if slug != f.team.Slug {
		return db.Team{}, errors.New("not found")
	}
	return f.team, nil
}

func (f *fakeStore) CheckTeamMembership(_ context.Context, teamID string, userID string) (bool, error) {
	return f.members && teamID == f.team.ID && userID == f.token.OwnerUserID, nil
}

func (f *fakeStore) GetTeamMembershipRole(_ context.Context, teamID string, userID string) (string, error) {
	if !f.members || teamID != f.team.ID || userID != f.token.OwnerUserID {
		return "", errors.New("not found")
	}
	return f.role, nil
}

func (f *fakeStore) ListUserTeams(_ context.Context, userID string, _ int32, _ *string) ([]db.TeamMembership, error) {
	if userID != f.token.OwnerUserID || !f.members {
		return nil, nil
	}
	return []db.TeamMembership{{
		Team:   db.Team{ID: f.team.ID, Slug: f.team.Slug, Name: f.team.Name, Plan: f.team.Plan},
		UserID: f.token.OwnerUserID,
		Role:   f.role,
	}}, nil
}

func (f *fakeStore) ListTeamApps(_ context.Context, teamID string, limit int32, afterID *string) ([]db.App, error) {
	if teamID != f.team.ID {
		return nil, errors.New("team mismatch")
	}
	out := make([]db.App, 0, len(f.apps))
	for _, app := range f.apps {
		if afterID != nil && app.ID <= *afterID {
			continue
		}
		out = append(out, app)
		if int32(len(out)) >= limit { //nolint:gosec // len() fits in int32
			break
		}
	}
	return out, nil
}

func (f *fakeStore) GetTeamAppBySlug(_ context.Context, teamID string, slug string) (db.App, error) {
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

func (f *fakeStore) ListDomainsByAppSlug(_ context.Context, teamID string, appSlug string) ([]db.DomainBinding, error) {
	if teamID != f.team.ID {
		return nil, errors.New("team mismatch")
	}
	out := make([]db.DomainBinding, 0)
	for _, item := range f.domains {
		if item.AppSlug == appSlug {
			out = append(out, item)
		}
	}
	return out, nil
}

func (f *fakeStore) GetLatestDeployByAppSlug(_ context.Context, teamID string, appSlug string) (db.DeployRun, error) {
	if teamID != f.team.ID {
		return db.DeployRun{}, errors.New("team mismatch")
	}
	for _, row := range f.deploys {
		if row.AppSlug == appSlug {
			return row, nil
		}
	}
	return db.DeployRun{}, pgx.ErrNoRows
}

func (f *fakeStore) ListDeployLogLines(ctx context.Context, teamID string, appSlug string, limit int) ([]db.DeployLogLine, error) {
	row, err := f.GetLatestDeployByAppSlug(ctx, teamID, appSlug)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > len(row.LogLines) {
		limit = len(row.LogLines)
	}
	return append([]db.DeployLogLine(nil), row.LogLines[:limit]...), nil
}

func (f *fakeStore) HasAnyOwner(_ context.Context) (bool, error) { return f.hasOwner, nil }

func (f *fakeStore) BootstrapOwner(_ context.Context, _ db.BootstrapOwnerParams) (string, string, error) {
	if f.hasOwner {
		return "", "", db.ErrBootstrapAlreadyDone
	}
	f.hasOwner = true
	return "team-bootstrap", "user-bootstrap", nil
}

func (f *fakeStore) ListTeamMembers(_ context.Context, _ string) ([]db.Member, error) {
	return append([]db.Member(nil), f.memberRows...), nil
}

func (f *fakeStore) ListTeamTokens(_ context.Context, teamID string) ([]db.CliToken, error) {
	out := make([]db.CliToken, 0, len(f.tokens))
	for _, tok := range f.tokens {
		if tok.TeamID == teamID && tok.Kind == "pat" {
			out = append(out, tok)
		}
	}
	return out, nil
}

func (f *fakeStore) CreatePreview(_ context.Context, teamID, actorUserID, action string, args json.RawMessage, _ string) (db.Preview, error) {
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

func (f *fakeStore) GetPreview(_ context.Context, previewID string) (db.Preview, error) {
	p, ok := f.previews[previewID]
	if !ok {
		return db.Preview{}, db.ErrPreviewNotFound
	}
	return p, nil
}

func (f *fakeStore) ConsumePreview(_ context.Context, _ string) error { return nil }

func (f *fakeStore) InviteMember(_ context.Context, params db.InviteMemberParams) (db.Member, error) {
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

func (f *fakeStore) RemoveMember(_ context.Context, _, _, _ string) error {
	return nil
}

func (f *fakeStore) ResolveUserDefaultTeamByGithubLogin(_ context.Context, githubLogin string) (string, string, string, error) {
	if githubLogin != "owner" {
		return "", "", "", pgx.ErrNoRows
	}
	return f.token.OwnerUserID, f.team.ID, f.team.Slug, nil
}

func (f *fakeStore) GetOrCreateUserAndPersonalTeam(ctx context.Context, githubLogin string) (string, string, string, error) {
	return f.ResolveUserDefaultTeamByGithubLogin(ctx, githubLogin)
}

func (f *fakeStore) CreateCLIToken(_ context.Context, ownerUserID, teamID string, scopes []string) (string, error) {
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

func (f *fakeStore) CreatePAT(_ context.Context, ownerUserID, teamID, name string, scopes []string, expiresAt time.Time) (string, error) {
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

func (f *fakeStore) RevokeCLITokenByID(_ context.Context, tokenID string) error {
	tok, ok := f.tokens[tokenID]
	if !ok {
		return pgx.ErrNoRows
	}
	now := time.Now().UTC()
	tok.RevokedAt = &now
	f.tokens[tokenID] = tok
	return nil
}

func (f *fakeStore) RevokePATByName(_ context.Context, teamID, name string) error {
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

func TestNewRouterInspectRepo(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).InspectRepo(context.Background(), store.team.Slug, "alpha")
	if err != nil {
		t.Fatalf("InspectRepo() error = %v", err)
	}
	if out.AppSlug != "alpha" {
		t.Fatalf("AppSlug = %q, want alpha", out.AppSlug)
	}
}

func TestNewRouterGetDeployStatus(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).GetDeployStatus(context.Background(), store.team.Slug, "alpha")
	if err != nil {
		t.Fatalf("GetDeployStatus() error = %v", err)
	}
	if out.Status != "succeeded" {
		t.Fatalf("Status = %q, want succeeded", out.Status)
	}
}

func TestNewRouterTailLogs(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).TailLogs(context.Background(), store.team.Slug, "alpha", 10)
	if err != nil {
		t.Fatalf("TailLogs() error = %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(out.Items))
	}
}

func TestNewRouterListDomains(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).ListDomains(context.Background(), store.team.Slug, "alpha")
	if err != nil {
		t.Fatalf("ListDomains() error = %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(out.Items))
	}
}

func TestDeviceLoginAndLogoutFlow(t *testing.T) {
	store, _ := newFakeStore()
	srv := httptest.NewServer(NewRouterWithGitHubOAuth(store, mockGitHubOAuthClient{
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

	client := backendclient.New(srv.URL, "")
	start, err := client.StartDeviceLogin(context.Background(), dto.DeviceStartRequest{
		GithubLogin: "owner",
	})
	if err != nil {
		t.Fatalf("StartDeviceLogin() error = %v", err)
	}
	if start.PollToken == "" {
		t.Fatal("expected poll token")
	}
	if start.UserCode == "" {
		t.Fatal("expected user code")
	}

	// Callback to verify the user code
	cbResp, err := client.CallbackDeviceLogin(context.Background(), dto.DeviceCallbackRequest{
		UserCode:    start.UserCode,
		AccessToken: "test-access-token",
	})
	if err != nil {
		t.Fatalf("CallbackDeviceLogin() error = %v", err)
	}
	if cbResp.Status != "verified" {
		t.Fatalf("expected status=verified, got %s", cbResp.Status)
	}

	poll, err := client.PollDeviceLogin(context.Background(), dto.DevicePollRequest{PollToken: start.PollToken})
	if err != nil {
		t.Fatalf("PollDeviceLogin() error = %v", err)
	}
	if poll.BearerToken == "" || poll.DefaultTeamSlug == "" {
		t.Fatalf("unexpected poll result: %#v", poll)
	}

	authClient := backendclient.New(srv.URL, poll.BearerToken)
	if _, err := authClient.ListTeams(context.Background()); err != nil {
		t.Fatalf("ListTeams() before logout error = %v", err)
	}
	if err := authClient.Logout(context.Background()); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if _, err := authClient.ListTeams(context.Background()); err == nil || !strings.Contains(err.Error(), "token_revoked") {
		t.Fatalf("ListTeams() after logout error = %v, want token_revoked", err)
	}
}

func TestReadEndpointsCrossTeamReturnNotFound(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	_, err := client.InspectRepo(context.Background(), "other-team", "alpha")
	if err == nil || !strings.Contains(err.Error(), "team_not_found") {
		t.Fatalf("InspectRepo cross-team error = %v, want team_not_found", err)
	}
	_, err = client.GetDeployStatus(context.Background(), "other-team", "alpha")
	if err == nil || !strings.Contains(err.Error(), "team_not_found") {
		t.Fatalf("GetDeployStatus cross-team error = %v, want team_not_found", err)
	}
	_, err = client.TailLogs(context.Background(), "other-team", "alpha", 10)
	if err == nil || !strings.Contains(err.Error(), "team_not_found") {
		t.Fatalf("TailLogs cross-team error = %v, want team_not_found", err)
	}
	_, err = client.ListDomains(context.Background(), "other-team", "alpha")
	if err == nil || !strings.Contains(err.Error(), "team_not_found") {
		t.Fatalf("ListDomains cross-team error = %v, want team_not_found", err)
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

func TestAuthTokensCreateDefaultsToNinetyDays(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	now := time.Now().UTC()
	out, err := client.CreateTeamToken(context.Background(), store.team.Slug, dto.PATCreateRequest{
		Name:   "default-expiry",
		Scopes: []string{"apps:read"},
	})
	if err != nil {
		t.Fatalf("CreateTeamToken() error = %v", err)
	}
	delta := out.ExpiresAt.Sub(out.CreatedAt)
	if delta < 89*24*time.Hour || delta > 91*24*time.Hour {
		t.Fatalf("expires_at delta = %s, want about 90d (now=%s)", delta, now)
	}
}

func TestAuthMiddlewareRejectsExpiredToken(t *testing.T) {
	store, token := newFakeStore()
	tok := store.tokens["token-1"]
	past := time.Now().UTC().Add(-time.Hour)
	tok.ExpiresAt = &past
	store.tokens["token-1"] = tok
	store.token.ExpiresAt = &past

	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	_, err := client.ListTeams(context.Background())
	if err == nil || !strings.Contains(err.Error(), "token_expired") {
		t.Fatalf("ListTeams() error = %v, want token_expired", err)
	}
}

func newFakeStore() (*fakeStore, string) {
	token, err := auth.NewBearerToken("device", "token-1")
	if err != nil {
		panic(err)
	}
	parsed, err := auth.ParseBearerToken(token)
	if err != nil {
		panic(err)
	}
	baseToken := db.CliToken{
		ID:          "token-1",
		OwnerUserID: "user-1",
		TeamID:      "team-1",
		TokenHash:   auth.HashBearerToken(parsed.Secret),
		Scopes:      []string{"apps:read", "teams:read", "members:manage"},
	}
	return &fakeStore{
		token:   baseToken,
		tokens:  map[string]db.CliToken{baseToken.ID: baseToken},
		team:    db.Team{ID: "team-1", Slug: "acme", Name: "Acme", Plan: "starter"},
		role:    "admin",
		members: true,
		apps: []db.App{
			{ID: "1", TeamID: "team-1", Slug: "alpha", Name: strPtr("Alpha"), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
			{ID: "2", TeamID: "team-1", Slug: "beta", Name: strPtr("Beta"), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		},
		domains: []db.DomainBinding{
			{ID: "d1", TeamID: "team-1", AppID: "1", AppSlug: "alpha", Hostname: "alpha.example.com", Kind: strPtr("primary"), Verified: true},
			{ID: "d2", TeamID: "team-1", AppID: "1", AppSlug: "alpha", Hostname: "www.alpha.example.com", Kind: strPtr("extra"), Verified: false},
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
		memberRows: []db.Member{{UserID: "user-1", GithubLogin: strPtr("owner"), Role: "owner"}},
		previews:   map[string]db.Preview{},
	}, token
}

func strPtr(v string) *string { return &v }
