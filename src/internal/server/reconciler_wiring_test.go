package server

import (
	"testing"

	"github.com/winshare/zeroops/internal/server/services/deleteapp"
	"github.com/winshare/zeroops/internal/server/services/reconciler"
)

// TestRegisterReconcilerHandlersRegistersCleanupResidue is the regression guard
// for the wiring bug that left the reconciler registry empty: every app delete
// enqueued a cleanup_residue job that nothing handled, so apps stuck in
// 'deleting' forever ("reconciler: unknown job kind"). The handler closure is
// never invoked here, so a nil store is fine — we only assert the kind resolves.
func TestRegisterReconcilerHandlersRegistersCleanupResidue(t *testing.T) {
	reg := reconciler.NewHandlerRegistry()

	if _, ok := reg.Lookup(deleteapp.ResidueJobKind); ok {
		t.Fatal("precondition: fresh registry must not already know cleanup_residue")
	}

	RegisterReconcilerHandlers(reg, nil)

	if _, ok := reg.Lookup(deleteapp.ResidueJobKind); !ok {
		t.Fatalf("RegisterReconcilerHandlers did not register %q", deleteapp.ResidueJobKind)
	}
}
