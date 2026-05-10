package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared/dto"
	"github.com/winshare/zeroops/internal/shared/rbac"
)

type appsStore interface {
	auth.Store
	ListTeamApps(ctx context.Context, teamID string, limit int32, afterSlug string) ([]db.App, error)
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

		rows, err := store.ListTeamApps(r.Context(), auth.TeamID(r.Context()), int32(pageSize+1), r.URL.Query().Get("cursor"))
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to list apps", nil)
			return
		}

		var nextCursor *string
		if len(rows) > pageSize {
			v := rows[pageSize-1].Slug
			nextCursor = &v
			rows = rows[:pageSize]
		}

		items := make([]dto.AppRef, 0, len(rows))
		for _, row := range rows {
			items = append(items, dto.AppRef{
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
			})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.ListAppsResponse{
			Items:      items,
			NextCursor: nextCursor,
			PageSize:   pageSize,
		})
	}
}

// NewRouter returns the HTTP router for the server.
func NewRouter(store appsStore) http.Handler {
	mw := auth.NewMiddleware(store)

	r := chi.NewRouter()

	r.Route("/v1/teams/{team_slug}", func(sr chi.Router) {
		sr.Use(mw.Bearer)
		sr.Use(mw.ResolveTeam)
		sr.Use(mw.CheckMembership)
		sr.Use(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionListApps, next)
		})
		sr.Get("/apps", listAppsHandler(store))
	})

	return r
}
