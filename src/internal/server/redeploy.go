package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/services/githubwebhook"
	"github.com/winshare/zeroops/internal/server/services/redeploy"
	workflowdispatch "github.com/winshare/zeroops/internal/server/services/workflowdispatch"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// redeployStore is the union of dependencies needed by the redeploy
// preview/confirm path. Implemented by *db.Repository and exercised by
// fakeStore (apps_test.go) through helper shims.
type redeployStore interface {
	redeploy.Store
}

// redeployTriggerFactory builds the shared *redeploy.Trigger that drives
// both the user-confirm and webhook-push flows. Defined as a factory so the
// HTTP handlers can stay free of dispatcher/signer wiring noise.
type redeployTriggerFactory func(store redeploy.TriggerStore) *redeploy.Trigger

// newRedeployTriggerFromEnv is the production factory. It reuses the same
// workflow_dispatch client and ops_token signer as create_app so the GHA
// pipeline contract stays consistent across action types.
func newRedeployTriggerFromEnv(store redeploy.TriggerStore) *redeploy.Trigger {
	dispatcher := redeployDispatcher()
	signer := redeployTokenSigner()
	return redeploy.NewTrigger(store, dispatcher, signer, callbackBaseURL())
}

// redeployDispatcher / redeployTokenSigner are var-typed so tests can stub
// them out without touching env vars.
var (
	redeployDispatcher  = func() redeploy.Dispatcher { return resolveRedeployDispatcher() }
	redeployTokenSigner = func() redeploy.OpsTokenSigner { return resolveRedeployTokenSigner() }
)

func resolveRedeployDispatcher() redeploy.Dispatcher {
	// redeploy intentionally bypasses the create_app RoutingDispatcher
	// because the local file:// path is scoped to create_app per ADR-0012
	// sub-spec § 2.2. Redeploying a file:// app is tracked as a follow-up;
	// today it falls through to the production GHA dispatcher (nil in dev
	// when env vars are unset, which the trigger handles gracefully).
	d, err := workflowdispatch.NewClientFromEnv(http.DefaultClient)
	if err != nil || d == nil {
		return nil
	}
	return d
}

func resolveRedeployTokenSigner() redeploy.OpsTokenSigner {
	s := newWorkflowDispatchTokenSigner()
	if s == nil {
		return nil
	}
	return s
}

// previewRedeployHandler exposes POST /v1/teams/{team_slug}/apps/{app_slug}/redeploys:preview
// and produces a preview row tied to the app/team scope.
func previewRedeployHandler(store redeployStore, trigger redeployTriggerFactory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		teamID := auth.TeamID(r.Context())
		teamSlug := auth.TeamSlug(r.Context())
		actorUserID := auth.ActorUserID(r.Context())
		appSlug := strings.TrimSpace(chi.URLParam(r, "app_slug"))
		if teamID == "" || appSlug == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "team_slug and app_slug are required", nil)
			return
		}
		var req dto.RedeployRequest
		// Empty body is fine — both fields are optional. Only validate JSON
		// when body is non-empty.
		if r.ContentLength > 0 {
			if !decodeJSON(w, r, &req) {
				return
			}
		}
		svc := redeploy.New(store, trigger(store))
		preview, err := svc.Preview(r.Context(), teamID, actorUserID, teamSlug, appSlug, req)
		if err != nil {
			writeRedeployError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.PreviewResponse{
			PreviewID: preview.PreviewID,
			Action:    redeploy.PreviewAction,
			Summary:   preview.Summary,
			ExpiresAt: preview.ExpiresAt,
		})
		recordPreviewCreatedMetric(redeploy.PreviewAction)
	}
}

// confirmRedeployHandler exposes POST /v1/teams/{team_slug}/redeploys.
func confirmRedeployHandler(store redeployStore, trigger redeployTriggerFactory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		teamID := auth.TeamID(r.Context())
		teamSlug := auth.TeamSlug(r.Context())
		actorUserID := auth.ActorUserID(r.Context())
		if teamID == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "team_slug is required", nil)
			return
		}
		var req dto.ConfirmRedeployRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.PreviewID) == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "preview_id is required", map[string]any{"field": "preview_id"})
			return
		}
		svc := redeploy.New(store, trigger(store))
		traceID := strings.TrimSpace(middleware.GetReqID(r.Context()))
		result, err := svc.Confirm(r.Context(), teamID, actorUserID, teamSlug, req.PreviewID, traceID)
		if err != nil {
			writeRedeployError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result.Response)
		outcome := "success"
		if result.Replayed {
			outcome = "idempotent_replay"
		}
		recordPreviewConsumedMetric(redeploy.PreviewAction, outcome, previewConsumeLatency(result.PreviewCreatedAt))
	}
}

