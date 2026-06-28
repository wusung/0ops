package sso

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/shared/rbac"
)

// Audit actions for SSO (sso-saml spec § 9.1). IdP-initiated rows
// (back-channel) use source=system + nil actor (hard rule #8).
const (
	ActionSSOLogin         = "sso_login"
	ActionSSOLogout        = "sso_logout"
	ActionSSOProvisionUser = "sso_provision_user"
	ActionSSODeprovision   = "sso_deprovision_user"
	ActionSSOConfigCreate  = "sso_config_create"
	ActionSSOConfigUpdate  = "sso_config_update"
	ActionSSOConfigDelete  = "sso_config_delete"
	ActionSSODomainVerify  = "sso_domain_verify"
)

// defaultSSOScopes mirror the device-flow grant set plus sso:manage so an
// SSO-logged-in owner can still drive the SSO CLI; role gating (owner for
// writes, admin for reads) is enforced at the middleware, exactly as
// members:manage works (apps.go device poll).
func defaultSSOScopes() []string {
	return []string{
		string(rbac.ScopeAppsRead),
		string(rbac.ScopeTeamsRead),
		string(rbac.ScopeMembersManage),
		string(rbac.ScopeSSOManage),
	}
}

// Store is the DB surface the SSO handlers need (satisfied by *db.Repository).
type Store interface {
	ResolveTeamBySlug(ctx context.Context, slug string) (db.Team, error)
	CreateIdPConfig(ctx context.Context, p db.CreateIdPConfigParams) (db.IdPConfig, error)
	GetIdPConfigByTeam(ctx context.Context, teamID string) (db.IdPConfig, error)
	UpdateIdPConfig(ctx context.Context, teamID string, patch db.IdPConfigPatch) (db.IdPConfig, error)
	DeleteIdPConfig(ctx context.Context, teamID string) error
	AddIdPDomain(ctx context.Context, idpConfigID, teamID, domain, verificationToken string) (db.IdPDomain, error)
	GetIdPDomain(ctx context.Context, domainID string) (db.IdPDomain, error)
	ListIdPDomains(ctx context.Context, idpConfigID string) ([]db.IdPDomain, error)
	MarkIdPDomainVerified(ctx context.Context, domainID string) (db.IdPDomain, error)
	FindSSOUserByEmail(ctx context.Context, idpConfigID, email string) (string, error)
	HasVerifiedDomain(ctx context.Context, idpConfigID string) (bool, error)
	IsDomainVerifiedForConfig(ctx context.Context, idpConfigID, domain string) (bool, error)
	JITProvision(ctx context.Context, p db.JITParams) (db.JITResult, error)
	IssueSSOToken(ctx context.Context, ownerUserID, teamID, idpConfigID string, scopes []string, expiresAt time.Time) (string, error)
	DeprovisionSSOUser(ctx context.Context, teamID, userID, idpConfigID string) (db.DeprovisionResult, error)
	DeprovisionSSOUserBySubject(ctx context.Context, teamID, idpConfigID, subject string) (db.DeprovisionResult, string, error)
}

// AuditWriter writes audit rows (satisfied by *audit.Service).
type AuditWriter interface {
	Log(ctx context.Context, entry audit.Entry) error
}

// IDTokenVerifier verifies OIDC id_tokens and back-channel logout tokens
// (satisfied by *Verifier).
type IDTokenVerifier interface {
	Discover(ctx context.Context, issuer string) (Discovery, error)
	VerifyIDToken(ctx context.Context, disc Discovery, clientID, rawIDToken, groupClaim string) (Claims, error)
	VerifyLogoutToken(ctx context.Context, disc Discovery, clientID, rawLogoutToken string) (string, error)
}

// CodeExchanger swaps an authorization code for a raw id_token at the IdP token
// endpoint. The real HTTP implementation needs the IdP client secret + a live
// IdP, so it is deferred-validation; tests inject a stub returning a signed
// id_token (sso-saml spec § 8.1).
type CodeExchanger interface {
	Exchange(ctx context.Context, disc Discovery, clientID, clientSecret, code, redirectURI, codeVerifier string) (rawIDToken string, idpExpiry time.Time, err error)
}

