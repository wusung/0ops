package db_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	dbpkg "github.com/wusung/0ops/internal/server/db"
)

// Postgres stores timestamptz at microsecond precision, so any wall clock
// the test compares against must be truncated to match what comes back.
func newInterval(teamID, appID string, started time.Time) dbpkg.AllocationInterval {
	started = started.Truncate(time.Microsecond)
	return dbpkg.AllocationInterval{
		PodUID:        uuid.NewString(),
		TeamID:        teamID,
		AppID:         appID,
		Namespace:     "team-x",
		PodName:       "web-abc",
		CPUMillicores: 100,
		MemoryBytes:   256 * 1024 * 1024,
		StartedAt:     started,
		LastSeenAt:    started,
	}
}

func seedUsageApp(ctx context.Context, t *testing.T, pool *pgxpool.Pool, slug string) (string, string) {
	t.Helper()
	teamID, _ := seedTeam(ctx, t, pool, slug+"-team", slug+" Team")
	appID := seedAppRow(ctx, t, pool, teamID, uniqueSuffix(t, slug+"-app"), "live")
	return teamID, appID
}

// A watch event and a reconcile pass can observe the same pod at the same
// moment. Opening must be idempotent or the pod gets billed twice.
func TestOpenAllocationIntervalIsIdempotent(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, appID := seedUsageApp(ctx, t, pool, "uidem")

	in := newInterval(teamID, appID, time.Now().UTC().Add(-time.Hour))
	created, err := repo.OpenAllocationInterval(ctx, in)
	if err != nil || !created {
		t.Fatalf("first open: created=%v err=%v", created, err)
	}
	for i := 0; i < 4; i++ {
		// Same pod re-reported with a later StartedAt: must not move the
		// original, or a restarting pod would silently shorten its bill.
		replay := in
		replay.StartedAt = in.StartedAt.Add(30 * time.Minute)
		created, err := repo.OpenAllocationInterval(ctx, replay)
		if err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
		if created {
			t.Fatalf("replay %d created a second interval", i)
		}
	}

	open, err := repo.ListOpenAllocationIntervals(ctx)
	if err != nil {
		t.Fatalf("ListOpen: %v", err)
	}
	var mine []dbpkg.OpenIntervalRef
	for _, o := range open {
		if o.TeamID == teamID {
			mine = append(mine, o)
		}
	}
	if len(mine) != 1 {
		t.Fatalf("want exactly 1 open interval, got %d", len(mine))
	}
	if !mine[0].StartedAt.Equal(in.StartedAt) {
		t.Errorf("StartedAt moved: got %v want %v", mine[0].StartedAt, in.StartedAt)
	}
}

// A replayed DELETE must not overwrite an authoritative end time with a
// later, weaker one — that would over-bill.
func TestCloseAllocationIntervalIsSingleShot(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, appID := seedUsageApp(ctx, t, pool, "uclose")

	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	in := newInterval(teamID, appID, start)
	if _, err := repo.OpenAllocationInterval(ctx, in); err != nil {
		t.Fatalf("open: %v", err)
	}

	authoritative := start.Add(37 * time.Second)
	closed, err := repo.CloseAllocationInterval(ctx, in.PodUID, authoritative, false, dbpkg.CloseReasonTerminated)
	if err != nil || !closed {
		t.Fatalf("first close: closed=%v err=%v", closed, err)
	}

	closed, err = repo.CloseAllocationInterval(ctx, in.PodUID, start.Add(time.Hour), true, dbpkg.CloseReasonReconciledMissing)
	if err != nil {
		t.Fatalf("second close: %v", err)
	}
	if closed {
		t.Fatal("second close overwrote an already-closed interval")
	}

	got, err := repo.ListAllocationIntervalsOverlapping(ctx, teamID, appID, start.Add(-time.Hour), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 interval, got %d", len(got))
	}
	if got[0].EndedAt == nil || !got[0].EndedAt.Equal(authoritative) {
		t.Errorf("EndedAt = %v, want the authoritative %v", got[0].EndedAt, authoritative)
	}
	if got[0].Estimated {
		t.Error("interval closed with a K8s timestamp must not be flagged estimated")
	}
}

