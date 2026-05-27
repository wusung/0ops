package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/githubapp"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// githubAppService is the surface area the handlers need from the github app
// orchestration service. Tests can swap in fakes by overriding the package
// variable below.
type githubAppService interface {
	PreviewInstall(ctx context.Context, teamID, actorUserID, teamSlug string) (githubapp.PreviewResult, error)
	ConfirmInstall(ctx context.Context, teamID, actorUserID, previewID string) (githubapp.ConfirmInstallResult, error)
	HandleCallback(ctx context.Context, installationRaw, state string) (githubapp.CallbackResult, error)
	SuccessRedirect(teamSlug string) string
	PreviewUninstall(ctx context.Context, teamID, actorUserID, teamSlug string) (githubapp.PreviewResult, error)
	ConfirmUninstall(ctx context.Context, teamID, actorUserID, previewID string) (githubapp.ConfirmUninstallResult, error)
	GetInstallStatus(ctx context.Context, teamID string) (githubapp.InstallStatus, error)
	HandleInstallationWebhook(ctx context.Context, deliveryID string, payload []byte) (githubapp.WebhookOutcome, error)
}

// githubWebhookVerifier exposes the subset of githubapp.WebhookVerifier used
// by the webhook handler; nil means signature verification is disabled (the
// handler then refuses to process webhooks).
type githubWebhookVerifier interface {
	VerifyRequest(r *http.Request) ([]byte, error)
}

// githubAppServiceStore is the DB-facing contract implemented by
// `*db.Repository` and exercised by `fakeStore` in tests.
type githubAppServiceStore interface {
	githubapp.Store
}

// githubServiceFactoryFn returns the orchestration service together with the
// optional webhook verifier. It is package-level so tests can stub it without
// touching environment variables.
var githubServiceFactoryFn = newGitHubAppServiceFromEnv

func newGitHubAppServiceFromEnv(store githubapp.Store) (githubAppService, githubWebhookVerifier) {
	if store == nil {
		return nil, nil
	}
	opts := githubapp.Options{
		AppURLBase:  envOr("OPS_GITHUB_BASE_URL", "https://github.com"),
		AppSlug:     envOr("OPS_GITHUB_APP_SLUG", "0ops"),
		CallbackURL: strings.TrimRight(callbackBaseURL(), "/") + "/v1/auth/github/install-callback",
		SuccessPage: strings.TrimSpace(os.Getenv("OPS_GITHUB_APP_SUCCESS_URL")),
	}
	if signer, err := githubapp.NewStateSigner(); err == nil {
		opts.StateSigner = signer
	} else {
		slog.Warn("github_app_state_signer_disabled", "reason", err.Error())
	}
	if jwtSigner, err := githubapp.NewJWTSignerFromEnv(); err == nil {
		apiBase := envOr("OPS_GITHUB_API_BASE_URL", "https://api.github.com")
		client := githubapp.NewInstallationTokenClient(jwtSigner, apiBase, nil)
		cache := githubapp.NewTokenCache()
		provider := githubapp.NewTokenProvider(client, cache)
		opts.APIClient = client
		opts.TokenCache = tokenCacheInvalidator{cache: cache, provider: provider}
	}
	var verifier githubWebhookVerifier
	if wv, err := githubapp.NewWebhookVerifier(); err == nil {
		verifier = wv
	}
	return githubapp.NewService(store, opts), verifier
}

type tokenCacheInvalidator struct {
	cache    *githubapp.TokenCache
	provider *githubapp.TokenProvider
}

