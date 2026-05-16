package leader

import (
	"context"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func newTestLeaseLeader(t *testing.T, identity string, obs Observer) *LeaseLeader {
	t.Helper()
	cs := fake.NewSimpleClientset()
	ll, err := NewLeaseLeader(Config{
		Namespace: "system-0ops",
		LeaseName: "0ops-backend-leader",
		Identity:  identity,
		Client:    cs.CoordinationV1(),
		Observer:  obs,
	})
	if err != nil {
		t.Fatalf("NewLeaseLeader: %v", err)
	}
	return ll
}

func TestLeaseLeaderStartsAsFollower(t *testing.T) {
	ll := newTestLeaseLeader(t, "pod-a_aaaaaaaa", NopObserver{})
	if ll.IsLeader() {
		t.Fatal("LeaseLeader must start as follower before OnStartedLeading fires")
	}
	if ll.Identity() != "pod-a_aaaaaaaa" {
		t.Fatalf("Identity() = %q, want %q", ll.Identity(), "pod-a_aaaaaaaa")
	}
}

func TestLeaseLeaderOnStartedLeadingFlipsIsLeader(t *testing.T) {
	rec := &recordingObserver{}
	ll := newTestLeaseLeader(t, "pod-a_aaaaaaaa", rec)
	ll.onStartedLeading(context.Background())
	if !ll.IsLeader() {
		t.Fatal("IsLeader must be true after OnStartedLeading")
	}
	if got, want := rec.gained, []string{"pod-a_aaaaaaaa"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("gained = %v, want %v", got, want)
	}
}

func TestLeaseLeaderOnStartedLeadingIdempotent(t *testing.T) {
	rec := &recordingObserver{}
	ll := newTestLeaseLeader(t, "pod-a_aaaaaaaa", rec)
	ll.onStartedLeading(context.Background())
	ll.onStartedLeading(context.Background())
	if !ll.IsLeader() {
		t.Fatal("IsLeader must remain true")
	}
	if len(rec.gained) != 1 {
		t.Fatalf("OnGained fired %d times, want 1 (idempotent)", len(rec.gained))
	}
}

func TestLeaseLeaderOnStoppedLeadingFlipsBack(t *testing.T) {
	rec := &recordingObserver{}
	ll := newTestLeaseLeader(t, "pod-a_aaaaaaaa", rec)
	ll.onStartedLeading(context.Background())
	ll.onStoppedLeading()
	if ll.IsLeader() {
		t.Fatal("IsLeader must be false after OnStoppedLeading")
	}
	if got := rec.lost; len(got) != 1 || got[0] != "pod-a_aaaaaaaa" {
		t.Fatalf("lost = %v, want [pod-a_aaaaaaaa]", got)
	}
}

func TestLeaseLeaderOnStoppedLeadingIdempotent(t *testing.T) {
	rec := &recordingObserver{}
	ll := newTestLeaseLeader(t, "pod-a_aaaaaaaa", rec)
	// OnStoppedLeading can fire without a prior OnStartedLeading
	// (client-go calls it on Run exit unconditionally).
	ll.onStoppedLeading()
	ll.onStoppedLeading()
	if ll.IsLeader() {
		t.Fatal("IsLeader must remain false")
	}
	if len(rec.lost) != 0 {
		t.Fatalf("OnLost fired %d times without prior gain, want 0", len(rec.lost))
	}
}

func TestLeaseLeaderOnNewLeaderCountsOnlyForeignHandovers(t *testing.T) {
	rec := &recordingObserver{}
	ll := newTestLeaseLeader(t, "pod-a_aaaaaaaa", rec)
	// Same identity as us → no handover.
	ll.onNewLeader("pod-a_aaaaaaaa")
	if len(rec.handovers) != 0 {
		t.Fatalf("self-leader event must not emit handover; got %v", rec.handovers)
	}
	// Different identity → one handover.
	ll.onNewLeader("pod-b_bbbbbbbb")
	if len(rec.handovers) != 1 || rec.handovers[0].next != "pod-b_bbbbbbbb" {
		t.Fatalf("handovers = %v, want [{pod-a_aaaaaaaa pod-b_bbbbbbbb}]", rec.handovers)
	}
	// Same identity repeated → no further handover.
	ll.onNewLeader("pod-b_bbbbbbbb")
	if len(rec.handovers) != 1 {
		t.Fatalf("repeat foreign leader must not emit handover; got %v", rec.handovers)
	}
	// Back to us → handover counted (leader transition).
	ll.onNewLeader("pod-a_aaaaaaaa")
	if len(rec.handovers) != 2 {
		t.Fatalf("handovers back to us = %d, want 2", len(rec.handovers))
	}
}

func TestNewLeaseLeaderRejectsMissingFields(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cases := []struct {
		name string
		cfg  Config
	}{
		{"missing namespace", Config{LeaseName: "n", Identity: "i", Client: cs.CoordinationV1()}},
		{"missing lease name", Config{Namespace: "n", Identity: "i", Client: cs.CoordinationV1()}},
		{"missing identity", Config{Namespace: "n", LeaseName: "n", Client: cs.CoordinationV1()}},
		{"missing client", Config{Namespace: "n", LeaseName: "n", Identity: "i"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewLeaseLeader(c.cfg); err == nil {
				t.Fatal("NewLeaseLeader must reject missing required field")
			}
		})
	}
}

func TestNewLeaseLeaderDefaultsLeaseTimings(t *testing.T) {
	cs := fake.NewSimpleClientset()
	ll, err := NewLeaseLeader(Config{
		Namespace: "system-0ops",
		LeaseName: "0ops-backend-leader",
		Identity:  "pod-a_aaaaaaaa",
		Client:    cs.CoordinationV1(),
	})
	if err != nil {
		t.Fatalf("NewLeaseLeader: %v", err)
	}
	if got, want := ll.LeaseDuration(), 15*time.Second; got != want {
		t.Fatalf("LeaseDuration = %v, want %v", got, want)
	}
	if got, want := ll.RenewDeadline(), 10*time.Second; got != want {
		t.Fatalf("RenewDeadline = %v, want %v", got, want)
	}
	if got, want := ll.RetryPeriod(), 2*time.Second; got != want {
		t.Fatalf("RetryPeriod = %v, want %v", got, want)
	}
	if !ll.ReleaseOnCancel() {
		t.Fatal("ReleaseOnCancel must always be true (spec § 14 hard rule #4)")
	}
}

// Smoke-level: a stopped context exits Run cleanly without panicking
// and reports follower state. We do not exercise full acquire because
// client-go's fake clientset does not support the full Lease semantics
// in a single sequential test run; spec § 10 verification of real-cluster
// handover lives in the deploy/server chart guard + ops staging drill.
func TestLeaseLeaderRunReturnsWhenCtxAlreadyCancelled(t *testing.T) {
	rec := &recordingObserver{}
	cs := fake.NewSimpleClientset(&coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "0ops-backend-leader",
			Namespace: "system-0ops",
		},
	})
	ll, err := NewLeaseLeader(Config{
		Namespace: "system-0ops",
		LeaseName: "0ops-backend-leader",
		Identity:  "pod-a_aaaaaaaa",
		Client:    cs.CoordinationV1(),
		Observer:  rec,
	})
	if err != nil {
		t.Fatalf("NewLeaseLeader: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		ll.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit within 2s after ctx cancel")
	}
	if ll.IsLeader() {
		t.Fatal("IsLeader must be false after Run exits")
	}
}
