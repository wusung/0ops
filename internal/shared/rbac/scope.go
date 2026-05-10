package rbac

// Scope is a bearer token permission.
type Scope string

const (
	ScopeAppsRead  Scope = "apps:read"
	ScopeTeamsRead Scope = "teams:read"
)
