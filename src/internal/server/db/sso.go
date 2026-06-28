package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	sharedtoken "github.com/winshare/zeroops/internal/shared/token"
)

// Sentinel errors for the SSO repository surface. Handlers map these to the
// HTTP error envelope (sso-saml spec § 12).
var (
	// ErrIdPConfigExists is returned when a team already has an IdP config
	// (one team one IdP, v1; spec § 5.3).
	ErrIdPConfigExists = errors.New("idp config already exists for team")
	// ErrDomainTaken is returned when a domain is already bound to any team's
	// IdP (global UNIQUE, hard rule #9).
	ErrDomainTaken = errors.New("domain already bound to a team")
)

// IdPConfig is a team's external identity provider configuration. The plaintext
// client secret never lives here — ClientSecretRef points at the secrets store.
type IdPConfig struct {
	ID              string
	TeamID          string
	Protocol        string
	DisplayName     *string
	Issuer          string
	DiscoveryURL    *string
	ClientID        string
	ClientSecretRef string
	Scopes          []string
	Enforce         bool
	JITDefaultRole  string
	GroupClaim      *string
	GroupRoleMap    map[string]string
	PATPolicy       string
	SessionMaxTTLS  int
	CreatedAt       time.Time
	UpdatedAt       *time.Time
}

// IdPDomain is a DNS-TXT-verified domain bound to an IdP config.
type IdPDomain struct {
	ID                string
	IdPConfigID       string
	TeamID            string
	Domain            string
	VerificationToken string
	Verified          bool
	VerifiedAt        *time.Time
	CreatedAt         time.Time
}

// CreateIdPConfigParams carries the fields needed to create an IdP config.
type CreateIdPConfigParams struct {
	TeamID          string
	Issuer          string
	DiscoveryURL    *string
	ClientID        string
	ClientSecretRef string
	DisplayName     *string
	Scopes          []string
	JITDefaultRole  string
	GroupClaim      *string
	GroupRoleMap    map[string]string
	PATPolicy       string
	SessionMaxTTLS  int
	CreatedBy       *string
}

const idpConfigColumns = `id::text, team_id::text, protocol, display_name, issuer, discovery_url,
		client_id, client_secret_ref, scopes, enforce, jit_default_role,
		group_claim, group_role_map, pat_policy, session_max_ttl_s, created_at, updated_at`

func scanIdPConfig(row pgx.Row) (IdPConfig, error) {
	var cfg IdPConfig
	var groupMap []byte
	if err := row.Scan(
		&cfg.ID, &cfg.TeamID, &cfg.Protocol, &cfg.DisplayName, &cfg.Issuer, &cfg.DiscoveryURL,
		&cfg.ClientID, &cfg.ClientSecretRef, &cfg.Scopes, &cfg.Enforce, &cfg.JITDefaultRole,
		&cfg.GroupClaim, &groupMap, &cfg.PATPolicy, &cfg.SessionMaxTTLS, &cfg.CreatedAt, &cfg.UpdatedAt,
	); err != nil {
		return IdPConfig{}, err
	}
	if len(groupMap) > 0 {
		if err := json.Unmarshal(groupMap, &cfg.GroupRoleMap); err != nil {
			return IdPConfig{}, fmt.Errorf("decode group_role_map: %w", err)
		}
	}
	return cfg, nil
}

// CreateIdPConfig inserts a team's IdP config. Returns ErrIdPConfigExists when
// the team already has one (team_id UNIQUE).
func (r *Repository) CreateIdPConfig(ctx context.Context, p CreateIdPConfigParams) (IdPConfig, error) {
	if p.SessionMaxTTLS <= 0 {
		p.SessionMaxTTLS = 28800
	}
	if p.JITDefaultRole == "" {
		p.JITDefaultRole = "member"
	}
	if p.PATPolicy == "" {
		p.PATPolicy = "allow"
	}
	if len(p.Scopes) == 0 {
		p.Scopes = []string{"openid", "email", "profile"}
	}
	var groupMap []byte
	if len(p.GroupRoleMap) > 0 {
		b, err := json.Marshal(p.GroupRoleMap)
		if err != nil {
			return IdPConfig{}, fmt.Errorf("encode group_role_map: %w", err)
		}
		groupMap = b
	}
	row := r.pool.QueryRow(ctx, `
INSERT INTO idp_config
		(team_id, issuer, discovery_url, client_id, client_secret_ref, display_name,
		 scopes, jit_default_role, group_claim, group_role_map, pat_policy, session_max_ttl_s, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING `+idpConfigColumns,
		p.TeamID, p.Issuer, p.DiscoveryURL, p.ClientID, p.ClientSecretRef, p.DisplayName,
		p.Scopes, p.JITDefaultRole, p.GroupClaim, groupMap, p.PATPolicy, p.SessionMaxTTLS, p.CreatedBy)
	cfg, err := scanIdPConfig(row)
	if err != nil {
		if isUniqueViolation(err) {
			return IdPConfig{}, ErrIdPConfigExists
		}
		return IdPConfig{}, err
	}
	return cfg, nil
}

