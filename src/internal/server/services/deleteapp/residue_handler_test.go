package deleteapp_test

import (
	"context"
	"testing"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/deleteapp"
)

// TestResidueJobKindMatchesEnqueue guards the producer/consumer contract: the
// kind delete_app enqueues (execute.go) must equal the kind cmd/server
// registers the handler under. A drift here is exactly the bug that left the
// registry empty and every delete stuck in 'deleting'.
func TestResidueJobKindConstant(t *testing.T) {
	if deleteapp.ResidueJobKind != "cleanup_residue" {
		t.Fatalf("ResidueJobKind = %q, want cleanup_residue", deleteapp.ResidueJobKind)
	}
}

// TestResidueHandlerHardDeletesWhenPruned drives the reconciler-facing adapter
// end to end: a cleanup_residue row whose ArgoCD Application is gone must hard
// delete the app and report Completed so the runner finalizes the job.
func TestResidueHandlerHardDeletesWhenPruned(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "deleting")
	ac := &fakeArgoCD{existsResponses: []bool{false}}
	svc := newServiceForTest(t, store, nil, nil, nil, ac)

	handler := svc.ResidueHandler()
	out := handler.Handle(context.Background(), db.ReconciliationJobRow{
		ID:          "job-1",
		TeamID:      "team-1",
		SubjectType: "app",
		SubjectID:   "app-1",
		Kind:        deleteapp.ResidueJobKind,
		Payload: map[string]any{
			"app_id":    "app-1",
			"app_slug":  "nextdemo",
			"team_slug": "team-acme",
		},
	})

	if !out.Completed {
		t.Fatalf("expected Completed outcome, got %+v", out)
	}
	if out.FailedPermanently {
		t.Fatalf("did not expect FailedPermanently, got %+v", out)
	}
	if len(store.deletedApps) != 1 || store.deletedApps[0] != "app-1" {
		t.Fatalf("expected app-1 hard deleted, got %v", store.deletedApps)
	}
}

// TestResidueHandlerRetriesWhileApplicationPresent proves the adapter maps the
// retry path: ArgoCD still reports the Application present → no hard delete and
// a non-terminal outcome so the runner reschedules.
func TestResidueHandlerRetriesWhileApplicationPresent(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "deleting")
	ac := &fakeArgoCD{existsResponses: []bool{true}}
	svc := newServiceForTest(t, store, nil, nil, nil, ac)

	handler := svc.ResidueHandler()
	out := handler.Handle(context.Background(), db.ReconciliationJobRow{
		ID:          "job-1",
		TeamID:      "team-1",
		SubjectType: "app",
		SubjectID:   "app-1",
		Kind:        deleteapp.ResidueJobKind,
		Payload:     map[string]any{"app_slug": "nextdemo", "team_slug": "team-acme"},
	})

	if out.Completed || out.FailedPermanently {
		t.Fatalf("expected non-terminal retry outcome, got %+v", out)
	}
	if len(store.deletedApps) != 0 {
		t.Fatalf("must not hard delete while application present, got %v", store.deletedApps)
	}
}

// TestResidueHandlerFallsBackToSubjectID confirms AppID resolves from the row's
// subject_id when the payload omits app_id (older enqueues / partial payloads).
func TestResidueHandlerFallsBackToSubjectID(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-9", "legacy", "deleting")
	ac := &fakeArgoCD{existsResponses: []bool{false}}
	svc := newServiceForTest(t, store, nil, nil, nil, ac)

	handler := svc.ResidueHandler()
	out := handler.Handle(context.Background(), db.ReconciliationJobRow{
		ID:          "job-2",
		TeamID:      "team-1",
		SubjectType: "app",
		SubjectID:   "app-9",
		Kind:        deleteapp.ResidueJobKind,
		Payload:     map[string]any{"app_slug": "legacy", "team_slug": "team-acme"}, // no app_id
	})

	if !out.Completed {
		t.Fatalf("expected Completed, got %+v", out)
	}
	if len(store.deletedApps) != 1 || store.deletedApps[0] != "app-9" {
		t.Fatalf("expected app-9 hard deleted via subject_id fallback, got %v", store.deletedApps)
	}
}
