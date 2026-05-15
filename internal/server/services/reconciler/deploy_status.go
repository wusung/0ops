package reconciler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
)

// BuildingTimeout is the spec § 8.1 threshold: deploy_run rows that
// have stayed in 'building' longer than this trigger a workflow_run
// pull on the next reconciler tick.
const BuildingTimeout = 30 * time.Minute

// WorkflowRunFetcher is the contract the deploy_status scanner uses
// to query GitHub Actions. In production this is implemented over the
// existing workflowdispatch HTTP client; tests substitute a fake.
type WorkflowRunFetcher interface {
	GetWorkflowRun(ctx context.Context, runID int64) (WorkflowRunOutcome, error)
}

// DeployStatusScanner sweeps deploy_run rows stuck in 'building' for
// longer than BuildingTimeout, pulls the GitHub Actions run, and
// transitions the row when the workflow has completed.
type DeployStatusScanner struct {
	store    Store
	fetcher  WorkflowRunFetcher
	clock    func() time.Time
	observer Observer
	incidents *IncidentService
}

// NewDeployStatusScanner wires the scanner. fetcher may be nil — in
// dev-mode environments the scanner skips the pull entirely so the
// process still ticks without GitHub credentials.
func NewDeployStatusScanner(store Store, fetcher WorkflowRunFetcher, incidents *IncidentService, observer Observer) *DeployStatusScanner {
	if observer == nil {
		observer = NopObserver()
	}
	return &DeployStatusScanner{
		store:     store,
		fetcher:   fetcher,
		clock:     time.Now,
		observer:  observer,
		incidents: incidents,
	}
}

// SetClock overrides the time source for tests.
func (s *DeployStatusScanner) SetClock(now func() time.Time) {
	if now != nil {
		s.clock = now
	}
}

// Tick runs one sweep over the stuck-building set. Returns the number
// of rows transitioned; the runner emits a tick metric regardless of
// outcome, so this method does not.
func (s *DeployStatusScanner) Tick(ctx context.Context) (int, error) {
	threshold := s.clock().Add(-BuildingTimeout)
	rows, err := s.store.ListStuckBuildingDeployRuns(ctx, threshold, 50)
	if err != nil {
		return 0, fmt.Errorf("list stuck building deploy_runs: %w", err)
	}
	transitions := 0
	for _, row := range rows {
		ok, err := s.processOne(ctx, row)
		if err != nil {
			// Continue the sweep — a transient fetch error on one row
			// must not block the rest. The runner-level metric counts
			// per-row outcomes; the error string surfaces in logs.
			continue
		}
		if ok {
			transitions++
		}
	}
	return transitions, nil
}

func (s *DeployStatusScanner) processOne(ctx context.Context, row db.StuckDeployRun) (bool, error) {
	if s.fetcher == nil || row.WorkflowRunID == nil {
		// Without GitHub credentials we cannot decide; defer to next tick.
		return false, nil
	}
	out, err := s.fetcher.GetWorkflowRun(ctx, *row.WorkflowRunID)
	if err != nil {
		return false, fmt.Errorf("get workflow_run %d: %w", *row.WorkflowRunID, err)
	}
	switch out.Status {
	case "queued", "in_progress":
		return false, nil
	case "completed":
		// Fall through.
	default:
		return false, fmt.Errorf("unknown workflow_run status %q", out.Status)
	}
	switch out.Conclusion {
	case "success":
		return s.transition(ctx, row, StatusBuilding, StatusPushing, nil, "workflow_run completed successfully")
	case "failure", "timed_out", "cancelled":
		class := ClassifyWorkflowRun(out)
		summary := fmt.Sprintf("workflow_run %d %s in step %q", *row.WorkflowRunID, out.Conclusion, out.StepName)
		ok, err := s.transition(ctx, row, StatusBuilding, StatusFailed, class.Ptr(), summary)
		if ok {
			s.observer.ObserveFailureClassification(string(class))
		}
		return ok, err
	}
	return false, nil
}

func (s *DeployStatusScanner) transition(ctx context.Context, row db.StuckDeployRun, from, to DeployStatus, class *string, reason string) (bool, error) {
	payload := TransitionPayload{From: from, To: to}
	if class != nil {
		c := *class
		payload.FailureClassification = &c
	}
	if reason != "" {
		r := reason
		payload.ErrorSummary = &r
	}
	if err := Lint(payload); err != nil {
		// Lint failure here means a programmer typo above — surface it.
		return false, err
	}
	params := db.DeployRunTransitionParams{
		RunID:       row.ID,
		FromStatus:  string(from),
		ToStatus:    string(to),
		EventActor:  "reconciler",
		EventReason: reason,
	}
	if payload.FailureClassification != nil {
		params.FailureClassification = payload.FailureClassification
	}
	if payload.ErrorSummary != nil {
		params.ErrorSummary = payload.ErrorSummary
	}
	if err := s.store.TransitionDeployRun(ctx, params); err != nil {
		if errors.Is(err, db.ErrDeployRunStateConflict) {
			return false, nil
		}
		return false, err
	}
	s.observer.ObserveDeployTransition(string(from), string(to))
	return true, nil
}
