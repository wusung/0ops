package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// AppDomainBinding describes a domain row attached to an app for the
// delete-app saga: hostname plus the Cloudflare custom-hostname id that
// must be released when kind = 'extra' (spec § 5.2 R2).
type AppDomainBinding struct {
	ID            string
	Hostname      string
	Kind          string
	CFHostnameID  *string
	CFDNSRecordID *string
}

// InFlightDeployRun captures the subset of deploy_run fields needed to
// cancel an in-flight GHA workflow during delete_app reversible execute
// (spec § 5.2 R1).
type InFlightDeployRun struct {
	ID            string
	WorkflowRunID *int64
	Status        string
}

// ReconciliationJobInsert carries the payload for a new reconciliation_job
// row scheduled by the delete-app saga (spec § 6.2 cleanup_residue).
type ReconciliationJobInsert struct {
	TeamID      string
	SubjectType string
	SubjectID   string
	Kind        string
	Payload     map[string]any
}

// AuditLogInsert captures one row to be appended to audit_log. delete_app
// rows are permanent (spec § 13 hard rule #10).
type AuditLogInsert struct {
	TeamID      string
	ActorUserID *string
	SubjectType string
	SubjectID   *string
	Action      string
	Args        map[string]any
	Result      map[string]any
	PreviewID   *string
	TraceID     *string
}

