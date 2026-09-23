package usage

import (
	"math/big"
	"time"

	"github.com/wusung/0ops/internal/server/db"
)

// Totals is the integral of a set of allocation intervals over a window.
//
// Integration is closed-form: an interval contributes (allocation x
// overlap). There is no interpolation, no gap filling and no sampling
// error to correct for — the inputs are exact.
type Totals struct {
	CPUMillicoreSeconds int64
	MemoryByteSeconds   *big.Int
	GPUCountSeconds     int64
	PodSeconds          int64
	EstimatedSeconds    int64
	IntervalCount       int
}

// Integrate sums intervals over [from, to). Intervals still open are
// clamped at `to`, which lets the same function serve both the rollup of
// a closed day and the live view of today — two implementations would
// eventually disagree (spec § 14 rule #6).
func Integrate(intervals []db.AllocationInterval, from, to time.Time) Totals {
	totals := Totals{MemoryByteSeconds: big.NewInt(0)}

	for _, in := range intervals {
		start := in.StartedAt
		if start.Before(from) {
			start = from
		}
		end := to
		if in.EndedAt != nil && in.EndedAt.Before(end) {
			end = *in.EndedAt
		}
		if !end.After(start) {
			continue
		}

		seconds := int64(end.Sub(start) / time.Second)
		if seconds == 0 {
			continue
		}

		totals.CPUMillicoreSeconds += int64(in.CPUMillicores) * seconds
		totals.MemoryByteSeconds.Add(totals.MemoryByteSeconds,
			new(big.Int).Mul(big.NewInt(in.MemoryBytes), big.NewInt(seconds)))
		totals.GPUCountSeconds += int64(in.GPUCount) * seconds
		totals.PodSeconds += seconds
		if in.Estimated {
			totals.EstimatedSeconds += seconds
		}
		totals.IntervalCount++
	}

	return totals
}

// DayBounds returns the UTC day containing t as a half-open range. An
// interval spanning midnight is split across both days rather than being
// attributed wholesale to one of them (spec § 14 rule #5).
func DayBounds(t time.Time) (from, to time.Time) {
	from = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return from, from.AddDate(0, 0, 1)
}

// RollupForDay integrates one app's intervals over one UTC day. The
// result is a complete replacement for that day's row: rollups are
// recomputed, never incremented, so re-running a day is a no-op.
func RollupForDay(teamID, appID string, day time.Time, intervals []db.AllocationInterval) db.DailyRollup {
	from, to := DayBounds(day)
	totals := Integrate(intervals, from, to)
	return db.DailyRollup{
		TeamID:              teamID,
		AppID:               appID,
		Day:                 from,
		CPUMillicoreSeconds: totals.CPUMillicoreSeconds,
		MemoryByteSeconds:   totals.MemoryByteSeconds,
		GPUCountSeconds:     totals.GPUCountSeconds,
		PodSeconds:          totals.PodSeconds,
		EstimatedSeconds:    totals.EstimatedSeconds,
		IntervalCount:       totals.IntervalCount,
	}
}
