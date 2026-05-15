package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/reconciler"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// incidentService is the dependency surface exposed to the HTTP handlers.
// cmd/server wires a *reconciler.IncidentService instance; tests can
// substitute a fake to keep handler logic stack-internal.
type incidentService interface {
	List(ctx context.Context, filter db.IncidentListFilter) (db.IncidentListResult, error)
	Get(ctx context.Context, teamID, id string) (db.IncidentRow, error)
	Close(ctx context.Context, in reconciler.CloseParams) (db.IncidentRow, error)
}

func listIncidentsHandler(svc incidentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter, err := parseIncidentFilter(r)
		if err != nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, err.Error(), nil)
			return
		}
		filter.TeamID = auth.TeamID(r.Context())
		out, err := svc.List(r.Context(), filter)
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to list incidents", nil)
			return
		}
		writeIncidentList(w, out, filter.PageSize)
	}
}

func getIncidentHandler(svc incidentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(chi.URLParam(r, "id"))
		if id == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "missing incident id", nil)
			return
		}
		row, err := svc.Get(r.Context(), auth.TeamID(r.Context()), id)
		if err != nil {
			if errors.Is(err, reconciler.ErrNotFound) {
				apperror.Write(w, "incident_not_found", apperror.ClassNotFound, "incident not found", nil)
				return
			}
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to fetch incident", nil)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(newIncidentDTO(row))
	}
}

func closeIncidentHandler(svc incidentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(chi.URLParam(r, "id"))
		if id == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "missing incident id", nil)
			return
		}
		var req dto.CloseIncidentRequest
		body, _ := io.ReadAll(io.LimitReader(r.Body, 16*1024))
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid request body", nil)
				return
			}
		}
		row, err := svc.Close(r.Context(), reconciler.CloseParams{
			TeamID:     auth.TeamID(r.Context()),
			IncidentID: id,
			ActorID:    auth.ActorUserID(r.Context()),
			Note:       strings.TrimSpace(req.Note),
		})
		if err != nil {
			switch {
			case errors.Is(err, reconciler.ErrNotFound):
				apperror.Write(w, "incident_not_found", apperror.ClassNotFound, "incident not found", nil)
			case errors.Is(err, reconciler.ErrAlreadyClosed):
				apperror.Write(w, "incident_already_closed", apperror.ClassConflict, "incident already closed", nil)
			default:
				apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to close incident", nil)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(newIncidentDTO(row))
	}
}

func parseIncidentFilter(r *http.Request) (db.IncidentListFilter, error) {
	q := r.URL.Query()
	filter := db.IncidentListFilter{
		Status:   strings.TrimSpace(q.Get("status")),
		Kind:     strings.TrimSpace(q.Get("kind")),
		Severity: strings.TrimSpace(q.Get("severity")),
		PageSize: 50,
	}
	if raw := strings.TrimSpace(q.Get("page_size")); raw != "" {
		var n int
		if _, err := fmt.Sscanf(raw, "%d", &n); err != nil || n < 1 {
			return db.IncidentListFilter{}, errors.New("invalid page_size")
		}
		if n > 200 {
			n = 200
		}
		filter.PageSize = n
	}
	if raw := strings.TrimSpace(q.Get("cursor")); raw != "" {
		ts, id, err := decodeIncidentCursor(raw)
		if err != nil {
			return db.IncidentListFilter{}, err
		}
		filter.CursorOpenedAt = &ts
		filter.CursorID = id
	}
	return filter, nil
}

func writeIncidentList(w http.ResponseWriter, out db.IncidentListResult, pageSize int) {
	items := make([]dto.Incident, 0, len(out.Items))
	for _, row := range out.Items {
		items = append(items, newIncidentDTO(row))
	}
	var next *string
	if out.NextOpenedAt != nil && out.NextID != "" {
		c := encodeIncidentCursor(*out.NextOpenedAt, out.NextID)
		next = &c
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dto.ListIncidentsResponse{
		Items:      items,
		PageSize:   pageSize,
		NextCursor: next,
	})
}

func newIncidentDTO(row db.IncidentRow) dto.Incident {
	out := dto.Incident{
		ID:          row.ID,
		TeamID:      row.TeamID,
		SubjectType: row.SubjectType,
		SubjectID:   row.SubjectID,
		Kind:        row.Kind,
		Severity:    row.Severity,
		Description: row.Description,
		TraceID:     row.TraceID,
		OpenedAt:    row.OpenedAt.UTC(),
		ClosedBy:    row.ClosedBy,
		ClosedNote:  row.ClosedNote,
	}
	if row.ClosedAt != nil {
		v := row.ClosedAt.UTC()
		out.ClosedAt = &v
	}
	return out
}

// incidentCursor is the base64-encoded JSON pair used to keyset-paginate
// the incidents list endpoint. Format mirrors audit's encoding so CLI
// callers see a consistent shape.
type incidentCursor struct {
	OpenedAt string `json:"t"`
	ID       string `json:"i"`
}

func encodeIncidentCursor(ts time.Time, id string) string {
	raw, err := json.Marshal(incidentCursor{
		OpenedAt: ts.UTC().Format(time.RFC3339Nano),
		ID:       id,
	})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeIncidentCursor(raw string) (time.Time, string, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	var payload incidentCursor
	if err := json.Unmarshal(data, &payload); err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor payload")
	}
	ts, err := time.Parse(time.RFC3339Nano, payload.OpenedAt)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor timestamp")
	}
	return ts, payload.ID, nil
}
