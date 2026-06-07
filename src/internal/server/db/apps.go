package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	sqlcgen "github.com/winshare/zeroops/internal/server/db/sqlc"
	sharedtoken "github.com/winshare/zeroops/internal/shared/token"
)

// App describes a team app record.
type App struct {
	ID                string
	TeamID            string
	Slug              string
	Name              *string
	RepoURL           *string
	RepoDefaultBranch *string
	ImageRef          *string
	Builder           *string
	Status            *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// AppCreateParams carries fields required for create_app confirmation.
type AppCreateParams struct {
	TeamID      string
	ActorUserID string
	Slug        string
	RepoURL     string
	Ref         string
	Builder     *string
	TraceID     string
}

// AppCreateResult is the durable result returned from create_app confirmation.
type AppCreateResult struct {
	AppID       string
	AppSlug     string
	DeployRunID string
}

// DeployCallbackParams carries callback fields for deploy_run status transition.
type DeployCallbackParams struct {
	RunID                 string
	Status                string
	TraceID               *string
	ErrorSummary          *string
	FailureClassification *string
	Event                 json.RawMessage
}

// CliToken describes an auth token record.
type CliToken struct {
	ID          string
	OwnerUserID string
	TeamID      string
	Kind        string
	Name        string
	TokenHash   string
	Scopes      []string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
}

// GetTeamMembershipRole returns the stored team role for a user.
func (r *Repository) GetTeamMembershipRole(ctx context.Context, teamID string, userID string) (string, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return "", fmt.Errorf("parse team id: %w", err)
	}

	parsedUserID, err := parseUUID(userID)
	if err != nil {
		return "", fmt.Errorf("parse user id: %w", err)
	}

	return r.queries.GetTeamMembershipRole(ctx, sqlcgen.GetTeamMembershipRoleParams{
		TeamID: parsedTeamID,
		UserID: parsedUserID,
	})
}

// ListTeamApps returns apps for a team ordered by app id.
func (r *Repository) ListTeamApps(ctx context.Context, teamID string, limit int32, afterID *string) ([]App, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return nil, fmt.Errorf("parse team id: %w", err)
	}

	var query string
	args := []any{parsedTeamID}
	if afterID != nil && *afterID != "" {
		query = `
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
  AND id > $2::uuid
ORDER BY id
LIMIT $3
`
		args = append(args, *afterID, limit)
	} else {
		query = `
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
ORDER BY id
LIMIT $2
`
		args = append(args, limit)
	}

	resultRows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer resultRows.Close()

	items := make([]App, 0)
	for resultRows.Next() {
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
		if err := resultRows.Scan(
			&row.ID,
			&row.TeamID,
			&row.Slug,
			&row.Name,
			&row.RepoURL,
			&row.RepoDefaultBranch,
			&row.ImageRef,
			&row.Builder,
			&row.Status,
			&row.CreatedAt,
			&row.UpdatedAt,
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
	if err := resultRows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

// GetTeamAppBySlug returns a single team-scoped app by slug.
func (r *Repository) GetTeamAppBySlug(ctx context.Context, teamID string, slug string) (App, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return App{}, fmt.Errorf("parse team id: %w", err)
	}

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

	err = r.pool.QueryRow(ctx, `
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
  AND slug = $2
`, parsedTeamID, slug).Scan(
		&row.ID,
		&row.TeamID,
		&row.Slug,
		&row.Name,
		&row.RepoURL,
		&row.RepoDefaultBranch,
		&row.ImageRef,
		&row.Builder,
		&row.Status,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return App{}, err
		}
		return App{}, fmt.Errorf("query app by slug: %w", err)
	}

	return App{
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
	}, nil
}

// GetAppRepoURLByTeamAndAppSlug fetches only repo_url for an app, identified
// by team slug + app slug. Used by localbuild.RoutingDispatcher (ADR-0012
// § 3.2) to route between GitHub and Local dispatchers without expanding the
// workflowdispatch.ClientPayload schema.
func (r *Repository) GetAppRepoURLByTeamAndAppSlug(ctx context.Context, teamSlug, appSlug string) (string, error) {
	var url pgtype.Text
	err := r.pool.QueryRow(ctx, `
SELECT a.repo_url
  FROM app a
  JOIN team t ON t.id = a.team_id
 WHERE t.slug = $1 AND a.slug = $2
 LIMIT 1
`, teamSlug, appSlug).Scan(&url)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", err
		}
		return "", fmt.Errorf("query repo_url by team+app slug: %w", err)
	}
	if !url.Valid {
		return "", nil
	}
	return url.String, nil
}