// writeRedeployError maps redeploy sentinel errors to spec § 9
// `error-model` codes.
func writeRedeployError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, redeploy.ErrAppNotFound):
		apperror.Write(w, "app_not_found", apperror.ClassNotFound, "app not found", nil)
	case errors.Is(err, redeploy.ErrAppPaused):
		apperror.Write(w, "app_paused", apperror.ClassBadRequest, "app is paused", nil)
	case errors.Is(err, redeploy.ErrAppBusy):
		apperror.Write(w, "app_busy", apperror.ClassConflict, "app has an in-flight deploy", nil)
	case errors.Is(err, redeploy.ErrPreviewNotFound):
		apperror.Write(w, "preview_not_found", apperror.ClassNotFound, "preview not found", nil)
	case errors.Is(err, redeploy.ErrPreviewConsumed):
		apperror.Write(w, "preview_consumed", apperror.ClassConflict, "preview already consumed", nil)
	case errors.Is(err, redeploy.ErrPreviewExpired):
		apperror.Write(w, "preview_expired", apperror.ClassConflict, "preview expired", nil)
	case errors.Is(err, redeploy.ErrValidationFailed):
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid request", nil)
	default:
		slog.Error("redeploy_internal_error", "error", err.Error())
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to redeploy", nil)
	}
}

// githubWebhookDispatchHandler is the v1 unified webhook handler that
// dispatches push and installation* events through one verifier/dedup
// pipeline (spec § 4.2).
func githubWebhookDispatchHandler(store dispatchStore, installation githubwebhook.InstallationHandler, verifier githubwebhook.SignatureVerifier, trigger redeployTriggerFactory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if verifier == nil {
			apperror.Write(w, "github_app_unconfigured", apperror.ClassInternal, "github webhook secret not configured", nil)
			return
		}
		pushHandler := githubwebhook.NewPushHandler(store, trigger(store))
		dispatcher := githubwebhook.NewDispatcher(store, verifier, pushHandler, installation)
		res, err := dispatcher.Dispatch(r.Context(), r)
		if err != nil {
			var sig githubwebhook.ErrSignatureInvalid
			var tooLarge githubwebhook.ErrPayloadTooLarge
			var missingDelivery githubwebhook.ErrMissingDeliveryID
			var unconfigured githubwebhook.ErrUnconfigured
			switch {
			case errors.As(err, &sig):
				slog.Warn("github_webhook_signature_invalid", "reason", sig.Reason)
				githubwebhook.RecordSignatureFailure("invalid_signature")
				apperror.Write(w, "webhook_signature_invalid", apperror.ClassUnauthorized, "invalid webhook signature", nil)
				return
			case errors.As(err, &tooLarge):
				apperror.Write(w, "webhook_payload_too_large", apperror.ClassBadRequest, "webhook payload exceeds 5MB", nil)
				return
			case errors.As(err, &missingDelivery):
				apperror.Write(w, "missing_delivery_id", apperror.ClassBadRequest, "X-GitHub-Delivery header is required", nil)
				return
			case errors.As(err, &unconfigured):
				apperror.Write(w, "github_app_unconfigured", apperror.ClassInternal, "github webhook verifier not configured", nil)
				return
			}
			slog.Error("github_webhook_dispatch_failed", "error", err.Error(), "event", res.Event, "delivery_id", res.DeliveryID)
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to dispatch webhook", nil)
			return
		}
		githubwebhook.RecordEvent(res.Event, res.Status)
		githubwebhook.EncodeResponse(w, res)
	}
}

// dispatchStore is the union of stores needed by the unified webhook
// dispatcher: dedup writer + the push-handler store contract.
type dispatchStore interface {
	githubwebhook.PushHandlerStore
	githubwebhook.DispatcherStore
}

