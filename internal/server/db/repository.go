// Package db provides read-only repository queries for team data.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "github.com/winshare/zeroops/internal/server/db/sqlc"
)

// Repository wraps the database pool and generated queries.
type Repository struct {
	pool    *pgxpool.Pool
	queries *sqlcgen.Queries
}

// NewRepository returns a repository backed by the given pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{
		pool:    pool,
		queries: sqlcgen.New(pool),
	}
}

// ResolveTeamBySlug loads a team by slug.
func (r *Repository) ResolveTeamBySlug(ctx context.Context, slug string) (Team, error) {
	var (
		id         pgtype.UUID
		archivedAt pgtype.Timestamptz
		team       Team
	)

	err := r.pool.QueryRow(ctx, `
SELECT id, slug, name, plan, archived_at
FROM team
WHERE slug = $1
`, slug).Scan(&id, &team.Slug, &team.Name, &team.Plan, &archivedAt)
	if err != nil {
		return Team{}, err
	}

	team.ID = id.String()
	team.ArchivedAt = timestamptzPtr(archivedAt)

	return team, nil
}

// ListUserTeams returns a user's teams in slug order.
func (r *Repository) ListUserTeams(ctx context.Context, userID string, limit int32, afterSlug *string) ([]TeamMembership, error) {
	parsedUserID, err := parseUUID(userID)
	if err != nil {
		return nil, fmt.Errorf("parse user id: %w", err)
	}

	params := sqlcgen.ListUserTeamsParams{
		UserID: parsedUserID,
		Limit:  limit,
	}
	if afterSlug != nil {
		params.Column2 = *afterSlug
	}

	rows, err := r.queries.ListUserTeams(ctx, params)
	if err != nil {
		return nil, err
	}

	items := make([]TeamMembership, 0, len(rows))
	for _, row := range rows {
		items = append(items, TeamMembership{
			Team: Team{
				ID:         row.ID.String(),
				Slug:       row.Slug,
				Name:       row.Name,
				Plan:       row.Plan,
				ArchivedAt: timestamptzPtr(row.ArchivedAt),
			},
			UserID:    row.UserID.String(),
			Role:      row.Role,
			JoinedAt:  timestamptzPtr(row.JoinedAt),
			InvitedAt: timestamptzPtr(row.InvitedAt),
		})
	}

	return items, nil
}

// CheckTeamMembership reports whether the user belongs to the team.
func (r *Repository) CheckTeamMembership(ctx context.Context, teamID string, userID string) (bool, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return false, fmt.Errorf("parse team id: %w", err)
	}

	parsedUserID, err := parseUUID(userID)
	if err != nil {
		return false, fmt.Errorf("parse user id: %w", err)
	}

	return r.queries.CheckTeamMembership(ctx, sqlcgen.CheckTeamMembershipParams{
		TeamID: parsedTeamID,
		UserID: parsedUserID,
	})
}

func parseUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		return pgtype.UUID{}, err
	}

	return id, nil
}

func timestamptzPtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}

	t := value.Time
	return &t
}

// IsToolGranted checks if a user has been granted a specific tool
func (r *Repository) IsToolGranted(ctx context.Context, teamID, userID, toolID string) (bool, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return false, fmt.Errorf("parse team id: %w", err)
	}

	parsedUserID, err := parseUUID(userID)
	if err != nil {
		return false, fmt.Errorf("parse user id: %w", err)
	}

	return r.queries.IsToolGranted(ctx, sqlcgen.IsToolGrantedParams{
		TeamID: parsedTeamID,
		UserID: parsedUserID,
		ToolID: toolID,
	})
}

// ListGrantedTools returns all tools granted to a user on a team
func (r *Repository) ListGrantedTools(ctx context.Context, teamID, userID string) ([]string, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return nil, fmt.Errorf("parse team id: %w", err)
	}

	parsedUserID, err := parseUUID(userID)
	if err != nil {
		return nil, fmt.Errorf("parse user id: %w", err)
	}

	return r.queries.ListGrantedTools(ctx, sqlcgen.ListGrantedToolsParams{
		TeamID: parsedTeamID,
		UserID: parsedUserID,
	})
}

// UpsertToolGrant creates or updates a tool grant for a user
func (r *Repository) UpsertToolGrant(ctx context.Context, teamID, userID, toolID string, allowed bool, grantedByActorID *string) error {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return fmt.Errorf("parse team id: %w", err)
	}

	parsedUserID, err := parseUUID(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}

	var parsedActorID pgtype.UUID
	if grantedByActorID != nil {
		if err := parsedActorID.Scan(*grantedByActorID); err != nil {
			return fmt.Errorf("parse actor id: %w", err)
		}
	}

	return r.queries.UpsertToolGrant(ctx, sqlcgen.UpsertToolGrantParams{
		TeamID:           parsedTeamID,
		UserID:           parsedUserID,
		ToolID:           toolID,
		Allowed:          allowed,
		GrantedByActorID: parsedActorID,
	})
}

// RevokeToolGrant removes a tool grant from a user
func (r *Repository) RevokeToolGrant(ctx context.Context, teamID, userID, toolID string) error {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return fmt.Errorf("parse team id: %w", err)
	}

	parsedUserID, err := parseUUID(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}

	return r.queries.RevokeToolGrant(ctx, sqlcgen.RevokeToolGrantParams{
		TeamID: parsedTeamID,
		UserID: parsedUserID,
		ToolID: toolID,
	})
}

// ListAllUserGrants returns all tool grants for a user, both allowed and revoked
func (r *Repository) ListAllUserGrants(ctx context.Context, teamID, userID string) ([]ToolGrant, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return nil, fmt.Errorf("parse team id: %w", err)
	}

	parsedUserID, err := parseUUID(userID)
	if err != nil {
		return nil, fmt.Errorf("parse user id: %w", err)
	}

	rows, err := r.queries.ListAllUserGrants(ctx, sqlcgen.ListAllUserGrantsParams{
		TeamID: parsedTeamID,
		UserID: parsedUserID,
	})
	if err != nil {
		return nil, err
	}

	items := make([]ToolGrant, 0, len(rows))
	for _, row := range rows {
		items = append(items, ToolGrant{
			ToolID:  row.ToolID,
			Allowed: row.Allowed,
		})
	}

	return items, nil
}
