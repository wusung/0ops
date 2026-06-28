package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/winshare/zeroops/internal/server/security"
	"github.com/winshare/zeroops/internal/server/services/audit"
)

var (
	// ErrBootstrapAlreadyDone indicates bootstrap was already executed.
	ErrBootstrapAlreadyDone = errors.New("bootstrap already done")
	// ErrPreviewNotFound indicates the preview was not found.
	ErrPreviewNotFound = errors.New("preview not found")
	// ErrPreviewExpired indicates the preview has expired.
	ErrPreviewExpired = errors.New("preview expired")
	// ErrPreviewConsumed indicates the preview has been consumed.
	ErrPreviewConsumed = errors.New("preview consumed")
	// ErrMemberNotFound indicates the member was not found.
	ErrMemberNotFound = errors.New("member not found")
	// ErrOwnerRemoval indicates an attempt to remove the owner.
	ErrOwnerRemoval = errors.New("cannot remove owner")
)

//nolint:revive // exported for public API
type BootstrapOwnerParams struct {
	TeamSlug    string
	TeamName    string
	GithubLogin string
	Email       *string
}

//nolint:revive // exported for public API
type Member struct {
	UserID      string
	GithubLogin *string
	Email       *string
	Role        string
	InvitedAt   *time.Time
	JoinedAt    *time.Time
}

//nolint:revive // exported for public API
type Preview struct {
	ID             string
	TeamID         string
	ActorUserID    string
	Action         string
	Args           json.RawMessage
	LastResult     json.RawMessage
	TraceID        string
	RiskLevel      string
	RequiredPhrase string
	CreatedAt      time.Time
	ExpiresAt      time.Time
	ConsumedAt     *time.Time
}

//nolint:revive // exported for public API
func (r *Repository) HasAnyOwner(ctx context.Context) (bool, error) {
	var has bool
	if err := r.pool.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM team_membership
  WHERE role = 'owner'
)
`).Scan(&has); err != nil {
		return false, err
	}
	return has, nil
}

//nolint:revive // exported for public API
func (r *Repository) BootstrapOwner(ctx context.Context, params BootstrapOwnerParams) (string, string, error) {
	hasOwner, err := r.HasAnyOwner(ctx)
	if err != nil {
		return "", "", err
	}
	if hasOwner {
		return "", "", ErrBootstrapAlreadyDone
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		userID pgtype.UUID
		teamID pgtype.UUID
	)

	if err := tx.QueryRow(ctx, `
INSERT INTO user_account (github_login, email)
VALUES ($1, $2)
ON CONFLICT (github_login)
DO UPDATE SET email = COALESCE(EXCLUDED.email, user_account.email)
RETURNING id
`, params.GithubLogin, textFromPtr(params.Email)).Scan(&userID); err != nil {
		return "", "", err
	}

	if err := tx.QueryRow(ctx, `
