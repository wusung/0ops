package notify

import "time"

// DefaultMaxAttempts is the spec § 7.3 retry ceiling. After this many failed
// attempts a delivery is dropped (status='dropped'), not retried.
const DefaultMaxAttempts = 6

// backoffLadder is the spec § 7.3 exponential retry schedule, indexed by the
// number of attempts already made. ladder[n] is the wait before attempt n+1.
// Index 0 is unused (no wait before the first attempt).
var backoffLadder = []time.Duration{
	0,
	1 * time.Minute,  // after attempt 1 → attempt 2
	5 * time.Minute,  // after attempt 2 → attempt 3
	30 * time.Minute, // after attempt 3 → attempt 4
	2 * time.Hour,    // after attempt 4 → attempt 5
	6 * time.Hour,    // after attempt 5 → attempt 6
}

// BaseBackoff returns the un-jittered wait before the next attempt, given the
// number of attempts already made. For attempts at or beyond the ladder it
// returns the last rung (callers drop at max_attempts before reaching here).
func BaseBackoff(attempt int) time.Duration {
	if attempt < 1 {
		return backoffLadder[1]
	}
	if attempt >= len(backoffLadder) {
		return backoffLadder[len(backoffLadder)-1]
	}
	return backoffLadder[attempt]
}

// NextBackoff applies ±10% jitter to BaseBackoff to avoid synchronized retry
// storms (spec § 7.3). rnd must return a value in [0,1); nil is treated as 0.5
// (no jitter). The result is always positive.
func NextBackoff(attempt int, rnd func() float64) time.Duration {
	base := BaseBackoff(attempt)
	r := 0.5
	if rnd != nil {
		r = rnd()
	}
	// map [0,1) → [-0.1, +0.1)
	factor := 1.0 + (r*2-1)*0.1
	d := time.Duration(float64(base) * factor)
	if d < 0 {
		d = base
	}
	return d
}
