package rbac

// Action identifies an endpoint-level authorization requirement.
type Action string

const (
	ActionListApps  Action = "list_apps"
	ActionListTeams Action = "list_teams"
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
	default:
		return Requirement{}
	}
}
