package githubapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
)

// Canonical action identifiers (github-app-install-flow spec § 4).
const (
	ActionInstall   = "install_github_app"
	ActionUninstall = "uninstall_github_app"
)

// PlanPreviewSummary returned to callers alongside the preview row to render
// the install/uninstall confirmation UX (spec § 4.2, § 5.1).
const (
	SummaryInstall   = "Install GitHub App for team %q"
	SummaryUninstall = "Uninstall GitHub App for team %q (pauses all team apps)"
)

// Spec § 4.5 polling alignment: install URL is valid until state HMAC expires.
const installURLTTL = 10 * time.Minute

// InstallURLDocsFallback is the redirect target used by the install callback
// while no v1 web UI exists (spec § 4.4 last paragraph).
const installURLDocsFallback = "https://docs.0ops.tw/integrations/github"

// Errors surfaced from the service layer; handlers map these to apperror codes.
var (
	ErrTeamMissing     = errors.New("missing team context")
	ErrActorMissing    = errors.New("missing actor context")
	ErrPreviewNotFound = errors.New("preview not found")
	ErrPreviewConsumed = errors.New("preview already consumed")
	ErrPreviewExpired  = errors.New("preview expired")
	ErrStateInvalid    = errors.New("state token invalid")
	ErrInstallMissing  = errors.New("team has no github installation")
	ErrSignerMissing   = errors.New("github app signer not configured")
)

// Store captures the DB calls the service needs.
type Store interface {
	CreatePreview(ctx context.Context, teamID, actorUserID, action string, args json.RawMessage, actionSummary string) (db.Preview, error)
	GetPreview(ctx context.Context, previewID string) (db.Preview, error)
	ConsumePreviewWithResult(ctx context.Context, previewID string, result json.RawMessage) error
	GetTeamByID(ctx context.Context, teamID string) (db.Team, error)
	FindTeamByGitHubInstallID(ctx context.Context, installID int64) (db.Team, error)
	SetTeamGitHubInstall(ctx context.Context, teamID, actorUserID string, installID *int64, action string, args map[string]any, result map[string]any) error
	PauseTeamApps(ctx context.Context, teamID string) (int64, error)
	GetTeamMembershipRole(ctx context.Context, teamID, userID string) (string, error)
	RegisterWebhookDelivery(ctx context.Context, provider, deliveryID string) (bool, error)
}

// GitHubAPIClient is the subset of `InstallationTokenClient` the service needs.
// Real callers pass a `*InstallationTokenClient`; tests pass a stub.
//
//nolint:revive // exported for public API
type GitHubAPIClient interface {
	DeleteInstallation(ctx context.Context, installID int64) error
}

// TokenInvalidator drops cached tokens after uninstall.
type TokenInvalidator interface {
	Invalidate(installID int64)
}

// Service orchestrates the install/uninstall preview/confirm/callback/webhook
// flows defined in `docs/features/github-app-install-flow/spec.md`.
type Service struct {
	store        Store
	stateSigner  *StateSigner
	apiClient    GitHubAPIClient
	tokenCache   TokenInvalidator
	appURLBase   string // e.g. https://github.com/apps
	appSlug      string // e.g. 0ops
	callbackURL  string // absolute URL served by backend, /v1/auth/github/install-callback
	successPage  string // redirect target on success
	now          func() time.Time
	stateTTL     time.Duration
	installURLFn func(state string) string
}

// Options configures the GitHub App service.
type Options struct {
	StateSigner *StateSigner
	APIClient   GitHubAPIClient
	TokenCache  TokenInvalidator
	// AppURLBase is the GitHub host base, e.g. "https://github.com".
	AppURLBase string
	// AppSlug is the GitHub App slug used in install URLs ("0ops" by default).
	AppSlug string
	// CallbackURL is the absolute redirect URI that GitHub returns to.
	CallbackURL string
	// SuccessPage is the optional redirect target after the callback handler.
	SuccessPage string
	// Now overrides time.Now for tests.
	Now func() time.Time
}

