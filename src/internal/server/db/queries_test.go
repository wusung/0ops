package db_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/winshare/zeroops/internal/server/auth"
	dbpkg "github.com/winshare/zeroops/internal/server/db"
)

func TestNewPoolAndRepositorySmoke(t *testing.T) {
	t.Helper()

	databaseURL := resolveTestDatabaseURL()
	if databaseURL == "" {
		t.Skip("DATABASE_URL is required for db smoke tests")
	}

	ctx := context.Background()
	pool, err := dbpkg.NewPool(ctx, dbpkg.Config{URL: databaseURL})
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	defer pool.Close()

	if err := dbpkg.MustPing(ctx, pool); err != nil {
		t.Fatalf("MustPing() error = %v", err)
	}
}

func TestConfigFromEnvDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://ops:ops_dev_pw@db:5432/ops?sslmode=disable")

	cfg := dbpkg.ConfigFromEnv()

	if cfg.URL == "" {
		t.Fatal("expected URL from env")
	}
	if cfg.MaxConns != 10 {
		t.Fatalf("expected MaxConns=10, got %d", cfg.MaxConns)
	}
	if cfg.MinConns != 0 {
		t.Fatalf("expected MinConns=0, got %d", cfg.MinConns)
	}
}

func TestGeneratedQueriesCompile(t *testing.T) {
	t.Helper()

	var _ = dbpkg.NewQueries
}

func TestRepositoryResolveTeamBySlug(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)

	userID := seedUser(ctx, t, pool, "alice")
	teamID, teamSlug := seedTeam(ctx, t, pool, "personal-alice", "Alice Team")
	seedMembership(ctx, t, pool, teamID, userID, "owner")

	team, err := repo.ResolveTeamBySlug(ctx, teamSlug)
	if err != nil {
		t.Fatalf("ResolveTeamBySlug() error = %v", err)
	}
	if team.Slug != teamSlug {
		t.Fatalf("expected slug %q, got %q", teamSlug, team.Slug)
	}
}

func TestRepositoryListUserTeams(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)

	userID := seedUser(ctx, t, pool, "alice")
	otherUserID := seedUser(ctx, t, pool, "bob")
	teamA, teamASlug := seedTeam(ctx, t, pool, "a-team", "A Team")
	teamB, teamBSlug := seedTeam(ctx, t, pool, "b-team", "B Team")
	otherTeam, _ := seedTeam(ctx, t, pool, "z-team", "Z Team")

	seedMembership(ctx, t, pool, teamA, userID, "owner")
	seedMembership(ctx, t, pool, teamB, userID, "member")
	seedMembership(ctx, t, pool, otherTeam, otherUserID, "viewer")

	items, err := repo.ListUserTeams(ctx, userID, 10, nil)
	if err != nil {
		t.Fatalf("ListUserTeams() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 teams, got %d", len(items))
	}
	if items[0].Team.Slug != teamASlug || items[1].Team.Slug != teamBSlug {
		t.Fatalf("unexpected order: %#v", items)
	}
}

func TestRepositoryCheckTeamMembership(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)

	userID := seedUser(ctx, t, pool, "alice")
	teamID, _ := seedTeam(ctx, t, pool, "alpha", "Alpha")
	seedMembership(ctx, t, pool, teamID, userID, "owner")

	ok, err := repo.CheckTeamMembership(ctx, teamID, userID)
	if err != nil {
		t.Fatalf("CheckTeamMembership() error = %v", err)
	}
	if !ok {
		t.Fatal("expected membership to exist")
	}
}

func TestRepositoryHasAnyOwner(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)

	hasOwner, err := repo.HasAnyOwner(ctx)
	if err != nil {
		t.Fatalf("HasAnyOwner() error = %v", err)
	}
	initial := hasOwner

	userID := seedUser(ctx, t, pool, "owner-user")
	teamID, _ := seedTeam(ctx, t, pool, "owner-team", "Owner Team")
	seedMembership(ctx, t, pool, teamID, userID, "owner")

	hasOwner, err = repo.HasAnyOwner(ctx)
	if err != nil {
		t.Fatalf("HasAnyOwner() after seed error = %v", err)
	}
	if !hasOwner {
		t.Fatalf("expected hasOwner=true, got false (initial=%v)", initial)
	}
}

func TestRepositoryCreateCLITokenStoresDeviceKind(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)

	userID := seedUser(ctx, t, pool, "owner-user")
	teamID, _ := seedTeam(ctx, t, pool, "owner-team", "Owner Team")

	token, err := repo.CreateCLIToken(ctx, userID, teamID, []string{"apps:read"})
	if err != nil {
		t.Fatalf("CreateCLIToken() error = %v", err)
	}

	parsed, err := auth.ParseBearerToken(token)
	if err != nil {
		t.Fatalf("ParseBearerToken() error = %v", err)
	}
	row, err := repo.FindCliTokenByID(ctx, parsed.ID)
	if err != nil {
		t.Fatalf("FindCliTokenByID() error = %v", err)
	}
	if row.Kind != "device" {
		t.Fatalf("Kind = %q, want device", row.Kind)
	}
	if row.TeamID != teamID || row.OwnerUserID != userID {
		t.Fatalf("unexpected token row: %#v", row)
	}
}

