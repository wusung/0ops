package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ReconciliationJobRow is the in-Go view of a reconciliation_job row used
// by the worker loop in internal/server/services/reconciler.
type ReconciliationJobRow struct {
	ID            string
	TeamID        string
	SubjectType   string
	SubjectID     string
	Kind          string
	Attempts      int
	NextAttemptAt *time.Time
	Payload       map[string]any
	LastError     *string
	Status        string
	TraceID       *string
	CreatedAt     time.Time
	CompletedAt   *time.Time
}

// StuckDeployRun is the subset of deploy_run needed by the scanners that
// poll GitHub Actions and ArgoCD for runs that overshot the spec § 8
// thresholds.
type StuckDeployRun struct {
	ID             string
	AppID          string
	TeamID         string
	AppSlug        string
	TeamSlug       string
	Ref            *string
	CommitSHA      *string
	WorkflowRunID  *int64
	TraceID        *string
	Status         string
	StartedAt      *time.Time
}

// ListPendingReconciliationJobs returns up to limit pending jobs whose
// next_attempt_at has elapsed, oldest first. The hot index defined in
// migration 00008 keeps this scan cheap regardless of completed-row
// volume.
func (r *Repository) ListPendingReconciliationJobs(ctx context.Context, now time.Time, limit int) ([]ReconciliationJobRow, error) {
	if limit <= 0 {
		limit = 32
	}
	rows, err := r.pool.Query(ctx, `
SELECT id, team_id, subject_type, subject_id, kind, attempts,
       next_attempt_at, payload, last_error, status, trace_id,
       created_at, completed_at
FROM reconciliation_job
WHERE status = 'pending'
  AND (next_attempt_at IS NULL OR next_attempt_at <= $1)
ORDER BY COALESCE(next_attempt_at, created_at)
LIMIT $2
`, now.UTC(), int32(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReconciliationJobs(rows)
}

// CountPendingReconciliationJobsByKind returns pending job counts grouped
// by kind. The reconciler runner uses this to drive the
// zeroops_reconciliation_jobs_pending gauge once per tick.
func (r *Repository) CountPendingReconciliationJobsByKind(ctx context.Context) (map[string]int, error) {
	rows, err := r.pool.Query(ctx, `
SELECT kind, COUNT(*)
FROM reconciliation_job
WHERE status = 'pending'
GROUP BY kind
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var (
			kind  string
			count int64
		)
		if err := rows.Scan(&kind, &count); err != nil {
			return nil, err
		}
		out[kind] = int(count)
	}
	return out, rows.Err()
}

// ClaimReconciliationJob atomically transitions a pending job into
// in_progress. It returns (false, nil) when the row no longer matches
// (already claimed by a competing worker / completed), and the caller
// should move on to the next candidate without retrying.
func (r *Repository) ClaimReconciliationJob(ctx context.Context, jobID string) (bool, error) {
	parsedJobID, err := parseUUID(jobID)
	if err != nil {
		return false, fmt.Errorf("parse job id: %w", err)
	}
	tag, err := r.pool.Exec(ctx, `
UPDATE reconciliation_job
SET status = 'in_progress'
WHERE id = $1
  AND status = 'pending'
`, parsedJobID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// CompleteReconciliationJob flips a row to status='completed' and stamps
// completed_at. Idempotent: an already-completed row is left alone.
func (r *Repository) CompleteReconciliationJob(ctx context.Context, jobID string) error {
	parsedJobID, err := parseUUID(jobID)
	if err != nil {
		return fmt.Errorf("parse job id: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
UPDATE reconciliation_job
SET status = 'completed',
    completed_at = COALESCE(completed_at, now())
WHERE id = $1
  AND status <> 'failed_permanently'
`, parsedJobID)
	return err
}

// RescheduleReconciliationJob records one failed attempt, captures the
// error text, and schedules the next retry. The caller picks the
// exponential backoff per spec § 16 #4.
func (r *Repository) RescheduleReconciliationJob(ctx context.Context, jobID string, lastErr string, nextAt time.Time) error {
	parsedJobID, err := parseUUID(jobID)
	if err != nil {
		return fmt.Errorf("parse job id: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
UPDATE reconciliation_job
SET attempts = attempts + 1,
    next_attempt_at = $2,
    last_error = NULLIF($3, ''),
    status = 'pending'
WHERE id = $1
`, parsedJobID, nextAt.UTC(), lastErr)
	return err
}

// FailReconciliationJobPermanently marks the job terminal with an
// error string preserved for incident creation downstream. completed_at
// is stamped so dashboards can render TTR cleanly.
func (r *Repository) FailReconciliationJobPermanently(ctx context.Context, jobID, lastErr string) error {
	parsedJobID, err := parseUUID(jobID)
	if err != nil {
		return fmt.Errorf("parse job id: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
UPDATE reconciliation_job
SET status = 'failed_permanently',
    last_error = NULLIF($2, ''),
    completed_at = COALESCE(completed_at, now())
WHERE id = $1
`, parsedJobID, lastErr)
	return err
}

// ListStuckBuildingDeployRuns returns deploy_run rows that have been
// in 'building' for longer than the spec § 8.1 threshold. The scanner
// joins app + team to enrich the workflow_run pull with the deterministic
// slugs the dispatcher needs.
func (r *Repository) ListStuckBuildingDeployRuns(ctx context.Context, threshold time.Time, limit int) ([]StuckDeployRun, error) {
	return r.listStuckDeployRuns(ctx, "building", threshold, limit)
}

// ListStuckSyncingDeployRuns mirrors the building scanner for the
// syncing > 15 min path (spec § 8.2).
func (r *Repository) ListStuckSyncingDeployRuns(ctx context.Context, threshold time.Time, limit int) ([]StuckDeployRun, error) {
	return r.listStuckDeployRuns(ctx, "syncing", threshold, limit)
}

func (r *Repository) listStuckDeployRuns(ctx context.Context, status string, threshold time.Time, limit int) ([]StuckDeployRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `
SELECT dr.id, dr.app_id, dr.team_id, a.slug, t.slug,
       dr.ref, dr.commit_sha, dr.workflow_run_id, dr.trace_id,
       dr.status, dr.started_at
FROM deploy_run dr
JOIN app a ON a.id = dr.app_id
JOIN team t ON t.id = dr.team_id
WHERE dr.status = $1
  AND dr.started_at IS NOT NULL
  AND dr.started_at < $2
ORDER BY dr.started_at
LIMIT $3
`, status, threshold.UTC(), int32(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]StuckDeployRun, 0)
	for rows.Next() {
		var (
			id, appID, teamID                pgtype.UUID
			appSlug, teamSlug, currentStatus string
			ref, commitSHA, traceID          pgtype.Text
			workflowRunID                    pgtype.Int8
			startedAt                        pgtype.Timestamptz
		)
		if err := rows.Scan(&id, &appID, &teamID, &appSlug, &teamSlug,
			&ref, &commitSHA, &workflowRunID, &traceID,
			&currentStatus, &startedAt); err != nil {
			return nil, err
		}
		row := StuckDeployRun{
			ID:       id.String(),
			AppID:    appID.String(),
			TeamID:   teamID.String(),
			AppSlug:  appSlug,
			TeamSlug: teamSlug,
			Status:   currentStatus,
		}
		if ref.Valid {
			v := ref.String
			row.Ref = &v
		}
		if commitSHA.Valid {
			v := commitSHA.String
			row.CommitSHA = &v
		}
		if traceID.Valid {
			v := traceID.String
			row.TraceID = &v
		}
		if workflowRunID.Valid {
			v := workflowRunID.Int64
			row.WorkflowRunID = &v
		}
		if startedAt.Valid {
			v := startedAt.Time
			row.StartedAt = &v
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// DeployRunTransitionParams captures the wire-level inputs for a
// deploy_run state transition recorded by the reconciler. The
// statemachine package validates the From → To pair before this method
// is called (spec § 6.4); the repository simply mirrors the change.
type DeployRunTransitionParams struct {
	RunID                 string
	FromStatus            string
	ToStatus              string
	FailureClassification *string
	ErrorSummary          *string
	TraceID               *string
	EventActor            string
	EventReason           string
}

// ErrDeployRunStateConflict is returned when the optimistic CAS on
// status fails — another writer (callback handler, redeploy trigger)
// raced the reconciler. The caller treats this as a no-op tick.
var ErrDeployRunStateConflict = errors.New("db: deploy_run state mismatch")

// TransitionDeployRun applies a single deploy_run state change with an
// optimistic compare-and-set on the current status and appends a
// transition event to deploy_run.events. The DB-level CHECK constraint
// guards the failure_classification invariant (spec § 6.3 + § 16 #1).
func (r *Repository) TransitionDeployRun(ctx context.Context, params DeployRunTransitionParams) error {
	parsedRunID, err := parseUUID(params.RunID)
	if err != nil {
		return fmt.Errorf("parse run id: %w", err)
	}
	event := map[string]any{
		"at":     time.Now().UTC().Format(time.RFC3339Nano),
		"from":   params.FromStatus,
		"to":     params.ToStatus,
		"actor":  params.EventActor,
		"reason": params.EventReason,
	}
	eventJSON, err := json.Marshal([]map[string]any{event})
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	tag, err := r.pool.Exec(ctx, `
UPDATE deploy_run
SET status = $3,
    failure_classification = COALESCE($4, failure_classification),
    error_summary = COALESCE($5, error_summary),
    trace_id = COALESCE($6, trace_id),
    events = events || $7::jsonb,
    finished_at = CASE
      WHEN $3 IN ('live', 'failed', 'canceled', 'rolled_back', 'failed_permanently') THEN now()
      ELSE finished_at
    END
WHERE id = $1
  AND status = $2
`, parsedRunID, params.FromStatus, params.ToStatus,
		textFromPtr(params.FailureClassification),
		textFromPtr(params.ErrorSummary),
		textFromPtr(params.TraceID),
		eventJSON)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrDeployRunStateConflict
	}
	return nil
}

func scanReconciliationJobs(rows pgx.Rows) ([]ReconciliationJobRow, error) {
	items := make([]ReconciliationJobRow, 0)
	for rows.Next() {
		var (
			id, teamID, subjectID                  pgtype.UUID
			subjectType, kind, status              string
			attempts                               int32
			nextAttemptAt, createdAt, completedAt  pgtype.Timestamptz
			payload                                []byte
			lastError, traceID                     pgtype.Text
		)
		if err := rows.Scan(&id, &teamID, &subjectType, &subjectID, &kind,
			&attempts, &nextAttemptAt, &payload, &lastError, &status, &traceID,
			&createdAt, &completedAt); err != nil {
			return nil, err
		}
		row := ReconciliationJobRow{
			ID:          id.String(),
			TeamID:      teamID.String(),
			SubjectType: subjectType,
			SubjectID:   subjectID.String(),
			Kind:        kind,
			Attempts:    int(attempts),
			Status:      status,
		}
		if nextAttemptAt.Valid {
			v := nextAttemptAt.Time
			row.NextAttemptAt = &v
		}
		if createdAt.Valid {
			row.CreatedAt = createdAt.Time
		}
		if completedAt.Valid {
			v := completedAt.Time
			row.CompletedAt = &v
		}
		if lastError.Valid {
			v := lastError.String
			row.LastError = &v
		}
		if traceID.Valid {
			v := traceID.String
			row.TraceID = &v
		}
		if len(payload) > 0 {
			var parsed map[string]any
			if err := json.Unmarshal(payload, &parsed); err == nil {
				row.Payload = parsed
			}
		}
		items = append(items, row)
	}
	return items, rows.Err()
}
