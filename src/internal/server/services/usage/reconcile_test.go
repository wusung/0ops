package usage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wusung/0ops/internal/server/db"
	"github.com/wusung/0ops/internal/server/services/k3s"
)

type fakeStore struct {
	refs   []db.AppRefRow
	open   []db.OpenIntervalRef
	opened []db.AllocationInterval
	closed []Close
	// touched records every TouchAllocationIntervals call, including
	// empty ones, so the test can assert a closed pod is never touched.
	touched  [][]string
	reopened []string
	// reopenable lists pod uids whose interval was closed on a guess and
	// can therefore come back.
	reopenable map[string]bool
	openErr    error
	closeErr   error
}

func (f *fakeStore) ListAppRefs(context.Context) ([]db.AppRefRow, error) { return f.refs, nil }
func (f *fakeStore) ListOpenAllocationIntervals(context.Context) ([]db.OpenIntervalRef, error) {
	return f.open, nil
}
func (f *fakeStore) OpenAllocationInterval(_ context.Context, in db.AllocationInterval) (bool, error) {
	if f.openErr != nil {
		return false, f.openErr
	}
	if f.reopenable[in.PodUID] {
		// Mirrors ON CONFLICT DO NOTHING: the row is already there.
		return false, nil
	}
	f.opened = append(f.opened, in)
	return true, nil
}

func (f *fakeStore) ReopenGuessedInterval(_ context.Context, podUID string, _ time.Time) (bool, error) {
	if !f.reopenable[podUID] {
		return false, nil
	}
	f.reopened = append(f.reopened, podUID)
	delete(f.reopenable, podUID)
	return true, nil
}
func (f *fakeStore) CloseAllocationInterval(_ context.Context, uid string, at time.Time, est bool, reason string) (bool, error) {
	if f.closeErr != nil {
		return false, f.closeErr
	}
	f.closed = append(f.closed, Close{PodUID: uid, EndedAt: at, Estimated: est, Reason: reason})
	return true, nil
}
func (f *fakeStore) TouchAllocationIntervals(_ context.Context, uids []string, _ time.Time) error {
	f.touched = append(f.touched, uids)
	return nil
}

type fakeLister struct {
	pods []k3s.PodSnapshot
	err  error
}

func (f fakeLister) ListManagedPods(context.Context) ([]k3s.PodSnapshot, error) {
	return f.pods, f.err
}

type recordingObserver struct {
	opened map[string]int
	closed map[string]int
	orphan int
	open   int
}

func newRecordingObserver() *recordingObserver {
	return &recordingObserver{opened: map[string]int{}, closed: map[string]int{}}
}
func (o *recordingObserver) ObserveIntervalsOpened(src string, n int) { o.opened[src] += n }
func (o *recordingObserver) ObserveIntervalsClosed(r string, n int)   { o.closed[r] += n }
func (o *recordingObserver) ObserveOpenIntervals(n int)               { o.open = n }
func (o *recordingObserver) ObserveOrphanPods(n int)                  { o.orphan = n }
func (o *recordingObserver) ObserveReconcileDuration(time.Duration)   {}

func TestReconcileOnceAppliesPlan(t *testing.T) {
	started := now.Add(-time.Hour)
	store := &fakeStore{
		refs: []db.AppRefRow{{AppID: "app-a", TeamID: "team-a", TeamSlug: "acme", AppSlug: "web"}},
	}
	obs := newRecordingObserver()
	r := NewReconciler(fakeLister{pods: []k3s.PodSnapshot{runningPod("p1", started)}}, store, obs, nil,
		func() time.Time { return now })

	plan, err := r.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(plan.Opens) != 1 || len(store.opened) != 1 {
		t.Fatalf("expected one interval opened, got plan=%d store=%d", len(plan.Opens), len(store.opened))
	}
	if store.opened[0].TeamID != "team-a" || store.opened[0].AppID != "app-a" {
		t.Errorf("namespace/slug not resolved to ids: %+v", store.opened[0])
	}
	if obs.opened["reconcile"] != 1 {
		t.Errorf("opened metric = %v", obs.opened)
	}
}

// The reconcile pass resolves ownership by joining the pod's namespace
// with its app label; a mismatch in either half must not attribute the
// pod to the wrong team.
func TestNamespaceMustMatchTheTeamSlug(t *testing.T) {
	store := &fakeStore{
		refs: []db.AppRefRow{{AppID: "app-a", TeamID: "team-a", TeamSlug: "other", AppSlug: "web"}},
	}
	obs := newRecordingObserver()
	r := NewReconciler(fakeLister{pods: []k3s.PodSnapshot{runningPod("p1", now.Add(-time.Minute))}}, store, obs, nil,
		func() time.Time { return now })

	if _, err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(store.opened) != 0 {
		t.Fatalf("pod in team-acme was attributed to an app owned by team 'other': %+v", store.opened)
	}
	if obs.orphan != 1 {
		t.Errorf("orphan metric = %d, want 1", obs.orphan)
	}
}