func TestTouchOnlyAdvancesOpenIntervals(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, appID := seedUsageApp(ctx, t, pool, "utouch")

	start := time.Now().UTC().Add(-2 * time.Hour)
	openOne := newInterval(teamID, appID, start)
	closedOne := newInterval(teamID, appID, start)
	for _, in := range []dbpkg.AllocationInterval{openOne, closedOne} {
		if _, err := repo.OpenAllocationInterval(ctx, in); err != nil {
			t.Fatalf("open: %v", err)
		}
	}
	endedAt := start.Add(time.Minute)
	if _, err := repo.CloseAllocationInterval(ctx, closedOne.PodUID, endedAt, false, dbpkg.CloseReasonDeleted); err != nil {
		t.Fatalf("close: %v", err)
	}

	seen := time.Now().UTC().Truncate(time.Second)
	if err := repo.TouchAllocationIntervals(ctx, []string{openOne.PodUID, closedOne.PodUID}, seen); err != nil {
		t.Fatalf("touch: %v", err)
	}

	all, err := repo.ListAllocationIntervalsOverlapping(ctx, teamID, appID, start.Add(-time.Hour), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, in := range all {
		switch in.PodUID {
		case openOne.PodUID:
			if !in.LastSeenAt.Equal(seen) {
				t.Errorf("open interval last_seen_at = %v, want %v", in.LastSeenAt, seen)
			}
		case closedOne.PodUID:
			if in.LastSeenAt.After(endedAt) {
				t.Errorf("closed interval last_seen_at moved to %v", in.LastSeenAt)
			}
		}
	}
}

// Cross-team reads must come back empty rather than leaking existence.
func TestListAllocationIntervalsIsTeamScoped(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamA, appA := seedUsageApp(ctx, t, pool, "uscopea")
	teamB, _ := seedUsageApp(ctx, t, pool, "uscopeb")

	start := time.Now().UTC().Add(-time.Hour)
	if _, err := repo.OpenAllocationInterval(ctx, newInterval(teamA, appA, start)); err != nil {
		t.Fatalf("open: %v", err)
	}

	got, err := repo.ListAllocationIntervalsOverlapping(ctx, teamB, "", start.Add(-time.Hour), time.Now().UTC())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("team B saw %d of team A's intervals", len(got))
	}
}

// Window selection is half-open on both ends: an interval that merely
// touches the boundary is outside the window.
func TestOverlapWindowBoundaries(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, appID := seedUsageApp(ctx, t, pool, "uwin")

	base := time.Now().UTC().Truncate(time.Hour).Add(-24 * time.Hour)
	before := newInterval(teamID, appID, base)
	if _, err := repo.OpenAllocationInterval(ctx, before); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := repo.CloseAllocationInterval(ctx, before.PodUID, base.Add(time.Hour), false, dbpkg.CloseReasonTerminated); err != nil {
		t.Fatalf("close: %v", err)
	}

	from := base.Add(time.Hour)
	to := base.Add(2 * time.Hour)
	got, err := repo.ListAllocationIntervalsOverlapping(ctx, teamID, appID, from, to)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("interval ending exactly at `from` must not overlap [from, to)")
	}

	got, err = repo.ListAllocationIntervalsOverlapping(ctx, teamID, appID, base, base.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("interval covering the window must be returned, got %d", len(got))
	}
}

