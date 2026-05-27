package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// InsertRedeployRunParams captures all attribution columns needed for a
// deploy_run row (spec § 12 schema diff).
type InsertRedeployRunParams struct {
	TeamID            string
	AppID             string
	Ref               string
	CommitSHA         string
	TraceID           string
	Source            string
	ActorUserID       string // optional; nil/empty written as NULL
	WebhookDeliveryID string // optional; nil/empty written as NULL
}

// InsertRedeployRunResult is the durable handle returned to the trigger
// layer; same identity the GHA callback will quote in its payload.
type InsertRedeployRunResult struct {
	DeployRunID string
}

// InsertRedeployRun creates a new deploy_run row in the `queued` state with
// the source/actor/delivery_id attribution fields populated. Callers MUST
// have validated the app/team scope; this method assumes both are valid.
func (r *Repository) InsertRedeployRun(ctx context.Context, params InsertRedeployRunParams) (InsertRedeployRunResult, error) {
	parsedTeamID, err := parseUUID(params.TeamID)
	if err != nil {
		return InsertRedeployRunResult{}, fmt.Errorf("parse team id: %w", err)
	}
	parsedAppID, err := parseUUID(params.AppID)
	if err != nil {
		return InsertRedeployRunResult{}, fmt.Errorf("parse app id: %w", err)
	}

	var actorValue any
	if strings.TrimSpace(params.ActorUserID) != "" {
		parsedActorID, err := parseUUID(params.ActorUserID)
		if err != nil {
			return InsertRedeployRunResult{}, fmt.Errorf("parse actor id: %w", err)
		}
		actorValue = parsedActorID
	}

	var deliveryValue any
	if strings.TrimSpace(params.WebhookDeliveryID) != "" {
		deliveryValue = params.WebhookDeliveryID
	}

	source := strings.TrimSpace(params.Source)
	if source == "" {
		source = "user"
	}

	var runID pgtype.UUID
	if err := r.pool.QueryRow(ctx, `
INSERT INTO deploy_run (
    app_id, team_id, ref, commit_sha, status, trace_id,
    source, actor_user_id, webhook_delivery_id, started_at
)
VALUES ($1, $2, $3, $4, 'queued', $5, $6, $7, $8, now())
RETURNING id
`, parsedAppID, parsedTeamID, textOrNull(params.Ref), textOrNull(params.CommitSHA), textOrNull(params.TraceID), source, actorValue, deliveryValue).Scan(&runID); err != nil {
		return InsertRedeployRunResult{}, err
	}
	return InsertRedeployRunResult{DeployRunID: runID.String()}, nil
}

// HasInFlightDeployRun reports whether the app has a non-terminal deploy_run
// row (spec § 5.2 in-flight skip; § 6.3 in-flight check on user confirm).
// Non-terminal = any status not in (live, failed, canceled, rolled_back).
func (r *Repository) HasInFlightDeployRun(ctx context.Context, appID string) (bool, error) {
	parsedAppID, err := parseUUID(appID)
	if err != nil {
		return false, fmt.Errorf("parse app id: %w", err)
	}
	var exists bool
	err = r.pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM deploy_run
    WHERE app_id = $1
      AND status NOT IN ('live', 'failed', 'canceled', 'rolled_back')
)
`, parsedAppID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// FindLiveAppsByRepoAndBranch returns every live app belonging to `teamID`
// whose repo_url matches `repoURL` and whose repo_default_branch matches
// `branch`. Used by the webhook push handler (spec § 5.2). repo_url is
// normalised before comparison: trailing slash and ".git" suffix stripped.
func (r *Repository) FindLiveAppsByRepoAndBranch(ctx context.Context, teamID, repoURL, branch string) ([]App, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return nil, fmt.Errorf("parse team id: %w", err)
	}
	normalized := normalizeRepoURL(repoURL)
	branch = strings.TrimSpace(branch)

	rows, err := r.pool.Query(ctx, `
SELECT
  id,
  team_id,
  slug,
  name,
  repo_url,
  repo_default_branch,
  image_ref,
  builder,
  status,
  created_at,
  updated_at
FROM app
WHERE team_id = $1
  AND status = 'live'
  AND coalesce(repo_default_branch, '') = $3
  AND (
        coalesce(repo_url, '') = $2
     OR coalesce(repo_url, '') = $2 || '/'
     OR coalesce(repo_url, '') = $2 || '.git'
  )
ORDER BY slug
`, parsedTeamID, normalized, branch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]App, 0)
	for rows.Next() {
		var row struct {
			ID                pgtype.UUID
			TeamID            pgtype.UUID
			Slug              string
			Name              pgtype.Text
			RepoURL           pgtype.Text
			RepoDefaultBranch pgtype.Text
			ImageRef          pgtype.Text
			Builder           pgtype.Text
			Status            pgtype.Text
			CreatedAt         pgtype.Timestamptz
			UpdatedAt         pgtype.Timestamptz
		}
		if err := rows.Scan(
			&row.ID, &row.TeamID, &row.Slug, &row.Name,
			&row.RepoURL, &row.RepoDefaultBranch, &row.ImageRef,
			&row.Builder, &row.Status, &row.CreatedAt, &row.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, App{
			ID:                row.ID.String(),
			TeamID:            row.TeamID.String(),
			Slug:              row.Slug,
			Name:              textPtr(row.Name),
			RepoURL:           textPtr(row.RepoURL),
			RepoDefaultBranch: textPtr(row.RepoDefaultBranch),
			ImageRef:          textPtr(row.ImageRef),
			Builder:           textPtr(row.Builder),
			Status:            textPtr(row.Status),
			CreatedAt:         row.CreatedAt.Time,
			UpdatedAt:         row.UpdatedAt.Time,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// AppendWebhookAudit writes one audit_log row attributing a webhook-driven
// action. actor_user_id is left NULL so the row is identifiable as a system
// event (spec § 5.2 step 7, § 9 `audit_log` row).
func (r *Repository) AppendWebhookAudit(ctx context.Context, teamID, action string, args map[string]any, result map[string]any) error {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return fmt.Errorf("parse team id: %w", err)
	}
	argsJSON, err := marshalAuditPayload(args)
	if err != nil {
		return err
	}
	resultJSON, err := marshalAuditPayload(result)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
INSERT INTO audit_log (team_id, actor_user_id, subject_type, action, args, result)
VALUES ($1, NULL, 'github_webhook', $2, $3::jsonb, $4::jsonb)
`, parsedTeamID, action, argsJSON, resultJSON)
	return err
}

func normalizeRepoURL(raw string) string {
	out := strings.TrimSpace(raw)
	out = strings.TrimRight(out, "/")
	out = strings.TrimSuffix(out, ".git")
	return out
}

func textOrNull(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