// CreateApp persists app/domain/deploy_run in one transaction for create_app.
func (r *Repository) CreateApp(ctx context.Context, params AppCreateParams) (AppCreateResult, error) {
	parsedTeamID, err := parseUUID(params.TeamID)
	if err != nil {
		return AppCreateResult{}, fmt.Errorf("parse team id: %w", err)
	}
	parsedActorID, err := parseUUID(params.ActorUserID)
	if err != nil {
		return AppCreateResult{}, fmt.Errorf("parse actor id: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return AppCreateResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var appID pgtype.UUID
	if err := tx.QueryRow(ctx, `
INSERT INTO app (team_id, slug, repo_url, repo_default_branch, builder, created_by, status)
VALUES ($1, $2, $3, $4, $5, $6, 'queued')
RETURNING id
`, parsedTeamID, params.Slug, params.RepoURL, params.Ref, textFromPtr(params.Builder), parsedActorID).Scan(&appID); err != nil {
		return AppCreateResult{}, err
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO domain_binding (app_id, team_id, hostname, kind, verified, verified_at)
VALUES ($1, $2, $3, 'primary', true, now())
`, appID, parsedTeamID, fmt.Sprintf("%s.winshare.tw", params.Slug)); err != nil {
		return AppCreateResult{}, err
	}

	var deployRunID pgtype.UUID
	if err := tx.QueryRow(ctx, `
INSERT INTO deploy_run (app_id, team_id, ref, status, trace_id, started_at)
VALUES ($1, $2, $3, 'queued', $4, now())
RETURNING id
`, appID, parsedTeamID, params.Ref, params.TraceID).Scan(&deployRunID); err != nil {
		return AppCreateResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return AppCreateResult{}, err
	}

	return AppCreateResult{
		AppID:       appID.String(),
		AppSlug:     params.Slug,
		DeployRunID: deployRunID.String(),
	}, nil
}

// DeleteAppByID removes an app and its cascaded resources by id.
func (r *Repository) DeleteAppByID(ctx context.Context, appID string) error {
	parsedAppID, err := parseUUID(appID)
	if err != nil {
		return fmt.Errorf("parse app id: %w", err)
	}
	_, err = r.pool.Exec(ctx, `DELETE FROM app WHERE id = $1`, parsedAppID)
	return err
}

// RegisterWebhookDelivery inserts webhook delivery id for dedup. Returns false on duplicate.
func (r *Repository) RegisterWebhookDelivery(ctx context.Context, provider, deliveryID string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
INSERT INTO webhook_dedup (provider, delivery_id)
VALUES ($1, $2)
ON CONFLICT (provider, delivery_id) DO NOTHING
`, provider, deliveryID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// GetDeployRunTeamID returns the team_id for a deploy_run, used by the
// callback handler to populate audit_log.team_id without a join.
func (r *Repository) GetDeployRunTeamID(ctx context.Context, runID string) (string, error) {
	parsedRunID, err := parseUUID(runID)
	if err != nil {
		return "", fmt.Errorf("parse run id: %w", err)
	}
	var teamID pgtype.UUID
	if err := r.pool.QueryRow(ctx,
		`SELECT team_id FROM deploy_run WHERE id = $1`,
		parsedRunID).Scan(&teamID); err != nil {
		return "", err
	}
	return teamID.String(), nil
}

// ApplyDeployCallback updates a deploy_run from external callback state.
func (r *Repository) ApplyDeployCallback(ctx context.Context, params DeployCallbackParams) error {
	parsedRunID, err := parseUUID(params.RunID)
	if err != nil {
		return fmt.Errorf("parse run id: %w", err)
	}
	tag, err := r.pool.Exec(ctx, `
UPDATE deploy_run
SET status = $2,
    trace_id = COALESCE($3, trace_id),
    error_summary = $4,
    failure_classification = $5,
    events = CASE
      WHEN $6::jsonb IS NULL THEN events
      ELSE events || $6::jsonb
    END,
    finished_at = CASE
      WHEN $2 IN ('live', 'failed', 'canceled', 'rolled_back') THEN now()
      ELSE finished_at
    END
WHERE id = $1
`, parsedRunID, params.Status, textFromPtr(params.TraceID), textFromPtr(params.ErrorSummary), textFromPtr(params.FailureClassification), params.Event)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ListDomainsByAppSlug returns domains for a team app.
func (r *Repository) ListDomainsByAppSlug(ctx context.Context, teamID string, appSlug string) ([]DomainBinding, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return nil, fmt.Errorf("parse team id: %w", err)
	}

	rows, err := r.pool.Query(ctx, `
SELECT
  d.id,
  d.team_id,
  d.app_id,
  a.slug,
  d.hostname,
  d.kind,
  d.verified,
  d.expires_at,
  d.verified_at
FROM domain_binding d
JOIN app a ON a.id = d.app_id
WHERE d.team_id = $1
  AND a.slug = $2
ORDER BY d.hostname
`, parsedTeamID, appSlug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]DomainBinding, 0)
	for rows.Next() {
		var row struct {
			ID         pgtype.UUID
			TeamID     pgtype.UUID
			AppID      pgtype.UUID
			AppSlug    string
			Hostname   string
			Kind       pgtype.Text
			Verified   bool
			ExpiresAt  pgtype.Timestamptz
			VerifiedAt pgtype.Timestamptz
		}
		if err := rows.Scan(
			&row.ID,
			&row.TeamID,
			&row.AppID,
			&row.AppSlug,
			&row.Hostname,
			&row.Kind,
			&row.Verified,
			&row.ExpiresAt,
			&row.VerifiedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, DomainBinding{
			ID:         row.ID.String(),
			TeamID:     row.TeamID.String(),
			AppID:      row.AppID.String(),
			AppSlug:    row.AppSlug,
			Hostname:   row.Hostname,
			Kind:       textPtr(row.Kind),
			Verified:   row.Verified,
			ExpiresAt:  timestamptzPtr(row.ExpiresAt),
			VerifiedAt: timestamptzPtr(row.VerifiedAt),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

// GetLatestDeployByAppSlug returns latest deploy status for a team app.
func (r *Repository) GetLatestDeployByAppSlug(ctx context.Context, teamID string, appSlug string) (DeployRun, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return DeployRun{}, fmt.Errorf("parse team id: %w", err)
	}

	var row struct {
		ID           pgtype.UUID
		TeamID       pgtype.UUID
		AppID        pgtype.UUID
		AppSlug      string
		Status       string
		TraceID      pgtype.Text
		CommitSHA    pgtype.Text
		Ref          pgtype.Text
		FailureClass pgtype.Text
		ErrorSummary pgtype.Text
		StartedAt    pgtype.Timestamptz
		FinishedAt   pgtype.Timestamptz
		Events       []byte
	}
	err = r.pool.QueryRow(ctx, `
SELECT
  dr.id,
  dr.team_id,
  dr.app_id,
  a.slug,
  dr.status,
  dr.trace_id,
  dr.commit_sha,
  dr.ref,
  dr.failure_classification,
  dr.error_summary,
  dr.started_at,
  dr.finished_at,
  dr.events
FROM deploy_run dr
JOIN app a ON a.id = dr.app_id
WHERE dr.team_id = $1
  AND a.slug = $2
ORDER BY dr.started_at DESC NULLS LAST, dr.id DESC
LIMIT 1
`, parsedTeamID, appSlug).Scan(
		&row.ID,
		&row.TeamID,
		&row.AppID,
		&row.AppSlug,
		&row.Status,
		&row.TraceID,
		&row.CommitSHA,
		&row.Ref,
		&row.FailureClass,
		&row.ErrorSummary,
		&row.StartedAt,
		&row.FinishedAt,
		&row.Events,
	)
	if err != nil {
		return DeployRun{}, err
	}

	out := DeployRun{
		ID:                    row.ID.String(),
		TeamID:                row.TeamID.String(),
		AppID:                 row.AppID.String(),
		AppSlug:               row.AppSlug,
		Status:                row.Status,
		TraceID:               textPtr(row.TraceID),
		CommitSHA:             textPtr(row.CommitSHA),
		Ref:                   textPtr(row.Ref),
		FailureClassification: textPtr(row.FailureClass),
		ErrorSummary:          textPtr(row.ErrorSummary),
		StartedAt:             timestamptzPtr(row.StartedAt),
		FinishedAt:            timestamptzPtr(row.FinishedAt),
	}
	if len(row.Events) > 0 {
		var events []struct {
			Timestamp *time.Time `json:"timestamp"`
			Message   string     `json:"message"`
		}
		if err := json.Unmarshal(row.Events, &events); err == nil {
			for _, event := range events {
				if event.Timestamp == nil || event.Message == "" {
					continue
				}
				out.LogLines = append(out.LogLines, DeployLogLine{
					Timestamp: *event.Timestamp,
					Message:   event.Message,
				})
			}
		}
	}

	return out, nil
}

// ListDeployLogLines returns latest deploy logs for a team app.
func (r *Repository) ListDeployLogLines(ctx context.Context, teamID string, appSlug string, limit int) ([]DeployLogLine, error) {
	deploy, err := r.GetLatestDeployByAppSlug(ctx, teamID, appSlug)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > len(deploy.LogLines) {
		limit = len(deploy.LogLines)
	}
	return append([]DeployLogLine(nil), deploy.LogLines[:limit]...), nil
}

// FindCliTokenByID loads a token by primary key.
func (r *Repository) FindCliTokenByID(ctx context.Context, tokenID string) (CliToken, error) {
	row, err := r.queries.FindCliTokenByID(ctx, tokenID)
	if err != nil {
		return CliToken{}, err
	}

	return CliToken{
		ID:          row.ID.String(),
		OwnerUserID: row.OwnerUserID.String(),
		TeamID:      row.TeamID.String(),
		Kind:        row.Kind,
		Name:        row.Name,
		TokenHash:   row.TokenHash,
		Scopes:      append([]string(nil), row.Scopes...),
		CreatedAt:   row.CreatedAt.Time,
		LastUsedAt:  timestamptzPtr(row.LastUsedAt),
		ExpiresAt:   timestamptzPtr(row.ExpiresAt),
		RevokedAt:   timestamptzPtr(row.RevokedAt),
	}, nil
}

// ListTeamTokens returns team-scoped tokens ordered by creation time.
func (r *Repository) ListTeamTokens(ctx context.Context, teamID string) ([]CliToken, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return nil, fmt.Errorf("parse team id: %w", err)
	}

	rows, err := r.pool.Query(ctx, `
SELECT
  id,
  owner_user_id,
  team_id,
  kind,
  name,
  token_hash,
  scopes,
  created_at,
  last_used_at,
  expires_at,
  revoked_at
FROM cli_token
WHERE team_id = $1
ORDER BY created_at DESC, name
`, parsedTeamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]CliToken, 0)
	for rows.Next() {
		var row struct {
			ID          pgtype.UUID
			OwnerUserID pgtype.UUID
			TeamID      pgtype.UUID
			Kind        string
			Name        string
			TokenHash   string
			Scopes      []string
			CreatedAt   pgtype.Timestamptz
			LastUsedAt  pgtype.Timestamptz
			ExpiresAt   pgtype.Timestamptz
			RevokedAt   pgtype.Timestamptz
		}
		if err := rows.Scan(
			&row.ID,
			&row.OwnerUserID,
			&row.TeamID,
			&row.Kind,
			&row.Name,
			&row.TokenHash,
			&row.Scopes,
			&row.CreatedAt,
			&row.LastUsedAt,
			&row.ExpiresAt,
			&row.RevokedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, CliToken{
			ID:          row.ID.String(),
			OwnerUserID: row.OwnerUserID.String(),
			TeamID:      row.TeamID.String(),
			Kind:        row.Kind,
			Name:        row.Name,
			TokenHash:   row.TokenHash,
			Scopes:      append([]string(nil), row.Scopes...),
			CreatedAt:   row.CreatedAt.Time,
			LastUsedAt:  timestamptzPtr(row.LastUsedAt),
			ExpiresAt:   timestamptzPtr(row.ExpiresAt),
			RevokedAt:   timestamptzPtr(row.RevokedAt),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// ResolveUserDefaultTeamByGithubLogin resolves a user and one of their team memberships.
func (r *Repository) ResolveUserDefaultTeamByGithubLogin(ctx context.Context, githubLogin string) (string, string, string, error) {
	var (
		userID pgtype.UUID
		teamID pgtype.UUID
		slug   string
	)
	err := r.pool.QueryRow(ctx, `
SELECT ua.id, t.id, t.slug
FROM user_account ua
JOIN team_membership tm ON tm.user_id = ua.id
JOIN team t ON t.id = tm.team_id
WHERE ua.github_login = $1
ORDER BY CASE tm.role
  WHEN 'owner' THEN 0
  WHEN 'admin' THEN 1
  WHEN 'member' THEN 2
  ELSE 3
END, t.slug
LIMIT 1
`, githubLogin).Scan(&userID, &teamID, &slug)
	if err != nil {
		return "", "", "", err
	}
	return userID.String(), teamID.String(), slug, nil
}

// DeviceTokenTTL is the default lifetime for device-flow CLI tokens.
// Matches migration 00003 backfill (created_at + 30 day) so newly
// minted device tokens align with the historical baseline that the
// schema NOT NULL constraint was sized for.
const DeviceTokenTTL = 30 * 24 * time.Hour

// CreateCLIToken creates a new CLI bearer token and stores only its hash.
// Migration 00003 made cli_token.expires_at NOT NULL; device tokens get
// DeviceTokenTTL from now. Callers may rotate via 0ops auth login.
func (r *Repository) CreateCLIToken(ctx context.Context, ownerUserID, teamID string, scopes []string) (string, error) {
	parsedUserID, err := parseUUID(ownerUserID)
	if err != nil {
		return "", fmt.Errorf("parse owner user id: %w", err)
	}
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return "", fmt.Errorf("parse team id: %w", err)
	}
	secret, err := sharedtoken.NewBearerTokenSecret()
	if err != nil {
		return "", err
	}
	hash, err := sharedtoken.HashBearerToken(secret)
	if err != nil {
		return "", err
	}
	expiresAt := time.Now().UTC().Add(DeviceTokenTTL)
	var tokenID string
	if err := r.pool.QueryRow(ctx, `
INSERT INTO cli_token (owner_user_id, team_id, kind, token_hash, name, scopes, expires_at)
VALUES ($1, $2, 'device', $3, $4, $5, $6)
RETURNING id
`, parsedUserID, parsedTeamID, hash, "device-login", scopes, expiresAt).Scan(&tokenID); err != nil {
		return "", err
	}
	return sharedtoken.FormatBearerToken("device", tokenID, secret), nil
}

// CreatePAT creates a new personal access token and returns the raw secret once.
func (r *Repository) CreatePAT(ctx context.Context, ownerUserID, teamID, name string, scopes []string, expiresAt time.Time) (string, error) {
	parsedUserID, err := parseUUID(ownerUserID)
	if err != nil {
		return "", fmt.Errorf("parse owner user id: %w", err)
	}
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return "", fmt.Errorf("parse team id: %w", err)
	}
	secret, err := sharedtoken.NewBearerTokenSecret()
	if err != nil {
		return "", err
	}
	hash, err := sharedtoken.HashBearerToken(secret)
	if err != nil {
		return "", err
	}
	var tokenID string
	if err := r.pool.QueryRow(ctx, `
INSERT INTO cli_token (owner_user_id, team_id, kind, token_hash, name, scopes, expires_at)
VALUES ($1, $2, 'pat', $3, $4, $5, $6)
RETURNING id
`, parsedUserID, parsedTeamID, hash, name, scopes, expiresAt).Scan(&tokenID); err != nil {
		return "", err
	}
	return sharedtoken.FormatBearerToken("pat", tokenID, secret), nil
}

// RevokeCLITokenByID marks a token as revoked.
func (r *Repository) RevokeCLITokenByID(ctx context.Context, tokenID string) error {
	tag, err := r.pool.Exec(ctx, `
UPDATE cli_token
SET revoked_at = now()
WHERE id = $1
  AND revoked_at IS NULL
`, tokenID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// RevokePATByName marks a PAT as revoked.
func (r *Repository) RevokePATByName(ctx context.Context, teamID, name string) error {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return fmt.Errorf("parse team id: %w", err)
	}
	tag, err := r.pool.Exec(ctx, `
UPDATE cli_token
SET revoked_at = now()
WHERE team_id = $1
  AND kind = 'pat'
  AND name = $2
  AND revoked_at IS NULL
`, parsedTeamID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func textPtr(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}
