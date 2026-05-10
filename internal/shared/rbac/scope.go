package rbac

// Scope is a bearer token permission.
type Scope string

const (
	ScopeAppsRead      Scope = "apps:read"
	ScopeMembersManage Scope = "members:manage"
)