func TestReconcileClosesVanishedPod(t *testing.T) {
	lastSeen := now.Add(-4 * time.Minute)
	store := &fakeStore{
		refs: []db.AppRefRow{{AppID: "app-a", TeamID: "team-a", TeamSlug: "acme", AppSlug: "web"}},
		open: []db.OpenIntervalRef{{PodUID: "gone", StartedAt: now.Add(-time.Hour), LastSeenAt: lastSeen}},
	}
	obs := newRecordingObserver()
	r := NewReconciler(fakeLister{}, store, obs, nil, func() time.Time { return now })

	if _, err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(store.closed) != 1 {
		t.Fatalf("want 1 close, got %d", len(store.closed))
	}
	if !store.closed[0].EndedAt.Equal(lastSeen) || !store.closed[0].Estimated {
		t.Errorf("vanished pod closed wrongly: %+v", store.closed[0])
	}
	if obs.closed[db.CloseReasonReconciledMissing] != 1 {
		t.Errorf("close reason metric = %v", obs.closed)
	}
}

// A cluster read failure must abort the pass rather than let the ledger
// conclude every pod disappeared and close the entire book.
func TestListFailureAbortsWithoutClosingAnything(t *testing.T) {
	store := &fakeStore{
		open: []db.OpenIntervalRef{{PodUID: "p1", StartedAt: now.Add(-time.Hour), LastSeenAt: now}},
	}
	r := NewReconciler(fakeLister{err: errors.New("api server down")}, store, nil, nil, func() time.Time { return now })

	if _, err := r.ReconcileOnce(context.Background()); err == nil {
		t.Fatal("want an error when the cluster cannot be read")
	}
	if len(store.closed) != 0 {
		t.Fatalf("a failed cluster read closed %d intervals", len(store.closed))
	}
}

func TestWriteFailureSurfaces(t *testing.T) {
	store := &fakeStore{
		refs:    []db.AppRefRow{{AppID: "app-a", TeamID: "team-a", TeamSlug: "acme", AppSlug: "web"}},
		openErr: errors.New("db down"),
	}
	r := NewReconciler(fakeLister{pods: []k3s.PodSnapshot{runningPod("p1", now.Add(-time.Minute))}}, store, nil, nil,
		func() time.Time { return now })

	if _, err := r.ReconcileOnce(context.Background()); err == nil {
		t.Fatal("want the write failure to surface rather than be swallowed")
	}
}

// A transient blind spot — an API hiccup, or an app row vanishing during
// a rename — closes the interval on a guess. When the pod turns out to
// be alive, a second insert hits the conflict clause and does nothing,
// so without a re-open the rest of that pod's life is never billed.
func TestReconcileReopensIntervalClosedOnAGuess(t *testing.T) {
	started := now.Add(-time.Hour)
	store := &fakeStore{
		refs:       []db.AppRefRow{{AppID: "app-a", TeamID: "team-a", TeamSlug: "acme", AppSlug: "web"}},
		reopenable: map[string]bool{"p1": true},
	}
	obs := newRecordingObserver()
	r := NewReconciler(fakeLister{pods: []k3s.PodSnapshot{runningPod("p1", started)}}, store, obs, nil,
		func() time.Time { return now })

	if _, err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(store.reopened) != 1 || store.reopened[0] != "p1" {
		t.Fatalf("interval not reopened: %v", store.reopened)
	}
	if obs.opened["reconcile_reopen"] != 1 {
		t.Errorf("reopen metric = %v", obs.opened)
	}
}

// A pod already correctly on the books must not be reopened — there is
// nothing to fix, and the attempt would count as activity.
func TestReconcileDoesNotReopenAHealthyInterval(t *testing.T) {
	started := now.Add(-time.Hour)
	store := &fakeStore{
		refs: []db.AppRefRow{{AppID: "app-a", TeamID: "team-a", TeamSlug: "acme", AppSlug: "web"}},
		open: []db.OpenIntervalRef{{PodUID: "p1", StartedAt: started, LastSeenAt: now.Add(-time.Minute)}},
	}
	r := NewReconciler(fakeLister{pods: []k3s.PodSnapshot{runningPod("p1", started)}}, store, nil, nil,
		func() time.Time { return now })

	if _, err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(store.reopened) != 0 {
		t.Fatalf("reopened a healthy interval: %v", store.reopened)
	}
}

// The gauge must reflect the ledger, not the plan: opens that hit the
// conflict clause and closes the guard rejected are both no-ops.
func TestOpenIntervalGaugeReadsTheLedger(t *testing.T) {
	started := now.Add(-time.Hour)
	store := &fakeStore{
		refs: []db.AppRefRow{{AppID: "app-a", TeamID: "team-a", TeamSlug: "acme", AppSlug: "web"}},
		open: []db.OpenIntervalRef{
			{PodUID: "p1", StartedAt: started, LastSeenAt: now},
			{PodUID: "p2", StartedAt: started, LastSeenAt: now},
		},
	}
	obs := newRecordingObserver()
	r := NewReconciler(fakeLister{pods: []k3s.PodSnapshot{runningPod("p1", started), runningPod("p2", started)}},
		store, obs, nil, func() time.Time { return now })

	if _, err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if obs.open != 2 {
		t.Errorf("open gauge = %d, want the 2 rows the ledger holds", obs.open)
	}
}