// NewService returns a configured service. The Store must always be supplied;
// signer/API client/token cache may be nil to enable degraded fallbacks for
// dev environments without GitHub credentials.
func NewService(store Store, opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	base := strings.TrimSpace(opts.AppURLBase)
	if base == "" {
		base = "https://github.com"
	}
	slug := strings.TrimSpace(opts.AppSlug)
	if slug == "" {
		slug = "0ops"
	}
	success := strings.TrimSpace(opts.SuccessPage)
	if success == "" {
		success = installURLDocsFallback
	}
	svc := &Service{
		store:       store,
		stateSigner: opts.StateSigner,
		apiClient:   opts.APIClient,
		tokenCache:  opts.TokenCache,
		appURLBase:  strings.TrimRight(base, "/"),
		appSlug:     slug,
		callbackURL: strings.TrimSpace(opts.CallbackURL),
		successPage: success,
		now:         opts.Now,
		stateTTL:    installURLTTL,
	}
	svc.installURLFn = svc.defaultInstallURL
	return svc
}

func (s *Service) defaultInstallURL(state string) string {
	u := fmt.Sprintf("%s/apps/%s/installations/new", s.appURLBase, url.PathEscape(s.appSlug))
	q := url.Values{}
	q.Set("state", state)
	return u + "?" + q.Encode()
}

// PreviewResult is the structured response returned from preview endpoints.
type PreviewResult struct {
	PreviewID string
	Action    string
	Summary   string
	ExpiresAt time.Time
}

// ConfirmInstallResult is the durable response from the install confirm
// endpoint; persisted as preview.last_result and replayed on retries.
type ConfirmInstallResult struct {
	InstallURL string    `json:"install_url"`
	ExpiresAt  time.Time `json:"expires_at"`
	Replayed   bool      `json:"-"`
}

// ConfirmUninstallResult is the durable response from the uninstall confirm
// endpoint; persisted as preview.last_result and replayed on retries.
type ConfirmUninstallResult struct {
	Status         string `json:"status"`           // "uninstalled" or "no_install"
	PausedAppCount int64  `json:"paused_app_count"` // total apps marked paused
	Replayed       bool   `json:"-"`
}

// InstallStatus describes the current install state polled by CLI.
type InstallStatus struct {
	Installed       bool   `json:"installed"`
	GithubInstallID *int64 `json:"github_install_id,omitempty"`
}

// PreviewInstall opens an install_github_app preview row. The team is loaded
// fresh so a stale URL never points at a deleted team.
func (s *Service) PreviewInstall(ctx context.Context, teamID, actorUserID, teamSlug string) (PreviewResult, error) {
	if teamID == "" {
		return PreviewResult{}, ErrTeamMissing
	}
	if actorUserID == "" {
		return PreviewResult{}, ErrActorMissing
	}
	summary := fmt.Sprintf(SummaryInstall, teamSlug)
	preview, err := s.store.CreatePreview(ctx, teamID, actorUserID, ActionInstall, json.RawMessage(`{}`), summary)
	if err != nil {
		return PreviewResult{}, err
	}
	return PreviewResult{
		PreviewID: preview.ID,
		Action:    preview.Action,
		Summary:   summary,
		ExpiresAt: preview.ExpiresAt,
	}, nil
}

// ConfirmInstall signs the install state token, persists it in the preview row
// for idempotent replay, and returns the GitHub install URL.
func (s *Service) ConfirmInstall(ctx context.Context, teamID, actorUserID, previewID string) (ConfirmInstallResult, error) {
	if teamID == "" {
		return ConfirmInstallResult{}, ErrTeamMissing
	}
	if actorUserID == "" {
		return ConfirmInstallResult{}, ErrActorMissing
	}
	preview, err := s.loadPreview(ctx, teamID, actorUserID, previewID, ActionInstall)
	if err != nil {
		return ConfirmInstallResult{}, err
	}
	if preview.ConsumedAt != nil {
		if len(preview.LastResult) == 0 {
			return ConfirmInstallResult{}, ErrPreviewConsumed
		}
		var replay ConfirmInstallResult
		if err := json.Unmarshal(preview.LastResult, &replay); err != nil {
			return ConfirmInstallResult{}, fmt.Errorf("decode last_result: %w", err)
		}
		replay.Replayed = true
		return replay, nil
	}
	if preview.ExpiresAt.Before(s.now().UTC()) {
		return ConfirmInstallResult{}, ErrPreviewExpired
	}
	if s.stateSigner == nil {
		return ConfirmInstallResult{}, ErrSignerMissing
	}

	state, err := s.stateSigner.SignState(teamID, actorUserID, previewID)
	if err != nil {
		return ConfirmInstallResult{}, fmt.Errorf("sign state: %w", err)
	}

	result := ConfirmInstallResult{
		InstallURL: s.installURLFn(state),
		ExpiresAt:  s.now().UTC().Add(s.stateTTL),
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return ConfirmInstallResult{}, fmt.Errorf("encode result: %w", err)
	}
	if err := s.store.ConsumePreviewWithResult(ctx, preview.ID, encoded); err != nil {
		if errors.Is(err, db.ErrPreviewConsumed) {
			return ConfirmInstallResult{}, ErrPreviewConsumed
		}
		return ConfirmInstallResult{}, err
	}
	return result, nil
}

