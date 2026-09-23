package usage

import (
	"math/big"
	"testing"
	"time"

	"github.com/wusung/0ops/internal/server/db"
)

var day = time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)

func interval(start time.Time, dur time.Duration, cpu int, mem int64) db.AllocationInterval {
	in := db.AllocationInterval{
		PodUID:        "p",
		CPUMillicores: cpu,
		MemoryBytes:   mem,
		StartedAt:     start,
	}
	if dur > 0 {
		end := start.Add(dur)
		in.EndedAt = &end
	}
	return in
}

// A pod allocated 500m for a whole day owes exactly 500 x 86400
// millicore-seconds. No sampling error, no tolerance.
func TestFullDayIsExact(t *testing.T) {
	from, to := DayBounds(day)
	totals := Integrate([]db.AllocationInterval{interval(from, 24*time.Hour, 500, 0)}, from, to)

	if want := int64(500) * 86400; totals.CPUMillicoreSeconds != want {
		t.Errorf("CPUMillicoreSeconds = %d, want %d", totals.CPUMillicoreSeconds, want)
	}
	if totals.PodSeconds != 86400 {
		t.Errorf("PodSeconds = %d, want 86400", totals.PodSeconds)
	}
}

// Sub-minute lifetimes are exactly the case a 5-minute sampler loses.
func TestShortLifetimeIsBilledToTheSecond(t *testing.T) {
	from, to := DayBounds(day)
	totals := Integrate([]db.AllocationInterval{interval(from.Add(time.Hour), 37*time.Second, 100, 0)}, from, to)

	if totals.PodSeconds != 37 {
		t.Errorf("PodSeconds = %d, want 37", totals.PodSeconds)
	}
	if want := int64(3700); totals.CPUMillicoreSeconds != want {
		t.Errorf("CPUMillicoreSeconds = %d, want %d", totals.CPUMillicoreSeconds, want)
	}
}

func TestIntervalSpanningMidnightIsSplit(t *testing.T) {
	from, to := DayBounds(day)
	// 23:50 → 00:10 the next day.
	in := interval(to.Add(-10*time.Minute), 20*time.Minute, 100, 0)

	first := Integrate([]db.AllocationInterval{in}, from, to)
	nextFrom, nextTo := DayBounds(to)
	second := Integrate([]db.AllocationInterval{in}, nextFrom, nextTo)

	if first.PodSeconds != 600 || second.PodSeconds != 600 {
		t.Fatalf("split = %d/%d seconds, want 600/600", first.PodSeconds, second.PodSeconds)
	}
	if first.PodSeconds+second.PodSeconds != 1200 {
		t.Error("split must conserve total time")
	}
}

func TestOpenIntervalIsClampedToWindowEnd(t *testing.T) {
	from, to := DayBounds(day)
	// Started mid-day, still running now (long after this day closed).
	totals := Integrate([]db.AllocationInterval{interval(from.Add(12*time.Hour), 0, 100, 0)}, from, to)

	if totals.PodSeconds != 12*3600 {
		t.Errorf("PodSeconds = %d, want %d — an open interval must not bill past the window",
			totals.PodSeconds, 12*3600)
	}
}

func TestIntervalOutsideWindowContributesNothing(t *testing.T) {
	from, to := DayBounds(day)
	before := interval(from.Add(-2*time.Hour), time.Hour, 100, 0)
	after := interval(to.Add(time.Hour), time.Hour, 100, 0)

	totals := Integrate([]db.AllocationInterval{before, after}, from, to)
	if totals.PodSeconds != 0 || totals.IntervalCount != 0 {
		t.Errorf("out-of-window intervals counted: %+v", totals)
	}
}

func TestMemorySecondsExceedInt64Range(t *testing.T) {
	from, to := DayBounds(day)
	// 128Gi for a day, twenty apps over: past what int64 holds.
	const gi128 = int64(137438953472)
	var intervals []db.AllocationInterval
	for i := 0; i < 20; i++ {
		intervals = append(intervals, interval(from, 24*time.Hour, 0, gi128))
	}
	totals := Integrate(intervals, from, to)

	want := new(big.Int).Mul(big.NewInt(gi128), big.NewInt(86400))
	want.Mul(want, big.NewInt(20))
	if totals.MemoryByteSeconds.Cmp(want) != 0 {
		t.Errorf("MemoryByteSeconds = %s, want %s", totals.MemoryByteSeconds, want)
	}
	if !want.IsInt64() {
		t.Log("confirmed: this total does not fit in an int64, which is why the column is numeric")
	}
}

func TestEstimatedSecondsTrackedSeparately(t *testing.T) {
	from, to := DayBounds(day)
	exact := interval(from, time.Hour, 100, 0)
	guessed := interval(from.Add(2*time.Hour), 30*time.Minute, 100, 0)
	guessed.Estimated = true

	totals := Integrate([]db.AllocationInterval{exact, guessed}, from, to)
	if totals.PodSeconds != 5400 {
		t.Errorf("PodSeconds = %d, want 5400", totals.PodSeconds)
	}
	if totals.EstimatedSeconds != 1800 {
		t.Errorf("EstimatedSeconds = %d, want 1800 — callers need to know how much of a day is a guess",
			totals.EstimatedSeconds)
	}
}

func TestGPUSecondsScaleWithCount(t *testing.T) {
	from, to := DayBounds(day)
	in := interval(from, time.Hour, 0, 0)
	in.GPUCount = 4

	totals := Integrate([]db.AllocationInterval{in}, from, to)
	if want := int64(4 * 3600); totals.GPUCountSeconds != want {
		t.Errorf("GPUCountSeconds = %d, want %d", totals.GPUCountSeconds, want)
	}
}

// Recomputing a day must land on the same numbers; anything else would
// mean a retry changes the bill.
func TestRollupForDayIsDeterministic(t *testing.T) {
	from, _ := DayBounds(day)
	intervals := []db.AllocationInterval{
		interval(from.Add(time.Hour), 2*time.Hour, 250, 1<<30),
		interval(from.Add(4*time.Hour), 30*time.Minute, 100, 1<<28),
	}
	first := RollupForDay("t", "a", day, intervals)
	second := RollupForDay("t", "a", day, intervals)

	if first.CPUMillicoreSeconds != second.CPUMillicoreSeconds ||
		first.PodSeconds != second.PodSeconds ||
		first.MemoryByteSeconds.Cmp(second.MemoryByteSeconds) != 0 ||
		first.IntervalCount != second.IntervalCount {
		t.Fatalf("recomputation drifted:\n%+v\n%+v", first, second)
	}
	if !first.Day.Equal(from) {
		t.Errorf("Day = %v, want the UTC day start %v", first.Day, from)
	}
}

// DayBounds must key off the UTC calendar, not the process timezone.
func TestDayBoundsIsUTC(t *testing.T) {
	loc := time.FixedZone("UTC+13", 13*3600)
	// 2026-09-21T23:00Z is already 2026-09-22 in UTC+13.
	local := time.Date(2026, 9, 21, 23, 0, 0, 0, time.UTC).In(loc)

	from, to := DayBounds(local.UTC())
	if from.Day() != 21 || to.Day() != 22 {
		t.Errorf("bounds = %v..%v, want the UTC day 2026-09-21", from, to)
	}
}
