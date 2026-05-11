package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	sqlcgen "github.com/winshare/zeroops/internal/server/db/sqlc"
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

// CliToken describes an auth token record.
type CliToken struct {
	ID          string
	OwnerUserID string
	TeamID      string
	TokenHash   string
	Scopes      []string
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
		CommitSHA    pgtype.Text
		Ref          pgtype.Text
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
  dr.commit_sha,
  dr.ref,
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
		&row.CommitSHA,
		&row.Ref,
		&row.ErrorSummary,
		&row.StartedAt,
		&row.FinishedAt,
		&row.Events,
	)
	if err != nil {
		return DeployRun{}, err
	}

	out := DeployRun{
		ID:           row.ID.String(),
		TeamID:       row.TeamID.String(),
		AppID:        row.AppID.String(),
		AppSlug:      row.AppSlug,
		Status:       row.Status,
		CommitSHA:    textPtr(row.CommitSHA),
		Ref:          textPtr(row.Ref),
		ErrorSummary: textPtr(row.ErrorSummary),
		StartedAt:    timestamptzPtr(row.StartedAt),
		FinishedAt:   timestamptzPtr(row.FinishedAt),
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

// FindCliTokenByHash loads a token by hash.
func (r *Repository) FindCliTokenByHash(ctx context.Context, tokenHash string) (CliToken, error) {
	row, err := r.queries.FindCliTokenByHash(ctx, tokenHash)
	if err != nil {
		return CliToken{}, err
	}

	return CliToken{
		ID:          row.ID.String(),
		OwnerUserID: row.OwnerUserID.String(),
		TeamID:      row.TeamID.String(),
		TokenHash:   row.TokenHash,
		Scopes:      append([]string(nil), row.Scopes...),
		RevokedAt:   timestamptzPtr(row.RevokedAt),
	}, nil
}

func textPtr(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}