// CallbackResult describes the parsed outcome of an install callback.
type CallbackResult struct {
	TeamID        string
	TeamSlug      string
	InstallID     int64
	PreviousValid bool
	Replaced      bool // true when the team previously had a different install_id
}

// HandleCallback verifies the state HMAC, ensures the actor is still owner of
// the team, and binds the installation id (spec § 4.4 hard rules #2, #4).
func (s *Service) HandleCallback(ctx context.Context, installationRaw, state string) (CallbackResult, error) {
	if s.stateSigner == nil {
		return CallbackResult{}, ErrSignerMissing
	}
	installID, err := strconv.ParseInt(strings.TrimSpace(installationRaw), 10, 64)
	if err != nil || installID <= 0 {
		return CallbackResult{}, fmt.Errorf("%w: invalid installation id", ErrStateInvalid)
	}

	teamID, actorUserID, previewID, _, err := s.stateSigner.VerifyState(state)
	if err != nil {
		return CallbackResult{}, fmt.Errorf("%w: %v", ErrStateInvalid, err)
	}

	preview, err := s.store.GetPreview(ctx, previewID)
	if err != nil {
		if errors.Is(err, db.ErrPreviewNotFound) {
			return CallbackResult{}, fmt.Errorf("%w: preview not found", ErrStateInvalid)
		}
		return CallbackResult{}, err
	}
	if preview.Action != ActionInstall || preview.TeamID != teamID || preview.ActorUserID != actorUserID {
		return CallbackResult{}, fmt.Errorf("%w: preview mismatch", ErrStateInvalid)
	}
	if preview.ConsumedAt == nil {
		return CallbackResult{}, fmt.Errorf("%w: preview not consumed", ErrStateInvalid)
	}

	role, err := s.store.GetTeamMembershipRole(ctx, teamID, actorUserID)
	if err != nil {
		return CallbackResult{}, fmt.Errorf("%w: role check failed", ErrStateInvalid)
	}
	if !strings.EqualFold(role, "owner") {
		return CallbackResult{}, fmt.Errorf("%w: actor not owner", ErrStateInvalid)
	}

	team, err := s.store.GetTeamByID(ctx, teamID)
	if err != nil {
		return CallbackResult{}, err
	}

	replaced := team.GithubInstallID != nil && *team.GithubInstallID != installID
	args := map[string]any{
		"installation_id": installID,
		"state_hash":      shortHash(state),
		"previous_id":     copyInstallID(team.GithubInstallID),
	}
	resultPayload := map[string]any{
		"status":   "ok",
		"replaced": replaced,
	}
	if err := s.store.SetTeamGitHubInstall(ctx, teamID, actorUserID, &installID, "github_app_install_callback", args, resultPayload); err != nil {
		return CallbackResult{}, err
	}

	return CallbackResult{
		TeamID:        teamID,
		TeamSlug:      team.Slug,
		InstallID:     installID,
		PreviousValid: team.GithubInstallID != nil,
		Replaced:      replaced,
	}, nil
}

// SuccessRedirect returns the URL the callback should redirect to.
func (s *Service) SuccessRedirect(teamSlug string) string {
	if s.successPage == "" {
		return installURLDocsFallback
	}
	u, err := url.Parse(s.successPage)
	if err != nil {
		return s.successPage
	}
	q := u.Query()
	q.Set("team", teamSlug)
	q.Set("status", "installed")
	u.RawQuery = q.Encode()
	return u.String()
}