func TestRepositoryCreatePATStoresExpiry(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)

	userID := seedUser(ctx, t, pool, "owner-user")
	teamID, _ := seedTeam(ctx, t, pool, "owner-team", "Owner Team")
	expiresAt := time.Now().UTC().Add(30 * 24 * time.Hour)

	token, err := repo.CreatePAT(ctx, userID, teamID, "ci", []string{"apps:read"}, expiresAt)
	if err != nil {
		t.Fatalf("CreatePAT() error = %v", err)
	}

	parsed, err := auth.ParseBearerToken(token)
	if err != nil {
		t.Fatalf("ParseBearerToken() error = %v", err)
	}
	row, err := repo.FindCliTokenByID(ctx, parsed.ID)
	if err != nil {
		t.Fatalf("FindCliTokenByID() error = %v", err)
	}
	if row.Kind != "pat" {
		t.Fatalf("Kind = %q, want pat", row.Kind)
	}
	if row.ExpiresAt == nil || row.ExpiresAt.IsZero() {
		t.Fatalf("expected expires_at, got %#v", row.ExpiresAt)
	}
}

func TestRepositoryRevokeCLITokenByID(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)

	userID := seedUser(ctx, t, pool, "owner-user")
	teamID, _ := seedTeam(ctx, t, pool, "owner-team", "Owner Team")

	token, err := repo.CreateCLIToken(ctx, userID, teamID, []string{"apps:read"})
	if err != nil {
		t.Fatalf("CreateCLIToken() error = %v", err)
	}
	parsed, err := auth.ParseBearerToken(token)
	if err != nil {
		t.Fatalf("ParseBearerToken() error = %v", err)
	}
	if err := repo.RevokeCLITokenByID(ctx, parsed.ID); err != nil {
		t.Fatalf("RevokeCLITokenByID() error = %v", err)
	}
	row, err := repo.FindCliTokenByID(ctx, parsed.ID)
	if err != nil {
		t.Fatalf("FindCliTokenByID() error = %v", err)
	}
	if row.RevokedAt == nil {
		t.Fatal("expected revoked_at to be set")
	}
}

func TestRepositoryRevokePATByName(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)

	userID := seedUser(ctx, t, pool, "owner-user")
	teamID, _ := seedTeam(ctx, t, pool, "owner-team", "Owner Team")

	token, err := repo.CreatePAT(ctx, userID, teamID, "ci", []string{"apps:read"}, time.Now().UTC().Add(30*24*time.Hour))
	if err != nil {
		t.Fatalf("CreatePAT() error = %v", err)
	}
	parsed, err := auth.ParseBearerToken(token)
	if err != nil {
		t.Fatalf("ParseBearerToken() error = %v", err)
	}
	if err := repo.RevokePATByName(ctx, teamID, "ci"); err != nil {
		t.Fatalf("RevokePATByName() error = %v", err)
	}
	row, err := repo.FindCliTokenByID(ctx, parsed.ID)
	if err != nil {
		t.Fatalf("FindCliTokenByID() error = %v", err)
	}
	if row.RevokedAt == nil {
		t.Fatal("expected revoked_at to be set")
	}
}

func newTestRepository(t *testing.T) (*dbpkg.Repository, context.Context, *pgxpool.Pool) {
	t.Helper()

	databaseURL := resolveTestDatabaseURL()
	if databaseURL == "" {
		t.Skip("DATABASE_URL is required for db smoke tests")
	}

	ctx := context.Background()
	pool, err := dbpkg.NewPool(ctx, dbpkg.Config{URL: databaseURL})
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	t.Cleanup(pool.Close)

	return dbpkg.NewRepository(pool), ctx, pool
}

func seedUser(ctx context.Context, t *testing.T, pool *pgxpool.Pool, login string) string {
	t.Helper()

	login = uniqueSuffix(t, login)
	var id pgtype.UUID
	err := pool.QueryRow(ctx, `
INSERT INTO user_account (github_login)
VALUES ($1)
RETURNING id
`, login).Scan(&id)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_account WHERE id = $1`, id)
	})

	return id.String()
}

func seedTeam(ctx context.Context, t *testing.T, pool *pgxpool.Pool, slug, name string) (string, string) {
	t.Helper()

	slug = uniqueSuffix(t, slug)
	name = uniqueSuffix(t, name)
	var id pgtype.UUID
	err := pool.QueryRow(ctx, `
INSERT INTO team (slug, name)
VALUES ($1, $2)
RETURNING id
`, slug, name).Scan(&id)
	if err != nil {
		t.Fatalf("seed team: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM team WHERE id = $1`, id)
	})

	return id.String(), slug
}

func seedMembership(ctx context.Context, t *testing.T, pool *pgxpool.Pool, teamID, userID, role string) {
	t.Helper()

	_, err := pool.Exec(ctx, `
INSERT INTO team_membership (team_id, user_id, role)
VALUES ($1, $2, $3)
`, teamID, userID, role)
	if err != nil {
		t.Fatalf("seed membership: %v", err)
	}
}

func uniqueSuffix(t *testing.T, value string) string {
	t.Helper()

	slug := strings.ToLower(value)
	slug = strings.ReplaceAll(slug, " ", "-")
	slug = strings.ReplaceAll(slug, "/", "-")

	return slug + "-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")) + "-" + time.Now().Format("150405.000000000")
}
