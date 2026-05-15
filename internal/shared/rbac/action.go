// Package rbac provides RBAC utilities.
package rbac

// Action identifies an endpoint-level authorization requirement.
type Action string

const (
	// ActionListApps requires read access to list apps.
	ActionListApps Action = "list_apps"
	// ActionCreateApp requires write access to create apps.
	ActionCreateApp Action = "create_app"
	// ActionRedeploy requires write access to trigger a redeploy.
	ActionRedeploy Action = "redeploy"
	// ActionListTeams requires read access to list teams.
	ActionListTeams Action = "list_teams"
	// ActionListMembers requires admin access to list members.
	ActionListMembers Action = "list_members"
	// ActionInviteMembers requires admin access to invite members.
	ActionInviteMembers Action = "invite_members"
	// ActionRemoveMembers requires admin access to remove members.
	ActionRemoveMembers Action = "remove_members"
	// ActionManageTokens requires admin access to manage tokens.
	ActionManageTokens Action = "manage_tokens"
	// ActionManageGithubApp requires owner role to install or uninstall the
	// GitHub App for a team (github-app-install-flow spec § 14 hard rule #2).
	ActionManageGithubApp Action = "manage_github_app"
)

// Requirement couples minimum role with required scope.
type Requirement struct {
	MinRole       Role
	RequiredScope Scope
}

// RequiredFor returns the authorization requirement for an action.
func RequiredFor(action Action) Requirement {
	switch action {
	case ActionListApps:
		return Requirement{MinRole: RoleViewer, RequiredScope: ScopeAppsRead}
	case ActionCreateApp:
		return Requirement{MinRole: RoleMember, RequiredScope: ScopeAppsWrite}
	case ActionRedeploy:
		return Requirement{MinRole: RoleMember, RequiredScope: ScopeAppsWrite}
	case ActionListTeams:
		return Requirement{RequiredScope: ScopeTeamsRead}
	case ActionListMembers:
		return Requirement{MinRole: RoleAdmin, RequiredScope: ScopeMembersManage}
	case ActionInviteMembers:
		return Requirement{MinRole: RoleAdmin, RequiredScope: ScopeMembersManage}
	case ActionRemoveMembers:
		return Requirement{MinRole: RoleAdmin, RequiredScope: ScopeMembersManage}
	case ActionManageTokens:
		return Requirement{MinRole: RoleAdmin, RequiredScope: ScopeMembersManage}
	case ActionManageGithubApp:
		return Requirement{MinRole: RoleOwner, RequiredScope: ScopeMembersManage}
	default:
		return Requirement{}
	}
}
