package reconciler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
)

// SyncingTimeout is the spec § 8.2 threshold for ArgoCD-applied
// deploy_run rows that have been in 'syncing' for longer than this.
const SyncingTimeout = 15 * time.Minute

// ApplicationStatusFetcher fetches an ArgoCD Application status. The
// existing internal/server/services/k3s.Client.GetApplicationStatus
// satisfies this; tests substitute a fake.
type ApplicationStatusFetcher interface {
	GetApplicationStatus(ctx context.Context, teamSlug, appSlug string) (ApplicationStatus, error)
}

// ApplicationStatus captures the ArgoCD Application sync + health.
// Mirrors internal/server/services/k3s.ApplicationStatus so cmd/server
// can adapt the existing client without an import cycle.
type ApplicationStatus struct {
	SyncStatus   string
	HealthStatus string
}

// ArgoSyncScanner sweeps deploy_run rows stuck in 'syncing' past
// SyncingTimeout, queries ArgoCD, and transitions to live / failed.
type ArgoSyncScanner struct {
	store    Store
	fetcher  ApplicationStatusFetcher
	clock    func() time.Time
	observer Observer
}

// NewArgoSyncScanner wires the scanner. fetcher may be nil (dev mode);
// scanner returns 0 transitions in that case.
func NewArgoSyncScanner(store Store, fetcher ApplicationStatusFetcher, observer Observer) *ArgoSyncScanner {
	if observer == nil {
		observer = NopObserver()
	}
	return &ArgoSyncScanner{
		store:    store,
		fetcher:  fetcher,
		clock:    time.Now,
		observer: observer,
	}
}

// SetClock overrides the time source for tests.
func (s *ArgoSyncScanner) SetClock(now func() time.Time) {
	if now != nil {
		s.clock = now
	}
}

// Tick runs one sweep over the stuck-syncing set.
func (s *ArgoSyncScanner) Tick(ctx context.Context) (int, error) {
	threshold := s.clock().Add(-SyncingTimeout)
	rows, err := s.store.ListStuckSyncingDeployRuns(ctx, threshold, 50)
	if err != nil {
		return 0, fmt.Errorf("list stuck syncing deploy_runs: %w", err)
	}
	transitions := 0
	for _, row := range rows {
		ok, err := s.processOne(ctx, row)
		if err != nil {
			continue
		}
		if ok {
			transitions++
		}
	}
	return transitions, nil
}

func (s *ArgoSyncScanner) processOne(ctx context.Context, row db.StuckDeployRun) (bool, error) {
	if s.fetcher == nil {
		return false, nil
	}
	status, err := s.fetcher.GetApplicationStatus(ctx, row.TeamSlug, row.AppSlug)
	if err != nil {
		return false, fmt.Errorf("get application %s/%s: %w", row.TeamSlug, row.AppSlug, err)
	}
	switch strings.ToLower(status.HealthStatus) {
	case "healthy":
		return s.transition(ctx, row, StatusSyncing, StatusLive, nil, "argocd reported Healthy")
	case "degraded":
		class := ClassifyArgoCD(ArgoCDHealth{Health: status.HealthStatus, Sync: status.SyncStatus})
		summary := fmt.Sprintf("argocd reported %s (sync=%s)", status.HealthStatus, status.SyncStatus)
		ok, err := s.transition(ctx, row, StatusSyncing, StatusFailed, class.Ptr(), summary)
		if ok {
			s.observer.ObserveFailureClassification(string(class))
		}
		return ok, err
	case "progressing", "":
		return false, nil
	default:
		// Missing / Suspended / unknown: treat as syncing timeout. Spec
		// § 8.2 maps Degraded -> failed; everything else outside healthy
		// counts as an argo_sync_timeout class once the threshold has
		// elapsed twice (this tick is past it).
		class := ClassArgoSyncTimeout
		summary := fmt.Sprintf("argocd reported %s past sync timeout", status.HealthStatus)
		ok, err := s.transition(ctx, row, StatusSyncing, StatusFailed, class.Ptr(), summary)
		if ok {
			s.observer.ObserveFailureClassification(string(class))
		}
		return ok, err
	}
}

func (s *ArgoSyncScanner) transition(ctx context.Context, row db.StuckDeployRun, from, to DeployStatus, class *string, reason string) (bool, error) {
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
