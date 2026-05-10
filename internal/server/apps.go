package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared/dto"
	"github.com/winshare/zeroops/internal/shared/rbac"
)

type appsStore interface {
	auth.Store
	ListTeamApps(ctx context.Context, teamID string, limit int32, afterID *string) ([]db.App, error)
	GetTeamAppBySlug(ctx context.Context, teamID string, slug string) (db.App, error)
}

type routerStore interface {
	appsStore
	teamsStore
}

type appCursor struct {
	ID string    `json:"id"`
	TS time.Time `json:"ts"`
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

// NewRouter returns the HTTP router for the server.
func NewRouter(store routerStore) http.Handler {
	mw := auth.NewMiddleware(store)

	r := chi.NewRouter()

	r.Route("/v1/me", func(sr chi.Router) {
		sr.Use(mw.Bearer)
		sr.Use(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionListTeams, next)
		})
		sr.Get("/teams", listTeamsHandler(store))
	})

	r.Route("/v1/teams/{team_slug}", func(sr chi.Router) {
		sr.Use(mw.Bearer)
		sr.Use(mw.ResolveTeam)
		sr.Use(mw.CheckMembership)
		sr.Use(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionListApps, next)
		})
		sr.Get("/apps", listAppsHandler(store))
		sr.Get("/apps/{app_slug}", getAppHandler(store))
	})

	return r
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
