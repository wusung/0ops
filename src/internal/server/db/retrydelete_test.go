package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	dbpkg "github.com/winshare/zeroops/internal/server/db"
)

// seedAppRow inserts an app row in the given status and returns its id.
func seedAppRow(ctx context.Context, t *testing.T, pool *pgxpool.Pool, teamID, slug, status string) string {
	t.Helper()
	var id pgtype.UUID
	err := pool.QueryRow(ctx, `
INSERT INTO app (team_id, slug, repo_url, status)
VALUES ($1, $2, 'file:///workspace/x', $3)
RETURNING id
`, teamID, slug, status).Scan(&id)
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM app WHERE id = $1`, id) })
	return id.String()
}

func TestRetryStuckDeleteEnqueuesJobForDeletingApp(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, teamSlug := seedTeam(ctx, t, pool, "retry-team", "Retry Team")
	appID := seedAppRow(ctx, t, pool, teamID, "stuck-app", "deleting")

	jobID, err := repo.RetryStuckDelete(ctx, teamSlug, "stuck-app")
	if err != nil {
		t.Fatalf("RetryStuckDelete() error = %v", err)
	}
	if jobID == "" {
		t.Fatal("expected a job id")
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM reconciliation_job WHERE id = $1`, jobID) })

	var (
		kind      string
		status    string
		subjectID pgtype.UUID
	)
	err = pool.QueryRow(ctx, `
SELECT kind, status, subject_id FROM reconciliation_job WHERE id = $1
`, jobID).Scan(&kind, &status, &subjectID)
	if err != nil {
		t.Fatalf("load enqueued job: %v", err)
	}
	if kind != "cleanup_residue" {
		t.Errorf("kind = %q, want cleanup_residue", kind)
	}
	if status != "pending" {
		t.Errorf("status = %q, want pending", status)
	}
	if subjectID.String() != appID {
		t.Errorf("subject_id = %s, want %s", subjectID.String(), appID)
	}
}

func TestRetryStuckDeleteRejectsNonDeletingApp(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, teamSlug := seedTeam(ctx, t, pool, "retry-team", "Retry Team")
	seedAppRow(ctx, t, pool, teamID, "live-app", "live")

	_, err := repo.RetryStuckDelete(ctx, teamSlug, "live-app")
	if !errors.Is(err, dbpkg.ErrAppNotDeleting) {
		t.Fatalf("error = %v, want ErrAppNotDeleting", err)
	}
}

func TestRetryStuckDeleteAppNotFound(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	_, teamSlug := seedTeam(ctx, t, pool, "retry-team", "Retry Team")

	_, err := repo.RetryStuckDelete(ctx, teamSlug, "no-such-app")
	if !errors.Is(err, dbpkg.ErrAppNotFound) {
		t.Fatalf("error = %v, want ErrAppNotFound", err)
	}
}

func TestRetryStuckDeleteTeamNotFound(t *testing.T) {
	repo, ctx, _ := newTestRepository(t)

	_, err := repo.RetryStuckDelete(ctx, "no-such-team-xyz", "no-such-app")
	if !errors.Is(err, dbpkg.ErrTeamNotFound) {
		t.Fatalf("error = %v, want ErrTeamNotFound", err)
	}
}
