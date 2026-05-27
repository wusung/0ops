package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared/dto"
)

type teamsStore interface {
	auth.Store
	ListUserTeams(ctx context.Context, userID string, limit int32, afterSlug *string) ([]db.TeamMembership, error)
}

func listTeamsHandler(store teamsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := listAllUserTeams(r.Context(), store, auth.ActorUserID(r.Context()))
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to list teams", nil)
			return
		}

		items := make([]dto.TeamMembership, 0, len(rows))
		for _, row := range rows {
			items = append(items, dto.TeamMembership{
				TeamSlug: row.Team.Slug,
				TeamName: row.Team.Name,
				Role:     row.Role,
				Plan:     row.Team.Plan,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.ListTeamsResponse{Items: items})
	}
}

func listAllUserTeams(ctx context.Context, store teamsStore, userID string) ([]db.TeamMembership, error) {
	const pageSize int32 = 200
	const maxPages = 1000

	items := make([]db.TeamMembership, 0, pageSize)
	var afterSlug *string

	for page := 0; page < maxPages; page++ {
		rows, err := store.ListUserTeams(ctx, userID, pageSize, afterSlug)
		if err != nil {
			return nil, err
		}

		items = append(items, rows...)
		if len(rows) < int(pageSize) {
			return items, nil
		}

		nextAfterSlug := rows[len(rows)-1].Team.Slug
		if nextAfterSlug == "" || (afterSlug != nil && nextAfterSlug == *afterSlug) {
			return nil, fmt.Errorf("team pagination did not advance")
		}
		afterSlug = &nextAfterSlug
	}

	return nil, fmt.Errorf("team pagination exceeded %d pages", maxPages)
}
