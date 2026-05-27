package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrTeamNotFound is returned when a team lookup misses by id.
var ErrTeamNotFound = errors.New("team not found")

// GetTeamByID loads a team (including github_install_id) by primary key.
func (r *Repository) GetTeamByID(ctx context.Context, teamID string) (Team, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return Team{}, fmt.Errorf("parse team id: %w", err)
	}

	var (
		id              pgtype.UUID
		archivedAt      pgtype.Timestamptz
		githubInstallID pgtype.Int8
		team            Team
	)
	err = r.pool.QueryRow(ctx, `
SELECT id, slug, name, plan, github_install_id, archived_at
FROM team
WHERE id = $1
`, parsedTeamID).Scan(&id, &team.Slug, &team.Name, &team.Plan, &githubInstallID, &archivedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Team{}, ErrTeamNotFound
		}
		return Team{}, err
	}

	team.ID = id.String()
	team.ArchivedAt = timestamptzPtr(archivedAt)
	if githubInstallID.Valid {
		v := githubInstallID.Int64
		team.GithubInstallID = &v
	}
	return team, nil
}

// FindTeamByGitHubInstallID resolves the team currently bound to the given
// installation id; returns ErrTeamNotFound when none is bound.
func (r *Repository) FindTeamByGitHubInstallID(ctx context.Context, installID int64) (Team, error) {
	var (
		id              pgtype.UUID
		archivedAt      pgtype.Timestamptz
		githubInstallID pgtype.Int8
		team            Team
	)
	err := r.pool.QueryRow(ctx, `
SELECT id, slug, name, plan, github_install_id, archived_at
FROM team
WHERE github_install_id = $1
`, installID).Scan(&id, &team.Slug, &team.Name, &team.Plan, &githubInstallID, &archivedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Team{}, ErrTeamNotFound
		}
		return Team{}, err
	}

	team.ID = id.String()
	team.ArchivedAt = timestamptzPtr(archivedAt)
	if githubInstallID.Valid {
		v := githubInstallID.Int64
		team.GithubInstallID = &v
	}
	return team, nil
}

// SetTeamGitHubInstall updates team.github_install_id and writes an audit_log
// row in the same transaction. Pass installID == nil to clear the binding.
func (r *Repository) SetTeamGitHubInstall(ctx context.Context, teamID, actorUserID string, installID *int64, action string, args map[string]any, result map[string]any) error {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return fmt.Errorf("parse team id: %w", err)
	}
	var parsedActorID pgtype.UUID
	if actorUserID != "" {
		parsedActorID, err = parseUUID(actorUserID)
		if err != nil {
			return fmt.Errorf("parse actor id: %w", err)
		}
	}

	argsJSON, err := marshalAuditPayload(args)
	if err != nil {
		return err
	}
	resultJSON, err := marshalAuditPayload(result)
	if err != nil {
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
UPDATE team SET github_install_id = $2 WHERE id = $1
`, parsedTeamID, installIDValue(installID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTeamNotFound
	}

	var actorValue any
	if actorUserID != "" {
		actorValue = parsedActorID
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit_log (team_id, actor_user_id, subject_type, subject_id, action, args, result)
VALUES ($1, $2, 'team', $1, $3, $4::jsonb, $5::jsonb)
`, parsedTeamID, actorValue, action, argsJSON, resultJSON); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// PauseTeamApps flips every active team app into the paused status. Returns
// the number of affected rows.
func (r *Repository) PauseTeamApps(ctx context.Context, teamID string) (int64, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return 0, fmt.Errorf("parse team id: %w", err)
	}
	tag, err := r.pool.Exec(ctx, `
UPDATE app
SET status = 'paused', updated_at = now()
WHERE team_id = $1
  AND (status IS NULL OR status <> 'paused')
`, parsedTeamID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func installIDValue(installID *int64) any {
	if installID == nil {
		return nil
	}
	return *installID
}

func marshalAuditPayload(payload map[string]any) ([]byte, error) {
	if len(payload) == 0 {
		return []byte("{}"), nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal audit payload: %w", err)
	}
	return data, nil
}
