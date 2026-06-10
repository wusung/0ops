package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/githuboauth"
	k3ssvc "github.com/winshare/zeroops/internal/server/services/k3s"
	workflowdispatch "github.com/winshare/zeroops/internal/server/services/workflowdispatch"
	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
)

type fakeStore struct {
	token             db.CliToken
	tokens            map[string]db.CliToken
	team              db.Team
	role              string
	retryDeleteErr    error
	retryDeleteCalls  []string
	apps              []db.App
	domains           []db.DomainBinding
	deploys           []db.DeployRun
	memberRows        []db.Member
	members           bool
	hasOwner          bool
	previews          map[string]db.Preview
	deliveries        map[string]struct{}
	lastCallbackEvent json.RawMessage
	// M4.1 webhook-and-redeploy bookkeeping (defaults zero-valued so old
	// tests continue to work without extra setup).
	auditEntries      []fakeAuditEntry
	redeployRuns      []db.InsertRedeployRunParams
	inFlightAppIDs    map[string]bool

	// M5.1 delete-app-flow bookkeeping. domainBindings/deployRuns mirror
	// the production tables; reconciliationJobs/auditLogRows capture the
	// rows the saga inserts.
	domainBindings     []db.DomainBinding
	deployRuns         []db.DeployRun
	reconciliationJobs []db.ReconciliationJobInsert
	auditLogRows       []db.AuditLogInsert

	// M6.8 app-source-ingestion upload bookkeeping.
	uploadRows []db.Upload
}

// fakeAuditEntry records calls to AppendWebhookAudit so push-handler tests
// can assert audit side-effects without a real DB.
type fakeAuditEntry struct {
	TeamID string
	Action string
	Args   map[string]any
	Result map[string]any
}

type mockGitHubOAuthClient struct {
	challenge githuboauth.DeviceAuthorization
	user      githuboauth.UserProfile
}

type fakeArgoCDStatusProvider struct {
	status argoCDApplicationStatus
}

