package leader

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coordinationv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// Config configures LeaseLeader. Namespace / LeaseName / Identity /
// Client are required. LeaseDuration / RenewDeadline / RetryPeriod
// default to spec § 4.2 (15s / 10s / 2s) when zero; production must
// not override these without an ADR (spec § 14 hard rule #3).
type Config struct {
	Namespace string
	LeaseName string
	Identity  string
	Client    coordinationv1client.LeasesGetter

	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration

	Observer Observer
}

// LeaseLeader implements Leader using a K8s coordination v1 Lease
// object. Production wires Run() in a background goroutine; the
// reconciler stays as the only consumer of IsLeader() via the pull
// gate (spec § 5.3, M5.3 Runner.runDeployStatus + friends).
type LeaseLeader struct {
	cfg     Config
	lock    *resourcelock.LeaseLock

	leader      atomic.Bool
	mu          sync.Mutex
	lastObserved string
}

// NewLeaseLeader builds a LeaseLeader. Lease timings default to
// spec § 4.2 when zero. ReleaseOnCancel is always true (spec § 14
// hard rule #4); the field is not exposed via Config.
func NewLeaseLeader(cfg Config) (*LeaseLeader, error) {
	if cfg.Namespace == "" {
		return nil, errors.New("leader: Config.Namespace is required")
	}
	if cfg.LeaseName == "" {
		return nil, errors.New("leader: Config.LeaseName is required")
	}
	if cfg.Identity == "" {
		return nil, errors.New("leader: Config.Identity is required")
	}
	if cfg.Client == nil {
		return nil, errors.New("leader: Config.Client is required")
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = 15 * time.Second
	}
	if cfg.RenewDeadline <= 0 {
		cfg.RenewDeadline = 10 * time.Second
	}
	if cfg.RetryPeriod <= 0 {
		cfg.RetryPeriod = 2 * time.Second
	}
	if cfg.Observer == nil {
		cfg.Observer = NopObserver{}
	}
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      cfg.LeaseName,
			Namespace: cfg.Namespace,
		},
		Client: cfg.Client,
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: cfg.Identity,
		},
	}
	return &LeaseLeader{cfg: cfg, lock: lock}, nil
}

// IsLeader returns whether the local pod is the active leader.
func (l *LeaseLeader) IsLeader() bool { return l.leader.Load() }

// Identity returns the holder identity reported to the Lease.
func (l *LeaseLeader) Identity() string { return l.cfg.Identity }

// LeaseDuration / RenewDeadline / RetryPeriod expose the timings; the
// chart_test layer + ops verification depend on these defaults
// matching spec § 4.2.
func (l *LeaseLeader) LeaseDuration() time.Duration { return l.cfg.LeaseDuration }
func (l *LeaseLeader) RenewDeadline() time.Duration { return l.cfg.RenewDeadline }
func (l *LeaseLeader) RetryPeriod() time.Duration   { return l.cfg.RetryPeriod }

// ReleaseOnCancel is always true; exposed for tests asserting the
// spec § 14 hard rule #4 invariant.
func (l *LeaseLeader) ReleaseOnCancel() bool { return true }

// Run blocks until ctx is cancelled or the underlying lease is lost
// permanently. On exit OnStoppedLeading fires once (client-go
// guarantee). Safe to call from a goroutine launched at process start.
func (l *LeaseLeader) Run(ctx context.Context) {
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            l.lock,
		LeaseDuration:   l.cfg.LeaseDuration,
		RenewDeadline:   l.cfg.RenewDeadline,
		RetryPeriod:     l.cfg.RetryPeriod,
		ReleaseOnCancel: true,
		Name:            l.cfg.LeaseName,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: l.onStartedLeading,
			OnStoppedLeading: l.onStoppedLeading,
			OnNewLeader:      l.onNewLeader,
		},
	})
}

func (l *LeaseLeader) onStartedLeading(_ context.Context) {
	// CompareAndSwap → fire OnGained exactly once per transition into
	// leader, even when client-go invokes the callback multiple times
	// (e.g. test injection, future client-go semantics changes).
	if !l.leader.CompareAndSwap(false, true) {
		return
	}
	l.cfg.Observer.OnGained(l.cfg.Identity)
}

func (l *LeaseLeader) onStoppedLeading() {
	// CompareAndSwap → only emit OnLost when we were leader. client-go
	// always calls OnStoppedLeading on Run exit, even without a prior
	// OnStartedLeading; we must not record a phantom "lost" in that case.
	if !l.leader.CompareAndSwap(true, false) {
		return
	}
	l.cfg.Observer.OnLost(l.cfg.Identity)
}

func (l *LeaseLeader) onNewLeader(newID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if newID == "" {
		return
	}
	if newID == l.lastObserved {
		return
	}
	previous := l.lastObserved
	l.lastObserved = newID
	// First sighting of any leader is not a "handover" — there was no
	// prior leader to hand over from.
	if previous == "" && newID == l.cfg.Identity {
		return
	}
	l.cfg.Observer.OnNewLeader(l.cfg.Identity, newID)
}
