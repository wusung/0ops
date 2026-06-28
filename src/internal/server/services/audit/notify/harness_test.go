package notify

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbpkg "github.com/winshare/zeroops/internal/server/db"
)

// resolveTestDatabaseURL mirrors the db package test harness: TEST_DATABASE_URL
// wins; otherwise DATABASE_URL with the compose-internal host translated to the
// host-published 127.0.0.1:15432; empty → skip.
func resolveTestDatabaseURL() string {
	if u := os.Getenv("TEST_DATABASE_URL"); u != "" {
		return u
	}
	u := os.Getenv("DATABASE_URL")
	if u == "" {
		return ""
	}
	return strings.ReplaceAll(u, "@db:5432", "@127.0.0.1:15432")
}

func newTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	url := resolveTestDatabaseURL()
	if url == "" {
		t.Skip("TEST_DATABASE_URL / DATABASE_URL required for notify db tests")
	}
	ctx := context.Background()
	pool, err := dbpkg.NewPool(ctx, dbpkg.Config{URL: url})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

func uniqueSuffix(t *testing.T, v string) string {
	t.Helper()
	v = strings.ToLower(v)
	v = strings.ReplaceAll(v, " ", "-")
	v = strings.ReplaceAll(v, "/", "-")
	return v + "-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")) + "-" + time.Now().Format("150405.000000000")
}

func seedUser(ctx context.Context, t *testing.T, pool *pgxpool.Pool, login string) string {
	t.Helper()
	login = uniqueSuffix(t, login)
	var id pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO user_account (github_login) VALUES ($1) RETURNING id`, login).Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM user_account WHERE id = $1`, id) })
	return id.String()
}

func seedTeam(ctx context.Context, t *testing.T, pool *pgxpool.Pool, slug, name string) (string, string) {
	t.Helper()
	slug = uniqueSuffix(t, slug)
	name = uniqueSuffix(t, name)
	var id pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO team (slug, name) VALUES ($1, $2) RETURNING id`, slug, name).Scan(&id); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM team WHERE id = $1`, id) })
	return id.String(), slug
}

func seedMembership(ctx context.Context, t *testing.T, pool *pgxpool.Pool, teamID, userID, role string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO team_membership (team_id, user_id, role) VALUES ($1, $2, $3)`, teamID, userID, role); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
}

// seedSubscription inserts an active subscription with a fresh signing key and
// returns its id. The deliveries are cleaned up by team CASCADE / explicit
// delete in t.Cleanup.
func seedSubscription(ctx context.Context, t *testing.T, pool *pgxpool.Pool, teamID, url string, events []string) string {
	t.Helper()
	_, b64, err := GenerateSigningKey()
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	var id pgtype.UUID
	err = pool.QueryRow(ctx, `
INSERT INTO webhook_subscription (team_id, url, events, secret_ref, secret_material)
VALUES ($1, $2, $3, $4, $5)
RETURNING id
`, teamID, url, events, "inline:pending", b64).Scan(&id)
	if err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM webhook_delivery WHERE subscription_id = $1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM webhook_subscription WHERE id = $1`, id)
	})
	return id.String()
}

// newRepoWithEnqueuer builds a db.Repository with the notify enqueuer wired in.
func newRepoWithEnqueuer(t *testing.T, pool *pgxpool.Pool, obs MetricObserver) *dbpkg.Repository {
	t.Helper()
	repo := dbpkg.NewRepository(pool)
	repo.SetAuditEnqueuer(NewEnqueuer(obs))
	return repo
}

func timeFixed() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

func uuidNew() string { return uuid.NewString() }

func countDeliveries(ctx context.Context, t *testing.T, pool *pgxpool.Pool, subscriptionID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM webhook_delivery WHERE subscription_id = $1`, subscriptionID).Scan(&n); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	return n
}
