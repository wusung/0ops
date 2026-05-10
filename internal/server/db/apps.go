package db

import (
	"context"
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