INSERT INTO team (slug, name)
VALUES ($1, $2)
RETURNING id
`, params.TeamSlug, params.TeamName).Scan(&teamID); err != nil {
		return "", "", err
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO team_membership (team_id, user_id, role, invited_at, joined_at, invited_by)
VALUES ($1, $2, 'owner', now(), now(), $2)
`, teamID, userID); err != nil {
		return "", "", err
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO audit_log (team_id, actor_user_id, subject_type, subject_id, action, args, result)
VALUES ($1, $2, 'team', $1, 'bootstrap_owner', '{}'::jsonb, '{"status":"created"}'::jsonb)
`, teamID, userID); err != nil {
		return "", "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return teamID.String(), userID.String(), nil
}

//nolint:revive // exported for public API
func (r *Repository) ListTeamMembers(ctx context.Context, teamID string) ([]Member, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return nil, fmt.Errorf("parse team id: %w", err)
	}

	rows, err := r.pool.Query(ctx, `
SELECT
  ua.id,
  ua.github_login,
  ua.email,
  tm.role,
  tm.invited_at,
  tm.joined_at
FROM team_membership tm
JOIN user_account ua ON ua.id = tm.user_id
WHERE tm.team_id = $1
ORDER BY tm.joined_at NULLS LAST, ua.id
`, parsedTeamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Member, 0)
	for rows.Next() {
		var (
			userID      pgtype.UUID
			githubLogin pgtype.Text
			email       pgtype.Text
			role        string
			invitedAt   pgtype.Timestamptz
			joinedAt    pgtype.Timestamptz
		)
		if err := rows.Scan(&userID, &githubLogin, &email, &role, &invitedAt, &joinedAt); err != nil {
			return nil, err
		}
		items = append(items, Member{
			UserID:      userID.String(),
			GithubLogin: textPtr(githubLogin),
			Email:       textPtr(email),
			Role:        role,
			InvitedAt:   timestamptzPtr(invitedAt),
			JoinedAt:    timestamptzPtr(joinedAt),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

//nolint:revive // exported for public API
func (r *Repository) CreatePreview(ctx context.Context, teamID, actorUserID, action string, args json.RawMessage, actionSummary string) (Preview, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return Preview{}, fmt.Errorf("parse team id: %w", err)
	}
	parsedActorID, err := parseUUID(actorUserID)
	if err != nil {
		return Preview{}, fmt.Errorf("parse actor id: %w", err)
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}

	key, err := randomKey()
	if err != nil {
		return Preview{}, err
	}

	traceID := audit.TraceIDFromContext(ctx)

	// Differentiated-confirmation metadata is a deterministic function of
	// (action, args) — the single backend chokepoint that stamps every
	// preview (security-hardening spec § 5.2). risk_level is read-only and
	// never sourced from the client (hard rule #2). required_phrase is the
	// backend-generated typed-confirmation token for high-risk actions.
	var argMap map[string]any
	if len(args) > 0 {
		_ = json.Unmarshal(args, &argMap)
	}
	riskLevel := string(security.RiskLevel(action, argMap))
	requiredPhrase := security.RequiredPhrase(action, argMap)

	var (
		id        pgtype.UUID
		createdAt pgtype.Timestamptz
		expiresAt pgtype.Timestamptz
		rowTrace  string
	)
	if err := r.pool.QueryRow(ctx, `
INSERT INTO preview (team_id, actor_user_id, action, args, action_summary, side_effects, idempotency_key, expires_at, trace_id, risk_level, required_phrase)
VALUES ($1, $2, $3, $4::jsonb, $5, '[]'::jsonb, $6, now() + interval '10 minute', $7, $8, $9)
RETURNING id, created_at, expires_at, trace_id
`, parsedTeamID, parsedActorID, action, []byte(args), actionSummary, key, traceID, riskLevel, requiredPhrase).Scan(&id, &createdAt, &expiresAt, &rowTrace); err != nil {
		return Preview{}, err
	}

	return Preview{
		ID:             id.String(),
		TeamID:         teamID,
		ActorUserID:    actorUserID,
		Action:         action,
		Args:           args,
		TraceID:        rowTrace,
		RiskLevel:      riskLevel,
		RequiredPhrase: requiredPhrase,
		CreatedAt:      createdAt.Time,
		ExpiresAt:      expiresAt.Time,
	}, nil
}

//nolint:revive // exported for public API
func (r *Repository) GetPreview(ctx context.Context, previewID string) (Preview, error) {
	parsedPreviewID, err := parseUUID(previewID)
	if err != nil {
		return Preview{}, fmt.Errorf("parse preview id: %w", err)
	}

	var (
		id             pgtype.UUID
		teamID         pgtype.UUID
		actorUserID    pgtype.UUID
		action         string
		args           []byte
		lastResult     []byte
		traceID        string
		riskLevel      string
		requiredPhrase string
		createdAt      pgtype.Timestamptz
		expiresAt      pgtype.Timestamptz
		consumedAt     pgtype.Timestamptz
	)

	if err := r.pool.QueryRow(ctx, `
SELECT id, team_id, actor_user_id, action, args, last_result,
       COALESCE(trace_id, '00000000000000000000000000000000'),
       COALESCE(risk_level, 'normal'),
       COALESCE(required_phrase, ''),
       created_at, expires_at, consumed_at
FROM preview
WHERE id = $1
`, parsedPreviewID).Scan(&id, &teamID, &actorUserID, &action, &args, &lastResult, &traceID, &riskLevel, &requiredPhrase, &createdAt, &expiresAt, &consumedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Preview{}, ErrPreviewNotFound
		}
		return Preview{}, err
	}

	return Preview{
		ID:             id.String(),
		TeamID:         teamID.String(),
		ActorUserID:    actorUserID.String(),
		Action:         action,
		Args:           json.RawMessage(args),
		LastResult:     json.RawMessage(lastResult),
		TraceID:        traceID,
		RiskLevel:      riskLevel,
		RequiredPhrase: requiredPhrase,
		CreatedAt:      createdAt.Time,
		ExpiresAt:      expiresAt.Time,
		ConsumedAt:     timestamptzPtr(consumedAt),
	}, nil
}

//nolint:revive // exported for public API
func (r *Repository) ConsumePreview(ctx context.Context, previewID string) error {
	parsedPreviewID, err := parseUUID(previewID)
	if err != nil {
		return fmt.Errorf("parse preview id: %w", err)
	}

	tag, err := r.pool.Exec(ctx, `
UPDATE preview
SET consumed_at = now()
WHERE id = $1
  AND consumed_at IS NULL
`, parsedPreviewID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPreviewConsumed
	}
	return nil
}

// ConsumePreviewWithResult marks preview as consumed and persists last_result.
func (r *Repository) ConsumePreviewWithResult(ctx context.Context, previewID string, result json.RawMessage) error {
	parsedPreviewID, err := parseUUID(previewID)
	if err != nil {
		return fmt.Errorf("parse preview id: %w", err)
	}
	if len(result) == 0 {
		result = json.RawMessage(`{}`)
	}

	tag, err := r.pool.Exec(ctx, `
UPDATE preview
SET consumed_at = now(), last_result = $2::jsonb
WHERE id = $1
  AND consumed_at IS NULL
`, parsedPreviewID, []byte(result))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPreviewConsumed
	}
	return nil
}

//nolint:revive // exported for public API
type InviteMemberParams struct {
	TeamID      string
	ActorUserID string
	GithubLogin *string
	Email       *string
	Role        string
}

//nolint:revive // exported for public API
func (r *Repository) InviteMember(ctx context.Context, params InviteMemberParams) (Member, error) {
	parsedTeamID, err := parseUUID(params.TeamID)
	if err != nil {
		return Member{}, fmt.Errorf("parse team id: %w", err)
	}
	parsedActorID, err := parseUUID(params.ActorUserID)
	if err != nil {
		return Member{}, fmt.Errorf("parse actor id: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Member{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID pgtype.UUID
	if params.GithubLogin != nil && *params.GithubLogin != "" {
		err = tx.QueryRow(ctx, `
SELECT id FROM user_account WHERE github_login = $1
`, *params.GithubLogin).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `
INSERT INTO user_account (github_login, email)
VALUES ($1, $2)
RETURNING id
`, *params.GithubLogin, textFromPtr(params.Email)).Scan(&userID)
		}
	} else if params.Email != nil && *params.Email != "" {
		err = tx.QueryRow(ctx, `
SELECT id FROM user_account WHERE email = $1 ORDER BY created_at LIMIT 1
`, *params.Email).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `
INSERT INTO user_account (email)
VALUES ($1)
RETURNING id
`, *params.Email).Scan(&userID)
		}
	} else {
		return Member{}, errors.New("github_login or email required")
	}
	if err != nil {
		return Member{}, err
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO team_membership (team_id, user_id, role, invited_at, joined_at, invited_by)
VALUES ($1, $2, $3, now(), now(), $4)
ON CONFLICT (team_id, user_id)
DO UPDATE SET role = EXCLUDED.role, invited_at = EXCLUDED.invited_at, invited_by = EXCLUDED.invited_by
`, parsedTeamID, userID, params.Role, parsedActorID); err != nil {
		return Member{}, err
	}

	var (
		githubLogin pgtype.Text
		email       pgtype.Text
		invitedAt   pgtype.Timestamptz
		joinedAt    pgtype.Timestamptz
	)
	if err := tx.QueryRow(ctx, `
SELECT ua.github_login, ua.email, tm.invited_at, tm.joined_at
FROM team_membership tm
JOIN user_account ua ON ua.id = tm.user_id
WHERE tm.team_id = $1 AND tm.user_id = $2
`, parsedTeamID, userID).Scan(&githubLogin, &email, &invitedAt, &joinedAt); err != nil {
		return Member{}, err
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO audit_log (team_id, actor_user_id, subject_type, subject_id, action, args, result)
VALUES ($1, $2, 'user', $3, 'member_invite', '{}'::jsonb, '{"status":"ok"}'::jsonb)
`, parsedTeamID, parsedActorID, userID); err != nil {
		return Member{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Member{}, err
	}

	return Member{
		UserID:      userID.String(),
		GithubLogin: textPtr(githubLogin),
		Email:       textPtr(email),
		Role:        params.Role,
		InvitedAt:   timestamptzPtr(invitedAt),
		JoinedAt:    timestamptzPtr(joinedAt),
	}, nil
}

//nolint:revive // exported for public API
func (r *Repository) RemoveMember(ctx context.Context, teamID, actorUserID, targetUserID string) error {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return fmt.Errorf("parse team id: %w", err)
	}
	parsedActorID, err := parseUUID(actorUserID)
	if err != nil {
		return fmt.Errorf("parse actor id: %w", err)
	}
	parsedTargetID, err := parseUUID(targetUserID)
	if err != nil {
		return fmt.Errorf("parse target user id: %w", err)
	}

	var role string
	if err := r.pool.QueryRow(ctx, `
SELECT role
FROM team_membership
WHERE team_id = $1 AND user_id = $2
`, parsedTeamID, parsedTargetID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrMemberNotFound
		}
		return err
	}
	if role == "owner" {
		return ErrOwnerRemoval
	}

	tag, err := r.pool.Exec(ctx, `
DELETE FROM team_membership
WHERE team_id = $1 AND user_id = $2
`, parsedTeamID, parsedTargetID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrMemberNotFound
	}

	if _, err := r.pool.Exec(ctx, `
INSERT INTO audit_log (team_id, actor_user_id, subject_type, subject_id, action, args, result)
VALUES ($1, $2, 'user', $3, 'member_remove', '{}'::jsonb, '{"status":"ok"}'::jsonb)
`, parsedTeamID, parsedActorID, parsedTargetID); err != nil {
		return err
	}
	return nil
}

// GetOrCreateUserAndPersonalTeam returns existing user + team, or creates new user with personal team on first login.
func (r *Repository) GetOrCreateUserAndPersonalTeam(ctx context.Context, githubLogin string) (userID string, teamID string, teamSlug string, err error) {
	userID, teamID, teamSlug, err = r.ResolveUserDefaultTeamByGithubLogin(ctx, githubLogin)
	if err == nil {
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", "", "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		userUUID pgtype.UUID
		teamUUID pgtype.UUID
	)

	if err := tx.QueryRow(ctx, `
INSERT INTO user_account (github_login)
VALUES ($1)
ON CONFLICT (github_login)
DO UPDATE SET github_login = EXCLUDED.github_login
RETURNING id
`, githubLogin).Scan(&userUUID); err != nil {
		return "", "", "", err
	}

	teamSlug = githubLogin
	if err := tx.QueryRow(ctx, `
INSERT INTO team (slug, name)
VALUES ($1, $2)
RETURNING id
`, teamSlug, githubLogin).Scan(&teamUUID); err != nil {
		return "", "", "", err
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO team_membership (team_id, user_id, role, invited_at, joined_at, invited_by)
VALUES ($1, $2, 'owner', now(), now(), $2)
`, teamUUID, userUUID); err != nil {
		return "", "", "", err
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO audit_log (team_id, actor_user_id, subject_type, subject_id, action, args, result)
VALUES ($1, $2, 'team', $1, 'device_login_auto_create', '{}'::jsonb, '{"status":"created"}'::jsonb)
`, teamUUID, userUUID); err != nil {
		return "", "", "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", "", "", err
	}
	return userUUID.String(), teamUUID.String(), teamSlug, nil
}

func textFromPtr(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func randomKey() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
