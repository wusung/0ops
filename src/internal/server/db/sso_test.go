package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	dbpkg "github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared/token"
)

func seedIdPConfig(ctx context.Context, t *testing.T, repo *dbpkg.Repository, teamID string) dbpkg.IdPConfig {
	t.Helper()
	cfg, err := repo.CreateIdPConfig(ctx, dbpkg.CreateIdPConfigParams{
		TeamID:          teamID,
		Issuer:          "https://idp.example.com",
		ClientID:        "client-123",
		ClientSecretRef: "secret://idp/client",
		Scopes:          []string{"openid", "email", "profile"},
		JITDefaultRole:  "member",
		PATPolicy:       "allow",
		SessionMaxTTLS:  28800,
	})
	if err != nil {
		t.Fatalf("CreateIdPConfig: %v", err)
	}
	return cfg
}

// TestCheckTeamMembershipExcludesDeactivated pins that a deprovisioned member
// (deactivated_at != null) is treated as a non-member, so CheckMembership
// returns 404 team-wide (sso-saml spec § 7.2, hard rule #5).
func TestCheckTeamMembershipExcludesDeactivated(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "ssomb", "SSO MB")
	userID := seedUser(ctx, t, pool, "ssomb-user")
	seedMembership(ctx, t, pool, teamID, userID, "member")

	ok, err := repo.CheckTeamMembership(ctx, teamID, userID)
	if err != nil || !ok {
		t.Fatalf("active member CheckTeamMembership = %v, %v; want true", ok, err)
	}

	if _, err := pool.Exec(ctx, `UPDATE team_membership SET deactivated_at = now() WHERE team_id = $1 AND user_id = $2`, teamID, userID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	ok, err = repo.CheckTeamMembership(ctx, teamID, userID)
	if err != nil {
		t.Fatalf("CheckTeamMembership after deactivate: %v", err)
	}
	if ok {
		t.Fatal("deactivated member must read as non-member (404 enumeration)")
	}
}

func TestCreateAndGetIdPConfig(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "ssocfg", "SSO CFG")

	cfg := seedIdPConfig(ctx, t, repo, teamID)
	if cfg.Protocol != "oidc" {
		t.Fatalf("protocol = %q, want oidc", cfg.Protocol)
	}
	if cfg.Enforce {
		t.Fatal("enforce should default false")
	}
	if cfg.JITDefaultRole != "member" {
		t.Fatalf("jit_default_role = %q, want member", cfg.JITDefaultRole)
	}

	got, err := repo.GetIdPConfigByTeam(ctx, teamID)
	if err != nil {
		t.Fatalf("GetIdPConfigByTeam: %v", err)
	}
	if got.ID != cfg.ID || got.ClientSecretRef != "secret://idp/client" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestCreateIdPConfigRejectsSecondPerTeam(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "sso1team", "SSO 1 Team")
	seedIdPConfig(ctx, t, repo, teamID)

	_, err := repo.CreateIdPConfig(ctx, dbpkg.CreateIdPConfigParams{
		TeamID:          teamID,
		Issuer:          "https://idp2.example.com",
		ClientID:        "c2",
		ClientSecretRef: "secret://idp2",
		JITDefaultRole:  "member",
		PATPolicy:       "allow",
		SessionMaxTTLS:  28800,
	})
	if !errors.Is(err, dbpkg.ErrIdPConfigExists) {
		t.Fatalf("second config err = %v, want ErrIdPConfigExists", err)
	}
}

func TestGetIdPConfigByTeamNotFound(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "ssonone", "SSO None")
	_, err := repo.GetIdPConfigByTeam(ctx, teamID)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing config err = %v, want pgx.ErrNoRows", err)
	}
}

func TestUpdateAndDeleteIdPConfig(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "ssoupd", "SSO Upd")
	seedIdPConfig(ctx, t, repo, teamID)

	enforce := true
	pat := "disallow"
	updated, err := repo.UpdateIdPConfig(ctx, teamID, dbpkg.IdPConfigPatch{Enforce: &enforce, PATPolicy: &pat})
	if err != nil {
		t.Fatalf("UpdateIdPConfig: %v", err)
	}
	if !updated.Enforce || updated.PATPolicy != "disallow" {
		t.Fatalf("update mismatch: enforce=%v pat=%q", updated.Enforce, updated.PATPolicy)
	}

	if err := repo.DeleteIdPConfig(ctx, teamID); err != nil {
		t.Fatalf("DeleteIdPConfig: %v", err)
	}
	if _, err := repo.GetIdPConfigByTeam(ctx, teamID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("after delete err = %v, want pgx.ErrNoRows", err)
	}
}

func TestDomainAddVerifyAndUniqueness(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamA, _ := seedTeam(ctx, t, pool, "ssodomA", "SSO Dom A")
	teamB, _ := seedTeam(ctx, t, pool, "ssodomB", "SSO Dom B")
	cfgA := seedIdPConfig(ctx, t, repo, teamA)
	cfgB := seedIdPConfig(ctx, t, repo, teamB)

	domain := uniqueSuffix(t, "acme") + ".example"
	dom, err := repo.AddIdPDomain(ctx, cfgA.ID, teamA, domain, "verify-token-A")
	if err != nil {
		t.Fatalf("AddIdPDomain: %v", err)
	}
	if dom.Verified {
		t.Fatal("new domain must be unverified")
	}

	has, err := repo.HasVerifiedDomain(ctx, cfgA.ID)
	if err != nil || has {
		t.Fatalf("HasVerifiedDomain before verify = %v, %v; want false", has, err)
	}

	verified, err := repo.MarkIdPDomainVerified(ctx, dom.ID)
	if err != nil {
		t.Fatalf("MarkIdPDomainVerified: %v", err)
	}
	if !verified.Verified || verified.VerifiedAt == nil {
		t.Fatal("domain should be verified with timestamp")
	}

	ok, err := repo.IsDomainVerifiedForConfig(ctx, cfgA.ID, domain)
	if err != nil || !ok {
		t.Fatalf("IsDomainVerifiedForConfig = %v, %v; want true", ok, err)
	}

	// Same domain cannot bind to another team's IdP (global UNIQUE, hard rule #9).
	if _, err := repo.AddIdPDomain(ctx, cfgB.ID, teamB, domain, "verify-token-B"); !errors.Is(err, dbpkg.ErrDomainTaken) {
		t.Fatalf("cross-team domain err = %v, want ErrDomainTaken", err)
	}
}

