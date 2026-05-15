package ratelimit

import (
	"context"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Config configures a Limiter.
type Config struct {
	// Quotas describes per-plan per-scope per-category requests-per-minute.
	Quotas map[Plan]PlanQuotas
	// Now is the clock used for retry-after / idle calculations. Defaults
	// to time.Now.
	Now func() time.Time
	// IdleTTL marks a bucket as eligible for cleanup if untouched longer
	// than this. Defaults to 24h.
	IdleTTL time.Duration
}

// Limiter is a per-key token bucket pool. Safe for concurrent use.
type Limiter struct {
	cfg     Config
	buckets sync.Map // string-key -> *bucketEntry
}

type bucketEntry struct {
	mu        sync.Mutex
	limiter   *rate.Limiter
	plan      Plan
	perMinute int
	lastUse   time.Time
}

// New constructs a Limiter from cfg. nil quotas → DefaultPlanQuotas.
func New(cfg Config) *Limiter {
	if cfg.Quotas == nil {
		cfg.Quotas = DefaultPlanQuotas()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.IdleTTL <= 0 {
		cfg.IdleTTL = 24 * time.Hour
	}
	return &Limiter{cfg: cfg}
}

// Allow consumes one token from the (scope,key,plan,cat) bucket. If the
// bucket is empty, returns ok=false and the duration the caller should
// wait before the next request would succeed.
func (l *Limiter) Allow(scope Scope, key string, plan Plan, cat Category) (bool, time.Duration) {
	q := QuotaFor(l.cfg.Quotas, plan, scope, cat)
	if q.PerMinute <= 0 {
		return true, 0
	}
	entry := l.entry(scope, key, cat, plan, q.PerMinute)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	now := l.cfg.Now()
	entry.lastUse = now
	res := entry.limiter.ReserveN(now, 1)
	if !res.OK() {
		return false, time.Hour
	}
	delay := res.DelayFrom(now)
	if delay > 0 {
		res.CancelAt(now)
		return false, delay
	}
	return true, 0
}

// InvalidateKey removes any cached bucket for (scope,key); the next Allow
// call rebuilds it (used when a team's plan tier changes).
func (l *Limiter) InvalidateKey(scope Scope, key string) {
	prefix := string(scope) + "|" + key + "|"
	l.buckets.Range(func(k, _ any) bool {
		ks, ok := k.(string)
		if !ok {
			return true
		}
		if strings.HasPrefix(ks, prefix) {
			l.buckets.Delete(k)
		}
		return true
	})
}

// ActiveBuckets returns the current count of in-memory buckets.
func (l *Limiter) ActiveBuckets() int {
	count := 0
	l.buckets.Range(func(_, _ any) bool { count++; return true })
	return count
}

// SweepIdle removes buckets untouched for longer than cfg.IdleTTL; returns
// the number removed.
func (l *Limiter) SweepIdle(_ context.Context) int {
	now := l.cfg.Now()
	cutoff := now.Add(-l.cfg.IdleTTL)
	removed := 0
	l.buckets.Range(func(k, v any) bool {
		entry, ok := v.(*bucketEntry)
		if !ok {
			return true
		}
		entry.mu.Lock()
		stale := entry.lastUse.Before(cutoff)
		entry.mu.Unlock()
		if stale {
			l.buckets.Delete(k)
			removed++
		}
		return true
	})
	return removed
}

// RunCleanup blocks until ctx is cancelled, sweeping idle buckets every
// interval. Intended for use in a background goroutine started from main.
func (l *Limiter) RunCleanup(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.SweepIdle(ctx)
		}
	}
}

// Quotas exposes the configured plan quota table (read-only callers).
func (l *Limiter) Quotas() map[Plan]PlanQuotas { return l.cfg.Quotas }

func (l *Limiter) entry(scope Scope, key string, cat Category, plan Plan, perMinute int) *bucketEntry {
	k := bucketKey(scope, key, cat)
	if v, ok := l.buckets.Load(k); ok {
		entry, ok := v.(*bucketEntry)
		if ok {
			entry.mu.Lock()
			needsRebuild := entry.plan != plan || entry.perMinute != perMinute
			entry.mu.Unlock()
			if !needsRebuild {
				return entry
			}
			l.buckets.Delete(k)
		}
	}
	entry := newBucketEntry(plan, perMinute, l.cfg.Now())
	actual, _ := l.buckets.LoadOrStore(k, entry)
	stored, _ := actual.(*bucketEntry)
	if stored == nil {
		return entry
	}
	return stored
}

func newBucketEntry(plan Plan, perMinute int, now time.Time) *bucketEntry {
	// rate.Limit is per-second; perMinute / 60.
	limit := rate.Limit(float64(perMinute) / 60.0)
	bucket := rate.NewLimiter(limit, perMinute)
	return &bucketEntry{
		limiter:   bucket,
		plan:      plan,
		perMinute: perMinute,
		lastUse:   now,
	}
}

func bucketKey(scope Scope, key string, cat Category) string {
	return string(scope) + "|" + key + "|" + string(cat)
}
