package db_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	dbpkg "github.com/winshare/zeroops/internal/server/db"
)

// TestApplyDeployCallbackWritesImageDigest proves the supply-chain-security
// spec § 6 end-to-end persistence: ApplyDeployCallback writes the canonical
// sha256 digest into the new deploy_run.image_digest column (migration 00016),
// and a nil digest on a later callback leaves the stored value untouched
// (COALESCE), so the SC3 digest anchor (hard rule #6) is durable.
func TestApplyDeployCallbackWritesImageDigest(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	_ = seedUser(ctx, t, pool, "alice")
	teamID, _ := seedTeam(ctx, t, pool, "acme", "Acme")

	var appID pgtype.UUID
	if err := pool.QueryRow(ctx, `
INSERT INTO app (team_id, slug, status)
VALUES ($1, $2, 'live')
RETURNING id`, teamID, uniqueSuffix(t, "web")).Scan(&appID); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM app WHERE id = $1`, appID) })

	var runID pgtype.UUID
	if err := pool.QueryRow(ctx, `
INSERT INTO deploy_run (app_id, team_id, status)
VALUES ($1, $2, 'building')
RETURNING id`, appID, teamID).Scan(&runID); err != nil {
		t.Fatalf("seed deploy_run: %v", err)
	}

	digest := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	trace := "trace-image-digest"
	if err := repo.ApplyDeployCallback(ctx, dbpkg.DeployCallbackParams{
		RunID:       runID.String(),
		Status:      "live",
		TraceID:     &trace,
		ImageDigest: &digest,
	}); err != nil {
		t.Fatalf("ApplyDeployCallback() error = %v", err)
	}

	var got pgtype.Text
	if err := pool.QueryRow(ctx, `SELECT image_digest FROM deploy_run WHERE id = $1`, runID).Scan(&got); err != nil {
		t.Fatalf("select image_digest: %v", err)
	}
	if !got.Valid || got.String != digest {
		t.Fatalf("image_digest = %q (valid=%v), want %q", got.String, got.Valid, digest)
	}

	// A subsequent callback without a digest must not clobber the anchor.
	if err := repo.ApplyDeployCallback(ctx, dbpkg.DeployCallbackParams{
		RunID:   runID.String(),
		Status:  "live",
		TraceID: &trace,
	}); err != nil {
		t.Fatalf("ApplyDeployCallback() second call error = %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT image_digest FROM deploy_run WHERE id = $1`, runID).Scan(&got); err != nil {
		t.Fatalf("re-select image_digest: %v", err)
	}
	if !got.Valid || got.String != digest {
		t.Fatalf("image_digest after nil callback = %q (valid=%v), want %q preserved", got.String, got.Valid, digest)
	}
}
