package dto

import "time"

// SSOConfigRequest creates a team's IdP config. ClientSecret is write-only and
// never echoed back; the server stores only a secret ref (hard rule #7).
//
//nolint:revive // exported for public API
type SSOConfigRequest struct {
	Issuer         string            `json:"issuer"`
	ClientID       string            `json:"client_id"`
	ClientSecret   string            `json:"client_secret"`
	DisplayName    string            `json:"display_name,omitempty"`
	DiscoveryURL   string            `json:"discovery_url,omitempty"`
	Scopes         []string          `json:"scopes,omitempty"`
	JITDefaultRole string            `json:"jit_default_role,omitempty"`
	PATPolicy      string            `json:"pat_policy,omitempty"`
	GroupClaim     string            `json:"group_claim,omitempty"`
	GroupRoleMap   map[string]string `json:"group_role_map,omitempty"`
}

// SSOConfigPatchRequest updates mutable IdP config fields; nil fields are left
// unchanged. Enforcing SSO requires at least one verified domain.
//
//nolint:revive // exported for public API
type SSOConfigPatchRequest struct {
	Enforce        *bool   `json:"enforce,omitempty"`
	PATPolicy      *string `json:"pat_policy,omitempty"`
	JITDefaultRole *string `json:"jit_default_role,omitempty"`
	DisplayName    *string `json:"display_name,omitempty"`
}

// SSODomainStatus is one domain and its verification state.
//
//nolint:revive // exported for public API
type SSODomainStatus struct {
	Domain   string `json:"domain"`
	Verified bool   `json:"verified"`
}

// SSOStatus is the secret-free IdP status returned by GET /sso and rendered by
// `0ops sso status`.
//
//nolint:revive // exported for public API
type SSOStatus struct {
	Protocol       string            `json:"protocol"`
	Issuer         string            `json:"issuer"`
	Enforce        bool              `json:"enforce"`
	JITDefaultRole string            `json:"jit_default_role"`
	PATPolicy      string            `json:"pat_policy"`
	Domains        []SSODomainStatus `json:"domains"`
}

// SSOAddDomainRequest registers a domain for DNS TXT verification.
//
//nolint:revive // exported for public API
type SSOAddDomainRequest struct {
	Domain string `json:"domain"`
}

// SSOAddDomainResponse returns the DNS TXT record the owner must publish.
//
//nolint:revive // exported for public API
type SSOAddDomainResponse struct {
	DomainID       string `json:"domain_id"`
	Domain         string `json:"domain"`
	DNSRecordName  string `json:"dns_record_name"`
	DNSRecordValue string `json:"dns_record_value"`
}

// SSODeprovisionRequest names the user to deprovision (email or user id).
//
//nolint:revive // exported for public API
type SSODeprovisionRequest struct {
	User string `json:"user"`
}

// SSODeprovisionResponse reports the central-revocation outcome.
//
//nolint:revive // exported for public API
type SSODeprovisionResponse struct {
	MembershipDeactivated bool   `json:"membership_deactivated"`
	TokensRevoked         int    `json:"tokens_revoked"`
	Message               string `json:"message"`
}

// SSOCallbackResponse hands the minted SSO bearer back to the device-flow poll
// loop (spec § 8.1).
//
//nolint:revive // exported for public API
type SSOCallbackResponse struct {
	BearerToken string    `json:"bearer_token"`
	TeamSlug    string    `json:"team_slug"`
	Email       string    `json:"email"`
	IssuedAt    time.Time `json:"issued_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}
