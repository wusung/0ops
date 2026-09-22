package usage

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/wusung/0ops/internal/server/db"
	"github.com/wusung/0ops/internal/shared/dto"
)

type fakeQueryStore struct {
	rollups   []db.DailyRollup
	intervals []db.AllocationInterval
	slugs     map[string]string
	observed  []db.ObservedUsage

	gotFrom, gotTo time.Time
}

func (f *fakeQueryStore) SummarizeObservedUsage(_ context.Context, _, _ string, _, _ time.Time) ([]db.ObservedUsage, error) {
	return f.observed, nil
}

func (f *fakeQueryStore) ListDailyRollups(_ context.Context, _, _ string, from, to time.Time) ([]db.DailyRollup, error) {
	f.gotFrom, f.gotTo = from, to
	return f.rollups, nil
}
func (f *fakeQueryStore) ListAllocationIntervalsOverlapping(_ context.Context, _, _ string, _, _ time.Time) ([]db.AllocationInterval, error) {
	return f.intervals, nil
}
func (f *fakeQueryStore) ListAppSlugsByTeam(context.Context, string) (map[string]string, error) {
	return f.slugs, nil
}

var fixedNow = time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)

func TestQueryMergesRollupsAndLiveToday(t *testing.T) {
	yesterday := utcDay(fixedNow).AddDate(0, 0, -1)
	todayStart := utcDay(fixedNow)
	store := &fakeQueryStore{
		slugs: map[string]string{"a1": "web"},
		rollups: []db.DailyRollup{{
			TeamID: "t", AppID: "a1", Day: yesterday,
			CPUMillicoreSeconds: 100 * 86400, MemoryByteSeconds: big.NewInt(0),
			PodSeconds: 86400,
		}},
		// Today: 100m running since midnight, still open.
		intervals: []db.AllocationInterval{{AppID: "a1", CPUMillicores: 100, StartedAt: todayStart}},
	}
	svc := NewQueryService(store, func() time.Time { return fixedNow })

	resp, err := svc.Query(context.Background(), "t", "", yesterday, todayStart, false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(resp.Apps) != 1 || len(resp.Apps[0].Days) != 2 {
		t.Fatalf("want one app with two days, got %+v", resp.Apps)
	}
	days := resp.Apps[0].Days
	if days[0].Source != dto.UsageSourceRollup || days[1].Source != dto.UsageSourceLive {
		t.Errorf("day sources = %q/%q, want rollup then live", days[0].Source, days[1].Source)
	}
	// Ten hours of 100m so far today.
	if want := int64(100 * 10 * 3600); days[1].Totals.CPUMillicoreSeconds != want {
		t.Errorf("today = %d millicore-seconds, want %d", days[1].Totals.CPUMillicoreSeconds, want)
	}
	if resp.Apps[0].AppSlug != "web" {
		t.Errorf("app slug not resolved: %q", resp.Apps[0].AppSlug)
	}
}

// Today is never read from rollups: it has none, and asking for it would
// return a stale partial day if one somehow existed.
func TestQueryNeverReadsTodayFromRollups(t *testing.T) {
	store := &fakeQueryStore{slugs: map[string]string{}}
	svc := NewQueryService(store, func() time.Time { return fixedNow })

	today := utcDay(fixedNow)
	if _, err := svc.Query(context.Background(), "t", "", today.AddDate(0, 0, -3), today, false); err != nil {
		t.Fatalf("query: %v", err)
	}
	if !store.gotTo.Before(today) {
		t.Errorf("rollup window ended at %v, must stop before today %v", store.gotTo, today)
	}
}

func TestQueryOfPastWindowDoesNotTouchLivePath(t *testing.T) {
	store := &fakeQueryStore{
		slugs: map[string]string{"a1": "web"},
		// A live interval that must not leak into a historical window.
		intervals: []db.AllocationInterval{{AppID: "a1", CPUMillicores: 999, StartedAt: utcDay(fixedNow)}},
		rollups: []db.DailyRollup{{
			TeamID: "t", AppID: "a1", Day: utcDay(fixedNow).AddDate(0, 0, -5),
			CPUMillicoreSeconds: 3600, MemoryByteSeconds: big.NewInt(0), PodSeconds: 3600,
		}},
	}
	svc := NewQueryService(store, func() time.Time { return fixedNow })

	from := utcDay(fixedNow).AddDate(0, 0, -5)
	resp, err := svc.Query(context.Background(), "t", "", from, from.AddDate(0, 0, 2), false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if resp.Totals.CPUMillicoreSeconds != 3600 {
		t.Errorf("historical window picked up live data: %d", resp.Totals.CPUMillicoreSeconds)
	}
}

func TestTotalsCarryExactMemoryAndRenderedHours(t *testing.T) {
	day := utcDay(fixedNow).AddDate(0, 0, -1)
	// 128Gi for a day, twice over: beyond int64.
	const gi128 = int64(137438953472)
	mem := new(big.Int).Mul(big.NewInt(gi128), big.NewInt(86400))
	mem.Mul(mem, big.NewInt(2))
	store := &fakeQueryStore{
		slugs: map[string]string{"a1": "web"},
		rollups: []db.DailyRollup{{
			TeamID: "t", AppID: "a1", Day: day,
			CPUMillicoreSeconds: 2000 * 3600, MemoryByteSeconds: mem,
			GPUCountSeconds: 2 * 3600, PodSeconds: 7200,
		}},
	}
	svc := NewQueryService(store, func() time.Time { return fixedNow })

	resp, err := svc.Query(context.Background(), "t", "", day, day, false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if resp.Totals.MemoryByteSeconds != mem.String() {
		t.Errorf("memory-seconds lost precision: %s vs %s", resp.Totals.MemoryByteSeconds, mem)
	}
	if got := resp.Totals.CPUCoreHours; got != 2 {
		t.Errorf("CPUCoreHours = %v, want 2", got)
	}
	if got := resp.Totals.GPUHours; got != 2 {
		t.Errorf("GPUHours = %v, want 2", got)
	}
}

func TestEstimatedRatioSurfacesPatchyDays(t *testing.T) {
	day := utcDay(fixedNow).AddDate(0, 0, -1)
	store := &fakeQueryStore{
		slugs: map[string]string{"a1": "web"},
		rollups: []db.DailyRollup{{
			TeamID: "t", AppID: "a1", Day: day,
			MemoryByteSeconds: big.NewInt(0), PodSeconds: 1000, EstimatedSeconds: 250,
		}},
	}
	svc := NewQueryService(store, func() time.Time { return fixedNow })

	resp, err := svc.Query(context.Background(), "t", "", day, day, false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got := resp.Totals.EstimatedRatio; got != 0.25 {
		t.Errorf("EstimatedRatio = %v, want 0.25", got)
	}
}

func TestResponseAlwaysCarriesBillingDisclaimer(t *testing.T) {
	svc := NewQueryService(&fakeQueryStore{slugs: map[string]string{}}, func() time.Time { return fixedNow })
	resp, err := svc.Query(context.Background(), "t", "", utcDay(fixedNow), utcDay(fixedNow), false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if resp.BillingDisclaimer != dto.UsageBillingDisclaimer {
		t.Error("every usage response must state that it is not a bill (spec § 14 rule #10)")
	}
}

func TestIntervalDetailIncludedOnRequest(t *testing.T) {
	day := utcDay(fixedNow).AddDate(0, 0, -1)
	end := day.Add(time.Hour)
	reason := db.CloseReasonTerminated
	store := &fakeQueryStore{
		slugs: map[string]string{"a1": "web"},
		intervals: []db.AllocationInterval{{
			PodUID: "p1", AppID: "a1", PodName: "web-1", Namespace: "team-acme",
			CPUMillicores: 100, StartedAt: day, EndedAt: &end, CloseReason: &reason,
		}},
	}
	svc := NewQueryService(store, func() time.Time { return fixedNow })

	resp, err := svc.Query(context.Background(), "t", "", day, day, true)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(resp.Intervals) != 1 {
		t.Fatalf("want 1 interval, got %d", len(resp.Intervals))
	}
	if resp.Intervals[0].EndedAt == nil || *resp.Intervals[0].EndedAt != end.Format(time.RFC3339) {
		t.Errorf("interval end not rendered: %+v", resp.Intervals[0])
	}
}

// Allocation and observation must arrive in separate fields: one says
// what was reserved, the other what was used, and adding them together
// would be meaningless.
func TestObservedUsageIsReportedSeparatelyFromAllocation(t *testing.T) {
	day := utcDay(fixedNow).AddDate(0, 0, -1)
	store := &fakeQueryStore{
		slugs: map[string]string{"a1": "web"},
		rollups: []db.DailyRollup{{
			TeamID: "t", AppID: "a1", Day: day,
			CPUMillicoreSeconds: 500 * 86400, MemoryByteSeconds: big.NewInt(0), PodSeconds: 86400,
		}},
		observed: []db.ObservedUsage{{
			TeamID: "t", AppID: "a1", Day: day,
			CPUMillicoresAvg: 42, CPUMillicoresMax: 310,
			MemoryBytesAvg: 100 << 20, MemoryBytesMax: 180 << 20, SampleCount: 288,
		}},
	}
	svc := NewQueryService(store, func() time.Time { return fixedNow })

	resp, err := svc.QueryWithObserved(context.Background(), "t", "", day, day, false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	app := resp.Apps[0]
	if app.Days[0].Observed == nil {
		t.Fatal("observed block missing")
	}
	if app.Days[0].Observed.CPUMillicoresAvg != 42 || app.Days[0].Observed.CPUMillicoresMax != 310 {
		t.Errorf("observed = %+v", app.Days[0].Observed)
	}
	// The allocation figure must be untouched by the observation.
	if app.Days[0].Totals.CPUMillicoreSeconds != 500*86400 {
		t.Errorf("allocation changed when observation was attached: %d", app.Days[0].Totals.CPUMillicoreSeconds)
	}
	if app.Observed == nil || app.Observed.SampleCount != 288 {
		t.Errorf("window summary = %+v", app.Observed)
	}
}

func TestObservedOmittedWhenNotRequested(t *testing.T) {
	day := utcDay(fixedNow).AddDate(0, 0, -1)
	store := &fakeQueryStore{
		slugs:    map[string]string{"a1": "web"},
		rollups:  []db.DailyRollup{{TeamID: "t", AppID: "a1", Day: day, MemoryByteSeconds: big.NewInt(0), PodSeconds: 1}},
		observed: []db.ObservedUsage{{TeamID: "t", AppID: "a1", Day: day, CPUMillicoresAvg: 42, SampleCount: 1}},
	}
	svc := NewQueryService(store, func() time.Time { return fixedNow })

	resp, err := svc.Query(context.Background(), "t", "", day, day, false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if resp.Apps[0].Observed != nil || resp.Apps[0].Days[0].Observed != nil {
		t.Error("observation must not appear unless asked for")
	}
}

// A day with three samples must not weigh as much as a day with 288
// when averaging across a window.
func TestWindowAverageIsWeightedBySampleCount(t *testing.T) {
	day1 := utcDay(fixedNow).AddDate(0, 0, -2)
	day2 := utcDay(fixedNow).AddDate(0, 0, -1)
	store := &fakeQueryStore{
		slugs: map[string]string{"a1": "web"},
		rollups: []db.DailyRollup{
			{TeamID: "t", AppID: "a1", Day: day1, MemoryByteSeconds: big.NewInt(0), PodSeconds: 1},
			{TeamID: "t", AppID: "a1", Day: day2, MemoryByteSeconds: big.NewInt(0), PodSeconds: 1},
		},
		observed: []db.ObservedUsage{
			{TeamID: "t", AppID: "a1", Day: day1, CPUMillicoresAvg: 100, CPUMillicoresMax: 100, SampleCount: 288},
			{TeamID: "t", AppID: "a1", Day: day2, CPUMillicoresAvg: 1000, CPUMillicoresMax: 1200, SampleCount: 12},
		},
	}
	svc := NewQueryService(store, func() time.Time { return fixedNow })

	resp, err := svc.QueryWithObserved(context.Background(), "t", "", day1, day2, false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	got := resp.Apps[0].Observed
	// Weighted: (100*288 + 1000*12) / 300 = 136. An unweighted mean
	// would give 550.
	if got.CPUMillicoresAvg != 136 {
		t.Errorf("CPUMillicoresAvg = %d, want 136 (weighted by sample count)", got.CPUMillicoresAvg)
	}
	if got.CPUMillicoresMax != 1200 {
		t.Errorf("CPUMillicoresMax = %d, want the window peak 1200", got.CPUMillicoresMax)
	}
}

// Not measured is not the same claim as measured zero.
func TestNoSamplesLeavesObservedAbsentRatherThanZero(t *testing.T) {
	day := utcDay(fixedNow).AddDate(0, 0, -1)
	store := &fakeQueryStore{
		slugs:   map[string]string{"a1": "web"},
		rollups: []db.DailyRollup{{TeamID: "t", AppID: "a1", Day: day, MemoryByteSeconds: big.NewInt(0), PodSeconds: 1}},
	}
	svc := NewQueryService(store, func() time.Time { return fixedNow })

	resp, err := svc.QueryWithObserved(context.Background(), "t", "", day, day, false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if resp.Apps[0].Observed != nil || resp.Apps[0].Days[0].Observed != nil {
		t.Error("an unmeasured window must leave the block absent, not report zeros")
	}
}

// appInterval is interval() with an owning app, which the live read
// path groups by.
func appInterval(appID string, start time.Time, dur time.Duration, cpu int, mem int64) db.AllocationInterval {
	in := interval(start, dur, cpu, mem)
	in.AppID = appID
	return in
}

// A closed day the hourly rollup has not reached yet must be computed
// from intervals, not reported as zero. Between UTC midnight and the
// first rollup tick, yesterday would otherwise read as "no usage".
func TestClosedDayWithoutRollupIsComputedLive(t *testing.T) {
	yesterday := utcDay(fixedNow).AddDate(0, 0, -1)
	store := &fakeQueryStore{
		slugs: map[string]string{"a1": "web"},
		// No rollup row for yesterday.
		intervals: []db.AllocationInterval{
			appInterval("a1", yesterday.Add(time.Hour), 2*time.Hour, 250, 0),
		},
	}
	svc := NewQueryService(store, func() time.Time { return fixedNow })

	resp, err := svc.Query(context.Background(), "t", "", yesterday, yesterday, false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(resp.Apps) != 1 || len(resp.Apps[0].Days) != 1 {
		t.Fatalf("an unsettled day must still report its usage, got %+v", resp.Apps)
	}
	day := resp.Apps[0].Days[0]
	if day.Source != dto.UsageSourceLive {
		t.Errorf("source = %q, want live for a day with no rollup", day.Source)
	}
	if want := int64(250 * 7200); day.Totals.CPUMillicoreSeconds != want {
		t.Errorf("CPUMillicoreSeconds = %d, want %d", day.Totals.CPUMillicoreSeconds, want)
	}
}

// Where a rollup exists it wins: recomputing a settled day from
// intervals would disagree once those intervals age out.
func TestSettledDayPrefersTheRollup(t *testing.T) {
	yesterday := utcDay(fixedNow).AddDate(0, 0, -1)
	store := &fakeQueryStore{
		slugs: map[string]string{"a1": "web"},
		rollups: []db.DailyRollup{{
			TeamID: "t", AppID: "a1", Day: yesterday,
			CPUMillicoreSeconds: 999, MemoryByteSeconds: big.NewInt(0), PodSeconds: 10,
		}},
		intervals: []db.AllocationInterval{
			appInterval("a1", yesterday.Add(time.Hour), 2*time.Hour, 250, 0),
		},
	}
	svc := NewQueryService(store, func() time.Time { return fixedNow })

	resp, err := svc.Query(context.Background(), "t", "", yesterday, yesterday, false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(resp.Apps) != 1 {
		t.Fatalf("a settled day must not also appear as a second entry: %+v", resp.Apps)
	}
	if len(resp.Apps[0].Days) != 1 {
		t.Fatalf("want exactly one entry for the day, got %d", len(resp.Apps[0].Days))
	}
	day := resp.Apps[0].Days[0]
	if day.Source != dto.UsageSourceRollup || day.Totals.CPUMillicoreSeconds != 999 {
		t.Errorf("settled day should come from the rollup, got %+v", day)
	}
}
