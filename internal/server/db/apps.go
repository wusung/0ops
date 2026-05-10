package db

import (
	"context"
	"fmt"
	"time"

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

// ListTeamApps returns apps for a team ordered by slug.
func (r *Repository) ListTeamApps(ctx context.Context, teamID string, limit int32, afterSlug string) ([]App, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return nil, fmt.Errorf("parse team id: %w", err)
	}

	rows, err := r.queries.ListAppsByTeam(ctx, sqlcgen.ListAppsByTeamParams{
		TeamID:  parsedTeamID,
		Column2: afterSlug,
		Limit:   limit,
	})
	if err != nil {
		return nil, err
	}

	items := make([]App, 0, len(rows))
	for _, row := range rows {
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

	return items, nil
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
