package rbac

// Scope is a bearer token permission.
type Scope string

const (
	// ScopeAppsRead grants read access to apps.
	ScopeAppsRead Scope = "apps:read"
	// ScopeAppsWrite grants write access to apps.
	ScopeAppsWrite Scope = "apps:write"
	// ScopeAppsDelete grants delete access to apps (delete-app-flow spec § 1).
	ScopeAppsDelete Scope = "apps:delete"
	// ScopeTeamsRead grants read access to teams.
	ScopeTeamsRead Scope = "teams:read"
	// ScopeMembersManage grants management access to members.
	ScopeMembersManage Scope = "members:manage"
	// ScopeAuditRead grants read access to audit_log entries
	// (audit-log spec § 6.2).
	ScopeAuditRead Scope = "audit:read"
	// ScopeAuditExport grants bulk export of audit_log for forensics /
	// compliance. Deliberately separate from audit:read so bulk extraction can
	// be granted / revoked independently (audit-export-and-integrity spec § 6.2,
	// hard rule #6).
	ScopeAuditExport Scope = "audit:export"
	// ScopeIncidentsRead grants read access to incident rows
	// (reconciler-and-incident spec § 9.3).
	ScopeIncidentsRead Scope = "incidents:read"
	// ScopeIncidentsWrite grants close access to incidents (spec § 9.3).
	ScopeIncidentsWrite Scope = "incidents:write"
	// ScopeSSOManage grants management of a team's SSO / external-identity
	// configuration. Deliberately distinct from members:manage so SSO control
	// can be granted / revoked independently and stays owner-only for writes
	// (sso-saml spec § 12, ADR-0016, hard rule #4).
	ScopeSSOManage Scope = "sso:manage"
)