// UpdateAppStatus transitions the app row to a new lifecycle state, e.g.
// 'live' → 'deleting' or 'deleting' → 'delete_compensated' (spec § 5.2).
func (r *Repository) UpdateAppStatus(ctx context.Context, appID, status string) error {
	parsedAppID, err := parseUUID(appID)
	if err != nil {
		return fmt.Errorf("parse app id: %w", err)
	}
	tag, err := r.pool.Exec(ctx, `
UPDATE app
SET status = $2, updated_at = now()
WHERE id = $1
`, parsedAppID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ListInFlightDeployRuns returns deploy_run rows for an app whose status is
// non-terminal — i.e. the set delete_app must cancel before pruning. The
// shape mirrors HasInFlightDeployRun's predicate so the two helpers stay
// in sync.
func (r *Repository) ListInFlightDeployRuns(ctx context.Context, appID string) ([]InFlightDeployRun, error) {
	parsedAppID, err := parseUUID(appID)
	if err != nil {
		return nil, fmt.Errorf("parse app id: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
SELECT id, workflow_run_id, status
FROM deploy_run
WHERE app_id = $1
  AND status NOT IN ('live', 'failed', 'canceled', 'rolled_back')
ORDER BY started_at NULLS LAST
`, parsedAppID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]InFlightDeployRun, 0)
	for rows.Next() {
		var (
			id         pgtype.UUID
			workflowID pgtype.Int8
			status     string
		)
		if err := rows.Scan(&id, &workflowID, &status); err != nil {
			return nil, err
		}
		dr := InFlightDeployRun{ID: id.String(), Status: status}
		if workflowID.Valid {
			v := workflowID.Int64
			dr.WorkflowRunID = &v
		}
		items = append(items, dr)
	}
	return items, rows.Err()
}

// CancelDeployRun marks a single deploy_run as canceled with a reason
// recorded in events. Used by the delete-app saga to terminate non-terminal
// runs (spec § 5.2 R1) before manifest prune.
func (r *Repository) CancelDeployRun(ctx context.Context, runID, reason string) error {
	parsedRunID, err := parseUUID(runID)
	if err != nil {
		return fmt.Errorf("parse run id: %w", err)
	}
	event := map[string]any{
		"event":       "canceled",
		"reason":      reason,
		"occurred_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	eventJSON, err := json.Marshal([]map[string]any{event})
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx, `
UPDATE deploy_run
SET status = 'canceled',
    finished_at = now(),
    events = events || $2::jsonb
WHERE id = $1
  AND status NOT IN ('live', 'failed', 'canceled', 'rolled_back')
`, parsedRunID, eventJSON)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ListAppDomainBindings returns every domain_binding row for an app along
// with the Cloudflare identifiers required to release custom hostnames
// (spec § 5.2 R2).
func (r *Repository) ListAppDomainBindings(ctx context.Context, appID string) ([]AppDomainBinding, error) {
	parsedAppID, err := parseUUID(appID)
	if err != nil {
		return nil, fmt.Errorf("parse app id: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
SELECT id, hostname, kind, cf_hostname_id, cf_dns_record_id
FROM domain_binding
WHERE app_id = $1
ORDER BY kind, hostname
`, parsedAppID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]AppDomainBinding, 0)
	for rows.Next() {
		var (
			id          pgtype.UUID
			hostname    string
			kind        pgtype.Text
			cfHost      pgtype.Text
			cfDNSRecord pgtype.Text
		)
		if err := rows.Scan(&id, &hostname, &kind, &cfHost, &cfDNSRecord); err != nil {
			return nil, err
		}
		b := AppDomainBinding{ID: id.String(), Hostname: hostname}
		if kind.Valid {
			b.Kind = kind.String
		}
		if cfHost.Valid {
			v := cfHost.String
			b.CFHostnameID = &v
		}
		if cfDNSRecord.Valid {
			v := cfDNSRecord.String
			b.CFDNSRecordID = &v
		}
		items = append(items, b)
	}
	return items, rows.Err()
}

// DeleteAppDomainBindings removes every domain_binding row for an app
// (spec § 5.2 R3). Should run after Cloudflare custom hostnames are
// released so a retry can re-read which rows still need cleanup.
func (r *Repository) DeleteAppDomainBindings(ctx context.Context, appID string) error {
	parsedAppID, err := parseUUID(appID)
	if err != nil {
		return fmt.Errorf("parse app id: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
DELETE FROM domain_binding WHERE app_id = $1
`, parsedAppID)
	return err
}

// EnqueueReconciliationJob inserts a reconciliation_job row, used by the
// delete-app saga to schedule cleanup_residue handling once reversible
// side effects have committed (spec § 6.2).
func (r *Repository) EnqueueReconciliationJob(ctx context.Context, in ReconciliationJobInsert) (string, error) {
	parsedTeamID, err := parseUUID(in.TeamID)
	if err != nil {
		return "", fmt.Errorf("parse team id: %w", err)
	}
	parsedSubjectID, err := parseUUID(in.SubjectID)
	if err != nil {
		return "", fmt.Errorf("parse subject id: %w", err)
	}
	payloadJSON, err := marshalAuditPayload(in.Payload)
	if err != nil {
		return "", err
	}
	var jobID pgtype.UUID
	if err := r.pool.QueryRow(ctx, `
INSERT INTO reconciliation_job (team_id, subject_type, subject_id, kind, payload, next_attempt_at)
VALUES ($1, $2, $3, $4, $5::jsonb, now())
RETURNING id
`, parsedTeamID, in.SubjectType, parsedSubjectID, in.Kind, payloadJSON).Scan(&jobID); err != nil {
		return "", err
	}
	return jobID.String(), nil
}

// MarkReconciliationJobAttempt records one attempt against a job. nextAt
// schedules the retry; lastErr captures the error string. If completed is
// true the row is marked terminal via completed_at.
func (r *Repository) MarkReconciliationJobAttempt(ctx context.Context, jobID, lastErr string, nextAt *time.Time, completed bool) error {
	parsedJobID, err := parseUUID(jobID)
	if err != nil {
		return fmt.Errorf("parse job id: %w", err)
	}
	var nextAttempt any
	if nextAt != nil {
		nextAttempt = *nextAt
	}
	var completedAt any
	if completed {
		completedAt = time.Now().UTC()
	}
	_, err = r.pool.Exec(ctx, `
UPDATE reconciliation_job
SET attempts        = attempts + 1,
    next_attempt_at = $2,
    last_error      = NULLIF($3, ''),
    completed_at    = COALESCE($4::timestamptz, completed_at)
WHERE id = $1
`, parsedJobID, nextAttempt, lastErr, completedAt)
	return err
}

// AppendAuditLog inserts a single audit_log row. delete_app uses this for
// preview and confirm; rows are permanent (spec § 13 hard rule #10) and
// retained beyond the standard 13 month window (enforced via policy, not
// schema).
func (r *Repository) AppendAuditLog(ctx context.Context, in AuditLogInsert) error {
	parsedTeamID, err := parseUUID(in.TeamID)
	if err != nil {
		return fmt.Errorf("parse team id: %w", err)
	}
	var actor any
	if in.ActorUserID != nil {
		parsed, err := parseUUID(*in.ActorUserID)
		if err != nil {
			return fmt.Errorf("parse actor id: %w", err)
		}
		actor = parsed
	}
	var subject any
	if in.SubjectID != nil {
		parsed, err := parseUUID(*in.SubjectID)
		if err != nil {
			return fmt.Errorf("parse subject id: %w", err)
		}
		subject = parsed
	}
	argsJSON, err := marshalAuditPayload(in.Args)
	if err != nil {
		return err
	}
	resultJSON, err := marshalAuditPayload(in.Result)
	if err != nil {
		return err
	}
	var previewID any
	if in.PreviewID != nil {
		parsed, err := parseUUID(*in.PreviewID)
		if err != nil {
			return fmt.Errorf("parse preview id: %w", err)
		}
		previewID = parsed
	}
	var traceID any
	if in.TraceID != nil && *in.TraceID != "" {
		traceID = *in.TraceID
	}
	_, err = r.pool.Exec(ctx, `
INSERT INTO audit_log (team_id, actor_user_id, subject_type, subject_id, action, args, result, preview_id, trace_id)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9)
`, parsedTeamID, actor, in.SubjectType, subject, in.Action, argsJSON, resultJSON, previewID, traceID)
	return err
}
