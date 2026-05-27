package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/winshare/zeroops/internal/server/leader"
	"github.com/winshare/zeroops/internal/server/observability"
	"github.com/winshare/zeroops/internal/server/services/reconciler"
)

func TestBuildLeaderAlwaysModeReturnsAlwaysLeader(t *testing.T) {
	metrics := observability.NewMetrics()
	l, run, err := buildLeader("always", "pod-a_aaaaaaaa", metrics, "")
	if err != nil {
		t.Fatalf("buildLeader: %v", err)
	}
	if _, ok := l.(leader.AlwaysLeader); !ok {
		t.Fatalf("mode=always must return AlwaysLeader, got %T", l)
	}
	if run != nil {
		t.Fatal("mode=always must not register a Run goroutine")
	}
	if !l.IsLeader() {
		t.Fatal("AlwaysLeader must report leader=true")
	}
}

func TestBuildLeaderUnknownModeIsRejected(t *testing.T) {
	metrics := observability.NewMetrics()
	_, _, err := buildLeader("master", "pod-a_aaaaaaaa", metrics, "")
	if err == nil {
		t.Fatal("unknown mode must error")
	}
	if !strings.Contains(err.Error(), "OPS_LEADER_MODE") {
		t.Fatalf("error must reference OPS_LEADER_MODE; got %v", err)
	}
}

func TestBuildLeaderEmptyModeDefaultsToAlways(t *testing.T) {
	metrics := observability.NewMetrics()
	l, _, err := buildLeader("", "pod-a_aaaaaaaa", metrics, "")
	if err != nil {
		t.Fatalf("buildLeader: %v", err)
	}
	if _, ok := l.(leader.AlwaysLeader); !ok {
		t.Fatalf("empty mode must default to AlwaysLeader, got %T", l)
	}
}

func TestBuildLeaderLeaseModeWithoutKubeconfigErrors(t *testing.T) {
	metrics := observability.NewMetrics()
	_, _, err := buildLeader("lease", "pod-a_aaaaaaaa", metrics, "/does/not/exist/kubeconfig")
	if err == nil {
		t.Fatal("lease mode without resolvable kubeconfig must error")
	}
}

func TestReconcilerLeaderGateMirrorsLeader(t *testing.T) {
	var l leader.Leader = leader.AlwaysLeader{Name: "pod-a"}
	g := reconcilerLeaderGate{l: l}
	if !g.IsLeader() {
		t.Fatal("gate must mirror AlwaysLeader")
	}
	// Smoke: the adapter satisfies the reconciler.Leader interface.
	var _ reconciler.Leader = g
}

func TestMetricsLeaderObserverEmitsAllCallbacks(t *testing.T) {
	metrics := observability.NewMetrics()
	o := newMetricsLeaderObserver(metrics)
	o.OnGained("pod-a_aaaaaaaa")
	o.OnLost("pod-a_aaaaaaaa")
	o.OnNewLeader("pod-a_aaaaaaaa", "pod-b_bbbbbbbb")
	o.OnLeaseRenew("acquired")
	// Smoke: each call must not panic and feed observability.Metrics.
	// Full prom render coverage lives in observability/metrics_test.go.
}

// guard against silent reordering: builder must not register Run when
// mode is "always".
func TestBuildLeaderRunOnlyForLease(t *testing.T) {
	metrics := observability.NewMetrics()
	_, run, _ := buildLeader("always", "id", metrics, "")
	if run != nil {
		t.Fatal("always mode must not return a Run handle")
	}
}

// Sentinel: keep test failures distinguishable from build errors when the
// constructor surface changes (helps future PRs notice signature drift).
var _ = errors.New
