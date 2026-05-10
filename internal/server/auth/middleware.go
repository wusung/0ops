package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared/rbac"
)

// Store is the auth/store contract needed by the middleware chain.
type Store interface {
	FindCliTokenByHash(ctx context.Context, tokenHash string) (db.CliToken, error)
	ResolveTeamBySlug(ctx context.Context, slug string) (db.Team, error)
	CheckTeamMembership(ctx context.Context, teamID string, userID string) (bool, error)
	GetTeamMembershipRole(ctx context.Context, teamID string, userID string) (string, error)
}

// Middleware bundles the auth chain dependencies.
type Middleware struct {
	repo Store
}

// NewMiddleware creates auth middlewares backed by the repository.
func NewMiddleware(repo Store) *Middleware {
	return &Middleware{repo: repo}
}

// Bearer validates the Authorization header and stores token context.
func (m *Middleware) Bearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if token == "" {
			apperror.Write(w, "unauthorized", apperror.ClassUnauthorized, "missing bearer token", nil)
			return
		}

		row, err := m.repo.FindCliTokenByHash(r.Context(), HashBearerToken(token))
		if err != nil {
			apperror.Write(w, "token_invalid", apperror.ClassUnauthorized, "invalid bearer token", nil)
			return
		}
		if row.RevokedAt != nil {
			apperror.Write(w, "token_revoked", apperror.ClassUnauthorized, "token revoked", nil)
			return
		}

		ctx := withActorUserID(r.Context(), row.OwnerUserID)
		ctx = withTokenTeamID(ctx, row.TeamID)
		ctx = withTokenScopes(ctx, row.Scopes)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ResolveTeam loads the team slug and enforces token/team affinity.
func (m *Middleware) ResolveTeam(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "team_slug")
		if slug == "" {
			apperror.Write(w, "team_not_found", apperror.ClassNotFound, "team not found", map[string]any{"team_slug": slug})
			return
		}

		team, err := m.repo.ResolveTeamBySlug(r.Context(), slug)
		if err != nil {
			apperror.Write(w, "team_not_found", apperror.ClassNotFound, "team not found", map[string]any{"team_slug": slug})
			return
		}
		if team.ID != TokenTeamID(r.Context()) {
			apperror.Write(w, "team_not_found", apperror.ClassNotFound, "team not found", map[string]any{"team_slug": slug})
			return
		}

		ctx := withTeam(r.Context(), team.ID, team.Slug)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CheckMembership ensures the actor belongs to the resolved team.
func (m *Middleware) CheckMembership(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok, err := m.repo.CheckTeamMembership(r.Context(), TeamID(r.Context()), ActorUserID(r.Context()))
		if err != nil || !ok {
			apperror.Write(w, "team_not_found", apperror.ClassNotFound, "team not found", map[string]any{"team_slug": TeamSlug(r.Context())})
			return
		}
		role, err := m.repo.GetTeamMembershipRole(r.Context(), TeamID(r.Context()), ActorUserID(r.Context()))
		if err != nil {
			apperror.Write(w, "team_not_found", apperror.ClassNotFound, "team not found", map[string]any{"team_slug": TeamSlug(r.Context())})
			return
		}
		ctx := withActorRole(r.Context(), role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CheckTokenScope ensures the bearer token grants the required scope.
func (m *Middleware) CheckTokenScope(action rbac.Action, next http.Handler) http.Handler {
	requirement := rbac.RequiredFor(action)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rbac.AtLeast(rbac.Role(ActorRole(r.Context())), requirement.MinRole) {
			apperror.Write(w, "forbidden_role", apperror.ClassForbidden, "forbidden role", map[string]any{
				"required_role": string(requirement.MinRole),
				"actor_role":    ActorRole(r.Context()),
			})
			return
		}
		if !containsScope(TokenScopes(r.Context()), string(requirement.RequiredScope)) {
			apperror.Write(w, "forbidden_scope", apperror.ClassForbidden, "forbidden scope", map[string]any{
				"required_scope": string(requirement.RequiredScope),
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func containsScope(scopes []string, want string) bool {
	for _, scope := range scopes {
		if scope == want || scope == "*" {
			return true
		}
	}
	return false
}