type fakeInfraK3sArgoClient struct {
	status   k3ssvc.ApplicationStatus
	called   bool
	teamSlug string
	appSlug  string
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

func (f fakeArgoCDStatusProvider) GetApplicationStatus(context.Context, string, string) (argoCDApplicationStatus, error) {
	return f.status, nil
}

func (f *fakeInfraK3sArgoClient) EnsureTeamIsolation(_ context.Context, _, _, _ string) (string, error) {
	return "team-test", nil
}

func (f *fakeInfraK3sArgoClient) EnsureResourceQuota(_ context.Context, _, _ string) error {
	return nil
}

func (f *fakeInfraK3sArgoClient) EnsureLimitRange(_ context.Context, _ string) error {
	return nil
}

func (f *fakeInfraK3sArgoClient) EnsureNetworkPolicy(_ context.Context, _ string) error {
	return nil
}

func (f *fakeInfraK3sArgoClient) PatchNamespacePSA(_ context.Context, _ string) error {
	return nil
}

func (f *fakeInfraK3sArgoClient) GetApplicationStatus(_ context.Context, teamSlug, appSlug string) (k3ssvc.ApplicationStatus, error) {
	f.called = true
	f.teamSlug = teamSlug
	f.appSlug = appSlug
	return f.status, nil
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

func (f *fakeStore) RetryStuckDelete(_ context.Context, teamSlug, appSlug string) (string, error) {
	f.retryDeleteCalls = append(f.retryDeleteCalls, teamSlug+"/"+appSlug)
	if f.retryDeleteErr != nil {
		return "", f.retryDeleteErr
	}
	return "job-retry-1", nil
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

func (f *fakeStore) ConsumePreview(_ context.Context, previewID string) error {
	preview, ok := f.previews[previewID]
	if !ok {
		return db.ErrPreviewNotFound
	}
	now := time.Now().UTC()
	preview.ConsumedAt = &now
	f.previews[previewID] = preview
	return nil
}

func (f *fakeStore) ConsumePreviewWithResult(_ context.Context, previewID string, result json.RawMessage) error {
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

func (f *fakeStore) CreateApp(_ context.Context, params db.AppCreateParams) (db.AppCreateResult, error) {
	appID := "app-nextdemo"
	deployRunID := "deploy-nextdemo"
	now := time.Now().UTC()
	app := db.App{
		ID:                appID,
		TeamID:            params.TeamID,
		Slug:              params.Slug,
		RepoURL:           &params.RepoURL,
		RepoDefaultBranch: &params.Ref,
		Builder:           params.Builder,
		Status:            strPtr("queued"),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	f.apps = append(f.apps, app)
	f.deploys = append(f.deploys, db.DeployRun{
		ID:      deployRunID,
		TeamID:  params.TeamID,
		AppID:   appID,
		AppSlug: params.Slug,
		Status:  "queued",
	})
	f.domains = append(f.domains, db.DomainBinding{
		ID:       "domain-nextdemo",
		TeamID:   params.TeamID,
		AppID:    appID,
		AppSlug:  params.Slug,
		Hostname: params.Slug + ".jesontech.com",
		Kind:     strPtr("primary"),
		Verified: true,
	})
	return db.AppCreateResult{
		AppID:       appID,
		AppSlug:     params.Slug,
		DeployRunID: deployRunID,
	}, nil
}

func (f *fakeStore) DeleteAppByID(_ context.Context, appID string) error {
	filteredApps := f.apps[:0]
	for _, app := range f.apps {
		if app.ID != appID {
			filteredApps = append(filteredApps, app)
		}
	}
	f.apps = filteredApps

	filteredDomains := f.domains[:0]
	for _, domain := range f.domains {
		if domain.AppID != appID {
			filteredDomains = append(filteredDomains, domain)
		}
	}
	f.domains = filteredDomains

	filteredDeploys := f.deploys[:0]
	for _, deploy := range f.deploys {
		if deploy.AppID != appID {
			filteredDeploys = append(filteredDeploys, deploy)
		}
	}
	f.deploys = filteredDeploys
	return nil
}

func (f *fakeStore) RegisterWebhookDelivery(_ context.Context, provider, deliveryID string) (bool, error) {
	key := provider + "::" + deliveryID
	if _, ok := f.deliveries[key]; ok {
		return false, nil
	}
	f.deliveries[key] = struct{}{}
	return true, nil
}

func (f *fakeStore) GetTeamByID(_ context.Context, teamID string) (db.Team, error) {
	if teamID != f.team.ID {
		return db.Team{}, db.ErrTeamNotFound
	}
	return f.team, nil
}

func (f *fakeStore) FindTeamByGitHubInstallID(_ context.Context, installID int64) (db.Team, error) {
	if f.team.GithubInstallID != nil && *f.team.GithubInstallID == installID {
		return f.team, nil
	}
	return db.Team{}, db.ErrTeamNotFound
}

func (f *fakeStore) SetTeamGitHubInstall(_ context.Context, teamID, _ string, installID *int64, _ string, _ map[string]any, _ map[string]any) error {
	if teamID != f.team.ID {
		return db.ErrTeamNotFound
	}
	if installID == nil {
		f.team.GithubInstallID = nil
	} else {
		v := *installID
		f.team.GithubInstallID = &v
	}
	return nil
}

func (f *fakeStore) FindLiveAppsByRepoAndBranch(_ context.Context, teamID, repoURL, branch string) ([]db.App, error) {
	if teamID != f.team.ID {
		return nil, nil
	}
	normalized := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(repoURL), "/"), ".git")
	out := make([]db.App, 0)
	for _, app := range f.apps {
		if app.Status == nil || *app.Status != "live" {
			continue
		}
		if app.RepoURL == nil || strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(*app.RepoURL), "/"), ".git") != normalized {
			continue
		}
		if app.RepoDefaultBranch == nil || strings.TrimSpace(*app.RepoDefaultBranch) != strings.TrimSpace(branch) {
			continue
		}
		out = append(out, app)
	}
	return out, nil
}

func (f *fakeStore) HasInFlightDeployRun(_ context.Context, appID string) (bool, error) {
	if f.inFlightAppIDs == nil {
		return false, nil
	}
	return f.inFlightAppIDs[appID], nil
}

func (f *fakeStore) InsertRedeployRun(_ context.Context, params db.InsertRedeployRunParams) (db.InsertRedeployRunResult, error) {
	f.redeployRuns = append(f.redeployRuns, params)
	id := "redeploy-run-" + strconv.Itoa(len(f.redeployRuns))
	source := params.Source
	if source == "" {
		source = "user"
	}
	f.deploys = append(f.deploys, db.DeployRun{
		ID:        id,
		TeamID:    params.TeamID,
		AppID:     params.AppID,
		AppSlug:   f.appSlugByID(params.AppID),
		Status:    "queued",
		TraceID:   strPtrOrNil(params.TraceID),
		CommitSHA: strPtrOrNil(params.CommitSHA),
		Ref:       strPtrOrNil(params.Ref),
	})
	return db.InsertRedeployRunResult{DeployRunID: id}, nil
}

func (f *fakeStore) AppendWebhookAudit(_ context.Context, teamID, action string, args map[string]any, result map[string]any) error {
	f.auditEntries = append(f.auditEntries, fakeAuditEntry{TeamID: teamID, Action: action, Args: args, Result: result})
	return nil
}

func (f *fakeStore) ListAppDomainBindings(_ context.Context, appID string) ([]db.AppDomainBinding, error) {
	out := []db.AppDomainBinding{}
	for _, d := range f.domainBindings {
		if d.AppID == appID {
			b := db.AppDomainBinding{ID: d.ID, Hostname: d.Hostname}
			if d.Kind != nil {
				b.Kind = *d.Kind
			}
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *fakeStore) DeleteAppDomainBindings(_ context.Context, appID string) error {
	remaining := f.domainBindings[:0]
	for _, d := range f.domainBindings {
		if d.AppID != appID {
			remaining = append(remaining, d)
		}
	}
	f.domainBindings = remaining
	return nil
}

func (f *fakeStore) ListInFlightDeployRuns(_ context.Context, appID string) ([]db.InFlightDeployRun, error) {
	out := []db.InFlightDeployRun{}
	for _, dr := range f.deployRuns {
		if dr.AppID != appID {
			continue
		}
		terminal := dr.Status == "live" || dr.Status == "failed" || dr.Status == "canceled" || dr.Status == "rolled_back"
		if terminal {
			continue
		}
		out = append(out, db.InFlightDeployRun{ID: dr.ID, Status: dr.Status})
	}
	return out, nil
}

func (f *fakeStore) CancelDeployRun(_ context.Context, runID, _ string) error {
	for idx := range f.deployRuns {
		if f.deployRuns[idx].ID == runID {
			f.deployRuns[idx].Status = "canceled"
		}
	}
	return nil
}

func (f *fakeStore) UpdateAppStatus(_ context.Context, appID, status string) error {
	for idx := range f.apps {
		if f.apps[idx].ID == appID {
			s := status
			f.apps[idx].Status = &s
			return nil
		}
	}
	return nil
}

func (f *fakeStore) EnqueueReconciliationJob(_ context.Context, in db.ReconciliationJobInsert) (string, error) {
	f.reconciliationJobs = append(f.reconciliationJobs, in)
	return "rj-test", nil
}

func (f *fakeStore) AppendAuditLog(_ context.Context, in db.AuditLogInsert) error {
	f.auditLogRows = append(f.auditLogRows, in)
	return nil
}

func (f *fakeStore) MarkReconciliationJobAttempt(_ context.Context, _, _ string, _ *time.Time, _ bool) error {
	return nil
}

func (f *fakeStore) appSlugByID(appID string) string {
	for _, app := range f.apps {
		if app.ID == appID {
			return app.Slug
		}
	}
	return ""
}

func strPtrOrNil(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	v := s
	return &v
}

func (f *fakeStore) PauseTeamApps(_ context.Context, teamID string) (int64, error) {
	if teamID != f.team.ID {
		return 0, nil
	}
	var paused int64
	for idx := range f.apps {
		if f.apps[idx].Status == nil || *f.apps[idx].Status != "paused" {
			s := "paused"
			f.apps[idx].Status = &s
			paused++
		}
	}
	return paused, nil
}

func (f *fakeStore) GetDeployRunTeamID(_ context.Context, runID string) (string, error) {
	for _, d := range f.deploys {
		if d.ID == runID {
			return d.TeamID, nil
		}
	}
	return "", pgx.ErrNoRows
}

func (f *fakeStore) ApplyDeployCallback(_ context.Context, params db.DeployCallbackParams) error {
	for idx := range f.deploys {
		if f.deploys[idx].ID != params.RunID {
			continue
		}
		f.deploys[idx].Status = params.Status
		f.deploys[idx].TraceID = params.TraceID
		f.deploys[idx].ErrorSummary = params.ErrorSummary
		if len(params.Event) > 0 {
			f.lastCallbackEvent = append(json.RawMessage(nil), params.Event...)
		}
		return nil
	}
	return pgx.ErrNoRows
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

func (f *fakeStore) IsToolGranted(_ context.Context, _ string, _ string, _ string) (bool, error) {
	return false, nil
}

func (f *fakeStore) ListGrantedTools(_ context.Context, _ string, _ string) ([]string, error) {
	return []string{}, nil
}

func (f *fakeStore) UpsertToolGrant(_ context.Context, _ string, _ string, _ string, _ bool, _ *string) error {
	return nil
}

func (f *fakeStore) RevokeToolGrant(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

func (f *fakeStore) ListAllUserGrants(_ context.Context, _ string, _ string) ([]db.ToolGrant, error) {
	return []db.ToolGrant{}, nil
}

func (f *fakeStore) GetAppRepoURLByTeamAndAppSlug(_ context.Context, _, _ string) (string, error) {
	return "", nil
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

func TestNewRouterGetDeployStatusUsesArgoCDProvider(t *testing.T) {
	store, token := newFakeStore()
	store.deploys[0].Status = "queued"
	prev := newArgoCDStatusProvider
	newArgoCDStatusProvider = func() argoCDStatusProvider {
		return fakeArgoCDStatusProvider{status: argoCDApplicationStatus{
			SyncStatus:   "Synced",
			HealthStatus: "Progressing",
		}}
	}
	t.Cleanup(func() { newArgoCDStatusProvider = prev })

	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).GetDeployStatus(context.Background(), store.team.Slug, "alpha")
	if err != nil {
		t.Fatalf("GetDeployStatus() error = %v", err)
	}
	if out.Status != "syncing" {
		t.Fatalf("Status = %q, want syncing", out.Status)
	}
}

func TestNewRouterWithInfraGetDeployStatusUsesK3sArgoProvider(t *testing.T) {
	store, token := newFakeStore()
	store.deploys[0].Status = "queued"
	prev := newArgoCDStatusProvider
	t.Cleanup(func() { newArgoCDStatusProvider = prev })

	k3sClient := &fakeInfraK3sArgoClient{
		status: k3ssvc.ApplicationStatus{
			SyncStatus:   "Synced",
			HealthStatus: "Healthy",
		},
	}

	srv := httptest.NewServer(NewRouterWithInfra(store, k3sClient, nil))
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).GetDeployStatus(context.Background(), store.team.Slug, "alpha")
	if err != nil {
		t.Fatalf("GetDeployStatus() error = %v", err)
	}
	if !k3sClient.called {
		t.Fatal("expected k3s ArgoCD status provider to be called")
	}
	if k3sClient.teamSlug != store.team.Slug || k3sClient.appSlug != "alpha" {
		t.Fatalf("provider called with team=%q app=%q, want team=%q app=%q", k3sClient.teamSlug, k3sClient.appSlug, store.team.Slug, "alpha")
	}
	if out.Status != "live" {
		t.Fatalf("Status = %q, want live", out.Status)
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

func TestNewRouterTailLogsFollowSSE(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		srv.URL+"/v1/teams/"+store.team.Slug+"/deploys/logs?app_slug=alpha&follow=true&limit=10",
		nil,
	)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", got)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	raw := string(body)
	if !strings.Contains(raw, "event: log\n") {
		t.Fatalf("missing log event: %s", raw)
	}
	if !strings.Contains(raw, "event: end\n") {
		t.Fatalf("missing end event: %s", raw)
	}
	if !strings.Contains(raw, "build started") || !strings.Contains(raw, "deploy succeeded") {
		t.Fatalf("missing log payload: %s", raw)
	}
}

func TestNewRouterTailLogsFollowSSEWithLastEventID(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	lastEventID := store.deploys[0].LogLines[0].Timestamp.Format(time.RFC3339Nano)
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		srv.URL+"/v1/teams/"+store.team.Slug+"/deploys/logs?app_slug=alpha&follow=true&limit=10",
		nil,
	)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Last-Event-ID", lastEventID)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	raw := string(body)
	if strings.Contains(raw, "build started") {
		t.Fatalf("expected old log line to be skipped, got: %s", raw)
	}
	if !strings.Contains(raw, "deploy succeeded") {
		t.Fatalf("expected latest log line, got: %s", raw)
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
	}, nil, nil))
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

func TestAppsPreviewCreateAndCreate(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	reqBody := `{"slug":"nextdemo","source":{"type":"github","github":{"url":"https://github.com/example/nextdemo","ref":"main"}}}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/apps:preview", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("preview request error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("preview status = %d, body = %s", resp.StatusCode, string(body))
	}

	var preview dto.PreviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&preview); err != nil {
		t.Fatalf("decode preview response: %v", err)
	}
	if preview.PreviewID == "" {
		t.Fatal("expected preview_id")
	}

	confirmReq, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/apps", strings.NewReader(`{"preview_id":"`+preview.PreviewID+`"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() confirm error = %v", err)
	}
	confirmReq.Header.Set("Authorization", "Bearer "+token)
	confirmReq.Header.Set("Content-Type", "application/json")

	confirmResp, err := http.DefaultClient.Do(confirmReq)
	if err != nil {
		t.Fatalf("confirm request error = %v", err)
	}
	defer func() { _ = confirmResp.Body.Close() }()
	if confirmResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(confirmResp.Body)
		t.Fatalf("confirm status = %d, body = %s", confirmResp.StatusCode, string(body))
	}
}

// TestAppsPreviewCreateRequiresGitHubInstall guards spec
// docs/features/create-app-flow/spec.md § 5.1 step 2 and § 15 hard rule #7:
// when team.github_install_id IS NULL, preview must fail 422
// github_app_not_installed instead of reaching confirm.
func TestAppsPreviewCreateRequiresGitHubInstall(t *testing.T) {
	store, token := newFakeStore()
	store.team.GithubInstallID = nil
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	reqBody := `{"slug":"nextdemo","source":{"type":"github","github":{"url":"https://github.com/example/nextdemo","ref":"main"}}}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/apps:preview", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("preview request error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("preview status = %d, want 422; body = %s", resp.StatusCode, string(body))
	}

	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.Error.Code != "github_app_not_installed" {
		t.Fatalf("error.code = %q, want github_app_not_installed", envelope.Error.Code)
	}
}

// TestAppsPreviewCreateBypassEnvAllowsMissingInstall guards the dev knob
// GITHUB_APP_DISABLE_INSTALL_CHECK=true, which keeps `./manage.sh dev` walkthroughs
// working when no real GitHub App is installed.
func TestAppsPreviewCreateBypassEnvAllowsMissingInstall(t *testing.T) {
	t.Setenv("GITHUB_APP_DISABLE_INSTALL_CHECK", "true")
	store, token := newFakeStore()
	store.team.GithubInstallID = nil
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	reqBody := `{"slug":"nextdemo","source":{"type":"github","github":{"url":"https://github.com/example/nextdemo","ref":"main"}}}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/apps:preview", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("preview request error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("preview status = %d, want 200; body = %s", resp.StatusCode, string(body))
	}
}

func TestAppsCreateConfirmIdempotentReplay(t *testing.T) {
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	preview, err := client.PreviewCreateApp(context.Background(), store.team.Slug, dto.AppCreateRequest{
		Slug: "nextdemo",
		Source: &dto.Source{
			Type:   dto.SourceKindGitHub,
			GitHub: &dto.SourceGitHub{URL: "https://github.com/example/nextdemo", Ref: "main"},
		},
	})
	if err != nil {
		t.Fatalf("PreviewCreateApp() error = %v", err)
	}

	first, err := client.CreateApp(context.Background(), store.team.Slug, dto.ConfirmCreateAppRequest{
		PreviewID: preview.PreviewID,
	})
	if err != nil {
		t.Fatalf("CreateApp() first call error = %v", err)
	}
	second, err := client.CreateApp(context.Background(), store.team.Slug, dto.ConfirmCreateAppRequest{
		PreviewID: preview.PreviewID,
	})
	if err != nil {
		t.Fatalf("CreateApp() replay call error = %v", err)
	}

	if second.AppID != first.AppID || second.DeployRunID != first.DeployRunID {
		t.Fatalf("replay mismatch: first=%+v second=%+v", first, second)
	}
}

func TestDeployRunCallbackHMACAndDedup(t *testing.T) {
	t.Setenv("OPS_CALLBACK_SECRET", "callback-secret")
	store, _ := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	body := `{"run_id":"deploy-1","status":"failure","trace_id":"trace-1","failure_classification":"build_compile_error","error_summary":"build failed"}`
	ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	mac := hmac.New(sha256.New, []byte("callback-secret"))
	_, _ = mac.Write([]byte(ts + "." + body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/internal/deploy-runs/deploy-1/callback", strings.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-0ops-Timestamp", ts)
	req.Header.Set("X-0ops-Signature", sig)
	req.Header.Set("X-0ops-Delivery-ID", "delivery-1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("callback request error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		bodyText, _ := io.ReadAll(resp.Body)
		t.Fatalf("callback status = %d, body = %s", resp.StatusCode, string(bodyText))
	}
	if got := store.deploys[0].Status; got != "failed" {
		t.Fatalf("store.deploys[0].Status = %q, want failed", got)
	}
	if got := store.deploys[0].TraceID; got == nil || *got != "trace-1" {
		t.Fatalf("store.deploys[0].TraceID = %v, want trace-1", got)
	}

	req2, err := http.NewRequest(http.MethodPost, srv.URL+"/internal/deploy-runs/deploy-1/callback", strings.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest() duplicate error = %v", err)
	}
	req2.Header = req.Header.Clone()
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("duplicate callback request error = %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		bodyText, _ := io.ReadAll(resp2.Body)
		t.Fatalf("duplicate callback status = %d, body = %s", resp2.StatusCode, string(bodyText))
	}
}

func TestDeployRunCallbackRejectInvalidSignature(t *testing.T) {
	t.Setenv("OPS_CALLBACK_SECRET", "callback-secret")
	store, _ := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	body := `{"run_id":"deploy-1","status":"success","trace_id":"trace-1"}`
	ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/internal/deploy-runs/deploy-1/callback", strings.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-0ops-Timestamp", ts)
	req.Header.Set("X-0ops-Signature", "sha256=deadbeef")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("callback request error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		bodyText, _ := io.ReadAll(resp.Body)
		t.Fatalf("callback status = %d, body = %s", resp.StatusCode, string(bodyText))
	}
}

func TestDeployRunCallbackRejectStaleTimestamp(t *testing.T) {
	t.Setenv("OPS_CALLBACK_SECRET", "callback-secret")
	store, _ := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	body := `{"run_id":"deploy-1","status":"success","trace_id":"trace-1"}`
	ts := strconv.FormatInt(time.Now().UTC().Add(-6*time.Minute).Unix(), 10)
	mac := hmac.New(sha256.New, []byte("callback-secret"))
	_, _ = mac.Write([]byte(ts + "." + body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/internal/deploy-runs/deploy-1/callback", strings.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-0ops-Timestamp", ts)
	req.Header.Set("X-0ops-Signature", sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("callback request error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		bodyText, _ := io.ReadAll(resp.Body)
		t.Fatalf("callback status = %d, body = %s", resp.StatusCode, string(bodyText))
	}

	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if out.Error.Code != "stale_timestamp" {
		t.Fatalf("error.code = %q, want stale_timestamp", out.Error.Code)
	}
}

func TestDeployRunCallbackRequiresTraceID(t *testing.T) {
	t.Setenv("OPS_CALLBACK_SECRET", "callback-secret")
	store, _ := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	body := `{"run_id":"deploy-1","status":"success"}`
	ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	mac := hmac.New(sha256.New, []byte("callback-secret"))
	_, _ = mac.Write([]byte(ts + "." + body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/internal/deploy-runs/deploy-1/callback", strings.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-0ops-Timestamp", ts)
	req.Header.Set("X-0ops-Signature", sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("callback request error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		bodyText, _ := io.ReadAll(resp.Body)
		t.Fatalf("callback status = %d, body = %s", resp.StatusCode, string(bodyText))
	}
}

func TestDeployRunCallbackFailedStatusRequiresFailureClassification(t *testing.T) {
	t.Setenv("OPS_CALLBACK_SECRET", "callback-secret")
	store, _ := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	body := `{"run_id":"deploy-1","status":"failure","trace_id":"trace-1"}`
	ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	mac := hmac.New(sha256.New, []byte("callback-secret"))
	_, _ = mac.Write([]byte(ts + "." + body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/internal/deploy-runs/deploy-1/callback", strings.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-0ops-Timestamp", ts)
	req.Header.Set("X-0ops-Signature", sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("callback request error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		bodyText, _ := io.ReadAll(resp.Body)
		t.Fatalf("callback status = %d, body = %s", resp.StatusCode, string(bodyText))
	}
}

func TestDeployRunCallbackWithOpsToken(t *testing.T) {
	t.Setenv("OPS_TOKEN_SIGNING_SECRET", "ops-signing-secret")
	store, _ := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	signer, err := workflowdispatch.NewOpsTokenSignerFromEnv()
	if err != nil {
		t.Fatalf("NewOpsTokenSignerFromEnv() error = %v", err)
	}
	opsToken, err := signer.Issue("deploy-1", "trace-ops", []string{"callback:write"})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	body := `{"run_id":"deploy-1","status":"success","trace_id":"trace-ops","ops_token":"` + opsToken + `","image":"ghcr.io/example/app:abc123","build_minutes":4.2,"image_size_bytes":123456,"scan_summary":{"high":0,"critical":0,"exit_code":0},"gitops_commit_sha":"def456"}`
	ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(opsToken))
	_, _ = mac.Write([]byte(ts + "." + body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/internal/deploy-runs/deploy-1/callback", strings.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-0ops-Timestamp", ts)
	req.Header.Set("X-0ops-Signature", sig)
	req.Header.Set("X-0ops-Delivery-ID", "delivery-ops-1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("callback request error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		bodyText, _ := io.ReadAll(resp.Body)
		t.Fatalf("callback status = %d, body = %s", resp.StatusCode, string(bodyText))
	}
	if got := store.deploys[0].Status; got != "live" {
		t.Fatalf("store.deploys[0].Status = %q, want live", got)
	}
	if len(store.lastCallbackEvent) == 0 {
		t.Fatal("expected callback event to be recorded")
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

// InsertUpload satisfies the uploadsStore interface and the extended
// appsStore interface; appends to uploadRows for test assertions.
func (f *fakeStore) InsertUpload(_ context.Context, u db.Upload) error {
	f.uploadRows = append(f.uploadRows, u)
	return nil
}

// GetUpload satisfies the uploadArchiveStore / appsStore interface (M6.9).
// Searches uploadRows for a matching (teamID, id) pair.
func (f *fakeStore) GetUpload(_ context.Context, teamID, id string) (db.Upload, error) {
	for _, u := range f.uploadRows {
		if u.TeamID == teamID && u.ID == id {
			return u, nil
		}
	}
	return db.Upload{}, db.ErrUploadNotFound
}

// seedUpload adds a db.Upload directly to uploadRows for test setup.
func (f *fakeStore) seedUpload(u db.Upload) {
	f.uploadRows = append(f.uploadRows, u)
}

// PinUpload satisfies the extended appsStore interface (M6.13).
func (f *fakeStore) PinUpload(_ context.Context, _, _ string, _ time.Time) error { return nil }

// SumInertBytesByTeam satisfies the appsStore interface (M6.20 quota).
// Returns sum of size_bytes for all received+pinned rows in uploadRows.
func (f *fakeStore) SumInertBytesByTeam(_ context.Context, teamID string) (int64, error) {
	var total int64
	for _, u := range f.uploadRows {
		if u.TeamID == teamID && (u.Status == "received" || u.Status == "pinned") {
			total += u.SizeBytes
		}
	}
	return total, nil
}

// CountPinnedByTeam satisfies the appsStore interface (M6.20 quota).
// Returns count of pinned rows for the team.
func (f *fakeStore) CountPinnedByTeam(_ context.Context, teamID string) (int, error) {
	var count int
	for _, u := range f.uploadRows {
		if u.TeamID == teamID && u.Status == "pinned" {
			count++
		}
	}
	return count, nil
}

// CountTeamUploadsSince satisfies the appsStore interface (M6.20 quota).
// Returns count of all rows for the team with ReceivedAt >= since.
func (f *fakeStore) CountTeamUploadsSince(_ context.Context, teamID string, since time.Time) (int, error) {
	var count int
	for _, u := range f.uploadRows {
		if u.TeamID == teamID && !u.ReceivedAt.Before(since) {
			count++
		}
	}
	return count, nil
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
		Scopes:      []string{"apps:read", "apps:write", "teams:read", "members:manage"},
	}
	defaultInstallID := int64(99999)
	return &fakeStore{
		token:   baseToken,
		tokens:  map[string]db.CliToken{baseToken.ID: baseToken},
		team:    db.Team{ID: "team-1", Slug: "acme", Name: "Acme", Plan: "starter", GithubInstallID: &defaultInstallID},
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
		deliveries: map[string]struct{}{},
	}, token
}

// ---------------------------------------------------------------------------
// validateAppCreateRequest unit tests (T11)
// ---------------------------------------------------------------------------

// decodeErrorCode extracts error.code from a JSON error envelope.
func decodeErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode error body: %v (body=%s)", err, body)
	}
	return resp.Error.Code
}

func callValidate(req *dto.AppCreateRequest) (bool, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	ok := validateAppCreateRequest(w, req)
	return ok, w
}

func TestValidateAppCreate_AcceptsSourceGitHub(t *testing.T) {
	req := &dto.AppCreateRequest{
		Slug: "my-app",
		Source: &dto.Source{
			Type:   dto.SourceKindGitHub,
			GitHub: &dto.SourceGitHub{URL: "https://github.com/example/repo", Ref: "main"},
		},
	}
	ok, _ := callValidate(req)
	if !ok {
		t.Fatal("expected true for valid github source")
	}
}

func TestValidateAppCreate_AcceptsSourceUpload(t *testing.T) {
	req := &dto.AppCreateRequest{
		Slug: "my-app",
		Source: &dto.Source{
			Type:   dto.SourceKindUpload,
			Upload: &dto.SourceUpload{UploadID: "upload-abc-123"},
		},
	}
	ok, _ := callValidate(req)
	if !ok {
		t.Fatal("expected true for valid upload source")
	}
}

// TestValidateAppCreate_RejectsRepoURLGitHubHTTPS guards M8: the deprecated
// github-via-repo_url alias is removed. github sources must use the Source sum
// type; a github repo_url is rejected (no longer normalized into Source).
func TestValidateAppCreate_RejectsRepoURLGitHubHTTPS(t *testing.T) {
	req := &dto.AppCreateRequest{
		Slug:    "my-app",
		RepoURL: "https://github.com/example/repo",
		Ref:     "main",
	}
	ok, w := callValidate(req)
	if ok {
		t.Fatal("expected false: github via repo_url is removed (M8); use source")
	}
	if got := decodeErrorCode(t, w.Body.Bytes()); got != "unsupported_source" {
		t.Fatalf("error.code = %q, want unsupported_source", got)
	}
	if req.Source != nil {
		t.Fatal("expected req.Source to remain nil; github repo_url must not normalize")
	}
}

// TestValidateAppCreate_RejectsRepoURLGitHubSSH guards the git@github.com form
// of the removed alias.
func TestValidateAppCreate_RejectsRepoURLGitHubSSH(t *testing.T) {
	req := &dto.AppCreateRequest{
		Slug:    "my-app",
		RepoURL: "git@github.com:example/repo.git",
		Ref:     "main",
	}
	ok, w := callValidate(req)
	if ok {
		t.Fatal("expected false: git@github.com via repo_url is removed (M8)")
	}
	if got := decodeErrorCode(t, w.Body.Bytes()); got != "unsupported_source" {
		t.Fatalf("error.code = %q, want unsupported_source", got)
	}
}

func TestValidateAppCreate_RejectsSourceAndRepoURLTogether(t *testing.T) {
	req := &dto.AppCreateRequest{
		Slug:    "my-app",
		RepoURL: "https://github.com/example/repo",
		Source: &dto.Source{
			Type:   dto.SourceKindGitHub,
			GitHub: &dto.SourceGitHub{URL: "https://github.com/example/repo", Ref: "main"},
		},
	}
	ok, w := callValidate(req)
	if ok {
		t.Fatal("expected false for source + repo_url conflict")
	}
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("http status = %d, want 422", w.Code)
	}
	if code := decodeErrorCode(t, w.Body.Bytes()); code != "source_conflict" {
		t.Fatalf("error.code = %q, want source_conflict", code)
	}
}

func TestValidateAppCreate_RejectsEmptyEverything(t *testing.T) {
	req := &dto.AppCreateRequest{Slug: "my-app"}
	ok, w := callValidate(req)
	if ok {
		t.Fatal("expected false when neither source nor repo_url provided")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("http status = %d, want 400", w.Code)
	}
	if code := decodeErrorCode(t, w.Body.Bytes()); code != "source_required" {
		t.Fatalf("error.code = %q, want source_required", code)
	}
}

func TestValidateAppCreate_RejectsFileURLInProduction(t *testing.T) {
	t.Setenv("OPS_ENV", "production")
	// AssertProductionSafe requires these two; set them so startup doesn't panic
	// if something calls it, and to prevent false positives in any boot path.
	t.Setenv("APP_SOURCE_INGEST_ROOT", "/tmp")
	t.Setenv("OPS_BUILD_TOKEN_SECRET", "testsecret")

	req := &dto.AppCreateRequest{
		Slug:    "my-app",
		RepoURL: "file:///some/local/path",
	}
	ok, w := callValidate(req)
	if ok {
		t.Fatal("expected false for file:// in production")
	}
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("http status = %d, want 422", w.Code)
	}
	if code := decodeErrorCode(t, w.Body.Bytes()); code != "unsupported_source" {
		t.Fatalf("error.code = %q, want unsupported_source", code)
	}
}

func TestValidateAppCreate_AcceptsFileURLInDev(t *testing.T) {
	t.Setenv("OPS_ENV", "")

	// Create a real temp dir to serve as the local repo root.
	root := t.TempDir()
	repoDir := root + "/myrepo"
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	t.Setenv("LOCAL_FILE_REPO_ENABLED", "true")
	t.Setenv("LOCAL_FILE_REPO_ROOT", root)

	req := &dto.AppCreateRequest{
		Slug:    "my-app",
		RepoURL: "file://" + repoDir,
	}
	ok, _ := callValidate(req)
	if !ok {
		t.Fatal("expected true for valid file:// in dev")
	}
	// Source must remain nil for the dev file:// path — T12 detects it.
	if req.Source != nil {
		t.Fatal("expected req.Source to remain nil for dev file:// path")
	}
}

func TestValidateAppCreate_RejectsSourceGitHubMissingPayload(t *testing.T) {
	req := &dto.AppCreateRequest{
		Slug:   "my-app",
		Source: &dto.Source{Type: dto.SourceKindGitHub, GitHub: nil},
	}
	ok, w := callValidate(req)
	if ok {
		t.Fatal("expected false")
	}
	if code := decodeErrorCode(t, w.Body.Bytes()); code != "source_invalid" {
		t.Fatalf("error.code = %q, want source_invalid", code)
	}
}

func TestValidateAppCreate_RejectsSourceGitHubMissingURL(t *testing.T) {
	req := &dto.AppCreateRequest{
		Slug:   "my-app",
		Source: &dto.Source{Type: dto.SourceKindGitHub, GitHub: &dto.SourceGitHub{URL: "", Ref: "main"}},
	}
	ok, w := callValidate(req)
	if ok {
		t.Fatal("expected false")
	}
	if code := decodeErrorCode(t, w.Body.Bytes()); code != "source_invalid" {
		t.Fatalf("error.code = %q, want source_invalid", code)
	}
}

func TestValidateAppCreate_RejectsSourceGitHubMissingRef(t *testing.T) {
	req := &dto.AppCreateRequest{
		Slug:   "my-app",
		Source: &dto.Source{Type: dto.SourceKindGitHub, GitHub: &dto.SourceGitHub{URL: "https://github.com/example/repo", Ref: ""}},
	}
	ok, w := callValidate(req)
	if ok {
		t.Fatal("expected false")
	}
	if code := decodeErrorCode(t, w.Body.Bytes()); code != "source_invalid" {
		t.Fatalf("error.code = %q, want source_invalid", code)
	}
}

func TestValidateAppCreate_RejectsSourceUploadMissingPayload(t *testing.T) {
	req := &dto.AppCreateRequest{
		Slug:   "my-app",
		Source: &dto.Source{Type: dto.SourceKindUpload, Upload: nil},
	}
	ok, w := callValidate(req)
	if ok {
		t.Fatal("expected false")
	}
	if code := decodeErrorCode(t, w.Body.Bytes()); code != "source_invalid" {
		t.Fatalf("error.code = %q, want source_invalid", code)
	}
}

func TestValidateAppCreate_RejectsSourceUploadMissingUploadID(t *testing.T) {
	req := &dto.AppCreateRequest{
		Slug:   "my-app",
		Source: &dto.Source{Type: dto.SourceKindUpload, Upload: &dto.SourceUpload{UploadID: ""}},
	}
	ok, w := callValidate(req)
	if ok {
		t.Fatal("expected false")
	}
	if code := decodeErrorCode(t, w.Body.Bytes()); code != "source_invalid" {
		t.Fatalf("error.code = %q, want source_invalid", code)
	}
}

func TestValidateAppCreate_RejectsUnknownSourceKind(t *testing.T) {
	req := &dto.AppCreateRequest{
		Slug:   "my-app",
		Source: &dto.Source{Type: "weird"},
	}
	ok, w := callValidate(req)
	if ok {
		t.Fatal("expected false")
	}
	if code := decodeErrorCode(t, w.Body.Bytes()); code != "source_kind_unsupported" {
		t.Fatalf("error.code = %q, want source_kind_unsupported", code)
	}
}

func TestValidateAppCreate_RejectsGitHubSourceWithBadURLScheme(t *testing.T) {
	req := &dto.AppCreateRequest{
		Slug: "my-app",
		Source: &dto.Source{
			Type:   dto.SourceKindGitHub,
			GitHub: &dto.SourceGitHub{URL: "https://gitlab.com/x/y", Ref: "main"},
		},
	}
	ok, w := callValidate(req)
	if ok {
		t.Fatal("expected false for non-github URL")
	}
	if code := decodeErrorCode(t, w.Body.Bytes()); code != "unsupported_source" {
		t.Fatalf("error.code = %q, want unsupported_source", code)
	}
}

func TestValidateAppCreate_RejectsLegacyRepoURLWithUnknownScheme(t *testing.T) {
	req := &dto.AppCreateRequest{
		Slug:    "my-app",
		RepoURL: "https://gitlab.com/x/y",
		Ref:     "main",
	}
	ok, w := callValidate(req)
	if ok {
		t.Fatal("expected false for unsupported repo_url scheme")
	}
	if code := decodeErrorCode(t, w.Body.Bytes()); code != "unsupported_source" {
		t.Fatalf("error.code = %q, want unsupported_source", code)
	}
}

func TestValidateAppCreate_AcceptsSourceGitHubWithSSHURL(t *testing.T) {
	req := &dto.AppCreateRequest{
		Slug: "valid-slug",
		Source: &dto.Source{
			Type: dto.SourceKindGitHub,
			GitHub: &dto.SourceGitHub{
				URL: "git@github.com:example/repo.git",
				Ref: "main",
			},
		},
	}
	ok, w := callValidate(req)
	if !ok {
		t.Fatalf("expected true for git@github.com URL, got body=%s", w.Body.String())
	}
}

func strPtr(v string) *string { return &v }