// GetIdPConfigByTeam returns the team's IdP config or pgx.ErrNoRows.
func (r *Repository) GetIdPConfigByTeam(ctx context.Context, teamID string) (IdPConfig, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+idpConfigColumns+` FROM idp_config WHERE team_id = $1`, teamID)
	return scanIdPConfig(row)
}

// IdPConfigPatch carries the mutable fields of an IdP config; nil fields are
// left unchanged.
type IdPConfigPatch struct {
	Enforce        *bool
	PATPolicy      *string
	JITDefaultRole *string
	DisplayName    *string
}

// UpdateIdPConfig applies a patch and returns the updated config.
func (r *Repository) UpdateIdPConfig(ctx context.Context, teamID string, patch IdPConfigPatch) (IdPConfig, error) {
	row := r.pool.QueryRow(ctx, `
UPDATE idp_config SET
		enforce          = COALESCE($2, enforce),
		pat_policy       = COALESCE($3, pat_policy),
		jit_default_role = COALESCE($4, jit_default_role),
		display_name     = COALESCE($5, display_name),
		updated_at       = now()
WHERE team_id = $1
RETURNING `+idpConfigColumns,
		teamID, patch.Enforce, patch.PATPolicy, patch.JITDefaultRole, patch.DisplayName)
	return scanIdPConfig(row)
}

// DeleteIdPConfig removes a team's IdP config (cascades domains + identities).
func (r *Repository) DeleteIdPConfig(ctx context.Context, teamID string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM idp_config WHERE team_id = $1`, teamID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// AddIdPDomain registers a domain for DNS-TXT verification. Returns
// ErrDomainTaken when the domain is already bound (global UNIQUE).
func (r *Repository) AddIdPDomain(ctx context.Context, idpConfigID, teamID, domain, verificationToken string) (IdPDomain, error) {
	var d IdPDomain
	err := r.pool.QueryRow(ctx, `
INSERT INTO idp_domain (idp_config_id, team_id, domain, verification_token)
VALUES ($1, $2, $3, $4)
RETURNING id::text, idp_config_id::text, team_id::text, domain, verification_token, verified, verified_at, created_at`,
		idpConfigID, teamID, domain, verificationToken).Scan(
		&d.ID, &d.IdPConfigID, &d.TeamID, &d.Domain, &d.VerificationToken, &d.Verified, &d.VerifiedAt, &d.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return IdPDomain{}, ErrDomainTaken
		}
		return IdPDomain{}, err
	}
	return d, nil
}

// ListIdPDomains returns all domains bound to a config, ordered by domain.
func (r *Repository) ListIdPDomains(ctx context.Context, idpConfigID string) ([]IdPDomain, error) {
	rows, err := r.pool.Query(ctx, `
SELECT id::text, idp_config_id::text, team_id::text, domain, verification_token, verified, verified_at, created_at
FROM idp_domain WHERE idp_config_id = $1 ORDER BY domain`, idpConfigID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IdPDomain
	for rows.Next() {
		var d IdPDomain
		if err := rows.Scan(&d.ID, &d.IdPConfigID, &d.TeamID, &d.Domain, &d.VerificationToken, &d.Verified, &d.VerifiedAt, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// FindSSOUserByEmail resolves a user_id from an IdP identity email (owner-driven
// deprovision by email, spec § 13). Returns pgx.ErrNoRows when absent.
func (r *Repository) FindSSOUserByEmail(ctx context.Context, idpConfigID, email string) (string, error) {
	var userID string
	err := r.pool.QueryRow(ctx, `
SELECT user_id::text FROM idp_identity WHERE idp_config_id = $1 AND email = $2 LIMIT 1`,
		idpConfigID, email).Scan(&userID)
	return userID, err
}

// GetIdPDomain returns a domain row by id or pgx.ErrNoRows.
func (r *Repository) GetIdPDomain(ctx context.Context, domainID string) (IdPDomain, error) {
	var d IdPDomain
	err := r.pool.QueryRow(ctx, `
SELECT id::text, idp_config_id::text, team_id::text, domain, verification_token, verified, verified_at, created_at
FROM idp_domain WHERE id = $1`, domainID).Scan(
		&d.ID, &d.IdPConfigID, &d.TeamID, &d.Domain, &d.VerificationToken, &d.Verified, &d.VerifiedAt, &d.CreatedAt)
	return d, err
}

// MarkIdPDomainVerified flips verified=true and stamps verified_at.
func (r *Repository) MarkIdPDomainVerified(ctx context.Context, domainID string) (IdPDomain, error) {
	var d IdPDomain
	err := r.pool.QueryRow(ctx, `
UPDATE idp_domain SET verified = true, verified_at = now()
WHERE id = $1
RETURNING id::text, idp_config_id::text, team_id::text, domain, verification_token, verified, verified_at, created_at`,
		domainID).Scan(&d.ID, &d.IdPConfigID, &d.TeamID, &d.Domain, &d.VerificationToken, &d.Verified, &d.VerifiedAt, &d.CreatedAt)
	return d, err
}

// HasVerifiedDomain reports whether the IdP config has at least one verified
// domain (enforce precondition, spec § 5.2).
func (r *Repository) HasVerifiedDomain(ctx context.Context, idpConfigID string) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM idp_domain WHERE idp_config_id = $1 AND verified = true)`, idpConfigID).Scan(&ok)
	return ok, err
}