// PreviewUninstall creates an uninstall_github_app preview row.
func (s *Service) PreviewUninstall(ctx context.Context, teamID, actorUserID, teamSlug string) (PreviewResult, error) {
	if teamID == "" {
		return PreviewResult{}, ErrTeamMissing
	}
	if actorUserID == "" {
		return PreviewResult{}, ErrActorMissing
	}
	summary := fmt.Sprintf(SummaryUninstall, teamSlug)
	preview, err := s.store.CreatePreview(ctx, teamID, actorUserID, ActionUninstall, json.RawMessage(`{}`), summary)
	if err != nil {
		return PreviewResult{}, err
	}
	return PreviewResult{
		PreviewID: preview.ID,
		Action:    preview.Action,
		Summary:   summary,
		ExpiresAt: preview.ExpiresAt,
	}, nil
}

// ConfirmUninstall enforces the spec § 5.1 sequence: GitHub DELETE → clear
// team binding → pause apps → drop token cache, all atomic via the audit
// transaction.
func (s *Service) ConfirmUninstall(ctx context.Context, teamID, actorUserID, previewID string) (ConfirmUninstallResult, error) {
	if teamID == "" {
		return ConfirmUninstallResult{}, ErrTeamMissing
	}
	if actorUserID == "" {
		return ConfirmUninstallResult{}, ErrActorMissing
	}
	preview, err := s.loadPreview(ctx, teamID, actorUserID, previewID, ActionUninstall)
	if err != nil {
		return ConfirmUninstallResult{}, err
	}
	if preview.ConsumedAt != nil {
		if len(preview.LastResult) == 0 {
			return ConfirmUninstallResult{}, ErrPreviewConsumed
		}
		var replay ConfirmUninstallResult
		if err := json.Unmarshal(preview.LastResult, &replay); err != nil {
			return ConfirmUninstallResult{}, fmt.Errorf("decode last_result: %w", err)
		}
		replay.Replayed = true
		return replay, nil
	}
	if preview.ExpiresAt.Before(s.now().UTC()) {
		return ConfirmUninstallResult{}, ErrPreviewExpired
	}

	team, err := s.store.GetTeamByID(ctx, teamID)
	if err != nil {
		return ConfirmUninstallResult{}, err
	}

	result := ConfirmUninstallResult{Status: "uninstalled"}
	if team.GithubInstallID == nil {
		result.Status = "no_install"
	} else {
		installID := *team.GithubInstallID
		if s.apiClient != nil {
			if err := s.apiClient.DeleteInstallation(ctx, installID); err != nil {
				return ConfirmUninstallResult{}, fmt.Errorf("github delete installation: %w", err)
			}
		}
		if s.tokenCache != nil {
			s.tokenCache.Invalidate(installID)
		}
	}

	paused, err := s.store.PauseTeamApps(ctx, teamID)
	if err != nil {
		return ConfirmUninstallResult{}, err
	}
	result.PausedAppCount = paused

	previousID := copyInstallID(team.GithubInstallID)
	args := map[string]any{
		"previous_id": previousID,
	}
	resultPayload := map[string]any{
		"status":           result.Status,
		"paused_app_count": paused,
	}
	if err := s.store.SetTeamGitHubInstall(ctx, teamID, actorUserID, nil, "github_app_uninstall", args, resultPayload); err != nil {
		return ConfirmUninstallResult{}, err
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return ConfirmUninstallResult{}, fmt.Errorf("encode result: %w", err)
	}
	if err := s.store.ConsumePreviewWithResult(ctx, preview.ID, encoded); err != nil {
		if errors.Is(err, db.ErrPreviewConsumed) {
			return ConfirmUninstallResult{}, ErrPreviewConsumed
		}
		return ConfirmUninstallResult{}, err
	}
	return result, nil
}

// GetInstallStatus reports whether the team has a current binding.
func (s *Service) GetInstallStatus(ctx context.Context, teamID string) (InstallStatus, error) {
	if teamID == "" {
		return InstallStatus{}, ErrTeamMissing
	}
	team, err := s.store.GetTeamByID(ctx, teamID)
	if err != nil {
		return InstallStatus{}, err
	}
	status := InstallStatus{}
	if team.GithubInstallID != nil {
		status.Installed = true
		status.GithubInstallID = copyInstallID(team.GithubInstallID)
	}
	return status, nil
}

