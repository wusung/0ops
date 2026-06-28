package notify

import (
	"testing"
	"time"
)

func TestBaseBackoffLadder(t *testing.T) {
	want := map[int]time.Duration{
		1: 1 * time.Minute,
		2: 5 * time.Minute,
		3: 30 * time.Minute,
		4: 2 * time.Hour,
		5: 6 * time.Hour,
	}
	for attempt, d := range want {
		if got := BaseBackoff(attempt); got != d {
			t.Errorf("BaseBackoff(%d) = %v, want %v", attempt, got, d)
		}
	}
}

func TestNextBackoffJitterWithinTenPercent(t *testing.T) {
	base := BaseBackoff(2) // 5 min
	// rnd = 0 → -10%, rnd → 1 (clamped) → +10%
	low := NextBackoff(2, func() float64 { return 0 })
	high := NextBackoff(2, func() float64 { return 0.999999 })
	if low >= base {
		t.Errorf("low jitter %v not below base %v", low, base)
	}
	if high <= base {
		t.Errorf("high jitter %v not above base %v", high, base)
	}
	minD := time.Duration(float64(base) * 0.89)
	maxD := time.Duration(float64(base) * 1.11)
	if low < minD || high > maxD {
		t.Errorf("jitter out of ±10%% bounds: low=%v high=%v", low, high)
	}
}

func TestNextBackoffNilRandIsBase(t *testing.T) {
	if got := NextBackoff(3, nil); got != BaseBackoff(3) {
		t.Errorf("nil rnd backoff = %v, want base %v", got, BaseBackoff(3))
	}
}
