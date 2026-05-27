package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared/rbac"
)

// MockStore implements auth.Store for testing
type MockStore struct {
	tokensByID   map[string]db.CliToken
	tokensByHash map[string]db.CliToken
	teams        map[string]db.Team
	members      map[string]map[string]bool
	roles        map[string]map[string]string
}

func NewMockStore() *MockStore {
	return &MockStore{
		tokensByID:   make(map[string]db.CliToken),
		tokensByHash: make(map[string]db.CliToken),
		teams:        make(map[string]db.Team),
		members:      make(map[string]map[string]bool),
		roles:        make(map[string]map[string]string),
	}
}

func (m *MockStore) FindCliTokenByID(_ context.Context, tokenID string) (db.CliToken, error) {
	if token, ok := m.tokensByID[tokenID]; ok {
		return token, nil
	}
	if len(m.tokensByHash) == 1 {
		for _, token := range m.tokensByHash {
			return token, nil
		}
	}
	return db.CliToken{}, errors.New("token not found")
}

func (m *MockStore) FindCliTokenByHash(_ context.Context, tokenHash string) (db.CliToken, error) {
	if token, ok := m.tokensByHash[tokenHash]; ok {
		return token, nil
	}
	return db.CliToken{}, errors.New("token not found")
}

func (m *MockStore) ResolveTeamBySlug(_ context.Context, slug string) (db.Team, error) {
	if team, ok := m.teams[slug]; ok {
		return team, nil
	}
	return db.Team{}, errors.New("team not found")
}

func (m *MockStore) CheckTeamMembership(_ context.Context, teamID string, userID string) (bool, error) {
	if members, ok := m.members[teamID]; ok {
		return members[userID], nil
	}
	return false, nil
}

func (m *MockStore) GetTeamMembershipRole(_ context.Context, teamID string, userID string) (string, error) {
	if roles, ok := m.roles[teamID]; ok {
		if role, ok := roles[userID]; ok {
			return role, nil
		}
	}
	return "", errors.New("membership not found")
}

func (m *MockStore) SetToken(token string, row db.CliToken) {
	parsed, err := ParseBearerToken(token)
	if err != nil {
		panic(err)
	}
	row.TokenHash = HashBearerToken(parsed.Secret)
	m.tokensByID[parsed.ID] = row
	m.tokensByHash[row.TokenHash] = row
}

func (m *MockStore) SetTeam(slug string, team db.Team) {
	m.teams[slug] = team
}

func (m *MockStore) SetMembership(teamID, userID string, member bool) {
	if m.members[teamID] == nil {
		m.members[teamID] = make(map[string]bool)
	}
	m.members[teamID][userID] = member
}

func (m *MockStore) SetRole(teamID, userID, role string) {
	if m.roles[teamID] == nil {
		m.roles[teamID] = make(map[string]string)
	}
	m.roles[teamID][userID] = role
}

// TestMiddlewareBearerValidToken tests Bearer middleware with valid token
func TestMiddlewareBearerValidToken(t *testing.T) {
	store := NewMockStore()
	mw := NewMiddleware(store)

	token, err := NewBearerToken("device", "token-12345")
	if err != nil {
		t.Fatalf("NewBearerToken() error = %v", err)
	}
	store.SetToken(token, db.CliToken{
		OwnerUserID: "user-1",
		TeamID:      "team-1",
		Scopes:      []string{"apps:read", "apps:write"},
		RevokedAt:   nil,
	})

	handler := mw.Bearer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ActorUserID(r.Context()) != "user-1" {
			t.Fatalf("expected actor_user_id=user-1, got %s", ActorUserID(r.Context()))
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// TestMiddlewareBearerRevokedToken tests Bearer middleware with revoked token
func TestMiddlewareBearerRevokedToken(t *testing.T) {
	store := NewMockStore()
	mw := NewMiddleware(store)

	token, err := NewBearerToken("device", "revoked-token")
	if err != nil {
		t.Fatalf("NewBearerToken() error = %v", err)
	}
	revokedAt := time.Now().AddDate(0, 0, -1)
	store.SetToken(token, db.CliToken{
		OwnerUserID: "user-1",
		TeamID:      "team-1",
		Scopes:      []string{"apps:read"},
		RevokedAt:   &revokedAt,
	})

	handler := mw.Bearer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// TestMiddlewareResolveTeamNotFound tests ResolveTeam middleware when team not found
func TestMiddlewareResolveTeamNotFound(t *testing.T) {
	store := NewMockStore()
	mw := NewMiddleware(store)

	// Set up chi router to extract URL params
	router := chi.NewRouter()
	router.Route("/teams/{team_slug}", func(r chi.Router) {
		r.Use(mw.ResolveTeam)
		r.Get("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
	})

	req := httptest.NewRequest("GET", "/teams/nonexistent", nil)
	req = req.WithContext(context.WithValue(req.Context(), contextKey("token_team_id"), "team-1"))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// TestMiddlewareCheckMembershipNotMember tests CheckMembership when user not in team
func TestMiddlewareCheckMembershipNotMember(t *testing.T) {
	store := NewMockStore()
	mw := NewMiddleware(store)

	store.SetTeam("team-slug", db.Team{ID: "team-1", Slug: "team-slug"})
	// User not added to members
	store.SetMembership("team-1", "user-2", false)

	handler := mw.CheckMembership(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	ctx := context.Background()
	ctx = withActorUserID(ctx, "user-2")
	ctx = withTeam(ctx, "team-1", "team-slug")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// TestMiddlewareCheckTokenScopeForbidden tests CheckTokenScope with insufficient scope
func TestMiddlewareCheckTokenScopeForbidden(t *testing.T) {
	store := NewMockStore()
	mw := NewMiddleware(store)

	// Create a handler that checks for write scope but token only has read
	handler := mw.CheckTokenScope(rbac.ActionInviteMembers, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	ctx := context.Background()
	ctx = withActorUserID(ctx, "user-1")
	ctx = withTokenScopes(ctx, []string{"apps:read"})
	ctx = withActorRole(ctx, "member")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

// TestMiddlewareCheckTokenScopeWithWildcard tests CheckTokenScope with wildcard scope
func TestMiddlewareCheckTokenScopeWithWildcard(t *testing.T) {
	store := NewMockStore()
	mw := NewMiddleware(store)

	handler := mw.CheckTokenScope(rbac.ActionListApps, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	ctx := context.Background()
	ctx = withActorUserID(ctx, "user-1")
	ctx = withTokenScopes(ctx, []string{"*"})
	ctx = withActorRole(ctx, "admin")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// TestMiddlewareCheckRoleForbidden tests CheckTokenScope with insufficient role
func TestMiddlewareCheckRoleForbidden(t *testing.T) {
	store := NewMockStore()
	mw := NewMiddleware(store)

	// ActionRemoveMembers requires admin role
	handler := mw.CheckTokenScope(rbac.ActionRemoveMembers, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	ctx := context.Background()
	ctx = withActorUserID(ctx, "user-1")
	ctx = withTokenScopes(ctx, []string{"*"})
	ctx = withActorRole(ctx, "viewer") // viewer can't remove members
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

// TestMiddlewareBearerMissingToken tests Bearer middleware with no token
func TestMiddlewareBearerMissingToken(t *testing.T) {
	store := NewMockStore()
	mw := NewMiddleware(store)

	handler := mw.Bearer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	// No Authorization header
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
