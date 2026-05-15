// Package server implements the 0ops backend server.
package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
	ratelimit "github.com/winshare/zeroops/internal/server/middleware/ratelimit"
	"github.com/winshare/zeroops/internal/server/services/cloudflare"
	createappsvc "github.com/winshare/zeroops/internal/server/services/createapp"
	"github.com/winshare/zeroops/internal/server/services/githuboauth"
	gitopssvc "github.com/winshare/zeroops/internal/server/services/gitops"
	k3ssvc "github.com/winshare/zeroops/internal/server/services/k3s"
	workflowdispatch "github.com/winshare/zeroops/internal/server/services/workflowdispatch"
	"github.com/winshare/zeroops/internal/shared/dto"
	"github.com/winshare/zeroops/internal/shared/rbac"
)

type appsStore interface {
	auth.Store
	ListTeamApps(ctx context.Context, teamID string, limit int32, afterID *string) ([]db.App, error)
	GetTeamAppBySlug(ctx context.Context, teamID string, slug string) (db.App, error)
	ListDomainsByAppSlug(ctx context.Context, teamID string, appSlug string) ([]db.DomainBinding, error)
	GetLatestDeployByAppSlug(ctx context.Context, teamID string, appSlug string) (db.DeployRun, error)
	ListDeployLogLines(ctx context.Context, teamID string, appSlug string, limit int) ([]db.DeployLogLine, error)
	HasAnyOwner(ctx context.Context) (bool, error)
	BootstrapOwner(ctx context.Context, params db.BootstrapOwnerParams) (teamID string, userID string, err error)
	ListTeamMembers(ctx context.Context, teamID string) ([]db.Member, error)
	ListTeamTokens(ctx context.Context, teamID string) ([]db.CliToken, error)
	CreatePreview(ctx context.Context, teamID, actorUserID, action string, args json.RawMessage, summary string) (db.Preview, error)
	GetPreview(ctx context.Context, previewID string) (db.Preview, error)
	ConsumePreview(ctx context.Context, previewID string) error
	ConsumePreviewWithResult(ctx context.Context, previewID string, result json.RawMessage) error
	InviteMember(ctx context.Context, params db.InviteMemberParams) (db.Member, error)
	RemoveMember(ctx context.Context, teamID, actorUserID, targetUserID string) error
	ResolveUserDefaultTeamByGithubLogin(ctx context.Context, githubLogin string) (userID string, teamID string, teamSlug string, err error)
	GetOrCreateUserAndPersonalTeam(ctx context.Context, githubLogin string) (userID string, teamID string, teamSlug string, err error)
	CreateCLIToken(ctx context.Context, ownerUserID, teamID string, scopes []string) (string, error)
	CreatePAT(ctx context.Context, ownerUserID, teamID, name string, scopes []string, expiresAt time.Time) (string, error)
	RevokeCLITokenByID(ctx context.Context, tokenID string) error
	RevokePATByName(ctx context.Context, teamID, name string) error
	CreateApp(ctx context.Context, params db.AppCreateParams) (db.AppCreateResult, error)
	DeleteAppByID(ctx context.Context, appID string) error
	RegisterWebhookDelivery(ctx context.Context, provider, deliveryID string) (bool, error)
	ApplyDeployCallback(ctx context.Context, params db.DeployCallbackParams) error
	GetTeamByID(ctx context.Context, teamID string) (db.Team, error)
	FindTeamByGitHubInstallID(ctx context.Context, installID int64) (db.Team, error)
	SetTeamGitHubInstall(ctx context.Context, teamID, actorUserID string, installID *int64, action string, args map[string]any, result map[string]any) error
	PauseTeamApps(ctx context.Context, teamID string) (int64, error)

	// M4.1 webhook-and-redeploy
	FindLiveAppsByRepoAndBranch(ctx context.Context, teamID, repoURL, branch string) ([]db.App, error)
	HasInFlightDeployRun(ctx context.Context, appID string) (bool, error)
	InsertRedeployRun(ctx context.Context, params db.InsertRedeployRunParams) (db.InsertRedeployRunResult, error)
	AppendWebhookAudit(ctx context.Context, teamID, action string, args map[string]any, result map[string]any) error
}

// infraK3sClient provides K3s namespace management operations.
// EnsureTeamIsolation is the atomic provisioning entry point used by the
// create_app saga; the secondary methods remain for reconciler / integration
// callers that need targeted re-application (e.g. plan-tier upgrade).
type infraK3sClient interface {
	EnsureTeamIsolation(ctx context.Context, teamID, teamSlug, planTier string) (string, error)
	EnsureResourceQuota(ctx context.Context, namespace, planTier string) error
	EnsureLimitRange(ctx context.Context, namespace string) error
	EnsureNetworkPolicy(ctx context.Context, namespace string) error
	PatchNamespacePSA(ctx context.Context, namespace string) error
}

// infraCloudflareClient provides Cloudflare tunnel and DNS management operations.
type infraCloudflareClient interface {
	RouteAppToDomain(ctx context.Context, teamID, teamSlug, appSlug string) (string, error)
	CreateTunnelRoute(ctx context.Context, teamID, appSlug, backendURL string) error
}

type toolGrantsStore interface {
	IsToolGranted(ctx context.Context, teamID, userID, toolID string) (bool, error)
	ListGrantedTools(ctx context.Context, teamID, userID string) ([]string, error)
	UpsertToolGrant(ctx context.Context, teamID, userID, toolID string, allowed bool, grantedByActorID *string) error
	RevokeToolGrant(ctx context.Context, teamID, userID, toolID string) error
	ListAllUserGrants(ctx context.Context, teamID, userID string) ([]db.ToolGrant, error)
}

type routerStore interface {
	appsStore
	teamsStore
	toolGrantsStore
}

type appCursor struct {
	ID string    `json:"id"`
	TS time.Time `json:"ts"`
}

const (
	previewActionInvite    = "invite_member"
	previewActionRemove    = "remove_member"
	previewActionCreate    = "create_app"
	deployCallbackProvider = "gha-callback"
)

var appSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$`)

var (
	recordCreateAppPreviewMetric = func(string) {}
	recordCreateAppConfirmMetric = func(string, bool) {}
	recordPreviewCreatedMetric   = func(string) {}
	recordPreviewConsumedMetric  = func(string, string, time.Duration) {}
	recordDeployTerminalMetric   = func(string) {}
	recordDeployLeadTimeMetric   = func(time.Duration) {}
	recordDeployFailureMetric    = func(string, string) {}
	newArgoCDStatusProvider      = func() argoCDStatusProvider { return nil }
)

type argoCDStatusProvider interface {
	GetApplicationStatus(ctx context.Context, teamSlug, appSlug string) (argoCDApplicationStatus, error)
}

type argoCDApplicationStatus struct {
	SyncStatus   string
	HealthStatus string
}

type k3sArgoCDClient interface {
	GetApplicationStatus(ctx context.Context, teamSlug, appSlug string) (k3ssvc.ApplicationStatus, error)
}

type k3sArgoCDStatusProvider struct {
	client k3sArgoCDClient
}

func (p k3sArgoCDStatusProvider) GetApplicationStatus(ctx context.Context, teamSlug, appSlug string) (argoCDApplicationStatus, error) {
	status, err := p.client.GetApplicationStatus(ctx, teamSlug, appSlug)
	if err != nil {
		return argoCDApplicationStatus{}, err
	}
	return argoCDApplicationStatus{
		SyncStatus:   status.SyncStatus,
		HealthStatus: status.HealthStatus,
	}, nil
}

// BindCreateAppMetrics wires create_app-specific metric recorders.
func BindCreateAppMetrics(
	previewRecorder func(outcome string),
	confirmRecorder func(outcome string, idempotentReplay bool),
) {
	if previewRecorder == nil {
		recordCreateAppPreviewMetric = func(string) {}
	} else {
		recordCreateAppPreviewMetric = previewRecorder
	}
	if confirmRecorder == nil {
		recordCreateAppConfirmMetric = func(string, bool) {}
	} else {
		recordCreateAppConfirmMetric = confirmRecorder
	}
}

// BindPlatformMetrics wires cross-feature observability recorders.
func BindPlatformMetrics(
	previewCreatedRecorder func(action string),
	previewConsumedRecorder func(action, outcome string, latency time.Duration),
	deployTerminalRecorder func(outcome string),
	deployLeadTimeRecorder func(latency time.Duration),
	deployFailureRecorder func(stage, classification string),
) {
	if previewCreatedRecorder == nil {
		recordPreviewCreatedMetric = func(string) {}
	} else {
		recordPreviewCreatedMetric = previewCreatedRecorder
	}
	if previewConsumedRecorder == nil {
		recordPreviewConsumedMetric = func(string, string, time.Duration) {}
	} else {
		recordPreviewConsumedMetric = previewConsumedRecorder
	}
	if deployTerminalRecorder == nil {
		recordDeployTerminalMetric = func(string) {}
	} else {
		recordDeployTerminalMetric = deployTerminalRecorder
	}
	if deployLeadTimeRecorder == nil {
		recordDeployLeadTimeMetric = func(time.Duration) {}
	} else {
		recordDeployLeadTimeMetric = deployLeadTimeRecorder
	}
	if deployFailureRecorder == nil {
		recordDeployFailureMetric = func(string, string) {}
	} else {
		recordDeployFailureMetric = deployFailureRecorder
	}
}

type deviceLoginSession struct {
	GithubLogin     string
	UserCode        string
	DeviceCode      string
	VerificationURI string
	AccessToken     string
	Status          string // "pending" or "verified"
	IntervalSeconds int
	ExpiresAt       time.Time
}

var deviceLoginSessions sync.Map

type githubOAuthClient interface {
	StartDeviceAuthorization(ctx context.Context) (githuboauth.DeviceAuthorization, error)
	ExchangeDeviceCode(ctx context.Context, deviceCode string) (githuboauth.AccessTokenResponse, error)
	FetchUser(ctx context.Context, accessToken string) (githuboauth.UserProfile, error)
}

type deployCallbackRequest struct {
	RunID                 string   `json:"run_id"`
	Status                string   `json:"status"`
	TraceID               *string  `json:"trace_id,omitempty"`
	ErrorSummary          *string  `json:"error_summary,omitempty"`
	FailureClassification *string  `json:"failure_classification,omitempty"`
	OpsToken              *string  `json:"ops_token,omitempty"`
	Image                 *string  `json:"image,omitempty"`
	BuildMinutes          *float64 `json:"build_minutes,omitempty"`
	ImageSizeBytes        *int64   `json:"image_size_bytes,omitempty"`
	GitopsCommitSHA       *string  `json:"gitops_commit_sha,omitempty"`
	ScanSummary           *struct {
		High     *int `json:"high,omitempty"`
		Critical *int `json:"critical,omitempty"`
		ExitCode *int `json:"exit_code,omitempty"`
	} `json:"scan_summary,omitempty"`
}

func listAppsHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const defaultPageSize = 50
		const maxPageSize = 200

		pageSize := defaultPageSize
		if raw := r.URL.Query().Get("page_size"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > maxPageSize {
				apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid page_size", map[string]any{"field": "page_size"})
				return
			}
			pageSize = n
		}

		afterID, err := decodeAppCursor(r.URL.Query().Get("cursor"))
		if err != nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid cursor", map[string]any{"field": "cursor"})
			return
		}

		rows, err := store.ListTeamApps(r.Context(), auth.TeamID(r.Context()), int32(pageSize+1), afterID)
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to list apps", nil)
			return
		}

		var nextCursor *string
		if len(rows) > pageSize {
			encoded := encodeAppCursor(rows[pageSize-1].ID, rows[pageSize-1].CreatedAt)
			nextCursor = &encoded
			rows = rows[:pageSize]
		}

		items := make([]dto.AppRef, 0, len(rows))
		for _, row := range rows {
			items = append(items, newAppRef(row))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.ListAppsResponse{
			Items:      items,
			NextCursor: nextCursor,
			PageSize:   pageSize,
		})
	}
}

func getAppHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		row, err := store.GetTeamAppBySlug(r.Context(), auth.TeamID(r.Context()), chi.URLParam(r, "app_slug"))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				apperror.Write(w, "app_not_found", apperror.ClassNotFound, "app not found", map[string]any{
					"app_slug": chi.URLParam(r, "app_slug"),
				})
				return
			}
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to get app", nil)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(newAppRef(row))
	}
}

func previewCreateAppHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		outcome := "error"
		defer func() { recordCreateAppPreviewMetric(outcome) }()

		var req dto.AppCreateRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if !validateAppCreateRequest(w, req) {
			return
		}

		if _, err := store.GetTeamAppBySlug(r.Context(), auth.TeamID(r.Context()), req.Slug); err == nil {
			apperror.Write(w, "slug_taken", apperror.ClassConflict, "app slug already exists", map[string]any{"slug": req.Slug})
			return
		} else if !errors.Is(err, pgx.ErrNoRows) {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to check app slug", nil)
			return
		}

		args, err := json.Marshal(req)
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to encode preview args", nil)
			return
		}

		summary := fmt.Sprintf("Create app %q from %s", req.Slug, req.RepoURL)
		out, err := store.CreatePreview(
			r.Context(),
			auth.TeamID(r.Context()),
			auth.ActorUserID(r.Context()),
			previewActionCreate,
			args,
			summary,
		)
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to create preview", nil)
			return
		}
		writePreviewResponse(w, out, summary)
		recordPreviewCreatedMetric(previewActionCreate)
		outcome = "success"
	}
}

func createAppHandler(store appsStore, k3sClient infraK3sClient, cfClient infraCloudflareClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		outcome := "error"
		idempotentReplay := false
		defer func() { recordCreateAppConfirmMetric(outcome, idempotentReplay) }()

		var req dto.ConfirmCreateAppRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		service := createappsvc.New(store, k3sClient, cfClient, newGitOpsService(), newWorkflowDispatchClient(), newWorkflowDispatchTokenSigner(), callbackBaseURL())
		confirmResult, err := service.Confirm(
			r.Context(),
			auth.TeamID(r.Context()),
			auth.ActorUserID(r.Context()),
			auth.TeamSlug(r.Context()),
			req.PreviewID,
			strings.TrimSpace(middleware.GetReqID(r.Context())),
		)
		if err != nil {
			switch {
			case errors.Is(err, createappsvc.ErrPreviewNotFound):
				apperror.Write(w, "preview_not_found", apperror.ClassNotFound, "preview not found", nil)
			case errors.Is(err, createappsvc.ErrPreviewConsumed):
				apperror.Write(w, "preview_consumed", apperror.ClassConflict, "preview already consumed", nil)
			case errors.Is(err, createappsvc.ErrPreviewExpired):
				apperror.Write(w, "preview_expired", apperror.ClassConflict, "preview expired", nil)
			case errors.Is(err, createappsvc.ErrSlugTaken):
				apperror.Write(w, "slug_taken", apperror.ClassConflict, "app slug already exists", nil)
			case errors.Is(err, createappsvc.ErrValidationFailed):
				apperror.Write(w, "validation_failed", apperror.ClassBadRequest, err.Error(), nil)
			case errors.Is(err, cloudflare.ErrRouteMissing), errors.Is(err, cloudflare.ErrConfigMissing):
				apperror.Write(w, "cloudflare_api_error", apperror.ClassUnavailable, "cloudflare api unavailable", nil)
			case errors.Is(err, cloudflare.ErrRateLimited):
				apperror.Write(w, "cloudflare_rate_limited", apperror.ClassTooManyRequests, "cloudflare rate limited", nil)
			default:
				apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to create app", nil)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(confirmResult.Response)
		outcome = "success"
		idempotentReplay = confirmResult.Replayed
		consumeOutcome := "success"
		if confirmResult.Replayed {
			consumeOutcome = "idempotent_replay"
		}
		recordPreviewConsumedMetric(previewActionCreate, consumeOutcome, previewConsumeLatency(confirmResult.PreviewCreatedAt))
	}
}

func newGitOpsService() gitopssvc.Service {
	svc, err := gitopssvc.NewServiceFromEnv()
	if err != nil {
		return nil
	}
	return svc
}

func deployRunCallbackHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid payload", nil)
			return
		}

		runID := strings.TrimSpace(chi.URLParam(r, "run_id"))
		if runID == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "run_id is required", nil)
			return
		}

		var req deployCallbackRequest
		if err := json.Unmarshal(body, &req); err != nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid json payload", nil)
			return
		}
		timestamp := strings.TrimSpace(r.Header.Get("X-0ops-Timestamp"))
		signature := strings.TrimSpace(r.Header.Get("X-0ops-Signature"))
		if !validateCallbackTimestamp(timestamp, time.Now().UTC(), 5*time.Minute) {
			apperror.Write(w, "stale_timestamp", apperror.ClassBadRequest, "stale callback timestamp", nil)
			return
		}
		if !validateDeployCallbackSignature(timestamp, body, signature, req.OpsToken) {
			apperror.Write(w, "invalid_signature", apperror.ClassUnauthorized, "invalid callback signature", nil)
			return
		}
		if strings.TrimSpace(req.RunID) != runID {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "run_id mismatch", nil)
			return
		}
		status, ok := normalizeDeployStatus(req.Status)
		if !ok {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid deploy status", map[string]any{"field": "status"})
			return
		}
		traceID := trimStringPtr(req.TraceID)
		if traceID == nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "trace_id is required", map[string]any{"field": "trace_id"})
			return
		}
		failureClassification := trimStringPtr(req.FailureClassification)
		if status == "failed" && failureClassification == nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "failure_classification is required for failed status", map[string]any{"field": "failure_classification"})
			return
		}
		if failureClassification != nil && !isValidFailureClassification(*failureClassification) {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid failure_classification", map[string]any{"field": "failure_classification"})
			return
		}

		deliveryID := strings.TrimSpace(r.Header.Get("X-0ops-Delivery-ID"))
		if deliveryID == "" {
			deliveryID = runID
		}
		inserted, err := store.RegisterWebhookDelivery(r.Context(), deployCallbackProvider, deliveryID)
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to record callback delivery", nil)
			return
		}
		if !inserted {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "duplicate"})
			return
		}

		err = store.ApplyDeployCallback(r.Context(), db.DeployCallbackParams{
			RunID:                 runID,
			Status:                status,
			TraceID:               traceID,
			ErrorSummary:          trimStringPtr(req.ErrorSummary),
			FailureClassification: failureClassification,
			Event:                 buildDeployCallbackEvent(req, status),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				apperror.Write(w, "deploy_run_not_found", apperror.ClassNotFound, "deploy run not found", nil)
				return
			}
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to apply deploy callback", nil)
			return
		}
		if outcome, ok := deployTerminalOutcome(status); ok {
			recordDeployTerminalMetric(outcome)
			if req.BuildMinutes != nil && *req.BuildMinutes > 0 {
				recordDeployLeadTimeMetric(time.Duration(*req.BuildMinutes * float64(time.Minute)))
			}
			if outcome == "failed" && failureClassification != nil {
				recordDeployFailureMetric("callback", *failureClassification)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}
}

func inspectRepoHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appSlug := chi.URLParam(r, "app_slug")
		row, err := store.GetTeamAppBySlug(r.Context(), auth.TeamID(r.Context()), appSlug)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				apperror.Write(w, "app_not_found", apperror.ClassNotFound, "app not found", map[string]any{"app_slug": appSlug})
				return
			}
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to inspect repo", nil)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.RepoInspectResponse{
			AppSlug:           row.Slug,
			RepoURL:           row.RepoURL,
			RepoDefaultBranch: row.RepoDefaultBranch,
			Builder:           row.Builder,
		})
	}
}

func getDeployStatusHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appSlug := r.URL.Query().Get("app_slug")
		if appSlug == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "app_slug is required", map[string]any{"field": "app_slug"})
			return
		}

		row, err := store.GetLatestDeployByAppSlug(r.Context(), auth.TeamID(r.Context()), appSlug)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				apperror.Write(w, "deploy_not_found", apperror.ClassNotFound, "deploy not found", map[string]any{"app_slug": appSlug})
				return
			}
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to get deploy status", nil)
			return
		}
		if provider := newArgoCDStatusProvider(); provider != nil {
			if argoStatus, err := provider.GetApplicationStatus(r.Context(), auth.TeamSlug(r.Context()), appSlug); err == nil {
				if mapped, ok := mapArgoCDDeployStatus(argoStatus.SyncStatus, argoStatus.HealthStatus); ok {
					row.Status = mapped
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.DeployStatusResponse{
			DeployID:     row.ID,
			AppSlug:      row.AppSlug,
			Status:       row.Status,
			CommitSHA:    row.CommitSHA,
			Ref:          row.Ref,
			ErrorSummary: row.ErrorSummary,
			StartedAt:    row.StartedAt,
			FinishedAt:   row.FinishedAt,
		})
	}
}

func tailLogsHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appSlug := r.URL.Query().Get("app_slug")
		if appSlug == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "app_slug is required", map[string]any{"field": "app_slug"})
			return
		}
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 1000 {
				apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid limit", map[string]any{"field": "limit"})
				return
			}
			limit = n
		}
		follow := false
		if raw := strings.TrimSpace(r.URL.Query().Get("follow")); raw != "" {
			v, err := strconv.ParseBool(raw)
			if err != nil {
				apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid follow", map[string]any{"field": "follow"})
				return
			}
			follow = v
		}

		rows, err := store.ListDeployLogLines(r.Context(), auth.TeamID(r.Context()), appSlug, limit)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				apperror.Write(w, "deploy_not_found", apperror.ClassNotFound, "deploy not found", map[string]any{"app_slug": appSlug})
				return
			}
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to tail logs", nil)
			return
		}
		if follow {
			flusher, ok := w.(http.Flusher)
			if !ok {
				apperror.Write(w, "internal_error", apperror.ClassInternal, "streaming unsupported", nil)
				return
			}

			var since *time.Time
			if raw := strings.TrimSpace(r.Header.Get("Last-Event-ID")); raw != "" {
				ts, err := time.Parse(time.RFC3339Nano, raw)
				if err != nil {
					apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid Last-Event-ID", map[string]any{"field": "Last-Event-ID"})
					return
				}
				since = &ts
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			traceID := middleware.GetReqID(r.Context())
			for _, row := range rows {
				ts := row.Timestamp.UTC()
				if since != nil && !ts.After(*since) {
					continue
				}
				payload, err := json.Marshal(dto.LogLine{Timestamp: ts, Message: row.Message})
				if err != nil {
					apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to encode log event", nil)
					return
				}
				if _, err := fmt.Fprintf(w, "id: %s\nevent: log\ndata: %s\n\n", ts.Format(time.RFC3339Nano), payload); err != nil {
					return
				}
				flusher.Flush()
				slog.Info("tail_logs_sse_event", "trace_id", traceID, "event", "log", "app_slug", appSlug)
			}

			if _, err := fmt.Fprint(w, "event: end\ndata: {\"reason\":\"eof\"}\n\n"); err != nil {
				return
			}
			flusher.Flush()
			slog.Info("tail_logs_sse_event", "trace_id", traceID, "event", "end", "app_slug", appSlug)
			return
		}

		items := make([]dto.LogLine, 0, len(rows))
		for _, row := range rows {
			items = append(items, dto.LogLine{Timestamp: row.Timestamp, Message: row.Message})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.TailLogsResponse{Items: items})
	}
}

func listDomainsHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appSlug := r.URL.Query().Get("app_slug")
		if appSlug == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "app_slug is required", map[string]any{"field": "app_slug"})
			return
		}

		rows, err := store.ListDomainsByAppSlug(r.Context(), auth.TeamID(r.Context()), appSlug)
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to list domains", nil)
			return
		}
		items := make([]dto.DomainRef, 0, len(rows))
		for _, row := range rows {
			items = append(items, dto.DomainRef{
				Hostname:   row.Hostname,
				Kind:       row.Kind,
				Verified:   row.Verified,
				VerifiedAt: row.VerifiedAt,
				ExpiresAt:  row.ExpiresAt,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.ListDomainsResponse{Items: items})
	}
}

func bootstrapOwnerHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dto.BootstrapOwnerRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.TeamSlug == "" || req.TeamName == "" || req.GithubLogin == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "team_slug, team_name, github_login are required", nil)
			return
		}

		teamID, userID, err := store.BootstrapOwner(r.Context(), db.BootstrapOwnerParams{
			TeamSlug:    req.TeamSlug,
			TeamName:    req.TeamName,
			GithubLogin: req.GithubLogin,
			Email:       req.Email,
		})
		if err != nil {
			if errors.Is(err, db.ErrBootstrapAlreadyDone) {
				apperror.Write(w, "bootstrap_already_done", apperror.ClassConflict, "owner already exists", nil)
				return
			}
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to bootstrap owner", nil)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(dto.BootstrapOwnerResponse{
			TeamID: teamID,
			UserID: userID,
		})
	}
}

func listMembersHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := store.ListTeamMembers(r.Context(), auth.TeamID(r.Context()))
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to list members", nil)
			return
		}
		items := make([]dto.MemberRef, 0, len(rows))
		for _, row := range rows {
			items = append(items, dto.MemberRef{
				UserID:      row.UserID,
				GithubLogin: row.GithubLogin,
				Email:       row.Email,
				Role:        row.Role,
				InvitedAt:   row.InvitedAt,
				JoinedAt:    row.JoinedAt,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.ListMembersResponse{Items: items})
	}
}

func previewInviteHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dto.InviteMemberRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.GithubLogin == nil && req.Email == nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "github_login or email is required", nil)
			return
		}
		if !validMemberRole(req.Role) {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid role", map[string]any{"field": "role"})
			return
		}
		args, err := json.Marshal(req)
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to encode preview args", nil)
			return
		}
		out, err := store.CreatePreview(r.Context(), auth.TeamID(r.Context()), auth.ActorUserID(r.Context()), previewActionInvite, args, "Invite team member")
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to create preview", nil)
			return
		}
		writePreviewResponse(w, out, "Invite team member")
		recordPreviewCreatedMetric(previewActionInvite)
	}
}

func inviteHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dto.ConfirmInviteMemberRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		preview, ok := validatePreview(w, r, store, req.PreviewID, previewActionInvite)
		if !ok {
			return
		}

		var payload dto.InviteMemberRequest
		if err := json.Unmarshal(preview.Args, &payload); err != nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid preview args", nil)
			return
		}
		member, err := store.InviteMember(r.Context(), db.InviteMemberParams{
			TeamID:      auth.TeamID(r.Context()),
			ActorUserID: auth.ActorUserID(r.Context()),
			GithubLogin: payload.GithubLogin,
			Email:       payload.Email,
			Role:        payload.Role,
		})
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to invite member", nil)
			return
		}
		if err := store.ConsumePreview(r.Context(), preview.ID); err != nil {
			apperror.Write(w, "preview_consumed", apperror.ClassConflict, "preview already consumed", nil)
			return
		}
		recordPreviewConsumedMetric(previewActionInvite, "success", previewConsumeLatency(preview.CreatedAt))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.InviteMemberResponse{
			Member: dto.MemberRef{
				UserID:      member.UserID,
				GithubLogin: member.GithubLogin,
				Email:       member.Email,
				Role:        member.Role,
				InvitedAt:   member.InvitedAt,
				JoinedAt:    member.JoinedAt,
			},
		})
	}
}

func previewRemoveHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dto.RemoveMemberRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.UserID == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "user_id is required", nil)
			return
		}
		args, err := json.Marshal(req)
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to encode preview args", nil)
			return
		}
		out, err := store.CreatePreview(r.Context(), auth.TeamID(r.Context()), auth.ActorUserID(r.Context()), previewActionRemove, args, "Remove team member")
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to create preview", nil)
			return
		}
		writePreviewResponse(w, out, "Remove team member")
		recordPreviewCreatedMetric(previewActionRemove)
	}
}

func removeHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dto.ConfirmRemoveMemberRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		preview, ok := validatePreview(w, r, store, req.PreviewID, previewActionRemove)
		if !ok {
			return
		}
		var payload dto.RemoveMemberRequest
		if err := json.Unmarshal(preview.Args, &payload); err != nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid preview args", nil)
			return
		}

		if err := store.RemoveMember(r.Context(), auth.TeamID(r.Context()), auth.ActorUserID(r.Context()), payload.UserID); err != nil {
			switch {
			case errors.Is(err, db.ErrMemberNotFound):
				apperror.Write(w, "member_not_found", apperror.ClassNotFound, "member not found", nil)
				return
			case errors.Is(err, db.ErrOwnerRemoval):
				apperror.Write(w, "owner_remove_forbidden", apperror.ClassConflict, "cannot remove owner", nil)
				return
			default:
				apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to remove member", nil)
				return
			}
		}
		if err := store.ConsumePreview(r.Context(), preview.ID); err != nil {
			apperror.Write(w, "preview_consumed", apperror.ClassConflict, "preview already consumed", nil)
			return
		}
		recordPreviewConsumedMetric(previewActionRemove, "success", previewConsumeLatency(preview.CreatedAt))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "removed"})
	}
}

func listTokensHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := store.ListTeamTokens(r.Context(), auth.TeamID(r.Context()))
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to list tokens", nil)
			return
		}
		items := make([]dto.PATListItem, 0, len(rows))
		for _, row := range rows {
			if row.Kind != "pat" {
				continue
			}
			items = append(items, dto.PATListItem{
				Name:       row.Name,
				Scopes:     append([]string(nil), row.Scopes...),
				CreatedAt:  row.CreatedAt,
				LastUsedAt: row.LastUsedAt,
				ExpiresAt:  row.ExpiresAt,
				RevokedAt:  row.RevokedAt,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.PATListResponse{Items: items})
	}
}

func createTokenHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dto.PATCreateRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "name is required", map[string]any{"field": "name"})
			return
		}
		if len(req.Scopes) == 0 {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "scopes are required", map[string]any{"field": "scopes"})
			return
		}
		expiresDays := req.ExpiresDays
		if expiresDays <= 0 {
			expiresDays = 90
		}
		if expiresDays > 365 {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "expires_days exceeds maximum 365", map[string]any{"field": "expires_days"})
			return
		}

		token, err := store.CreatePAT(r.Context(), auth.ActorUserID(r.Context()), auth.TeamID(r.Context()), req.Name, req.Scopes, time.Now().UTC().Add(time.Duration(expiresDays)*24*time.Hour))
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to create token", nil)
			return
		}

		now := time.Now().UTC()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.PATCreateResponse{
			Token:     token,
			Name:      req.Name,
			Scopes:    append([]string(nil), req.Scopes...),
			CreatedAt: now,
			ExpiresAt: now.Add(time.Duration(expiresDays) * 24 * time.Hour),
		})
	}
}

func revokeTokenHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "token_name")
		if strings.TrimSpace(name) == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "token name is required", nil)
			return
		}
		if err := store.RevokePATByName(r.Context(), auth.TeamID(r.Context()), name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				apperror.Write(w, "token_not_found", apperror.ClassNotFound, "token not found", map[string]any{"name": name})
				return
			}
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to revoke token", nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func startDeviceLoginHandler(_ appsStore, githubClient githubOAuthClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dto.DeviceStartRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.GithubLogin) == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "github_login is required", map[string]any{"field": "github_login"})
			return
		}

		authChallenge, err := githubClient.StartDeviceAuthorization(r.Context())
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to start github device authorization", nil)
			return
		}

		pollToken, err := newRandomToken()
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to start device login", nil)
			return
		}
		now := time.Now().UTC()
		deviceLoginSessions.Store(pollToken, deviceLoginSession{
			GithubLogin:     req.GithubLogin,
			UserCode:        authChallenge.UserCode,
			DeviceCode:      authChallenge.DeviceCode,
			VerificationURI: authChallenge.VerificationURI,
			Status:          "pending",
			IntervalSeconds: authChallenge.IntervalSeconds,
			ExpiresAt:       now.Add(time.Duration(authChallenge.ExpiresInSeconds) * time.Second),
		})

		ttlSeconds := authChallenge.ExpiresInSeconds
		if ttlSeconds <= 0 {
			ttlSeconds = int(time.Until(now.Add(10 * time.Minute)).Seconds())
		}
		intervalSeconds := authChallenge.IntervalSeconds
		if intervalSeconds <= 0 {
			intervalSeconds = 1
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.DeviceStartResponse{
			UserCode:        authChallenge.UserCode,
			VerificationURI: authChallenge.VerificationURI,
			PollToken:       pollToken,
			IntervalSeconds: intervalSeconds,
			TTLSeconds:      ttlSeconds,
		})
	}
}

func callbackDeviceLoginHandler(_ appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dto.DeviceCallbackRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		// Find session by user_code
		var foundToken string
		var foundSession deviceLoginSession
		deviceLoginSessions.Range(func(key, value interface{}) bool {
			session := value.(deviceLoginSession)
			if session.UserCode == req.UserCode {
				foundToken = key.(string)
				foundSession = session
				return false // Stop iteration
			}
			return true
		})

		if foundToken == "" {
			apperror.Write(w, "invalid_user_code", apperror.ClassBadRequest, "user code not found or expired", nil)
			return
		}

		if time.Now().UTC().After(foundSession.ExpiresAt) {
			deviceLoginSessions.Delete(foundToken)
			apperror.Write(w, "user_code_expired", apperror.ClassBadRequest, "user code expired", nil)
			return
		}

		// Mark session as verified
		foundSession.Status = "verified"
		foundSession.AccessToken = req.AccessToken
		deviceLoginSessions.Store(foundToken, foundSession)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(dto.DeviceCallbackResponse{
			Status: "verified",
		})
	}
}

func pollDeviceLoginHandler(store appsStore, githubClient githubOAuthClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dto.DevicePollRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.PollToken) == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "poll_token is required", map[string]any{"field": "poll_token"})
			return
		}

		raw, ok := deviceLoginSessions.Load(req.PollToken)
		if !ok {
			apperror.Write(w, "invalid_poll_token", apperror.ClassUnauthorized, "invalid poll token", nil)
			return
		}
		session := raw.(deviceLoginSession)
		if time.Now().UTC().After(session.ExpiresAt) {
			deviceLoginSessions.Delete(req.PollToken)
			apperror.Write(w, "poll_token_expired", apperror.ClassUnauthorized, "poll token expired", nil)
			return
		}

		accessToken := strings.TrimSpace(session.AccessToken)
		if accessToken == "" && session.Status == "pending" {
			exchange, err := githubClient.ExchangeDeviceCode(r.Context(), session.DeviceCode)
			if err != nil {
				switch {
				case errors.Is(err, githuboauth.ErrAuthorizationPending), errors.Is(err, githuboauth.ErrSlowDown):
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusAccepted)
					_ = json.NewEncoder(w).Encode(dto.DevicePollPendingResponse{
						Status: "pending",
					})
					return
				case errors.Is(err, githuboauth.ErrAccessDenied):
					apperror.Write(w, "access_denied", apperror.ClassBadRequest, "github authorization denied", nil)
					return
				case errors.Is(err, githuboauth.ErrExpiredToken):
					deviceLoginSessions.Delete(req.PollToken)
					apperror.Write(w, "poll_token_expired", apperror.ClassBadRequest, "poll token expired", nil)
					return
				default:
					apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to exchange github device code", nil)
					return
				}
			}
			accessToken = exchange.AccessToken
			session.AccessToken = accessToken
			session.Status = "verified"
			deviceLoginSessions.Store(req.PollToken, session)
		}

		if session.Status != "verified" {
			apperror.Write(w, "invalid_session_state", apperror.ClassInternal, "session in invalid state", nil)
			return
		}

		userProfile, err := githubClient.FetchUser(r.Context(), accessToken)
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to fetch github user", nil)
			return
		}
		if strings.TrimSpace(userProfile.Login) == "" {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "github user login missing", nil)
			return
		}

		// Session verified - create user and team, issue token
		userID, teamID, teamSlug, err := store.GetOrCreateUserAndPersonalTeam(r.Context(), userProfile.Login)
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, fmt.Sprintf("failed to resolve or create github user: %v", err), nil)
			return
		}
		bearerToken, err := store.CreateCLIToken(r.Context(), userID, teamID, []string{
			string(rbac.ScopeAppsRead),
			string(rbac.ScopeTeamsRead),
			string(rbac.ScopeMembersManage),
		})
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to create bearer token", nil)
			return
		}
		deviceLoginSessions.Delete(req.PollToken)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.DevicePollResponse{ //nolint:gosec // BearerToken is expected in API response
			BearerToken:     bearerToken,
			DefaultTeamSlug: teamSlug,
			GithubLogin:     userProfile.Login,
			IssuedAt:        time.Now().UTC(),
		})
	}
}

func logoutHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.RevokeCLITokenByID(r.Context(), auth.TokenID(r.Context())); err != nil {
			apperror.Write(w, "token_invalid", apperror.ClassUnauthorized, "invalid bearer token", nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}


// NewRouter returns the HTTP router for the server.
func NewRouter(store routerStore) http.Handler {
	githubClient := newGitHubOAuthClient()
	return newRouterFull(store, githubClient, nil, nil, nil, nil)
}

// NewRouterWithInfra creates a router with infrastructure clients.
func NewRouterWithInfra(store routerStore, k3sClient infraK3sClient, cfClient infraCloudflareClient) http.Handler {
	githubClient := newGitHubOAuthClient()
	return newRouterFull(store, githubClient, k3sClient, cfClient, nil, nil)
}

// NewRouterWithRateLimit creates a router with infrastructure clients and a
// per-token / per-team rate-limit middleware (M4.2 — spec § 14 hard rule #2:
// the limiter MUST sit after auth.Bearer + auth.ResolveTeam).
//
//nolint:revive // exported for public API
func NewRouterWithRateLimit(store routerStore, k3sClient infraK3sClient, cfClient infraCloudflareClient, limiter *ratelimit.Limiter, observerFn func(scope, category, plan string)) http.Handler {
	githubClient := newGitHubOAuthClient()
	var observer ratelimit.Observer
	if observerFn != nil {
		observer = ratelimit.ObserverFunc(func(scope ratelimit.Scope, cat ratelimit.Category, plan ratelimit.Plan) {
			observerFn(string(scope), string(cat), string(plan))
		})
	}
	return newRouterFull(store, githubClient, k3sClient, cfClient, limiter, observer)
}

//nolint:revive // exported for public API
func NewRouterWithGitHubOAuth(store routerStore, githubClient githubOAuthClient, k3sClient infraK3sClient, cfClient infraCloudflareClient) http.Handler {
	return newRouterFull(store, githubClient, k3sClient, cfClient, nil, nil)
}

func newRouterFull(store routerStore, githubClient githubOAuthClient, k3sClient infraK3sClient, cfClient infraCloudflareClient, limiter *ratelimit.Limiter, observer ratelimit.Observer) http.Handler {
	if argoClient, ok := k3sClient.(k3sArgoCDClient); ok && argoClient != nil {
		newArgoCDStatusProvider = func() argoCDStatusProvider {
			return k3sArgoCDStatusProvider{client: argoClient}
		}
	}

	mw := auth.NewMiddleware(store)
	githubSvc, githubWebhookVer := githubServiceFactoryFn(store)

	var rateLimitFn func(http.Handler) http.Handler
	if limiter != nil {
		rateLimitFn = ratelimit.NewMiddleware(limiter, observer).Handler
	}

	r := chi.NewRouter()
	r.Post("/v1/admin/bootstrap-owner", bootstrapOwnerHandler(store))
	r.Post("/internal/deploy-runs/{run_id}/callback", deployRunCallbackHandler(store))
	r.Route("/v1/auth", func(sr chi.Router) {
		sr.Post("/device/start", startDeviceLoginHandler(store, githubClient))
		sr.Post("/device/callback", callbackDeviceLoginHandler(store))
		sr.Post("/device/poll", pollDeviceLoginHandler(store, githubClient))
		sr.Get("/github/install-callback", githubInstallCallbackV2Handler(githubSvc))
		sr.With(mw.Bearer).Post("/logout", logoutHandler(store))
	})
	r.Post("/v1/webhooks/github", githubWebhookDispatchHandler(store, githubSvc, githubWebhookVer, newRedeployTriggerFromEnv))

	// Tool authorization endpoint (requires temporary token)
	r.Post("/v1/teams/{team_slug}/auth:grant-tools", authorizeToolsHandler(store))

	r.Route("/v1/me", func(sr chi.Router) {
		sr.Use(mw.Bearer)
		if rateLimitFn != nil {
			sr.Use(rateLimitFn)
		}
		sr.Use(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionListTeams, next)
		})
		sr.Get("/teams", listTeamsHandler(store))
		sr.Patch("/auth/tool-grants", patchToolGrantsHandler(store))
	})

	r.Route("/v1/teams/{team_slug}", func(sr chi.Router) {
		sr.Use(mw.Bearer)
		sr.Use(mw.ResolveTeam)
		sr.Use(mw.CheckMembership)
		if rateLimitFn != nil {
			sr.Use(rateLimitFn)
		}
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionListApps, next)
		}).Get("/apps", listAppsHandler(store))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionListApps, next)
		}).Get("/apps/{app_slug}", getAppHandler(store))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionCreateApp, next)
		}).Post("/apps:preview", previewCreateAppHandler(store))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionCreateApp, next)
		}).Post("/apps", createAppHandler(store, k3sClient, cfClient))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionListApps, next)
		}).Get("/repos/{app_slug}:inspect", inspectRepoHandler(store))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionRedeploy, next)
		}).Post("/apps/{app_slug}/redeploys:preview", previewRedeployHandler(store, newRedeployTriggerFromEnv))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionRedeploy, next)
		}).Post("/redeploys", confirmRedeployHandler(store, newRedeployTriggerFromEnv))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionListApps, next)
		}).Get("/deploys/status", getDeployStatusHandler(store))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionListApps, next)
		}).Get("/deploys/logs", tailLogsHandler(store))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionListApps, next)
		}).Get("/domains", listDomainsHandler(store))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionListMembers, next)
		}).Get("/members", listMembersHandler(store))
		sr.Route("/tokens", func(tr chi.Router) {
			tr.With(func(next http.Handler) http.Handler {
				return mw.CheckTokenScope(rbac.ActionManageTokens, next)
			}).Get("/", listTokensHandler(store))
			tr.With(func(next http.Handler) http.Handler {
				return mw.CheckTokenScope(rbac.ActionManageTokens, next)
			}).Post("/", createTokenHandler(store))
			tr.With(func(next http.Handler) http.Handler {
				return mw.CheckTokenScope(rbac.ActionManageTokens, next)
			}).Delete("/{token_name}", revokeTokenHandler(store))
		})
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionInviteMembers, next)
		}).Post("/members:preview-invite", previewInviteHandler(store))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionInviteMembers, next)
		}).Post("/members:invite", inviteHandler(store))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionRemoveMembers, next)
		}).Post("/members:preview-remove", previewRemoveHandler(store))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionRemoveMembers, next)
		}).Post("/members:remove", removeHandler(store))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionManageGithubApp, next)
		}).Post("/github:preview-install", previewGitHubInstallV2Handler(githubSvc))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionManageGithubApp, next)
		}).Post("/github:install", confirmGitHubInstallV2Handler(githubSvc))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionManageGithubApp, next)
		}).Post("/github:preview-uninstall", previewGitHubUninstallHandler(githubSvc))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionManageGithubApp, next)
		}).Post("/github:uninstall", confirmGitHubUninstallHandler(githubSvc))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionListMembers, next)
		}).Get("/github/install-status", githubInstallStatusHandler(githubSvc))
	})

	return r
}

func getCallbackSecret() string {
	if v := strings.TrimSpace(os.Getenv("OPS_CALLBACK_SECRET")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("OPS_GHA_CALLBACK_SECRET"))
}

func validateCallbackTimestamp(raw string, now time.Time, maxSkew time.Duration) bool {
	sec, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return false
	}
	ts := time.Unix(sec, 0).UTC()
	if now.Sub(ts) > maxSkew {
		return false
	}
	if ts.Sub(now) > maxSkew {
		return false
	}
	return true
}

func validateCallbackSignature(secret, timestamp string, body []byte, got string) bool {
	if !strings.HasPrefix(got, "sha256=") {
		return false
	}
	rawSig := strings.TrimPrefix(got, "sha256=")
	wantBytes, err := hex.DecodeString(rawSig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	want := mac.Sum(nil)
	return subtle.ConstantTimeCompare(want, wantBytes) == 1
}

func normalizeDeployStatus(raw string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(raw))

	// New canonical states (ADR-0002 state machine)
	switch normalized {
	case "queued", "preparing", "building", "pushing", "rendering", "syncing", "live", "failed", "canceled", "rolled_back":
		return normalized, true
	}

	// Legacy status mappings (backward compatibility)
	switch normalized {
	case "success":
		return "live", true
	case "failure":
		return "failed", true
	case "cancelled":
		return "canceled", true
	}

	return "", false
}

func mapArgoCDDeployStatus(syncStatus, healthStatus string) (string, bool) {
	switch {
	case strings.EqualFold(syncStatus, "synced") && strings.EqualFold(healthStatus, "healthy"):
		return "live", true
	case strings.EqualFold(syncStatus, "synced") && strings.EqualFold(healthStatus, "progressing"):
		return "syncing", true
	case strings.EqualFold(syncStatus, "outofsync") && strings.EqualFold(healthStatus, "progressing"):
		return "syncing", true
	case strings.EqualFold(syncStatus, "synced") && strings.EqualFold(healthStatus, "degraded"):
		return "failed", true
	case strings.EqualFold(syncStatus, "unknown") || strings.EqualFold(healthStatus, "unknown"):
		return "syncing", true
	default:
		return "", false
	}
}

func validateDeployCallbackSignature(timestamp string, body []byte, signature string, opsToken *string) bool {
	if opsToken != nil && strings.TrimSpace(*opsToken) != "" {
		tokenSecret := strings.TrimSpace(os.Getenv("OPS_TOKEN_SIGNING_SECRET"))
		if tokenSecret != "" {
			if payload, err := workflowdispatch.ParseOpsToken(strings.TrimSpace(*opsToken), []byte(tokenSecret)); err == nil && payload != nil {
				if validateCallbackSignature(strings.TrimSpace(*opsToken), timestamp, body, signature) {
					return true
				}
			}
		}
	}

	secret := strings.TrimSpace(getCallbackSecret())
	if secret == "" {
		return false
	}
	return validateCallbackSignature(secret, timestamp, body, signature)
}

func buildDeployCallbackEvent(req deployCallbackRequest, status string) json.RawMessage {
	event := map[string]any{
		"status": status,
	}
	if req.Image != nil {
		event["image"] = strings.TrimSpace(*req.Image)
	}
	if req.BuildMinutes != nil {
		event["build_minutes"] = *req.BuildMinutes
	}
	if req.ImageSizeBytes != nil {
		event["image_size_bytes"] = *req.ImageSizeBytes
	}
	if req.GitopsCommitSHA != nil {
		event["gitops_commit_sha"] = strings.TrimSpace(*req.GitopsCommitSHA)
	}
	if req.ScanSummary != nil {
		event["scan_summary"] = req.ScanSummary
	}
	if req.OpsToken != nil {
		event["ops_token"] = strings.TrimSpace(*req.OpsToken)
	}
	data, err := json.Marshal(event)
	if err != nil {
		return nil
	}
	return data
}

func trimStringPtr(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func isValidFailureClassification(raw string) bool {
	switch strings.TrimSpace(raw) {
	case "repo_checkout_failed", "registry_auth_failed", "buildpack_detect_failed",
		"build_compile_error", "build_timeout", "registry_push_failed",
		"image_scan_blocked", "gitops_push_conflict", "unknown":
		return true
	default:
		return false
	}
}

type disabledGitHubOAuthClient struct {
	err error
}

func (c disabledGitHubOAuthClient) StartDeviceAuthorization(context.Context) (githuboauth.DeviceAuthorization, error) {
	return githuboauth.DeviceAuthorization{}, c.err
}

func (c disabledGitHubOAuthClient) ExchangeDeviceCode(context.Context, string) (githuboauth.AccessTokenResponse, error) {
	return githuboauth.AccessTokenResponse{}, c.err
}

func (c disabledGitHubOAuthClient) FetchUser(context.Context, string) (githuboauth.UserProfile, error) {
	return githuboauth.UserProfile{}, c.err
}

func newGitHubOAuthClient() githubOAuthClient {
	client, err := githuboauth.NewClientFromEnv(http.DefaultClient)
	if err != nil {
		return disabledGitHubOAuthClient{err: err}
	}
	return client
}

func newWorkflowDispatchClient() createappsvc.Dispatcher {
	client, err := workflowdispatch.NewClientFromEnv(http.DefaultClient)
	if err != nil {
		return nil
	}
	return client
}

func newWorkflowDispatchTokenSigner() createappsvc.OpsTokenSigner {
	signer, err := workflowdispatch.NewOpsTokenSignerFromEnv()
	if err != nil {
		return nil
	}
	return signer
}

func callbackBaseURL() string {
	base := strings.TrimSpace(os.Getenv("OPS_PUBLIC_BASE_URL"))
	if base == "" {
		base = strings.TrimSpace(os.Getenv("OPS_SERVER_PUBLIC_BASE_URL"))
	}
	if base == "" {
		base = "http://localhost:8080"
	}
	return strings.TrimRight(base, "/")
}

func newRandomToken() (string, error) {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

func newAppRef(row db.App) dto.AppRef {
	return dto.AppRef{
		ID:                row.ID,
		TeamID:            row.TeamID,
		Slug:              row.Slug,
		Name:              row.Name,
		RepoURL:           row.RepoURL,
		RepoDefaultBranch: row.RepoDefaultBranch,
		ImageRef:          row.ImageRef,
		Builder:           row.Builder,
		Status:            row.Status,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}

func encodeAppCursor(id string, ts time.Time) string {
	data, _ := json.Marshal(appCursor{ID: id, TS: ts.UTC()})
	return base64.StdEncoding.EncodeToString(data)
}

func decodeAppCursor(raw string) (*string, error) {
	if raw == "" {
		return nil, nil
	}

	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}

	var cursor appCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return nil, err
	}
	if cursor.ID == "" {
		return nil, nil
	}
	return &cursor.ID, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid json payload", nil)
		return false
	}
	return true
}

func validMemberRole(role string) bool {
	switch role {
	case string(rbac.RoleOwner), string(rbac.RoleAdmin), string(rbac.RoleMember), string(rbac.RoleViewer):
		return true
	default:
		return false
	}
}

func validateAppCreateRequest(w http.ResponseWriter, req dto.AppCreateRequest) bool {
	slug := strings.TrimSpace(req.Slug)
	if !appSlugPattern.MatchString(slug) {
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid slug", map[string]any{"field": "slug"})
		return false
	}
	switch slug {
	case "system", "api", "auth", "v1", "me":
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "reserved slug", map[string]any{"field": "slug"})
		return false
	}
	if strings.TrimSpace(req.RepoURL) == "" {
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "repo_url is required", map[string]any{"field": "repo_url"})
		return false
	}
	repoURL := strings.TrimSpace(req.RepoURL)
	if !strings.HasPrefix(repoURL, "https://github.com/") && !strings.HasPrefix(repoURL, "git@github.com:") {
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "unsupported repo_url", map[string]any{"field": "repo_url"})
		return false
	}
	if strings.TrimSpace(req.Ref) == "" {
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "ref is required", map[string]any{"field": "ref"})
		return false
	}
	return true
}

func validatePreview(w http.ResponseWriter, r *http.Request, store appsStore, previewID, action string) (db.Preview, bool) {
	if previewID == "" {
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "preview_id is required", nil)
		return db.Preview{}, false
	}
	preview, err := store.GetPreview(r.Context(), previewID)
	if err != nil {
		if errors.Is(err, db.ErrPreviewNotFound) {
			apperror.Write(w, "preview_not_found", apperror.ClassNotFound, "preview not found", nil)
			return db.Preview{}, false
		}
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to load preview", nil)
		return db.Preview{}, false
	}
	if preview.Action != action || preview.TeamID != auth.TeamID(r.Context()) || preview.ActorUserID != auth.ActorUserID(r.Context()) {
		apperror.Write(w, "preview_not_found", apperror.ClassNotFound, "preview not found", nil)
		return db.Preview{}, false
	}
	if preview.ConsumedAt != nil {
		apperror.Write(w, "preview_consumed", apperror.ClassConflict, "preview already consumed", nil)
		return db.Preview{}, false
	}
	if preview.ExpiresAt.Before(time.Now().UTC()) {
		apperror.Write(w, "preview_expired", apperror.ClassConflict, "preview expired", nil)
		return db.Preview{}, false
	}
	return preview, true
}

func validateCreateAppPreview(w http.ResponseWriter, r *http.Request, store appsStore, previewID string) (db.Preview, bool, bool) {
	if previewID == "" {
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "preview_id is required", nil)
		return db.Preview{}, false, false
	}
	preview, err := store.GetPreview(r.Context(), previewID)
	if err != nil {
		if errors.Is(err, db.ErrPreviewNotFound) {
			apperror.Write(w, "preview_not_found", apperror.ClassNotFound, "preview not found", nil)
			return db.Preview{}, false, false
		}
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to load preview", nil)
		return db.Preview{}, false, false
	}
	if preview.Action != previewActionCreate || preview.TeamID != auth.TeamID(r.Context()) || preview.ActorUserID != auth.ActorUserID(r.Context()) {
		apperror.Write(w, "preview_not_found", apperror.ClassNotFound, "preview not found", nil)
		return db.Preview{}, false, false
	}
	if preview.ConsumedAt != nil {
		if len(preview.LastResult) == 0 {
			apperror.Write(w, "preview_consumed", apperror.ClassConflict, "preview already consumed", nil)
			return db.Preview{}, false, false
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(preview.LastResult)
		return db.Preview{}, false, true
	}
	if preview.ExpiresAt.Before(time.Now().UTC()) {
		apperror.Write(w, "preview_expired", apperror.ClassConflict, "preview expired", nil)
		return db.Preview{}, false, false
	}
	return preview, true, false
}

func writePreviewResponse(w http.ResponseWriter, preview db.Preview, summary string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dto.PreviewResponse{
		PreviewID: preview.ID,
		Action:    preview.Action,
		Summary:   summary,
		ExpiresAt: preview.ExpiresAt,
	})
}

func previewConsumeLatency(createdAt time.Time) time.Duration {
	if createdAt.IsZero() {
		return 0
	}
	now := time.Now().UTC()
	if now.Before(createdAt) {
		return 0
	}
	return now.Sub(createdAt)
}

func deployTerminalOutcome(status string) (string, bool) {
	switch status {
	case "live":
		return "success", true
	case "failed":
		return "failed", true
	case "canceled":
		return "canceled", true
	case "rolled_back":
		return "rolled_back", true
	default:
		return "", false
	}
}