func TestJITProvisionCreatesUserMembershipIdentity(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "ssojit", "SSO JIT")
	cfg := seedIdPConfig(ctx, t, repo, teamID)

	email := uniqueSuffix(t, "alice") + "@acme.example"
	res, err := repo.JITProvision(ctx, dbpkg.JITParams{
		IdPConfigID: cfg.ID,
		TeamID:      teamID,
		Subject:     "sub-alice-1",
		Email:       email,
		Role:        "member",
	})
	if err != nil {
		t.Fatalf("JITProvision: %v", err)
	}
	if res.UserID == "" || !res.ProvisionedMembership {
		t.Fatalf("first JIT result = %+v", res)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM user_account WHERE id = $1`, res.UserID) })

	role, err := repo.GetTeamMembershipRole(ctx, teamID, res.UserID)
	if err != nil || role != "member" {
		t.Fatalf("membership role = %q, %v; want member", role, err)
	}

	// Second login for same subject reuses the identity, no new membership.
	res2, err := repo.JITProvision(ctx, dbpkg.JITParams{
		IdPConfigID: cfg.ID,
		TeamID:      teamID,
		Subject:     "sub-alice-1",
		Email:       email,
		Role:        "member",
	})
	if err != nil {
		t.Fatalf("second JITProvision: %v", err)
	}
	if res2.UserID != res.UserID || res2.ProvisionedMembership {
		t.Fatalf("second JIT must reuse user without new membership: %+v", res2)
	}
}

func TestDeprovisionRevokesAllTokensAndMembership(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "ssodep", "SSO Dep")
	cfg := seedIdPConfig(ctx, t, repo, teamID)

	email := uniqueSuffix(t, "bob") + "@acme.example"
	jit, err := repo.JITProvision(ctx, dbpkg.JITParams{
		IdPConfigID: cfg.ID, TeamID: teamID, Subject: "sub-bob-1", Email: email, Role: "member",
	})
	if err != nil {
		t.Fatalf("JITProvision: %v", err)
	}
	userID := jit.UserID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM user_account WHERE id = $1`, userID) })

	// User holds 2 device (SSO) tokens + 1 PAT (spec § 7.2 "one covers all").
	if _, err := repo.IssueSSOToken(ctx, userID, teamID, cfg.ID, []string{"apps:read"}, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("IssueSSOToken 1: %v", err)
	}
	if _, err := repo.IssueSSOToken(ctx, userID, teamID, cfg.ID, []string{"apps:read"}, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("IssueSSOToken 2: %v", err)
	}
	if _, err := repo.CreatePAT(ctx, userID, teamID, "ci-pat", []string{"apps:read"}, time.Now().UTC().Add(24*time.Hour)); err != nil {
		t.Fatalf("CreatePAT: %v", err)
	}

	res, err := repo.DeprovisionSSOUser(ctx, teamID, userID, cfg.ID)
	if err != nil {
		t.Fatalf("DeprovisionSSOUser: %v", err)
	}
	if !res.MembershipDeactivated || res.TokensRevoked != 3 {
		t.Fatalf("deprovision result = %+v; want membership + 3 tokens", res)
	}

	// Membership now reads as non-member (404 path).
	ok, _ := repo.CheckTeamMembership(ctx, teamID, userID)
	if ok {
		t.Fatal("membership must be deactivated after deprovision")
	}

	// Every token row carries revoked_at.
	var live int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM cli_token WHERE owner_user_id = $1 AND team_id = $2 AND revoked_at IS NULL`, userID, teamID).Scan(&live); err != nil {
		t.Fatalf("count live tokens: %v", err)
	}
	if live != 0 {
		t.Fatalf("live tokens after deprovision = %d, want 0", live)
	}
}

func TestIssueSSOTokenSetsAuthSourceAndLink(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "ssotok", "SSO Tok")
	userID := seedUser(ctx, t, pool, "ssotok-user")
	cfg := seedIdPConfig(ctx, t, repo, teamID)

	bearer, err := repo.IssueSSOToken(ctx, userID, teamID, cfg.ID, []string{"apps:read"}, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("IssueSSOToken: %v", err)
	}
	parsed, err := token.ParseBearerToken(bearer)
	if err != nil {
		t.Fatalf("ParseBearerToken: %v", err)
	}

	var authSource, kind string
	var linkedCfg *string
	if err := pool.QueryRow(ctx, `SELECT auth_source, kind, idp_config_id::text FROM cli_token WHERE id = $1`, parsed.ID).Scan(&authSource, &kind, &linkedCfg); err != nil {
		t.Fatalf("load token row: %v", err)
	}
	if authSource != "sso" || kind != "device" {
		t.Fatalf("token auth_source=%q kind=%q; want sso/device", authSource, kind)
	}
	if linkedCfg == nil || *linkedCfg != cfg.ID {
		t.Fatalf("idp_config_id link = %v, want %s", linkedCfg, cfg.ID)
	}
}
