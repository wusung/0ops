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
)