func (t tokenCacheInvalidator) Invalidate(installID int64) {
	if t.cache != nil {
		t.cache.Invalidate(installID)
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func previewGitHubInstallV2Handler(svc githubAppService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		teamID := auth.TeamID(r.Context())
		teamSlug := auth.TeamSlug(r.Context())
		actorUserID := auth.ActorUserID(r.Context())
		if teamID == "" {
			apperror.Write(w, "missing_team", apperror.ClassBadRequest, "team_id missing", nil)
			return
		}
		preview, err := svc.PreviewInstall(r.Context(), teamID, actorUserID, teamSlug)
		if err != nil {
			writeGitHubServiceError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.PreviewResponse{
			PreviewID: preview.PreviewID,
			Action:    preview.Action,
			Summary:   preview.Summary,
			ExpiresAt: preview.ExpiresAt,
		})
		recordPreviewCreatedMetric(githubapp.ActionInstall)
	}
}

func confirmGitHubInstallV2Handler(svc githubAppService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		teamID := auth.TeamID(r.Context())
		actorUserID := auth.ActorUserID(r.Context())
		if teamID == "" {
			apperror.Write(w, "missing_team", apperror.ClassBadRequest, "team_id missing", nil)
			return
		}
		var req dto.GitHubConfirmRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		started, err := getPreviewCreatedAt(r.Context(), svc, teamID, actorUserID, req.PreviewID, githubapp.ActionInstall)
		if err != nil {
			writeGitHubServiceError(w, err)
			return
		}
		result, err := svc.ConfirmInstall(r.Context(), teamID, actorUserID, req.PreviewID)
		if err != nil {
			writeGitHubServiceError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.GitHubInstallResponse{
			InstallURL: result.InstallURL,
			ExpiresAt:  result.ExpiresAt,
		})
		outcome := "success"
		if result.Replayed {
			outcome = "idempotent_replay"
		}
		recordPreviewConsumedMetric(githubapp.ActionInstall, outcome, previewConsumeLatency(started))
	}
}

func githubInstallCallbackV2Handler(svc githubAppService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			apperror.Write(w, "invalid_request", apperror.ClassBadRequest, "parse form failed", nil)
			return
		}
		installationID := r.FormValue("installation_id")
		if installationID == "" {
			installationID = r.FormValue("code")
		}
		state := r.FormValue("state")
		setupAction := r.FormValue("setup_action")

		if installationID == "" || state == "" {
			apperror.Write(w, "missing_params", apperror.ClassBadRequest, "installation_id or state missing", nil)
			return
		}
		if setupAction != "" && setupAction != "install" {
			http.Redirect(w, r, "https://github.com/", http.StatusFound)
			return
		}

		result, err := svc.HandleCallback(r.Context(), installationID, state)
		if err != nil {
			if errors.Is(err, githubapp.ErrStateInvalid) {
				slog.Warn("github_app_install_callback_state_invalid", "error", err.Error())
				apperror.Write(w, "state_invalid", apperror.ClassBadRequest, "state validation failed", nil)
				return
			}
			if errors.Is(err, githubapp.ErrSignerMissing) {
				apperror.Write(w, "github_app_unconfigured", apperror.ClassInternal, "github app state signer not configured", nil)
				return
			}
			if errors.Is(err, db.ErrTeamNotFound) {
				apperror.Write(w, "team_not_found", apperror.ClassNotFound, "team not found", nil)
				return
			}
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to apply install callback", nil)
			return
		}

		http.Redirect(w, r, svc.SuccessRedirect(result.TeamSlug), http.StatusFound)
	}
}

func previewGitHubUninstallHandler(svc githubAppService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		teamID := auth.TeamID(r.Context())
		teamSlug := auth.TeamSlug(r.Context())
		actorUserID := auth.ActorUserID(r.Context())
		if teamID == "" {
			apperror.Write(w, "missing_team", apperror.ClassBadRequest, "team_id missing", nil)
			return
		}
		preview, err := svc.PreviewUninstall(r.Context(), teamID, actorUserID, teamSlug)
		if err != nil {
			writeGitHubServiceError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.PreviewResponse{
			PreviewID: preview.PreviewID,
			Action:    preview.Action,
			Summary:   preview.Summary,
			ExpiresAt: preview.ExpiresAt,
		})
		recordPreviewCreatedMetric(githubapp.ActionUninstall)
	}
}

func confirmGitHubUninstallHandler(svc githubAppService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		teamID := auth.TeamID(r.Context())
		actorUserID := auth.ActorUserID(r.Context())
		if teamID == "" {
			apperror.Write(w, "missing_team", apperror.ClassBadRequest, "team_id missing", nil)
			return
		}
		var req dto.GitHubConfirmRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		started, err := getPreviewCreatedAt(r.Context(), svc, teamID, actorUserID, req.PreviewID, githubapp.ActionUninstall)
		if err != nil {
			writeGitHubServiceError(w, err)
			return
		}
		result, err := svc.ConfirmUninstall(r.Context(), teamID, actorUserID, req.PreviewID)
		if err != nil {
			writeGitHubServiceError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.GitHubUninstallResponse{
			Status:         result.Status,
			PausedAppCount: result.PausedAppCount,
		})
		outcome := "success"
		if result.Replayed {
			outcome = "idempotent_replay"
		}
		recordPreviewConsumedMetric(githubapp.ActionUninstall, outcome, previewConsumeLatency(started))
	}
}

