package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestLimiterAllowDrainsBucket(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	lim := New(Config{Quotas: DefaultPlanQuotas(), Now: clock})

	// free per-token write = 30/min. Burst defaults to perMinute (initial bucket full).
	for i := 0; i < 30; i++ {
		ok, _ := lim.Allow(ScopePerToken, "tok-1", PlanFree, CategoryWrite)
		if !ok {
			t.Fatalf("Allow #%d should succeed (bucket should hold full per-minute burst)", i+1)
		}
	}
	ok, retryAfter := lim.Allow(ScopePerToken, "tok-1", PlanFree, CategoryWrite)
	if ok {
		t.Fatalf("Allow #31 should fail (bucket drained)")
	}
	if retryAfter <= 0 || retryAfter > time.Minute {
		t.Fatalf("retryAfter = %v, want (0, 60s]", retryAfter)
	}
}

func TestLimiterBucketsIsolatedByKey(t *testing.T) {
	lim := New(Config{Quotas: DefaultPlanQuotas()})
	for i := 0; i < 30; i++ {
		if ok, _ := lim.Allow(ScopePerToken, "tok-A", PlanFree, CategoryWrite); !ok {
			t.Fatalf("tok-A request #%d should succeed", i+1)
		}
	}
	if ok, _ := lim.Allow(ScopePerToken, "tok-A", PlanFree, CategoryWrite); ok {
		t.Fatalf("tok-A should be drained")
	}
	if ok, _ := lim.Allow(ScopePerToken, "tok-B", PlanFree, CategoryWrite); !ok {
		t.Fatalf("tok-B should be unaffected by tok-A drain")
	}
}

func TestLimiterUnlimitedWhenQuotaZero(t *testing.T) {
	lim := New(Config{Quotas: DefaultPlanQuotas()})
	// per-team read has no quota cell; should always allow.
	for i := 0; i < 5000; i++ {
		if ok, _ := lim.Allow(ScopePerTeam, "team-1", PlanFree, CategoryRead); !ok {
			t.Fatalf("per-team read should be unlimited (no quota cell)")
		}
	}
}

func TestLimiterInvalidatePlanRebuildsBucket(t *testing.T) {
	lim := New(Config{Quotas: DefaultPlanQuotas()})
	// Drain free per-token write bucket.
	for i := 0; i < 30; i++ {
		_, _ = lim.Allow(ScopePerToken, "tok-1", PlanFree, CategoryWrite)
	}
	if ok, _ := lim.Allow(ScopePerToken, "tok-1", PlanFree, CategoryWrite); ok {
		t.Fatalf("expected drained bucket")
	}
	// Plan upgraded → invalidate → new bucket has full burst.
	lim.InvalidateKey(ScopePerToken, "tok-1")
	if ok, _ := lim.Allow(ScopePerToken, "tok-1", PlanStarter, CategoryWrite); !ok {
		t.Fatalf("after InvalidateKey starter bucket should be available")
	}
}

func TestLimiterCleanupRemovesIdleBuckets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := &fakeClock{now: now}
	lim := New(Config{Quotas: DefaultPlanQuotas(), Now: clock.Now, IdleTTL: 24 * time.Hour})
	_, _ = lim.Allow(ScopePerToken, "tok-1", PlanFree, CategoryWrite)
	if got := lim.ActiveBuckets(); got != 1 {
		t.Fatalf("ActiveBuckets = %d, want 1", got)
	}
	clock.advance(25 * time.Hour)
	removed := lim.SweepIdle(context.Background())
	if removed != 1 || lim.ActiveBuckets() != 0 {
		t.Fatalf("SweepIdle removed=%d, active=%d, want 1/0", removed, lim.ActiveBuckets())
	}
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time           { return c.now }
func (c *fakeClock) advance(d time.Duration)  { c.now = c.now.Add(d) }