// WebhookOutcome summarises the side effects of a single installation webhook
// for log/audit/test assertions.
type WebhookOutcome struct {
	Action         string
	TeamID         string
	TeamSlug       string
	InstallID      int64
	PausedAppCount int64
	Acted          bool
	Duplicate      bool
}

// HandleInstallationWebhook applies the side effects mandated by spec § 7.1
// for the `installation` event. The webhook signature is expected to be
// validated by the caller; this method handles dedup + state transitions.
func (s *Service) HandleInstallationWebhook(ctx context.Context, deliveryID string, payload []byte) (WebhookOutcome, error) {
	inserted, err := s.store.RegisterWebhookDelivery(ctx, "github", deliveryID)
	if err != nil {
		return WebhookOutcome{}, fmt.Errorf("dedup register: %w", err)
	}
	if !inserted {
		return WebhookOutcome{Duplicate: true}, nil
	}

	var event struct {
		Action       string `json:"action"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return WebhookOutcome{}, fmt.Errorf("decode payload: %w", err)
	}
	if event.Installation.ID <= 0 {
		return WebhookOutcome{}, errors.New("missing installation.id")
	}
	outcome := WebhookOutcome{
		Action:    event.Action,
		InstallID: event.Installation.ID,
	}

	switch event.Action {
	case "deleted", "suspend":
		team, err := s.store.FindTeamByGitHubInstallID(ctx, event.Installation.ID)
		if err != nil {
			if errors.Is(err, db.ErrTeamNotFound) {
				return outcome, nil
			}
			return WebhookOutcome{}, err
		}
		paused, err := s.store.PauseTeamApps(ctx, team.ID)
		if err != nil {
			return WebhookOutcome{}, err
		}
		args := map[string]any{
			"installation_id": event.Installation.ID,
			"webhook_action":  event.Action,
			"delivery_id":     deliveryID,
		}
		resultPayload := map[string]any{
			"status":           "unbound",
			"paused_app_count": paused,
		}
		action := "github_app_webhook_deleted"
		if event.Action == "suspend" {
			action = "github_app_webhook_suspended"
		}
		if err := s.store.SetTeamGitHubInstall(ctx, team.ID, "", nil, action, args, resultPayload); err != nil {
			return WebhookOutcome{}, err
		}
		if s.tokenCache != nil {
			s.tokenCache.Invalidate(event.Installation.ID)
		}
		outcome.TeamID = team.ID
		outcome.TeamSlug = team.Slug
		outcome.PausedAppCount = paused
		outcome.Acted = true
		return outcome, nil

	case "unsuspend":
		// Suspended teams keep no install id (cleared above), so unsuspend
		// must restore the binding from the payload but does NOT auto-resume
		// apps (spec § 7.1 table row).
		team, err := s.store.FindTeamByGitHubInstallID(ctx, event.Installation.ID)
		if err == nil {
			outcome.TeamID = team.ID
			outcome.TeamSlug = team.Slug
		}
		return outcome, nil

	case "created", "new_permissions_accepted":
		// `created` is redundant with the callback flow; webhook dedup row is
		// enough to record receipt. `new_permissions_accepted` only audits
		// the scope change (spec § 7.1 table row).
		return outcome, nil

	default:
		return outcome, nil
	}
}

func (s *Service) loadPreview(ctx context.Context, teamID, actorUserID, previewID, expectedAction string) (db.Preview, error) {
	if strings.TrimSpace(previewID) == "" {
		return db.Preview{}, ErrPreviewNotFound
	}
	preview, err := s.store.GetPreview(ctx, previewID)
	if err != nil {
		if errors.Is(err, db.ErrPreviewNotFound) {
			return db.Preview{}, ErrPreviewNotFound
		}
		return db.Preview{}, err
	}
	if preview.Action != expectedAction || preview.TeamID != teamID || preview.ActorUserID != actorUserID {
		return db.Preview{}, ErrPreviewNotFound
	}
	return preview, nil
}

func shortHash(state string) string {
	if len(state) <= 8 {
		return state
	}
	return state[:8]
}

func copyInstallID(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
