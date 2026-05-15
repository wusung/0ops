package reconciler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
)

type fakeWorkflowFetcher struct {
	byRunID map[int64]WorkflowRunOutcome
	err     error
}

func (f *fakeWorkflowFetcher) GetWorkflowRun(_ context.Context, runID int64) (WorkflowRunOutcome, error) {
	if f.err != nil {
		return WorkflowRunOutcome{}, f.err
	}
	out, ok := f.byRunID[runID]
	if !ok {
		return WorkflowRunOutcome{}, errors.New("no fixture")
	}
	return out, nil
}

func TestDeployStatusScannerTransitionsBuildingToPushingOnSuccess(t *testing.T) {
	store := newFakeStore()
	started := time.Now().Add(-31 * time.Minute)
	runID := int64(42)
	store.stuckBuilding = []db.StuckDeployRun{
		{ID: "deploy-1", AppID: "app-1", TeamID: "team-1", AppSlug: "a", TeamSlug: "t",
			Status: "building", WorkflowRunID: &runID, StartedAt: &started},
	}
	store.deployStatuses["deploy-1"] = "building"
	fetcher := &fakeWorkflowFetcher{
		byRunID: map[int64]WorkflowRunOutcome{
			42: {Status: "completed", Conclusion: "success"},
		},
	}
	scanner := NewDeployStatusScanner(store, fetcher, nil, NopObserver())
	n, err := scanner.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 transition, got %d", n)
	}
	if len(store.transitions) != 1 {
		t.Fatalf("transitions = %d, want 1", len(store.transitions))
	}
	got := store.transitions[0]
	if got.FromStatus != "building" || got.ToStatus != "pushing" {
		t.Fatalf("transition = %s → %s, want building → pushing", got.FromStatus, got.ToStatus)
	}
	if got.FailureClassification != nil {
		t.Fatalf("classification should be nil for success path, got %v", *got.FailureClassification)
	}
	if got.EventActor != "reconciler" {
		t.Fatalf("event_actor = %q, want reconciler", got.EventActor)
	}
}

func TestDeployStatusScannerTransitionsBuildingToFailedWithClassification(t *testing.T) {
	store := newFakeStore()
	started := time.Now().Add(-31 * time.Minute)
	runID := int64(7)
	store.stuckBuilding = []db.StuckDeployRun{
		{ID: "deploy-7", AppID: "app", TeamID: "team", AppSlug: "a", TeamSlug: "t",
			Status: "building", WorkflowRunID: &runID, StartedAt: &started},
	}
	store.deployStatuses["deploy-7"] = "building"
	fetcher := &fakeWorkflowFetcher{
		byRunID: map[int64]WorkflowRunOutcome{
			7: {Status: "completed", Conclusion: "timed_out"},
		},
	}
	scanner := NewDeployStatusScanner(store, fetcher, nil, NopObserver())
	n, err := scanner.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Fatalf("transitions = %d, want 1", n)
	}
	tr := store.transitions[0]
	if tr.ToStatus != "failed" {
		t.Fatalf("to = %s, want failed", tr.ToStatus)
	}
	if tr.FailureClassification == nil || *tr.FailureClassification != string(ClassBuildTimeout) {
		t.Fatalf("classification = %v, want build_timeout", tr.FailureClassification)
	}
}

func TestDeployStatusScannerSkipsInProgressRuns(t *testing.T) {
	store := newFakeStore()
	started := time.Now().Add(-31 * time.Minute)
	runID := int64(7)
	store.stuckBuilding = []db.StuckDeployRun{
		{ID: "deploy-7", Status: "building", WorkflowRunID: &runID, StartedAt: &started},
	}
	store.deployStatuses["deploy-7"] = "building"
	fetcher := &fakeWorkflowFetcher{
		byRunID: map[int64]WorkflowRunOutcome{
			7: {Status: "in_progress"},
		},
	}
	scanner := NewDeployStatusScanner(store, fetcher, nil, NopObserver())
	n, _ := scanner.Tick(context.Background())
	if n != 0 {
		t.Fatalf("expected 0 transitions, got %d", n)
	}
}

func TestDeployStatusScannerHandlesRowsBelowThreshold(t *testing.T) {
	store := newFakeStore()
	started := time.Now().Add(-1 * time.Minute)
	runID := int64(7)
	store.stuckBuilding = []db.StuckDeployRun{
		{ID: "deploy-7", Status: "building", WorkflowRunID: &runID, StartedAt: &started},
	}
	scanner := NewDeployStatusScanner(store, &fakeWorkflowFetcher{}, nil, NopObserver())
	// Trigger at present; the row is 1m old so threshold rejects it.
	n, _ := scanner.Tick(context.Background())
	if n != 0 {
		t.Fatalf("expected 0 transitions, got %d", n)
	}
}

func TestDeployStatusScannerCASConflictIsNoOp(t *testing.T) {
	store := newFakeStore()
	started := time.Now().Add(-31 * time.Minute)
	runID := int64(7)
	store.stuckBuilding = []db.StuckDeployRun{
		{ID: "deploy-7", Status: "building", WorkflowRunID: &runID, StartedAt: &started},
	}
	store.deployStatuses["deploy-7"] = "pushing" // already moved by callback
	fetcher := &fakeWorkflowFetcher{
		byRunID: map[int64]WorkflowRunOutcome{
			7: {Status: "completed", Conclusion: "success"},
		},
	}
	scanner := NewDeployStatusScanner(store, fetcher, nil, NopObserver())
	n, err := scanner.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 transitions, got %d", n)
	}
}
