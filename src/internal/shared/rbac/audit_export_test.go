package rbac

import "testing"

// TestExportAuditRequiresAdminAndExportScope pins the slice-c RBAC contract
// (audit-export-and-integrity spec § 6.2; hard rule #6): bulk export is gated
// on admin role plus the dedicated audit:export scope, deliberately NOT reusing
// audit:read, so export can be granted / revoked independently of paged reads.
func TestExportAuditRequiresAdminAndExportScope(t *testing.T) {
	req := RequiredFor(ActionExportAudit)

	if req.MinRole != RoleAdmin {
		t.Fatalf("export min role = %q, want %q", req.MinRole, RoleAdmin)
	}
	if req.RequiredScope != ScopeAuditExport {
		t.Fatalf("export scope = %q, want %q", req.RequiredScope, ScopeAuditExport)
	}
	if ScopeAuditExport == ScopeAuditRead {
		t.Fatal("audit:export must be distinct from audit:read (hard rule #6)")
	}
}
