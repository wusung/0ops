package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
)

type fakeArgoFetcher struct {
	byTeamApp map[string]ApplicationStatus
}

func (f *fakeArgoFetcher) GetApplicationStatus(_ context.Context, teamSlug, appSlug string) (ApplicationStatus, error) {
	return f.byTeamApp[teamSlug+"/"+appSlug], nil
}

func TestArgoSyncScannerTransitionsSyncingToLive(t *testing.T) {
	store := newFakeStore()
	started := time.Now().Add(-16 * time.Minute)
	store.stuckSyncing = []db.StuckDeployRun{
		{ID: "deploy-1", AppSlug: "demo", TeamSlug: "acme", Status: "syncing", StartedAt: &started},
	}
	store.deployStatuses["deploy-1"] = "syncing"
	fetcher := &fakeArgoFetcher{
		byTeamApp: map[string]ApplicationStatus{
			"acme/demo": {SyncStatus: "Synced", HealthStatus: "Healthy"},
		},
	}
	scanner := NewArgoSyncScanner(store, fetcher, NopObserver())
	n, err := scanner.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Fatalf("transitions = %d, want 1", n)
	}
	tr := store.transitions[0]
	if tr.ToStatus != "live" {
		t.Fatalf("to = %s, want live", tr.ToStatus)
	}
}

func TestArgoSyncScannerTransitionsSyncingToFailedOnDegraded(t *testing.T) {
	store := newFakeStore()
	started := time.Now().Add(-16 * time.Minute)
	store.stuckSyncing = []db.StuckDeployRun{
		{ID: "deploy-1", AppSlug: "demo", TeamSlug: "acme", Status: "syncing", StartedAt: &started},
	}
	store.deployStatuses["deploy-1"] = "syncing"
	fetcher := &fakeArgoFetcher{
		byTeamApp: map[string]ApplicationStatus{
			"acme/demo": {SyncStatus: "Synced", HealthStatus: "Degraded"},
		},
	}
	scanner := NewArgoSyncScanner(store, fetcher, NopObserver())
	n, _ := scanner.Tick(context.Background())
	if n != 1 {
		t.Fatalf("transitions = %d, want 1", n)
	}
	tr := store.transitions[0]
	if tr.ToStatus != "failed" || tr.FailureClassification == nil || *tr.FailureClassification != string(ClassHealthCheckFailed) {
		t.Fatalf("transition = %+v, want failed/health_check_failed", tr)
	}
}

func TestArgoSyncScannerProgressingDeferred(t *testing.T) {
	store := newFakeStore()
	started := time.Now().Add(-16 * time.Minute)
	store.stuckSyncing = []db.StuckDeployRun{
		{ID: "deploy-1", AppSlug: "demo", TeamSlug: "acme", Status: "syncing", StartedAt: &started},
	}
	fetcher := &fakeArgoFetcher{
		byTeamApp: map[string]ApplicationStatus{
			"acme/demo": {SyncStatus: "OutOfSync", HealthStatus: "Progressing"},
		},
	}
	scanner := NewArgoSyncScanner(store, fetcher, NopObserver())
	n, _ := scanner.Tick(context.Background())
	if n != 0 {
		t.Fatalf("transitions = %d, want 0", n)
	}
}

func TestArgoSyncScannerHandlesMissingApplicationAsTimeout(t *testing.T) {
	store := newFakeStore()
	started := time.Now().Add(-16 * time.Minute)
	store.stuckSyncing = []db.StuckDeployRun{
		{ID: "deploy-1", AppSlug: "demo", TeamSlug: "acme", Status: "syncing", StartedAt: &started},
	}
	store.deployStatuses["deploy-1"] = "syncing"
	fetcher := &fakeArgoFetcher{
		byTeamApp: map[string]ApplicationStatus{
			"acme/demo": {SyncStatus: "OutOfSync", HealthStatus: "Missing"},
		},
	}
	scanner := NewArgoSyncScanner(store, fetcher, NopObserver())
	n, _ := scanner.Tick(context.Background())
	if n != 1 {
		t.Fatalf("transitions = %d, want 1", n)
	}
	tr := store.transitions[0]
	if tr.ToStatus != "failed" || tr.FailureClassification == nil || *tr.FailureClassification != string(ClassArgoSyncTimeout) {
		t.Fatalf("transition = %+v, want failed/argo_sync_timeout", tr)
	}
}