func TestUpsertDailyRollupIsIdempotentAndKeepsBigValues(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, appID := seedUsageApp(ctx, t, pool, "uroll")

	day := time.Now().UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	// 128Gi held for a full day: overflows int64 once a few of these are
	// summed, which is why the column is numeric.
	mem := new(big.Int).Mul(big.NewInt(137438953472), big.NewInt(86400))
	ru := dbpkg.DailyRollup{
		TeamID:              teamID,
		AppID:               appID,
		Day:                 day,
		CPUMillicoreSeconds: 500 * 86400,
		MemoryByteSeconds:   mem,
		GPUCountSeconds:     86400,
		PodSeconds:          86400,
		EstimatedSeconds:    300,
		IntervalCount:       3,
	}
	for i := 0; i < 3; i++ {
		if err := repo.UpsertDailyRollup(ctx, ru); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}

	got, err := repo.ListDailyRollups(ctx, teamID, appID, day, day)
	if err != nil {
		t.Fatalf("list rollups: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 rollup row, got %d", len(got))
	}
	if got[0].CPUMillicoreSeconds != ru.CPUMillicoreSeconds || got[0].IntervalCount != 3 {
		t.Errorf("re-running a day changed its values: %+v", got[0])
	}
	if got[0].MemoryByteSeconds.Cmp(mem) != 0 {
		t.Errorf("memory_byte_seconds roundtrip: got %s want %s", got[0].MemoryByteSeconds, mem)
	}
}

// An interval open for a year means a pod alive for a year. GC must not
// mistake it for a stale row.
func TestDeleteIntervalsBeforeSparesOpenOnes(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, appID := seedUsageApp(ctx, t, pool, "ugc")

	old := time.Now().UTC().AddDate(0, -14, 0)
	stale := newInterval(teamID, appID, old)
	longLived := newInterval(teamID, appID, old)
	for _, in := range []dbpkg.AllocationInterval{stale, longLived} {
		if _, err := repo.OpenAllocationInterval(ctx, in); err != nil {
			t.Fatalf("open: %v", err)
		}
	}
	if _, err := repo.CloseAllocationInterval(ctx, stale.PodUID, old.Add(time.Hour), false, dbpkg.CloseReasonTerminated); err != nil {
		t.Fatalf("close: %v", err)
	}

	cutoff := time.Now().UTC().AddDate(0, -13, 0)
	if _, err := repo.DeleteAllocationIntervalsBefore(ctx, cutoff); err != nil {
		t.Fatalf("gc: %v", err)
	}

	got, err := repo.ListAllocationIntervalsOverlapping(ctx, teamID, appID, old.Add(-time.Hour), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].PodUID != longLived.PodUID {
		t.Fatalf("GC must keep the still-open interval and drop only the closed one; got %d rows", len(got))
	}
}

// The pending-days query is the one piece of rollup logic that lives in
// SQL, so it gets exercised against a real Postgres.
func TestListPendingRollupDaysExpandsSpannedDays(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, appID := seedUsageApp(ctx, t, pool, "upend")

	// 23:30 two days ago → 00:30 yesterday: touches two closed days.
	twoDaysAgo := time.Now().UTC().AddDate(0, 0, -2).Truncate(24 * time.Hour)
	in := newInterval(teamID, appID, twoDaysAgo.Add(23*time.Hour+30*time.Minute))
	if _, err := repo.OpenAllocationInterval(ctx, in); err != nil {
		t.Fatalf("open: %v", err)
	}
	end := twoDaysAgo.AddDate(0, 0, 1).Add(30 * time.Minute)
	if _, err := repo.CloseAllocationInterval(ctx, in.PodUID, end, false, dbpkg.CloseReasonTerminated); err != nil {
		t.Fatalf("close: %v", err)
	}

	days, err := repo.ListPendingRollupDays(ctx, 100)
	if err != nil {
		t.Fatalf("ListPendingRollupDays: %v", err)
	}
	var mine []time.Time
	for _, d := range days {
		if d.TeamID == teamID {
			mine = append(mine, d.Day)
		}
	}
	if len(mine) != 2 {
		t.Fatalf("interval spanning midnight should make 2 days pending, got %d: %v", len(mine), mine)
	}
	if !mine[0].Equal(twoDaysAgo) || !mine[1].Equal(twoDaysAgo.AddDate(0, 0, 1)) {
		t.Errorf("pending days = %v, want %v and the day after", mine, twoDaysAgo)
	}
}