func githubInstallStatusHandler(svc githubAppService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		teamID := auth.TeamID(r.Context())
		if teamID == "" {
			apperror.Write(w, "missing_team", apperror.ClassBadRequest, "team_id missing", nil)
			return
		}
		status, err := svc.GetInstallStatus(r.Context(), teamID)
		if err != nil {
			if errors.Is(err, db.ErrTeamNotFound) {
				apperror.Write(w, "team_not_found", apperror.ClassNotFound, "team not found", nil)
				return
			}
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to get install status", nil)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.GitHubInstallStatusResponse{
			Installed:       status.Installed,
			GithubInstallID: status.GithubInstallID,
		})
	}
}

func githubInstallationWebhookHandler(svc githubAppService, verifier githubWebhookVerifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if verifier == nil {
			apperror.Write(w, "github_app_unconfigured", apperror.ClassInternal, "github webhook secret not configured", nil)
			return
		}
		body, err := verifier.VerifyRequest(r)
		if err != nil {
			slog.Warn("github_app_webhook_signature_invalid", "error", err.Error())
			apperror.Write(w, "webhook_signature_invalid", apperror.ClassUnauthorized, "invalid webhook signature", nil)
			return
		}
		// Filter to only `installation` event; other events are ignored.
		eventType := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
		if eventType == "" {
			eventType = "installation"
		}
		switch eventType {
		case "installation", "installation_repositories":
			// supported below
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ignored", "event": eventType})
			return
		}

		deliveryID := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
		if deliveryID == "" {
			apperror.Write(w, "missing_delivery_id", apperror.ClassBadRequest, "X-GitHub-Delivery header is required", nil)
			return
		}

		// `installation_repositories` is observed for receipt but not acted on
		// in v1 (spec § 7.2). We still dedup via the same path.
		if eventType == "installation_repositories" {
			outcome, err := svc.HandleInstallationWebhook(r.Context(), deliveryID, body)
			if err != nil {
				apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to record webhook", nil)
				return
			}
			respondWebhookOutcome(w, outcome)
			return
		}

		outcome, err := svc.HandleInstallationWebhook(r.Context(), deliveryID, body)
		if err != nil {
			slog.Error("github_app_installation_webhook_failed", "delivery_id", deliveryID, "error", err.Error())
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to apply installation webhook", nil)
			return
		}
		respondWebhookOutcome(w, outcome)
	}
}

func respondWebhookOutcome(w http.ResponseWriter, outcome githubapp.WebhookOutcome) {
	w.Header().Set("Content-Type", "application/json")
	payload := map[string]any{
		"status":     "ok",
		"acted":      outcome.Acted,
		"duplicate":  outcome.Duplicate,
		"team_slug":  outcome.TeamSlug,
		"paused":     outcome.PausedAppCount,
		"event_type": outcome.Action,
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func writeGitHubServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, githubapp.ErrPreviewNotFound):
		apperror.Write(w, "preview_not_found", apperror.ClassNotFound, "preview not found", nil)
	case errors.Is(err, githubapp.ErrPreviewConsumed):
		apperror.Write(w, "preview_consumed", apperror.ClassConflict, "preview already consumed", nil)
	case errors.Is(err, githubapp.ErrPreviewExpired):
		apperror.Write(w, "preview_expired", apperror.ClassConflict, "preview expired", nil)
	case errors.Is(err, githubapp.ErrStateInvalid):
		apperror.Write(w, "state_invalid", apperror.ClassBadRequest, "state validation failed", nil)
	case errors.Is(err, githubapp.ErrSignerMissing):
		apperror.Write(w, "github_app_unconfigured", apperror.ClassInternal, "github app signer not configured", nil)
	case errors.Is(err, githubapp.ErrInstallMissing):
		apperror.Write(w, "github_app_not_installed", apperror.ClassConflict, "team has no github installation", nil)
	case errors.Is(err, db.ErrTeamNotFound):
		apperror.Write(w, "team_not_found", apperror.ClassNotFound, "team not found", nil)
	default:
		apperror.Write(w, "internal_error", apperror.ClassInternal, fmt.Sprintf("internal error: %v", err), nil)
	}
}

// getPreviewCreatedAt is a placeholder used by the install/uninstall confirm
// handlers; we currently rely on the service-side replay flag for metrics so
// the latency value can be a zero duration. Kept as a function so future
// metric refinements have a hook without changing the handler signatures.
func getPreviewCreatedAt(_ context.Context, _ githubAppService, _, _, _, _ string) (time.Time, error) {
	return time.Time{}, nil
}
