package rbac

import "testing"

func TestManageWebhookRequiresAdminAndWriteScope(t *testing.T) {
	req := RequiredFor(ActionManageWebhook)
	if req.MinRole != RoleAdmin {
		t.Errorf("manage_webhook min role = %q, want %q", req.MinRole, RoleAdmin)
	}
	if req.RequiredScope != ScopeWebhookWrite {
		t.Errorf("manage_webhook scope = %q, want %q", req.RequiredScope, ScopeWebhookWrite)
	}
}

func TestReadWebhookRequiresAdminAndReadScope(t *testing.T) {
	req := RequiredFor(ActionReadWebhook)
	if req.MinRole != RoleAdmin {
		t.Errorf("read_webhook min role = %q, want %q", req.MinRole, RoleAdmin)
	}
	if req.RequiredScope != ScopeWebhookRead {
		t.Errorf("read_webhook scope = %q, want %q", req.RequiredScope, ScopeWebhookRead)
	}
}

// Member / viewer must not satisfy webhook actions (spec § 10: member/viewer
// rejected with forbidden_role).
func TestWebhookActionsRejectMemberAndViewer(t *testing.T) {
	for _, action := range []Action{ActionManageWebhook, ActionReadWebhook} {
		req := RequiredFor(action)
		for _, role := range []Role{RoleMember, RoleViewer} {
			if AtLeast(role, req.MinRole) {
				t.Errorf("%s: role %q unexpectedly satisfies min role %q", action, role, req.MinRole)
			}
		}
	}
}
