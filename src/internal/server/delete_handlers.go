package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/services/audit"
	deleteappsvc "github.com/winshare/zeroops/internal/server/services/deleteapp"
	"github.com/winshare/zeroops/internal/shared/dto"
)

const previewActionDelete = "delete_app"

// deleteAppDeps captures the optional infrastructure clients used by the
// delete_app saga. The HTTP wiring constructs default no-op clients in
// dev; integration callers can override via SetDeleteAppDeps.
type deleteAppDeps struct {
	customHostname deleteappsvc.CustomHostnameClient
	workflow       deleteappsvc.WorkflowCanceler
	gitops         deleteappsvc.GitOpsRemover
	argoCD         deleteappsvc.ArgoCDChecker
}

var defaultDeleteAppDeps = deleteAppDeps{}

// SetDeleteAppDeps overrides the dependencies used by the delete_app
// HTTP handlers. Production binaries call this once at startup.
func SetDeleteAppDeps(deps deleteAppDeps) {
	defaultDeleteAppDeps = deps
}

func newDeleteAppService(store appsStore) *deleteappsvc.Service {
	return deleteappsvc.New(store,
		defaultDeleteAppDeps.customHostname,
		defaultDeleteAppDeps.workflow,
		defaultDeleteAppDeps.gitops,
		defaultDeleteAppDeps.argoCD,
	)
}

func previewDeleteAppHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		appSlug := strings.TrimSpace(chi.URLParam(r, "app_slug"))
		if appSlug == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "app_slug is required", nil)
			return
		}

		var req dto.AppDeleteRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		svc := newDeleteAppService(store)
		out, err := svc.Preview(
			r.Context(),
			auth.TeamID(r.Context()),
			auth.ActorUserID(r.Context()),
			auth.TeamSlug(r.Context()),
			appSlug,
			req.Confirm,
		)
		if err != nil {
			writeDeleteAppError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.PreviewResponse{
			PreviewID:   out.Preview.ID,
			Action:      previewActionDelete,
			Summary:     out.Summary,
			ExpiresAt:   out.Preview.ExpiresAt,
			SideEffects: out.SideEffects,
		})
		recordPreviewCreatedMetric(previewActionDelete)
	}
}

func deleteAppHandler(store appsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dto.ConfirmDeleteAppRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		svc := newDeleteAppService(store)
		out, err := svc.Confirm(
			r.Context(),
			auth.TeamID(r.Context()),
			auth.ActorUserID(r.Context()),
			auth.TeamSlug(r.Context()),
			req.PreviewID,
			audit.TraceIDFromContext(r.Context()),
		)
		if err != nil {
			writeDeleteAppError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out.Response)

		consumeOutcome := "success"
		if out.Replayed {
			consumeOutcome = "idempotent_replay"
		}
		recordPreviewConsumedMetric(previewActionDelete, consumeOutcome, previewConsumeLatency(out.PreviewCreatedAt))
	}
}

func writeDeleteAppError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, deleteappsvc.ErrConfirmMismatch):
		apperror.Write(w, "confirm_mismatch", apperror.ClassBadRequest, "confirm must equal app_slug", nil)
	case errors.Is(err, deleteappsvc.ErrValidationFailed):
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest, err.Error(), nil)
	case errors.Is(err, deleteappsvc.ErrAppNotFound):
		apperror.Write(w, "app_not_found", apperror.ClassNotFound, "app not found", nil)
	case errors.Is(err, deleteappsvc.ErrAlreadyDeleting):
		apperror.Write(w, "already_deleting", apperror.ClassConflict, "app delete already in progress", nil)
	case errors.Is(err, deleteappsvc.ErrPreviewNotFound):
		apperror.Write(w, "preview_not_found", apperror.ClassNotFound, "preview not found", nil)
	case errors.Is(err, deleteappsvc.ErrPreviewConsumed):
		apperror.Write(w, "preview_consumed", apperror.ClassConflict, "preview already consumed", nil)
	case errors.Is(err, deleteappsvc.ErrPreviewExpired):
		apperror.Write(w, "preview_expired", apperror.ClassConflict, "preview expired", nil)
	case errors.Is(err, deleteappsvc.ErrCompensateMarked):
		apperror.Write(w, "delete_compensated", apperror.ClassConflict, "delete reversed; cleanup_residue scheduled", nil)
	default:
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to delete app", nil)
	}
}
