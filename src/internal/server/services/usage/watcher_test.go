package usage

import (
	"context"
	"testing"
	"time"

	"github.com/wusung/0ops/internal/server/db"
	"github.com/wusung/0ops/internal/server/services/k3s"
)

type stubResolver struct {
	ref AppRef
	ok  bool
}

func (s stubResolver) Resolve(context.Context, string, string) (AppRef, bool) {
	return s.ref, s.ok
}

type stubLeader bool

func (s stubLeader) IsLeader() bool { return bool(s) }

type watchObserver struct {
	opened   map[string]int
	closed   map[string]int
	degraded []bool
}

func newWatchObserver() *watchObserver {
	return &watchObserver{opened: map[string]int{}, closed: map[string]int{}}
}
func (o *watchObserver) ObserveIntervalsOpened(src string, n int) { o.opened[src] += n }
func (o *watchObserver) ObserveIntervalsClosed(r string, n int)   { o.closed[r] += n }
func (o *watchObserver) ObserveWatchDegraded(d bool)              { o.degraded = append(o.degraded, d) }

func newWatcher(store Store, leader bool) (*Watcher, *watchObserver) {
	obs := newWatchObserver()
	w := NewWatcher(nil, store, stubResolver{ref: teamA, ok: true}, stubLeader(leader), obs, nil)
	return w, obs
}

func TestWatchOpensIntervalOnPodAdd(t *testing.T) {
	store := &fakeStore{}
	w, obs := newWatcher(store, true)
	started := now.Add(-time.Minute)

	w.OnPodUpsert(context.Background(), runningPod("p1", started))

	if len(store.opened) != 1 {
		t.Fatalf("want 1 interval opened, got %d", len(store.opened))
	}
	if !store.opened[0].StartedAt.Equal(started) {
		t.Errorf("StartedAt = %v, want the pod's own %v", store.opened[0].StartedAt, started)
	}
	if obs.opened["watch"] != 1 {
		t.Errorf("opened metric = %v", obs.opened)
	}
}

// The whole point of watching: a terminated pod's authoritative finish
// time is recorded when the event arrives, not inferred later.
func TestWatchClosesWithAuthoritativeFinishTime(t *testing.T) {
	store := &fakeStore{}
	w, obs := newWatcher(store, true)
	started := now.Add(-time.Hour)
	finished := now.Add(-time.Minute)

	pod := runningPod("p1", started)
	pod.TerminatedAt = &finished
	w.OnPodUpsert(context.Background(), pod)

	if len(store.closed) != 1 {
		t.Fatalf("want 1 close, got %d", len(store.closed))
	}
	c := store.closed[0]
	if !c.EndedAt.Equal(finished) || c.Estimated {
		t.Errorf("close = %+v, want an exact close at %v", c, finished)
	}
	if obs.closed[db.CloseReasonTerminated] != 1 {
		t.Errorf("closed metric = %v", obs.closed)
	}
}

func TestWatchClosesOnDeleteWithFinalState(t *testing.T) {
	store := &fakeStore{}
	w, _ := newWatcher(store, true)
	started := now.Add(-time.Hour)
	deleted := now.Add(-30 * time.Second)

	pod := runningPod("p1", started)
	pod.DeletedAt = &deleted
	w.OnPodDelete(context.Background(), pod, false)

	if len(store.closed) != 1 || !store.closed[0].EndedAt.Equal(deleted) {
		t.Fatalf("close = %+v, want %v", store.closed, deleted)
	}
}

// A tombstone means the final object was lost. Inventing an end time
// would bill to an arbitrary moment; the reconcile pass closes it at
// last_seen_at instead, which errs downward.
func TestWatchDefersTombstoneToReconcile(t *testing.T) {
	store := &fakeStore{}
	w, _ := newWatcher(store, true)

	w.OnPodDelete(context.Background(), runningPod("p1", now.Add(-time.Hour)), true)

	if len(store.closed) != 0 {
		t.Fatalf("tombstone must not produce a guessed end time: %+v", store.closed)
	}
}

func TestWatchIgnoresEventsWhenNotLeader(t *testing.T) {
	store := &fakeStore{}
	w, _ := newWatcher(store, false)
	started := now.Add(-time.Minute)

	w.OnPodUpsert(context.Background(), runningPod("p1", started))
	pod := runningPod("p1", started)
	end := now
	pod.TerminatedAt = &end
	w.OnPodDelete(context.Background(), pod, false)

	if len(store.opened) != 0 || len(store.closed) != 0 {
		t.Fatalf("follower wrote to the ledger: opened=%d closed=%d", len(store.opened), len(store.closed))
	}
}

func TestWatchSkipsUnscheduledAndOrphanPods(t *testing.T) {
	store := &fakeStore{}
	obs := newWatchObserver()
	w := NewWatcher(nil, store, stubResolver{ok: false}, stubLeader(true), obs, nil)

	// Orphan: labels resolve to nothing.
	w.OnPodUpsert(context.Background(), runningPod("p1", now.Add(-time.Minute)))

	// Unscheduled: no start time.
	w2, _ := newWatcher(store, true)
	pod := runningPod("p2", now)
	pod.StartedAt = nil
	w2.OnPodUpsert(context.Background(), pod)

	if len(store.opened) != 0 {
		t.Fatalf("opened %d intervals for pods that own nothing", len(store.opened))
	}
}

func TestWatchDegradationIsReportedOncePerTransition(t *testing.T) {
	store := &fakeStore{}
	w, obs := newWatcher(store, true)

	w.setDegraded(true)
	w.setDegraded(true)
	if !w.Degraded() {
		t.Fatal("Degraded() = false after degradation")
	}
	w.OnPodUpsert(context.Background(), runningPod("p1", now.Add(-time.Minute)))
	if w.Degraded() {
		t.Error("a delivered event means the watch is healthy again")
	}
	if len(obs.degraded) != 2 || !obs.degraded[0] || obs.degraded[1] {
		t.Errorf("degraded transitions = %v, want [true false]", obs.degraded)
	}
}

// Watch and reconcile run concurrently and must be able to act on the
// same pod without producing two intervals or two closes.
func TestWatchAndReconcileAgreeOnTheSamePod(t *testing.T) {
	store := &fakeStore{
		refs: []db.AppRefRow{{AppID: teamA.AppID, TeamID: teamA.TeamID, TeamSlug: "acme", AppSlug: "web"}},
	}
	w, _ := newWatcher(store, true)
	started := now.Add(-time.Hour)
	finished := now.Add(-time.Minute)

	pod := runningPod("p1", started)
	pod.TerminatedAt = &finished

	// Watch sees it first.
	w.OnPodUpsert(context.Background(), pod)
	// Then a reconcile pass sees the same terminated pod.
	rec := NewReconciler(fakeLister{pods: []k3s.PodSnapshot{pod}}, store, nil, nil, func() time.Time { return now })
	if _, err := rec.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// The fake store records every attempt; the real one's ON CONFLICT
	// and `ended_at IS NULL` guards collapse them. What matters here is
	// that both paths derive the same values.
	for _, in := range store.opened {
		if !in.StartedAt.Equal(started) {
			t.Errorf("paths disagree on start: %v vs %v", in.StartedAt, started)
		}
	}
	for _, c := range store.closed {
		if !c.EndedAt.Equal(finished) || c.Estimated {
			t.Errorf("paths disagree on end: %+v, want exact %v", c, finished)
		}
	}
}