// IsDomainVerifiedForConfig reports whether the given domain is verified for the
// config (JIT domain-match guard, spec § 6.1).
func (r *Repository) IsDomainVerifiedForConfig(ctx context.Context, idpConfigID, domain string) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM idp_domain WHERE idp_config_id = $1 AND domain = $2 AND verified = true)`,
		idpConfigID, domain).Scan(&ok)
	return ok, err
}

// JITParams carries the resolved identity for just-in-time provisioning. Role
// is resolved + capped by the caller (admin max, never owner; hard rule #3).
type JITParams struct {
	IdPConfigID string
	TeamID      string
	Subject     string
	Email       string
	Role        string
}

// JITResult reports the provisioning outcome.
type JITResult struct {
	UserID                string
	Role                  string
	ProvisionedMembership bool
}

// JITProvision upserts user_account + idp_identity + team_membership in one
// transaction (spec § 6.1). Idempotent: a repeat login for the same subject
// reuses the user and does not re-provision an active membership.
func (r *Repository) JITProvision(ctx context.Context, p JITParams) (JITResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return JITResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID string
	err = tx.QueryRow(ctx, `
SELECT user_id::text FROM idp_identity WHERE idp_config_id = $1 AND idp_subject = $2`,
		p.IdPConfigID, p.Subject).Scan(&userID)
	switch {
	case err == nil:
		// Existing identity: refresh last_login_at.
		if _, err := tx.Exec(ctx, `
UPDATE idp_identity SET last_login_at = now(), email = $3
WHERE idp_config_id = $1 AND idp_subject = $2`, p.IdPConfigID, p.Subject, p.Email); err != nil {
			return JITResult{}, err
		}
	case errors.Is(err, pgx.ErrNoRows):
		// Resolve or create the user_account (no new user table; spec § 6.1).
		if err := tx.QueryRow(ctx, `SELECT id::text FROM user_account WHERE email = $1 LIMIT 1`, p.Email).Scan(&userID); err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return JITResult{}, err
			}
			if err := tx.QueryRow(ctx, `INSERT INTO user_account (email) VALUES ($1) RETURNING id::text`, p.Email).Scan(&userID); err != nil {
				return JITResult{}, err
			}
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO idp_identity (idp_config_id, user_id, idp_subject, email, last_login_at)
VALUES ($1, $2, $3, $4, now())`, p.IdPConfigID, userID, p.Subject, p.Email); err != nil {
			return JITResult{}, err
		}
	default:
		return JITResult{}, err
	}

	var hasActive bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM team_membership WHERE team_id = $1 AND user_id = $2 AND deactivated_at IS NULL)`,
		p.TeamID, userID).Scan(&hasActive); err != nil {
		return JITResult{}, err
	}

	provisioned := false
	if !hasActive {
		if _, err := tx.Exec(ctx, `
