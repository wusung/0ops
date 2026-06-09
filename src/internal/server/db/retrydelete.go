package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrAppNotFound indicates the (team, app) pair did not resolve to a row.
var ErrAppNotFound = errors.New("db: app not found")

// ErrAppNotDeleting indicates a retry-delete targeted an app that is not in
// the 'deleting' state — only a stuck delete can be re-driven.
var ErrAppNotDeleting = errors.New("db: app is not in deleting state")

// RetryStuckDelete re-enqueues a cleanup_residue reconciliation_job for an app
// stuck in 'deleting'. It is the operator recovery path for the spec § 6.2 case
// where the original cleanup_residue job exhausted its retries and went
// failed_permanently: the runner only scans pending jobs, so a terminal job
// never converges on its own and the app row lingers in 'deleting'.
//
// Guards: team + app must exist and the app must currently be 'deleting'. The
// freshly enqueued job (attempts=0, next_attempt_at=now) is claimed on the next
// job_queue tick. HandleResidue is idempotent, so a duplicate enqueue races
// harmlessly — whichever job wins hard-deletes the row; the loser finds it gone
// and completes. The kind literal matches the producer in deleteapp/execute.go
// and the registered consumer deleteapp.ResidueJobKind.
func (r *Repository) RetryStuckDelete(ctx context.Context, teamSlug, appSlug string) (string, error) {
	team, err := r.ResolveTeamBySlug(ctx, teamSlug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrTeamNotFound
		}
		return "", fmt.Errorf("resolve team: %w", err)
	}

	app, err := r.GetTeamAppBySlug(ctx, team.ID, appSlug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrAppNotFound
		}
		return "", fmt.Errorf("load app: %w", err)
	}
	if app.Status == nil || *app.Status != "deleting" {
		return "", ErrAppNotDeleting
	}

	jobID, err := r.EnqueueReconciliationJob(ctx, ReconciliationJobInsert{
		TeamID:      team.ID,
		SubjectType: "app",
		SubjectID:   app.ID,
		Kind:        "cleanup_residue",
		Payload: map[string]any{
			"app_id":    app.ID,
			"app_slug":  app.Slug,
			"team_slug": team.Slug,
			"retried":   true,
		},
	})
	if err != nil {
		return "", fmt.Errorf("enqueue cleanup_residue: %w", err)
	}
	return jobID, nil
}
