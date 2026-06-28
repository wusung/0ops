package notify

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// Webhook subscription API error codes (error-model § 5.3 extension). String
// values are part of the stable API contract.
const (
	codeWebhookURLInvalid = "webhook_url_invalid"
	codeWebhookEvents     = "webhook_events_invalid"
	codeWebhookQuota      = "webhook_quota_exceeded"
	codeWebhookNotFound   = "webhook_subscription_not_found"
	codeDeliveryNotFound  = "webhook_delivery_not_found"
	codePreviewNotFound   = "preview_not_found"
	codePreviewConsumed   = "preview_consumed"
	codePreviewExpired    = "preview_expired"
	codeValidationFailed  = "validation_failed"
)

// PreviewCreateSubscription handles POST .../webhooks/subscriptions:preview.
func (s *Service) PreviewCreateSubscription(w http.ResponseWriter, r *http.Request) {
	teamID := auth.TeamID(r.Context())
	actor := auth.ActorUserID(r.Context())
	var req dto.CreateWebhookSubscriptionRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := s.PreviewCreate(r.Context(), teamID, actor, req)
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writePreview(w, PreviewAction, res)
}

// ConfirmCreateSubscription handles POST .../webhooks/subscriptions.
func (s *Service) ConfirmCreateSubscription(w http.ResponseWriter, r *http.Request) {
	s.confirm(w, r)
}

// PreviewRotateSecret handles POST .../webhooks/subscriptions/{id}:preview-rotate-secret.
func (s *Service) PreviewRotateSecret(w http.ResponseWriter, r *http.Request) {
	teamID := auth.TeamID(r.Context())
	actor := auth.ActorUserID(r.Context())
	id := chi.URLParam(r, "subscription_id")
	res, err := s.PreviewRotate(r.Context(), teamID, actor, id)
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writePreview(w, PreviewAction, res)
}

// PreviewDeleteSubscription handles POST .../webhooks/subscriptions/{id}:preview-delete.
func (s *Service) PreviewDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	teamID := auth.TeamID(r.Context())
	actor := auth.ActorUserID(r.Context())
	id := chi.URLParam(r, "subscription_id")
	res, err := s.PreviewDelete(r.Context(), teamID, actor, id)
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writePreview(w, PreviewAction, res)
}

// ConfirmWrite handles POST .../webhooks/subscriptions:confirm (rotate/delete
// confirm share the same preview-confirm entrypoint as create).
func (s *Service) ConfirmWrite(w http.ResponseWriter, r *http.Request) {
	s.confirm(w, r)
}

func (s *Service) confirm(w http.ResponseWriter, r *http.Request) {
	teamID := auth.TeamID(r.Context())
	actor := auth.ActorUserID(r.Context())
	var req dto.ConfirmWebhookSubscriptionRequest
	if !decode(w, r, &req) {
		return
	}
	traceID := audit.TraceIDFromContext(r.Context())
	resp, err := s.Confirm(r.Context(), teamID, actor, req.PreviewID, traceID)
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ListSubscriptions handles GET .../webhooks/subscriptions (secret-free).
func (s *Service) ListSubscriptions(w http.ResponseWriter, r *http.Request) {
	teamID := auth.TeamID(r.Context())
	items, err := s.List(r.Context(), teamID)
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dto.ListWebhookSubscriptionsResponse{Items: items})
}

// GetSubscription handles GET .../webhooks/subscriptions/{id} (secret-free).
func (s *Service) GetSubscription(w http.ResponseWriter, r *http.Request) {
	teamID := auth.TeamID(r.Context())
	id := chi.URLParam(r, "subscription_id")
	resp, err := s.Get(r.Context(), teamID, id)
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ListDeliveriesHandler handles GET .../webhooks/deliveries (admin read).
func (s *Service) ListDeliveriesHandler(w http.ResponseWriter, r *http.Request) {
	teamID := auth.TeamID(r.Context())
	items, err := s.ListDeliveries(r.Context(), teamID, 0)
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dto.ListWebhookDeliveriesResponse{Items: items})
}

// RedeliverHandler handles POST .../webhooks/deliveries/{delivery_id}:redeliver.
func (s *Service) RedeliverHandler(w http.ResponseWriter, r *http.Request) {
	teamID := auth.TeamID(r.Context())
	actor := auth.ActorUserID(r.Context())
	id := chi.URLParam(r, "delivery_id")
	resp, err := s.Redeliver(r.Context(), teamID, actor, id)
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	resp.TraceID = audit.TraceIDFromContext(r.Context())
	writeJSON(w, http.StatusOK, resp)
}

func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.Body == nil {
		apperror.Write(w, codeValidationFailed, apperror.ClassBadRequest, "missing body", nil)
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		apperror.Write(w, codeValidationFailed, apperror.ClassBadRequest, "invalid json payload", nil)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writePreview(w http.ResponseWriter, action string, res PreviewResult) {
	effects := make([]dto.SideEffect, 0, len(res.SideEffects))
	for _, e := range res.SideEffects {
		effects = append(effects, dto.SideEffect{Effect: e, Reversible: false, Description: e})
	}
	writeJSON(w, http.StatusOK, dto.PreviewResponse{
		PreviewID:   res.PreviewID,
		Action:      action,
		Summary:     res.Summary,
		ExpiresAt:   res.ExpiresAt,
		SideEffects: effects,
	})
}

// writeWebhookError maps service sentinels to apperror codes / classes.
func writeWebhookError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidWebhookURL):
		apperror.Write(w, codeWebhookURLInvalid, apperror.ClassUnprocessable, "webhook url must be https and public", nil)
	case errors.Is(err, ErrInvalidEvents):
		apperror.Write(w, codeWebhookEvents, apperror.ClassUnprocessable, "events must be a non-empty subset of the catalog", nil)
	case errors.Is(err, ErrQuotaExceeded):
		apperror.Write(w, codeWebhookQuota, apperror.ClassConflict, "subscription quota exceeded", nil)
	case errors.Is(err, ErrSubscriptionNotFound):
		apperror.Write(w, codeWebhookNotFound, apperror.ClassNotFound, "subscription not found", nil)
	case errors.Is(err, ErrDeliveryNotFound):
		apperror.Write(w, codeDeliveryNotFound, apperror.ClassNotFound, "delivery not found", nil)
	case errors.Is(err, ErrPreviewNotFound):
		apperror.Write(w, codePreviewNotFound, apperror.ClassNotFound, "preview not found", nil)
	case errors.Is(err, ErrPreviewConsumed):
		apperror.Write(w, codePreviewConsumed, apperror.ClassConflict, "preview already consumed", nil)
	case errors.Is(err, ErrPreviewExpired):
		apperror.Write(w, codePreviewExpired, apperror.ClassConflict, "preview expired", nil)
	case errors.Is(err, ErrValidation):
		apperror.Write(w, codeValidationFailed, apperror.ClassBadRequest, "validation failed", nil)
	default:
		apperror.Write(w, "internal_error", apperror.ClassInternal, "internal error", nil)
	}
}
