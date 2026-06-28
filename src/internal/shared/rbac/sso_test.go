package rbac

import "testing"

// TestManageSSORequiresOwnerAndSSOScope pins the SSO write contract (sso-saml
// spec § 12, hard rule #4): creating / updating / deleting the IdP config,
// verifying a domain, toggling enforce, and deprovisioning are owner-only and
// gated on the dedicated sso:manage scope.
func TestManageSSORequiresOwnerAndSSOScope(t *testing.T) {
	req := RequiredFor(ActionManageSSO)
	if req.MinRole != RoleOwner {
		t.Fatalf("manage_sso min role = %q, want %q", req.MinRole, RoleOwner)
	}
	if req.RequiredScope != ScopeSSOManage {
		t.Fatalf("manage_sso scope = %q, want %q", req.RequiredScope, ScopeSSOManage)
	}
}

// TestReadSSORequiresAdminAndSSOScope pins that admins may read the IdP config
// (without secrets) but the read still requires the sso:manage scope (spec
// § 5.1, § 12).
func TestReadSSORequiresAdminAndSSOScope(t *testing.T) {
	req := RequiredFor(ActionReadSSO)
	if req.MinRole != RoleAdmin {
		t.Fatalf("read_sso min role = %q, want %q", req.MinRole, RoleAdmin)
	}
	if req.RequiredScope != ScopeSSOManage {
		t.Fatalf("read_sso scope = %q, want %q", req.RequiredScope, ScopeSSOManage)
	}
}

// TestSSOScopeIsDistinct guards hard rule #4: sso:manage is its own scope, not
// an alias of members:manage, so SSO control can be granted independently.
func TestSSOScopeIsDistinct(t *testing.T) {
	if ScopeSSOManage == ScopeMembersManage {
		t.Fatal("sso:manage must be distinct from members:manage (hard rule #4)")
	}
}