// Today is still accumulating; rolling it up would freeze a partial day
// into the permanent record.
func TestListPendingRollupDaysExcludesToday(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, appID := seedUsageApp(ctx, t, pool, "utoday")

	in := newInterval(teamID, appID, time.Now().UTC().Add(-time.Hour))
	if _, err := repo.OpenAllocationInterval(ctx, in); err != nil {
		t.Fatalf("open: %v", err)
	}

	days, err := repo.ListPendingRollupDays(ctx, 100)
	if err != nil {
		t.Fatalf("ListPendingRollupDays: %v", err)
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for _, d := range days {
		if d.TeamID == teamID && d.Day.Equal(today) {
			t.Fatal("today must not be rolled up while it is still accumulating")
		}
	}
}

func TestListPendingRollupDaysSkipsAlreadyRolledDays(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, appID := seedUsageApp(ctx, t, pool, "udone")

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	in := newInterval(teamID, appID, yesterday.Add(time.Hour))
	if _, err := repo.OpenAllocationInterval(ctx, in); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := repo.CloseAllocationInterval(ctx, in.PodUID, yesterday.Add(2*time.Hour), false, dbpkg.CloseReasonTerminated); err != nil {
		t.Fatalf("close: %v", err)
	}

	pendingBefore, err := repo.ListPendingRollupDays(ctx, 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !containsDay(pendingBefore, teamID, yesterday) {
		t.Fatal("precondition: yesterday should start out pending")
	}

	if err := repo.UpsertDailyRollup(ctx, dbpkg.DailyRollup{
		TeamID: teamID, AppID: appID, Day: yesterday,
		MemoryByteSeconds: big.NewInt(0), PodSeconds: 3600,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	pendingAfter, err := repo.ListPendingRollupDays(ctx, 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if containsDay(pendingAfter, teamID, yesterday) {
		t.Error("a day with a rollup row must stop being pending")
	}
}

func containsDay(days []dbpkg.PendingRollupDay, teamID string, day time.Time) bool {
	for _, d := range days {
		if d.TeamID == teamID && d.Day.Equal(day) {
			return true
		}
	}
	return false
}

func TestObservedUsageAggregatesPerDay(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, appID := seedUsageApp(ctx, t, pool, "uobs")

	day := time.Now().UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	samples := []dbpkg.UsageSample{
		{TeamID: teamID, AppID: appID, SampledAt: day.Add(time.Hour), CPUMillicores: 100, MemoryBytes: 1 << 20, Active: true},
		{TeamID: teamID, AppID: appID, SampledAt: day.Add(2 * time.Hour), CPUMillicores: 300, MemoryBytes: 3 << 20, Active: true},
		{TeamID: teamID, AppID: appID, SampledAt: day.Add(3 * time.Hour), CPUMillicores: 200, MemoryBytes: 2 << 20, Active: false},
	}
	if err := repo.InsertUsageSamples(ctx, samples); err != nil {
		t.Fatalf("InsertUsageSamples: %v", err)
	}

	got, err := repo.SummarizeObservedUsage(ctx, teamID, appID, day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("SummarizeObservedUsage: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want one row per day, got %d", len(got))
	}
	o := got[0]
	if o.CPUMillicoresAvg != 200 || o.CPUMillicoresMax != 300 {
		t.Errorf("cpu avg/max = %d/%d, want 200/300", o.CPUMillicoresAvg, o.CPUMillicoresMax)
	}
	if o.MemoryBytesAvg != 2<<20 || o.MemoryBytesMax != 3<<20 {
		t.Errorf("memory avg/max = %d/%d", o.MemoryBytesAvg, o.MemoryBytesMax)
	}
	if o.SampleCount != 3 {
		t.Errorf("SampleCount = %d, want 3", o.SampleCount)
	}
	if !o.Day.Equal(day) {
		t.Errorf("Day = %v, want the UTC day %v", o.Day, day)
	}
}

func TestObservedUsageIsTeamScoped(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamA, appA := seedUsageApp(ctx, t, pool, "uobsa")
	teamB, _ := seedUsageApp(ctx, t, pool, "uobsb")

	at := time.Now().UTC().Add(-time.Hour)
	if err := repo.InsertUsageSamples(ctx, []dbpkg.UsageSample{
		{TeamID: teamA, AppID: appA, SampledAt: at, CPUMillicores: 100},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := repo.SummarizeObservedUsage(ctx, teamB, "", at.Add(-time.Hour), at.Add(time.Hour))
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("team B saw %d of team A's observations", len(got))
	}
}

func TestUsageSampleGCRespectsRetention(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, appID := seedUsageApp(ctx, t, pool, "uobsgc")

	old := time.Now().UTC().AddDate(0, 0, -40)
	recent := time.Now().UTC().Add(-time.Hour)
	if err := repo.InsertUsageSamples(ctx, []dbpkg.UsageSample{
		{TeamID: teamID, AppID: appID, SampledAt: old, CPUMillicores: 10},
		{TeamID: teamID, AppID: appID, SampledAt: recent, CPUMillicores: 20},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if _, err := repo.DeleteUsageSamplesBefore(ctx, time.Now().UTC().AddDate(0, 0, -30)); err != nil {
		t.Fatalf("gc: %v", err)
	}

	got, err := repo.SummarizeObservedUsage(ctx, teamID, appID, old.Add(-time.Hour), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want only the recent day to survive, got %d rows", len(got))
	}
	if got[0].CPUMillicoresMax != 20 {
		t.Errorf("surviving row = %+v, want the recent sample", got[0])
	}
}

// The over-billing case: the day was rolled up while the pod was still
// open, so it recorded a clamped full day. Once the real end time lands,
// that day has to come back for recomputation or it keeps the inflated
// figure forever.
func TestLateCloseMakesARolledDayPendingAgain(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, appID := seedUsageApp(ctx, t, pool, "ulate")

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	in := newInterval(teamID, appID, yesterday.Add(time.Hour))
	if _, err := repo.OpenAllocationInterval(ctx, in); err != nil {
		t.Fatalf("open: %v", err)
	}

	// Rolled up while still open.
	if err := repo.UpsertDailyRollup(ctx, dbpkg.DailyRollup{
		TeamID: teamID, AppID: appID, Day: yesterday,
		MemoryByteSeconds: big.NewInt(0), PodSeconds: 82800,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	pending, err := repo.ListPendingRollupDays(ctx, 500)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if containsDay(pending, teamID, yesterday) {
		t.Fatal("precondition: a freshly rolled day should not be pending")
	}

	// The real end time arrives.
	if _, err := repo.CloseAllocationInterval(ctx, in.PodUID,
		yesterday.Add(2*time.Hour), false, dbpkg.CloseReasonTerminated); err != nil {
		t.Fatalf("close: %v", err)
	}

	pending, err = repo.ListPendingRollupDays(ctx, 500)
	if err != nil {
		t.Fatalf("list after close: %v", err)
	}
	if !containsDay(pending, teamID, yesterday) {
		t.Error("a day rolled up before its interval closed must be recomputed; " +
			"otherwise the clamped figure stands and the team is over-billed")
	}
}

// The under-billing case: a restart backfills an interval into a day
// that already has a rollup. Those seconds must still land.
func TestBackfilledIntervalMakesARolledDayPendingAgain(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, appID := seedUsageApp(ctx, t, pool, "uback")

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	if err := repo.UpsertDailyRollup(ctx, dbpkg.DailyRollup{
		TeamID: teamID, AppID: appID, Day: yesterday,
		MemoryByteSeconds: big.NewInt(0), PodSeconds: 3600,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// A pod from yesterday only now reaches the ledger.
	in := newInterval(teamID, appID, yesterday.Add(5*time.Hour))
	if _, err := repo.OpenAllocationInterval(ctx, in); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := repo.CloseAllocationInterval(ctx, in.PodUID,
		yesterday.Add(6*time.Hour), false, dbpkg.CloseReasonTerminated); err != nil {
		t.Fatalf("close: %v", err)
	}

	pending, err := repo.ListPendingRollupDays(ctx, 500)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !containsDay(pending, teamID, yesterday) {
		t.Error("an interval arriving after its day was rolled up must bring that day back")
	}
}

// A day whose intervals have not changed since it was computed stays
// settled — otherwise every rollup tick would rewrite the whole history.
func TestSettledDayStaysSettled(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, appID := seedUsageApp(ctx, t, pool, "usettled")

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	in := newInterval(teamID, appID, yesterday.Add(time.Hour))
	if _, err := repo.OpenAllocationInterval(ctx, in); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := repo.CloseAllocationInterval(ctx, in.PodUID,
		yesterday.Add(2*time.Hour), false, dbpkg.CloseReasonTerminated); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := repo.UpsertDailyRollup(ctx, dbpkg.DailyRollup{
		TeamID: teamID, AppID: appID, Day: yesterday,
		MemoryByteSeconds: big.NewInt(0), PodSeconds: 3600,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	pending, err := repo.ListPendingRollupDays(ctx, 500)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if containsDay(pending, teamID, yesterday) {
		t.Error("a day with no changed intervals must not be recomputed every tick")
	}
}

// last_seen_at is the fallback end time for a pod that vanishes. The
// watcher and the reconcile loop stamp it from different clocks and
// interleave, so a plain assignment lets it move backwards.
func TestTouchNeverRewindsLastSeen(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, appID := seedUsageApp(ctx, t, pool, "urewind")

	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	in := newInterval(teamID, appID, start)
	if _, err := repo.OpenAllocationInterval(ctx, in); err != nil {
		t.Fatalf("open: %v", err)
	}

	ahead := start.Add(30 * time.Minute)
	if err := repo.TouchAllocationIntervals(ctx, []string{in.PodUID}, ahead); err != nil {
		t.Fatalf("touch ahead: %v", err)
	}
	if err := repo.TouchAllocationIntervals(ctx, []string{in.PodUID}, start.Add(5*time.Minute)); err != nil {
		t.Fatalf("touch stale: %v", err)
	}

	got, err := repo.ListAllocationIntervalsOverlapping(ctx, teamID, appID, start.Add(-time.Hour), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 interval, got %d", len(got))
	}
	if got[0].LastSeenAt.Before(ahead) {
		t.Errorf("last_seen_at rewound to %v, want at least %v", got[0].LastSeenAt, ahead)
	}
}

// Closing on a guess is reversible; closing on a K8s timestamp is not.
func TestReopenOnlyUndoesGuessedCloses(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, appID := seedUsageApp(ctx, t, pool, "ureopen")

	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	guessed := newInterval(teamID, appID, start)
	factual := newInterval(teamID, appID, start)
	for _, in := range []dbpkg.AllocationInterval{guessed, factual} {
		if _, err := repo.OpenAllocationInterval(ctx, in); err != nil {
			t.Fatalf("open: %v", err)
		}
	}
	if _, err := repo.CloseAllocationInterval(ctx, guessed.PodUID, start.Add(time.Minute), true, dbpkg.CloseReasonReconciledMissing); err != nil {
		t.Fatalf("close guessed: %v", err)
	}
	if _, err := repo.CloseAllocationInterval(ctx, factual.PodUID, start.Add(time.Minute), false, dbpkg.CloseReasonTerminated); err != nil {
		t.Fatalf("close factual: %v", err)
	}

	back, err := repo.ReopenGuessedInterval(ctx, guessed.PodUID, time.Now().UTC())
	if err != nil || !back {
		t.Fatalf("guessed close should reopen: back=%v err=%v", back, err)
	}
	back, err = repo.ReopenGuessedInterval(ctx, factual.PodUID, time.Now().UTC())
	if err != nil {
		t.Fatalf("reopen factual: %v", err)
	}
	if back {
		t.Error("a close backed by a K8s timestamp is a fact and must never be reopened")
	}

	open, err := repo.ListOpenAllocationIntervals(ctx)
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	var mine int
	for _, o := range open {
		if o.TeamID == teamID {
			mine++
		}
	}
	if mine != 1 {
		t.Errorf("want exactly the reopened interval to be open, got %d", mine)
	}
}
