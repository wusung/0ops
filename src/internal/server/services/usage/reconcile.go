package usage

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/wusung/0ops/internal/server/db"
	"github.com/wusung/0ops/internal/server/services/k3s"
)

// PodLister is the cluster half of a reconcile pass.
type PodLister interface {
	ListManagedPods(ctx context.Context) ([]k3s.PodSnapshot, error)
}

// Store is the ledger half. Narrowed to what reconcile needs so tests can
// substitute it without a database.
type Store interface {
	ListAppRefs(ctx context.Context) ([]db.AppRefRow, error)
	ListOpenAllocationIntervals(ctx context.Context) ([]db.OpenIntervalRef, error)
	OpenAllocationInterval(ctx context.Context, in db.AllocationInterval) (bool, error)
	ReopenGuessedInterval(ctx context.Context, podUID string, seenAt time.Time) (bool, error)
	CloseAllocationInterval(ctx context.Context, podUID string, endedAt time.Time, estimated bool, reason string) (bool, error)
	TouchAllocationIntervals(ctx context.Context, podUIDs []string, seenAt time.Time) error
}

// Observer receives reconcile outcomes. Counts only — no pod, app or team
// identifier may become a metric label (spec § 14 rule #9).
type Observer interface {
	ObserveIntervalsOpened(source string, n int)
	ObserveIntervalsClosed(reason string, n int)
	ObserveOpenIntervals(n int)
	ObserveOrphanPods(n int)
	ObserveReconcileDuration(d time.Duration)
}

// NopObserver discards everything; the default when no metrics are wired.
type NopObserver struct{}

func (NopObserver) ObserveIntervalsOpened(string, int)     {}
func (NopObserver) ObserveIntervalsClosed(string, int)     {}
func (NopObserver) ObserveOpenIntervals(int)               {}
func (NopObserver) ObserveOrphanPods(int)                  {}
func (NopObserver) ObserveReconcileDuration(time.Duration) {}

// Reconciler compares the cluster against the ledger and applies the
// difference. Applying is deliberately dumb: every decision was already
// made by the pure Reconcile function.
type Reconciler struct {
	pods  PodLister
	store Store
	obs   Observer
	log   *slog.Logger
	now   func() time.Time
}

// NewReconciler wires a reconciler. now defaults to time.Now().UTC() and
// exists so tests can pin the clock; it is only ever used as a last-seen
// stamp, never as an interval boundary.
func NewReconciler(pods PodLister, store Store, obs Observer, log *slog.Logger, now func() time.Time) *Reconciler {
	if obs == nil {
		obs = NopObserver{}
	}
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Reconciler{pods: pods, store: store, obs: obs, log: log, now: now}
}

// ReconcileOnce runs a single pass and reports what it did.
func (r *Reconciler) ReconcileOnce(ctx context.Context) (Plan, error) {
	started := time.Now()
	defer func() { r.obs.ObserveReconcileDuration(time.Since(started)) }()

	pods, err := r.pods.ListManagedPods(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("list managed pods: %w", err)
	}
	refs, err := r.store.ListAppRefs(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("list app refs: %w", err)
	}
	open, err := r.store.ListOpenAllocationIntervals(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("list open intervals: %w", err)
	}

	apps := make(map[string]AppRef, len(refs))
	for _, ref := range refs {
		apps["team-"+ref.TeamSlug+"/"+ref.AppSlug] = AppRef{TeamID: ref.TeamID, AppID: ref.AppID}
	}

	plan := Reconcile(pods, open, apps, r.now())
	if err := r.apply(ctx, plan); err != nil {
		return plan, err
	}

	// Count what the ledger actually holds rather than deriving it from
	// the plan: an open that hit the conflict clause and a close the
	// `ended_at IS NULL` guard rejected are both no-ops, and a gauge
	// built from intended writes drifts away from reality.
	if live, err := r.store.ListOpenAllocationIntervals(ctx); err == nil {
		r.obs.ObserveOpenIntervals(len(live))
	}

	r.obs.ObserveOrphanPods(plan.OrphanPods)
	return plan, nil
}

func (r *Reconciler) apply(ctx context.Context, plan Plan) error {
	opened, reopened := 0, 0
	for _, in := range plan.Opens {
		created, err := r.store.OpenAllocationInterval(ctx, in)
		if err != nil {
			return fmt.Errorf("open interval for pod %s: %w", in.PodUID, err)
		}
		if created {
			opened++
			continue
		}
		// The insert hit an existing row. Usually that is a pod already
		// on the books; sometimes it is one we closed on a guess and is
		// now visibly alive again, which only a re-open can fix.
		back, err := r.store.ReopenGuessedInterval(ctx, in.PodUID, in.LastSeenAt)
		if err != nil {
			return fmt.Errorf("reopen interval for pod %s: %w", in.PodUID, err)
		}
		if back {
			reopened++
			r.log.Warn("usage: reopened an interval closed on a guess; the pod is visible again",
				slog.String("namespace", in.Namespace))
		}
	}
	if opened > 0 {
		r.obs.ObserveIntervalsOpened("reconcile", opened)
	}
	if reopened > 0 {
		r.obs.ObserveIntervalsOpened("reconcile_reopen", reopened)
	}

	closedByReason := map[string]int{}
	for _, c := range plan.Closes {
		closed, err := r.store.CloseAllocationInterval(ctx, c.PodUID, c.EndedAt, c.Estimated, c.Reason)
		if err != nil {
			return fmt.Errorf("close interval for pod %s: %w", c.PodUID, err)
		}
		if closed {
			closedByReason[c.Reason]++
		}
	}
	for reason, n := range closedByReason {
		r.obs.ObserveIntervalsClosed(reason, n)
	}

	if err := r.store.TouchAllocationIntervals(ctx, plan.Touches, r.now()); err != nil {
		return fmt.Errorf("touch intervals: %w", err)
	}

	if plan.OrphanPods > 0 {
		// Namespace is deliberately absent: orphans are a platform-level
		// signal, and logging per-pod detail on every pass is noise.
		r.log.Warn("usage: pods reference apps that no longer exist",
			slog.Int("orphan_pods", plan.OrphanPods))
	}
	return nil
}

// Tick adapts ReconcileOnce to the reconciler.Runner loop contract. It
// returns plain counts so the runner package does not need to know this
// package's types.
func (r *Reconciler) Tick(ctx context.Context) (opened, closed int, err error) {
	plan, err := r.ReconcileOnce(ctx)
	if err != nil {
		return 0, 0, err
	}
	return len(plan.Opens), len(plan.Closes), nil
}
