package usage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/wusung/0ops/internal/server/db"
	"github.com/wusung/0ops/internal/server/services/k3s"
)

// SampleRetention is how long instantaneous observations are kept. They
// answer "is this app sized right", a question about the recent past;
// the permanent record is the allocation ledger.
const SampleRetention = 30 * 24 * time.Hour

// UsageLister reads instantaneous consumption. *k3s.Client satisfies it.
type UsageLister interface {
	ListManagedPods(ctx context.Context) ([]k3s.PodSnapshot, error)
	ListPodUsage(ctx context.Context) ([]k3s.PodUsage, error)
}

// SampleStore is the write surface for observations.
type SampleStore interface {
	ListAppRefs(ctx context.Context) ([]db.AppRefRow, error)
	InsertUsageSamples(ctx context.Context, samples []db.UsageSample) error
	DeleteUsageSamplesBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// SampleObserver reports sampling health.
type SampleObserver interface {
	ObserveSamplesWritten(n int)
	ObserveSampleFailure(reason string)
}

// NopSampleObserver discards everything.
type NopSampleObserver struct{}

func (NopSampleObserver) ObserveSamplesWritten(int)   {}
func (NopSampleObserver) ObserveSampleFailure(string) {}

// Sampler records what apps are actually consuming.
//
// This is the observation track (spec § 8), deliberately separate from
// the ledger: metrics-server reports gauges, which cannot be integrated
// into a defensible total, and it may not be installed at all. Nothing
// here feeds metering, and the ledger does not depend on any of it.
type Sampler struct {
	cluster   UsageLister
	store     SampleStore
	obs       SampleObserver
	log       *slog.Logger
	now       func() time.Time
	retention time.Duration

	consecutiveFailures int
}

// NewSampler wires the observation track.
func NewSampler(cluster UsageLister, store SampleStore, obs SampleObserver, log *slog.Logger, now func() time.Time) *Sampler {
	if obs == nil {
		obs = NopSampleObserver{}
	}
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Sampler{cluster: cluster, store: store, obs: obs, log: log, now: now, retention: SampleRetention}
}

// ConsecutiveFailures reports how many ticks in a row could not read the
// metrics API. Callers escalate on a sustained streak.
func (s *Sampler) ConsecutiveFailures() int { return s.consecutiveFailures }

// Tick records one round of observations and expires old ones.
//
// When the metrics API is unavailable the tick writes nothing at all. It
// must never fall back to declared resources: that would file an
// allocation figure as if it were a measurement, and nothing downstream
// could tell the difference afterwards.
func (s *Sampler) Tick(ctx context.Context) (written int, err error) {
	usage, err := s.cluster.ListPodUsage(ctx)
	if err != nil {
		s.consecutiveFailures++
		reason := "metrics_api_error"
		if errors.Is(err, k3s.ErrMetricsAPIUnavailable) {
			reason = "metrics_api_unavailable"
		}
		s.obs.ObserveSampleFailure(reason)
		s.log.Warn("usage: skipping observation tick; metrics unavailable (allocation ledger is unaffected)",
			"reason", reason, "consecutive_failures", s.consecutiveFailures, "err", err)
		return 0, nil
	}

	pods, err := s.cluster.ListManagedPods(ctx)
	if err != nil {
		s.consecutiveFailures++
		s.obs.ObserveSampleFailure("pods_list_failed")
		return 0, fmt.Errorf("list managed pods: %w", err)
	}
	refs, err := s.store.ListAppRefs(ctx)
	if err != nil {
		return 0, fmt.Errorf("list app refs: %w", err)
	}
	s.consecutiveFailures = 0

	samples := aggregateSamples(pods, usage, refs, s.now())
	if err := s.store.InsertUsageSamples(ctx, samples); err != nil {
		s.obs.ObserveSampleFailure("write_failed")
		return 0, fmt.Errorf("insert usage samples: %w", err)
	}
	if len(samples) > 0 {
		s.obs.ObserveSamplesWritten(len(samples))
	}

	if _, err := s.store.DeleteUsageSamplesBefore(ctx, s.now().Add(-s.retention)); err != nil {
		return len(samples), fmt.Errorf("expire usage samples: %w", err)
	}
	return len(samples), nil
}

// aggregateSamples folds per-pod readings into one row per app.
//
// Pure, and exported to the package only through tests: attribution and
// summing are where this would go wrong quietly.
func aggregateSamples(pods []k3s.PodSnapshot, usage []k3s.PodUsage, refs []db.AppRefRow, at time.Time) []db.UsageSample {
	appByKey := make(map[string]AppRef, len(refs))
	for _, ref := range refs {
		appByKey["team-"+ref.TeamSlug+"/"+ref.AppSlug] = AppRef{TeamID: ref.TeamID, AppID: ref.AppID}
	}

	type podInfo struct {
		ref   AppRef
		ready bool
	}
	byUID := make(map[string]podInfo, len(pods))
	for _, pod := range pods {
		ref, ok := appByKey[pod.Namespace+"/"+pod.AppSlug]
		if !ok {
			continue
		}
		byUID[pod.UID] = podInfo{ref: ref, ready: pod.Ready}
	}

	type acc struct {
		ref    AppRef
		cpu    int
		memory int64
		ready  bool
	}
	byApp := map[string]*acc{}
	for _, u := range usage {
		info, ok := byUID[u.UID]
		if !ok {
			// Metrics for a pod we cannot attribute: an orphan, or one
			// that vanished between the two reads. Not ours to record.
			continue
		}
		a := byApp[info.ref.AppID]
		if a == nil {
			a = &acc{ref: info.ref}
			byApp[info.ref.AppID] = a
		}
		a.cpu += u.CPUMillicores
		a.memory += u.MemoryBytes
		// An app counts as active if any of its pods is ready.
		a.ready = a.ready || info.ready
	}

	out := make([]db.UsageSample, 0, len(byApp))
	for _, a := range byApp {
		out = append(out, db.UsageSample{
			TeamID:        a.ref.TeamID,
			AppID:         a.ref.AppID,
			SampledAt:     at,
			CPUMillicores: a.cpu,
			MemoryBytes:   a.memory,
			Active:        a.ready,
		})
	}
	return out
}
