package usage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wusung/0ops/internal/server/db"
	"github.com/wusung/0ops/internal/server/services/k3s"
)

type fakeSampleCluster struct {
	pods     []k3s.PodSnapshot
	usage    []k3s.PodUsage
	usageErr error
	podsErr  error
}

func (f fakeSampleCluster) ListManagedPods(context.Context) ([]k3s.PodSnapshot, error) {
	return f.pods, f.podsErr
}
func (f fakeSampleCluster) ListPodUsage(context.Context) ([]k3s.PodUsage, error) {
	return f.usage, f.usageErr
}

type fakeSampleStore struct {
	refs     []db.AppRefRow
	written  []db.UsageSample
	gcCutoff time.Time
	writeErr error
}

func (f *fakeSampleStore) ListAppRefs(context.Context) ([]db.AppRefRow, error) { return f.refs, nil }
func (f *fakeSampleStore) InsertUsageSamples(_ context.Context, s []db.UsageSample) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.written = append(f.written, s...)
	return nil
}
func (f *fakeSampleStore) DeleteUsageSamplesBefore(_ context.Context, cutoff time.Time) (int64, error) {
	f.gcCutoff = cutoff
	return 0, nil
}

type sampleObserver struct {
	written  int
	failures []string
}

func (o *sampleObserver) ObserveSamplesWritten(n int) { o.written += n }
func (o *sampleObserver) ObserveSampleFailure(r string) {
	o.failures = append(o.failures, r)
}

func appRefs() []db.AppRefRow {
	return []db.AppRefRow{{AppID: teamA.AppID, TeamID: teamA.TeamID, TeamSlug: "acme", AppSlug: "web"}}
}

func readyPod(uid string, ready bool) k3s.PodSnapshot {
	started := now.Add(-time.Hour)
	p := runningPod(uid, started)
	p.Ready = ready
	return p
}

func TestSamplerAggregatesPodsIntoOneRowPerApp(t *testing.T) {
	cluster := fakeSampleCluster{
		pods: []k3s.PodSnapshot{readyPod("p1", true), readyPod("p2", false)},
		usage: []k3s.PodUsage{
			{UID: "p1", CPUMillicores: 40, MemoryBytes: 100 << 20},
			{UID: "p2", CPUMillicores: 25, MemoryBytes: 50 << 20},
		},
	}
	store := &fakeSampleStore{refs: appRefs()}
	obs := &sampleObserver{}
	s := NewSampler(cluster, store, obs, nil, func() time.Time { return now })

	written, err := s.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if written != 1 || len(store.written) != 1 {
		t.Fatalf("want one row per app, got %d", len(store.written))
	}
	got := store.written[0]
	if got.CPUMillicores != 65 || got.MemoryBytes != 150<<20 {
		t.Errorf("readings not summed across pods: %+v", got)
	}
	if !got.Active {
		t.Error("an app with one ready pod is active")
	}
	if obs.written != 1 {
		t.Errorf("written metric = %d", obs.written)
	}
}

func TestSamplerMarksAppInactiveWhenNoPodIsReady(t *testing.T) {
	cluster := fakeSampleCluster{
		pods:  []k3s.PodSnapshot{readyPod("p1", false)},
		usage: []k3s.PodUsage{{UID: "p1", CPUMillicores: 10}},
	}
	store := &fakeSampleStore{refs: appRefs()}
	s := NewSampler(cluster, store, nil, nil, func() time.Time { return now })

	if _, err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if store.written[0].Active {
		t.Error("no ready pod means the app is not active")
	}
}

// The one rule that matters here: when metrics are unavailable, record
// nothing. Substituting declared resources would file an allocation
// figure as a measurement, and nothing downstream could tell after.
func TestSamplerWritesNothingWhenMetricsUnavailable(t *testing.T) {
	cluster := fakeSampleCluster{
		pods:     []k3s.PodSnapshot{readyPod("p1", true)},
		usageErr: k3s.ErrMetricsAPIUnavailable,
	}
	store := &fakeSampleStore{refs: appRefs()}
	obs := &sampleObserver{}
	s := NewSampler(cluster, store, obs, nil, func() time.Time { return now })

	written, err := s.Tick(context.Background())
	if err != nil {
		t.Fatalf("a missing metrics API is a degradation, not a tick failure: %v", err)
	}
	if written != 0 || len(store.written) != 0 {
		t.Fatalf("wrote %d rows without metrics", len(store.written))
	}
	if len(obs.failures) != 1 || obs.failures[0] != "metrics_api_unavailable" {
		t.Errorf("failures = %v", obs.failures)
	}
	if s.ConsecutiveFailures() != 1 {
		t.Errorf("ConsecutiveFailures = %d, want 1", s.ConsecutiveFailures())
	}
}

func TestSamplerFailureStreakResetsOnSuccess(t *testing.T) {
	cluster := fakeSampleCluster{usageErr: k3s.ErrMetricsAPIUnavailable}
	store := &fakeSampleStore{refs: appRefs()}
	s := NewSampler(cluster, store, nil, nil, func() time.Time { return now })

	for i := 0; i < 3; i++ {
		if _, err := s.Tick(context.Background()); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	if s.ConsecutiveFailures() != 3 {
		t.Fatalf("ConsecutiveFailures = %d, want 3", s.ConsecutiveFailures())
	}

	s.cluster = fakeSampleCluster{
		pods:  []k3s.PodSnapshot{readyPod("p1", true)},
		usage: []k3s.PodUsage{{UID: "p1", CPUMillicores: 10}},
	}
	if _, err := s.Tick(context.Background()); err != nil {
		t.Fatalf("recovery tick: %v", err)
	}
	if s.ConsecutiveFailures() != 0 {
		t.Errorf("streak not reset after a successful tick: %d", s.ConsecutiveFailures())
	}
}

// Metrics arrive for a pod the ledger cannot attribute (an orphan, or
// one that vanished between the two reads). It belongs to nobody.
func TestSamplerSkipsUnattributablePods(t *testing.T) {
	cluster := fakeSampleCluster{
		pods:  nil,
		usage: []k3s.PodUsage{{UID: "ghost", CPUMillicores: 500}},
	}
	store := &fakeSampleStore{refs: appRefs()}
	s := NewSampler(cluster, store, nil, nil, func() time.Time { return now })

	if _, err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(store.written) != 0 {
		t.Fatalf("attributed an unknown pod: %+v", store.written)
	}
}

func TestSamplerExpiresAtThirtyDays(t *testing.T) {
	cluster := fakeSampleCluster{}
	store := &fakeSampleStore{refs: appRefs()}
	s := NewSampler(cluster, store, nil, nil, func() time.Time { return now })

	if _, err := s.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if days := now.Sub(store.gcCutoff).Hours() / 24; days != 30 {
		t.Errorf("GC cutoff is %.0f days back, want 30", days)
	}
}

func TestSamplerSurfacesWriteFailure(t *testing.T) {
	cluster := fakeSampleCluster{
		pods:  []k3s.PodSnapshot{readyPod("p1", true)},
		usage: []k3s.PodUsage{{UID: "p1", CPUMillicores: 10}},
	}
	store := &fakeSampleStore{refs: appRefs(), writeErr: errors.New("db down")}
	obs := &sampleObserver{}
	s := NewSampler(cluster, store, obs, nil, func() time.Time { return now })

	if _, err := s.Tick(context.Background()); err == nil {
		t.Fatal("want the write failure to surface")
	}
	if len(obs.failures) != 1 || obs.failures[0] != "write_failed" {
		t.Errorf("failures = %v", obs.failures)
	}
}