INSERT INTO team_membership (team_id, user_id, role, auth_source, joined_at)
VALUES ($1, $2, $3, 'sso', now())
ON CONFLICT (team_id, user_id) DO UPDATE
SET role = EXCLUDED.role, auth_source = 'sso', deactivated_at = NULL, joined_at = now()`,
			p.TeamID, userID, p.Role); err != nil {
			return JITResult{}, err
		}
		provisioned = true
	}

	if err := tx.Commit(ctx); err != nil {
		return JITResult{}, err
	}
	return JITResult{UserID: userID, Role: p.Role, ProvisionedMembership: provisioned}, nil
}

// IssueSSOToken mints an SSO-issued bearer (kind='device', auth_source='sso',
// linked to idp_config). TTL is fixed at issue time — SSO tokens do not roll
// (spec § 7.3); the caller computes expiresAt via SSOTokenTTL.
func (r *Repository) IssueSSOToken(ctx context.Context, ownerUserID, teamID, idpConfigID string, scopes []string, expiresAt time.Time) (string, error) {
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
INSERT INTO cli_token (owner_user_id, team_id, kind, auth_source, idp_config_id, token_hash, name, scopes, expires_at)
VALUES ($1, $2, 'device', 'sso', $3, $4, 'sso-login', $5, $6)
RETURNING id::text`,
		ownerUserID, teamID, idpConfigID, hash, scopes, expiresAt).Scan(&tokenID); err != nil {
		return "", err
	}
	return sharedtoken.FormatBearerToken("device", tokenID, secret), nil
}

// DeprovisionResult reports what a deprovision touched.
type DeprovisionResult struct {
	MembershipDeactivated bool
	TokensRevoked         int
}

// DeprovisionSSOUser is the central revocation path (spec § 7.2, hard rule #5):
// in one transaction it deactivates the IdP identity + team membership and
// revokes every cli_token the user holds in that team (device + PAT, sso +
// native). A single call covers all of the user's tokens.
func (r *Repository) DeprovisionSSOUser(ctx context.Context, teamID, userID, idpConfigID string) (DeprovisionResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return DeprovisionResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
UPDATE idp_identity SET deactivated_at = now()
WHERE idp_config_id = $1 AND user_id = $2 AND deactivated_at IS NULL`, idpConfigID, userID); err != nil {
		return DeprovisionResult{}, err
	}

	memTag, err := tx.Exec(ctx, `
UPDATE team_membership SET deactivated_at = now()
WHERE team_id = $1 AND user_id = $2 AND deactivated_at IS NULL`, teamID, userID)
	if err != nil {
		return DeprovisionResult{}, err
	}

	tokTag, err := tx.Exec(ctx, `
UPDATE cli_token SET revoked_at = now()
WHERE owner_user_id = $1 AND team_id = $2 AND revoked_at IS NULL`, userID, teamID)
	if err != nil {
		return DeprovisionResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return DeprovisionResult{}, err
	}
	return DeprovisionResult{
		MembershipDeactivated: memTag.RowsAffected() > 0,
		TokensRevoked:         int(tokTag.RowsAffected()),
	}, nil
}

// DeprovisionSSOUserBySubject resolves the user from the IdP subject (used by
// back-channel logout, spec § 7.1) then deprovisions. Returns the user id.
func (r *Repository) DeprovisionSSOUserBySubject(ctx context.Context, teamID, idpConfigID, subject string) (DeprovisionResult, string, error) {
	var userID string
	if err := r.pool.QueryRow(ctx, `
SELECT user_id::text FROM idp_identity WHERE idp_config_id = $1 AND idp_subject = $2`,
		idpConfigID, subject).Scan(&userID); err != nil {
		return DeprovisionResult{}, "", err
	}
	res, err := r.DeprovisionSSOUser(ctx, teamID, userID, idpConfigID)
	return res, userID, err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
