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
	HasAnyOwner(ctx context.Context) (bool, error)
	BootstrapOwner(ctx context.Context, params db.BootstrapOwnerParams) (teamID string, userID string, err error)
	ListTeamMembers(ctx context.Context, teamID string) ([]db.Member, error)
	CreatePreview(ctx context.Context, teamID, actorUserID, action string, args json.RawMessage, summary string) (db.Preview, error)
	GetPreview(ctx context.Context, previewID string) (db.Preview, error)
	ConsumePreview(ctx context.Context, previewID string) error
	InviteMember(ctx context.Context, params db.InviteMemberParams) (db.Member, error)
	RemoveMember(ctx context.Context, teamID, actorUserID, targetUserID string) error
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
	previewActionInvite = "invite_member"
	previewActionRemove = "remove_member"
)

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

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "removed"})
	}
}

// NewRouter returns the HTTP router for the server.
func NewRouter(store routerStore) http.Handler {
	mw := auth.NewMiddleware(store)

	r := chi.NewRouter()
	r.Post("/v1/admin/bootstrap-owner", bootstrapOwnerHandler(store))

	// Device flow endpoints (no auth required)
	r.Route("/v1/auth/device", func(sr chi.Router) {
		sr.Post("/start", deviceFlowStartHandler())
		sr.Post("/poll", deviceFlowPollHandler())
	})

	// Tool authorization endpoint (requires temporary token)
	r.Post("/v1/teams/{team_slug}/auth:grant-tools", authorizeToolsHandler())

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
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionListApps, next)
		}).Get("/apps", listAppsHandler(store))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionListApps, next)
		}).Get("/apps/{app_slug}", getAppHandler(store))
		sr.With(func(next http.Handler) http.Handler {
			return mw.CheckTokenScope(rbac.ActionListMembers, next)
		}).Get("/members", listMembersHandler(store))
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

func writePreviewResponse(w http.ResponseWriter, preview db.Preview, summary string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dto.PreviewResponse{
		PreviewID: preview.ID,
		Action:    preview.Action,
		Summary:   summary,
		ExpiresAt: preview.ExpiresAt,
	})
}