// SecretStore persists the IdP client secret out of the DB (hard rule #7). The
// default is process-local; durable at-rest encryption is deferred to
// secrets-management.
type SecretStore interface {
	Put(ref, secret string) error
	Get(ref string) (string, error)
}

// StateStore correlates the OIDC state nonce with its PKCE verifier + redirect
// across the authorize → callback hop (one-time consume).
type StateStore interface {
	Save(state string, data StateData)
	Consume(state string) (StateData, bool)
}

// StateData is the per-login correlation payload.
type StateData struct {
	TeamSlug     string
	RedirectURI  string
	CodeVerifier string
}

// DNSResolver looks up TXT records for domain verification (injectable so tests
// avoid real DNS; spec § 5.2).
type DNSResolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// Service bundles the SSO handler dependencies.
type Service struct {
	Store     Store
	Audit     AuditWriter
	Verifier  IDTokenVerifier
	Exchanger CodeExchanger
	Secrets   SecretStore
	State     StateStore
	Resolver  DNSResolver
	BaseURL   string
	now       func() time.Time
}

// NewService builds an SSO service. A nil now defaults to time.Now().UTC.
func NewService(store Store, auditW AuditWriter, verifier IDTokenVerifier, exchanger CodeExchanger, secrets SecretStore, state StateStore, resolver DNSResolver, baseURL string) *Service {
	return &Service{
		Store:     store,
		Audit:     auditW,
		Verifier:  verifier,
		Exchanger: exchanger,
		Secrets:   secrets,
		State:     state,
		Resolver:  resolver,
		BaseURL:   strings.TrimRight(baseURL, "/"),
		now:       func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now().UTC()
}

// callbackRedirectURI is the IdP redirect target for a team (spec § 17: derived
// from OPS_HOST / BaseURL).
func (s *Service) callbackRedirectURI(teamSlug string) string {
	return s.BaseURL + "/v1/auth/sso/" + url.PathEscape(teamSlug) + "/callback"
}

// PATDisabled reports whether personal access tokens are blocked for the team
// (enforce + pat_policy=disallow; spec § 7.4).
func PATDisabled(cfg db.IdPConfig) bool {
	return cfg.Enforce && cfg.PATPolicy == "disallow"
}

// ResolveJITRole maps the verified IdP groups to a 0ops role, capped at admin —
// JIT never grants owner (hard rule #3). Multiple matching groups take the
// highest; unmatched falls back to jit_default_role.
func ResolveJITRole(cfg db.IdPConfig, groups []string) string {
	role := cfg.JITDefaultRole
	if cfg.GroupClaim != nil && *cfg.GroupClaim != "" && len(cfg.GroupRoleMap) > 0 {
		for _, g := range groups {
			if mapped, ok := cfg.GroupRoleMap[g]; ok && roleRank(mapped) > roleRank(role) {
				role = mapped
			}
		}
	}
	return capRole(role)
}

func roleRank(r string) int {
	switch rbac.Role(r) {
	case rbac.RoleViewer:
		return 0
	case rbac.RoleMember:
		return 1
	case rbac.RoleAdmin:
		return 2
	case rbac.RoleOwner:
		return 3
	default:
		return -1
	}
}

func capRole(r string) string {
	switch {
	case roleRank(r) < 0:
		return string(rbac.RoleMember)
	case roleRank(r) >= roleRank(string(rbac.RoleOwner)):
		return string(rbac.RoleAdmin) // never owner via JIT
	default:
		return r
	}
}

// BuildAuthorizeURL constructs the IdP authorization-code + PKCE URL
// (spec § 8.1, auth-login-flow § 4.3 PKCE).
func BuildAuthorizeURL(disc Discovery, cfg db.IdPConfig, redirectURI, state, codeChallenge string) (string, error) {
	u, err := url.Parse(disc.AuthorizationEndpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	scope := strings.Join(cfg.Scopes, " ")
	if scope == "" {
		scope = "openid email profile"
	}
	q.Set("scope", scope)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}
