// Package rbac provides RBAC utilities.
package rbac

// Action identifies an endpoint-level authorization requirement.
type Action string

const (
	// ActionListApps requires read access to list apps.
	ActionListApps Action = "list_apps"
	// ActionCreateApp requires write access to create apps.
	ActionCreateApp Action = "create_app"
	// ActionDeleteApp requires admin role plus delete scope (delete-app-flow
	// spec § 13 hard rule #2).
	ActionDeleteApp Action = "delete_app"
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
	// ActionListAudit requires admin role + audit:read scope to query the
	// team's full audit_log (audit-log spec § 6.2).
	ActionListAudit Action = "list_audit"
	// ActionListSelfAudit requires viewer role + audit:read scope to query
	// audit_log restricted to the calling actor (audit-log spec § 6.2).
	ActionListSelfAudit Action = "list_self_audit"
	// ActionExportAudit requires admin role + audit:export scope to bulk-export
	// the team's audit_log (audit-export-and-integrity spec § 6.2, hard rule #6).
	ActionExportAudit Action = "export_audit"
	// ActionListIncidents requires viewer role + incidents:read scope
	// to list / get incident rows (reconciler-and-incident spec § 9.3).
	ActionListIncidents Action = "list_incidents"
	// ActionCloseIncident requires admin role + incidents:write scope
	// to close an incident (reconciler-and-incident spec § 9.3, § 16 #8).
	ActionCloseIncident Action = "close_incident"
	// ActionCreateUpload requires member role + apps:write scope to upload an
	// app-source archive (ADR-0013 § 5.1). Same grant set as ActionCreateApp
	// (Owner, Admin, Member; NOT Viewer).
	ActionCreateUpload Action = "create_upload"
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
	case ActionDeleteApp:
		return Requirement{MinRole: RoleAdmin, RequiredScope: ScopeAppsDelete}
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
	case ActionListAudit:
		return Requirement{MinRole: RoleAdmin, RequiredScope: ScopeAuditRead}
	case ActionListSelfAudit:
		return Requirement{MinRole: RoleViewer, RequiredScope: ScopeAuditRead}
	case ActionExportAudit:
		return Requirement{MinRole: RoleAdmin, RequiredScope: ScopeAuditExport}
	case ActionListIncidents:
		return Requirement{MinRole: RoleViewer, RequiredScope: ScopeIncidentsRead}
	case ActionCloseIncident:
		return Requirement{MinRole: RoleAdmin, RequiredScope: ScopeIncidentsWrite}
	case ActionCreateUpload:
		return Requirement{MinRole: RoleMember, RequiredScope: ScopeAppsWrite}
	default:
		return Requirement{}
	}
}
