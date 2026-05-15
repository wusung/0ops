package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/shared/dto"
	"github.com/winshare/zeroops/internal/shared/rbac"
)

// auditQueryService is the dependency surface used by the audit HTTP
// handlers; tests substitute a fake implementation.
type auditQueryService interface {
	Query(ctx context.Context, filter audit.QueryFilter) (audit.QueryResult, error)
	Get(ctx context.Context, teamID string, id int64, scope audit.QueryScope, actorUserID string) (audit.Row, error)
}

func listAuditHandler(svc auditQueryService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter, err := parseAuditFilter(r)
		if err != nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, err.Error(), nil)
			return
		}
		filter.TeamID = auth.TeamID(r.Context())

		actorRole := rbac.Role(auth.ActorRole(r.Context()))
		isAdmin := rbac.AtLeast(actorRole, rbac.RoleAdmin)

		// Self-scope: actor=me (or actor=<my-uuid>) and not admin → restrict
		// to the caller's rows; spec § 6.2.
		actorUserID := auth.ActorUserID(r.Context())
		callerIsActor := filter.ActorUserID != "" && (filter.ActorUserID == actorUserID || strings.EqualFold(filter.ActorUserID, "me"))

		switch {
		case callerIsActor:
			filter.Scope = audit.QueryScopeSelf
			filter.ActorUserID = actorUserID
		case isAdmin:
			filter.Scope = audit.QueryScopeTeam
		default:
			apperror.Write(w, "forbidden_role", apperror.ClassForbidden, "team-wide audit query requires admin role", map[string]any{
				"required_role": string(rbac.RoleAdmin),
				"actor_role":    string(actorRole),
			})
			return
		}

		out, err := svc.Query(r.Context(), filter)
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to query audit_log", nil)
			return
		}
		writeAuditList(w, out, filter.PageSize)
	}
}

func getAuditHandler(svc auditQueryService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || id <= 0 {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid audit id", nil)
			return
		}

		actorRole := rbac.Role(auth.ActorRole(r.Context()))
		actorUserID := auth.ActorUserID(r.Context())
		scope := audit.QueryScopeTeam
		if !rbac.AtLeast(actorRole, rbac.RoleAdmin) {
			scope = audit.QueryScopeSelf
		}

		row, err := svc.Get(r.Context(), auth.TeamID(r.Context()), id, scope, actorUserID)
		if err != nil {
			switch {
			case errors.Is(err, audit.ErrNotFound):
				apperror.Write(w, "audit_not_found", apperror.ClassNotFound, "audit row not found", nil)
			case errors.Is(err, audit.ErrForbidden):
				apperror.Write(w, "forbidden_role", apperror.ClassForbidden, "audit row outside scope", nil)
			default:
				apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to fetch audit row", nil)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(newAuditEntry(row))
	}
}

func parseAuditFilter(r *http.Request) (audit.QueryFilter, error) {
	q := r.URL.Query()

	pageSize, err := audit.ParsePageSize(q.Get("page_size"))
	if err != nil {
		return audit.QueryFilter{}, err
	}

	var since, until time.Time
	if v := strings.TrimSpace(q.Get("since")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return audit.QueryFilter{}, errors.New("invalid since")
		}
		since = t
	}
	if v := strings.TrimSpace(q.Get("until")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return audit.QueryFilter{}, errors.New("invalid until")
		}
		until = t
	}

	return audit.QueryFilter{
		Since:       since,
		Until:       until,
		Action:      strings.TrimSpace(q.Get("action")),
		ActorUserID: strings.TrimSpace(q.Get("actor")),
		TraceID:     strings.TrimSpace(q.Get("trace_id")),
		PageSize:    pageSize,
		Cursor:      strings.TrimSpace(q.Get("cursor")),
	}, nil
}

func writeAuditList(w http.ResponseWriter, out audit.QueryResult, pageSize int) {
	items := make([]dto.AuditLogEntry, 0, len(out.Items))
	for _, row := range out.Items {
		items = append(items, newAuditEntry(row))
	}
	var next *string
	if out.NextCursor != "" {
		v := out.NextCursor
		next = &v
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dto.ListAuditResponse{
		Items:      items,
		NextCursor: next,
		PageSize:   pageSize,
	})
}

func newAuditEntry(row audit.Row) dto.AuditLogEntry {
	out := dto.AuditLogEntry{
		ID:          row.ID,
		Time:        row.CreatedAt.UTC(),
		Source:      row.Source,
		ActorUserID: row.ActorUserID,
		Action:      row.Action,
		SubjectType: row.SubjectType,
		SubjectID:   row.SubjectID,
		Outcome:     row.Outcome,
		PreviewID:   row.PreviewID,
		HTTPStatus:  row.HTTPStatus,
	}
	if row.ActorLogin != nil && *row.ActorLogin != "" {
		actor := *row.ActorLogin
		out.Actor = &actor
	}
	if row.TraceID != "" {
		t := row.TraceID
		out.TraceID = &t
	}
	if len(row.Args) > 0 && string(row.Args) != "null" {
		out.Args = append(json.RawMessage(nil), row.Args...)
	}
	if len(row.Result) > 0 && string(row.Result) != "null" {
		out.Result = append(json.RawMessage(nil), row.Result...)
	}
	return out
}
