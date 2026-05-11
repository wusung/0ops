package rbac

// Action identifies an endpoint-level authorization requirement.
type Action string

const (
	ActionListApps         Action = "list_apps"
	ActionListTeams        Action = "list_teams"
	ActionListMembers      Action = "list_members"
	ActionInviteMembers    Action = "invite_members"
	ActionRemoveMembers    Action = "remove_members"
	ActionManageTokens     Action = "manage_tokens"
	ActionManageGithubApp  Action = "manage_github_app"
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
		return Requirement{MinRole: RoleAdmin, RequiredScope: ScopeMembersManage}
	default:
		return Requirement{}
	}
}
