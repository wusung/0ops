package domainverify

import (
	"sync"
	"testing"
	"time"
)

func TestMetricsDefaultRecordersAreSafeToCall(t *testing.T) {
	// Reset to defaults so this test is independent of order.
	BindMetrics(nil, nil, nil, nil)
	recordVerifyAttempt("pending", "success")
	recordExpiredCleanup("expired")
	recordGraceTransition("released")
	recordPollerTick("verifyPending", time.Millisecond)
}

func TestBindMetricsCapturesCalls(t *testing.T) {
	var mu sync.Mutex
	var attempts []string
	var cleanups []string
	var graces []string
	var ticks []string
	BindMetrics(
		func(stage, outcome string) {
			mu.Lock()
			defer mu.Unlock()
			attempts = append(attempts, stage+":"+outcome)
		},
		func(outcome string) {
			mu.Lock()
			defer mu.Unlock()
			cleanups = append(cleanups, outcome)
		},
		func(outcome string) {
			mu.Lock()
			defer mu.Unlock()
			graces = append(graces, outcome)
		},
		func(tick string, _ time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			ticks = append(ticks, tick)
		},
	)
	t.Cleanup(func() { BindMetrics(nil, nil, nil, nil) })

	recordVerifyAttempt("pending", "success")
	recordExpiredCleanup("expired")
	recordGraceTransition("released")
	recordPollerTick("verifyPending", time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 1 || attempts[0] != "pending:success" {
		t.Fatalf("attempts=%v", attempts)
	}
	if len(cleanups) != 1 || cleanups[0] != "expired" {
		t.Fatalf("cleanups=%v", cleanups)
	}
	if len(graces) != 1 || graces[0] != "released" {
		t.Fatalf("graces=%v", graces)
	}
	if len(ticks) != 1 || ticks[0] != "verifyPending" {
		t.Fatalf("ticks=%v", ticks)
	}
}

func TestBindMetricsNilResetsToNoOp(t *testing.T) {
	BindMetrics(
		func(string, string) { t.Fatal("verify recorder leaked") },
		func(string) { t.Fatal("cleanup recorder leaked") },
		func(string) { t.Fatal("grace recorder leaked") },
		func(string, time.Duration) { t.Fatal("tick recorder leaked") },
	)
	BindMetrics(nil, nil, nil, nil)
	recordVerifyAttempt("pending", "success")
	recordExpiredCleanup("expired")
	recordGraceTransition("released")
	recordPollerTick("verifyPending", time.Millisecond)
}
