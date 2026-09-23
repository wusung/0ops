package usage

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/wusung/0ops/internal/server/db"
	"github.com/wusung/0ops/internal/server/services/k3s"
)

// PodWatcher is the cluster event source. *k3s.Client satisfies it.
type PodWatcher interface {
	WatchManagedPods(ctx context.Context, handler k3s.PodEventHandler) error
}

// LeaderGate reports whether this replica owns the ledger. Only the
// leader writes: two replicas closing the same interval would race over
// which end time wins.
type LeaderGate interface {
	IsLeader() bool
}

// WatchObserver reports watch health.
type WatchObserver interface {
	ObserveIntervalsOpened(source string, n int)
	ObserveIntervalsClosed(reason string, n int)
	ObserveWatchDegraded(degraded bool)
}

// Watcher keeps the ledger current between reconcile passes.
//
// It holds no state of its own: the ledger in Postgres is the only
// truth, and every write it makes is idempotent. That is what lets the
// watch and the relist pass run concurrently without coordination.
type Watcher struct {
	pods     PodWatcher
	store    Store
	resolver AppResolver
	leader   LeaderGate
	obs      WatchObserver
	log      *slog.Logger

	mu       sync.Mutex
	degraded bool
}

// AppResolver maps a pod's namespace and app slug to its owning ids.
type AppResolver interface {
	Resolve(ctx context.Context, namespace, appSlug string) (AppRef, bool)
}

// NewWatcher wires a watcher.
func NewWatcher(pods PodWatcher, store Store, resolver AppResolver, leader LeaderGate, obs WatchObserver, log *slog.Logger) *Watcher {
	if log == nil {
		log = slog.Default()
	}
	return &Watcher{pods: pods, store: store, resolver: resolver, leader: leader, obs: obs, log: log}
}

// Run streams pod events until ctx is cancelled, reconnecting with a
// fixed backoff.
//
// A watch failure is a degradation, not an outage: the reconcile loop
// keeps the ledger correct on its own, only with end times inferred
// rather than observed. So this never returns an error upward — it
// records the degradation and keeps trying.
func (w *Watcher) Run(ctx context.Context, retry time.Duration) {
	if retry <= 0 {
		retry = 30 * time.Second
	}
	for {
		if ctx.Err() != nil {
			return
		}
		err := w.pods.WatchManagedPods(ctx, w)
		if ctx.Err() != nil {
			return
		}
		w.setDegraded(true)
		w.log.Warn("usage: pod watch dropped; ledger falls back to periodic reconcile (end times will be inferred)",
			"err", err, "retry_in", retry)

		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
	}
}

// OnPodUpsert opens an interval for a pod we have not seen, and closes
// one the moment K8s reports it finished.
func (w *Watcher) OnPodUpsert(ctx context.Context, pod k3s.PodSnapshot) {
	if !w.owns() || pod.StartedAt == nil {
		return
	}
	w.setDegraded(false)

	ref, ok := w.resolver.Resolve(ctx, pod.Namespace, pod.AppSlug)
	if !ok {
		return
	}

	gpuType := (*string)(nil)
	if pod.GPUType != "" {
		t := pod.GPUType
		gpuType = &t
	}
	created, err := w.store.OpenAllocationInterval(ctx, db.AllocationInterval{
		PodUID:        pod.UID,
		TeamID:        ref.TeamID,
		AppID:         ref.AppID,
		Namespace:     pod.Namespace,
		PodName:       pod.Name,
		CPUMillicores: pod.CPUMillicores,
		MemoryBytes:   pod.MemoryBytes,
		GPUCount:      pod.GPUCount,
		GPUType:       gpuType,
		StartedAt:     *pod.StartedAt,
		LastSeenAt:    time.Now().UTC(),
	})
	if err != nil {
		w.log.Error("usage: open interval from watch failed", "err", err)
		return
	}
	switch {
	case created && w.obs != nil:
		w.obs.ObserveIntervalsOpened("watch", 1)
	case !created:
		// Already on the books — unless it was closed on a guess, in
		// which case seeing it alive proves the guess wrong.
		back, err := w.store.ReopenGuessedInterval(ctx, pod.UID, time.Now().UTC())
		if err != nil {
			w.log.Error("usage: reopen interval from watch failed", "err", err)
		} else if back && w.obs != nil {
			w.obs.ObserveIntervalsOpened("watch_reopen", 1)
		}
	}

	if end, reason := authoritativeEnd(pod); end != nil {
		w.close(ctx, pod, *end, false, reason)
	}
}

// OnPodDelete closes the interval. With the final object in hand the end
// time is authoritative; with only a tombstone it is not, so the close is
// left to the reconcile pass rather than guessed at here.
func (w *Watcher) OnPodDelete(ctx context.Context, pod k3s.PodSnapshot, tombstone bool) {
	if !w.owns() {
		return
	}
	if tombstone {
		// The final state was lost. Inventing an end time now would bill
		// to an arbitrary moment; the reconcile pass will close it at
		// last_seen_at instead, which at least errs downward.
		w.log.Warn("usage: pod delete arrived without final state; deferring close to reconcile",
			"namespace", pod.Namespace)
		return
	}
	end, reason := authoritativeEnd(pod)
	if end == nil {
		// Deleted while still running and with no deletionTimestamp on
		// the object: nothing authoritative to record.
		return
	}
	w.close(ctx, pod, *end, false, reason)
}

func (w *Watcher) close(ctx context.Context, pod k3s.PodSnapshot, end time.Time, estimated bool, reason string) {
	if pod.StartedAt != nil && end.Before(*pod.StartedAt) {
		end = *pod.StartedAt
	}
	closed, err := w.store.CloseAllocationInterval(ctx, pod.UID, end, estimated, reason)
	if err != nil {
		w.log.Error("usage: close interval from watch failed", "err", err)
		return
	}
	if closed && w.obs != nil {
		w.obs.ObserveIntervalsClosed(reason, 1)
	}
}

func (w *Watcher) owns() bool {
	return w.leader == nil || w.leader.IsLeader()
}

func (w *Watcher) setDegraded(degraded bool) {
	w.mu.Lock()
	changed := w.degraded != degraded
	w.degraded = degraded
	w.mu.Unlock()
	if changed && w.obs != nil {
		w.obs.ObserveWatchDegraded(degraded)
	}
}

// Degraded reports whether the watch is currently down.
func (w *Watcher) Degraded() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.degraded
}

// RepoAppResolver resolves pod labels against the app table.
//
// It caches nothing: an app deleted a second ago must stop being billable
// immediately, and the lookup is a single indexed query.
type RepoAppResolver struct {
	Store interface {
		ListAppRefs(ctx context.Context) ([]db.AppRefRow, error)
	}
}

// Resolve implements AppResolver.
func (r RepoAppResolver) Resolve(ctx context.Context, namespace, appSlug string) (AppRef, bool) {
	refs, err := r.Store.ListAppRefs(ctx)
	if err != nil {
		return AppRef{}, false
	}
	for _, ref := range refs {
		if "team-"+ref.TeamSlug == namespace && ref.AppSlug == appSlug {
			return AppRef{TeamID: ref.TeamID, AppID: ref.AppID}, true
		}
	}
	return AppRef{}, false
}
