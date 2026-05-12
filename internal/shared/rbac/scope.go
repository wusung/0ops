package rbac

// Scope is a bearer token permission.
type Scope string

const (
	// ScopeAppsRead grants read access to apps.
	ScopeAppsRead Scope = "apps:read"
	// ScopeTeamsRead grants read access to teams.
	ScopeTeamsRead Scope = "teams:read"
	// ScopeMembersManage grants management access to members.
	ScopeMembersManage Scope = "members:manage"
)
