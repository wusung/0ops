package usage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wusung/0ops/internal/server/db"
)

type fakeRollupStore struct {
	pending   []db.PendingRollupDay
	intervals map[string][]db.AllocationInterval
	upserts   []db.DailyRollup
	oldest    time.Time
	gcCutoff  time.Time
	gcRows    int64
	listErr   error
}

func (f *fakeRollupStore) ListPendingRollupDays(context.Context, int) ([]db.PendingRollupDay, error) {
	return f.pending, f.listErr
}
func (f *fakeRollupStore) ListAllocationIntervalsOverlapping(_ context.Context, teamID, appID string, _, _ time.Time) ([]db.AllocationInterval, error) {
	return f.intervals[teamID+"/"+appID], nil
}
func (f *fakeRollupStore) UpsertDailyRollup(_ context.Context, ru db.DailyRollup) error {
	f.upserts = append(f.upserts, ru)
	return nil
}
func (f *fakeRollupStore) OldestPendingRollupDay(context.Context) (time.Time, error) {
	return f.oldest, nil
}
func (f *fakeRollupStore) DeleteAllocationIntervalsBefore(_ context.Context, cutoff time.Time) (int64, error) {
	f.gcCutoff = cutoff
	return f.gcRows, nil
}

type fakeRollupObserver struct {
	lag  int
	gcd  int64
	sets int
}

func (o *fakeRollupObserver) ObserveRollupLagDays(d int)      { o.lag = d; o.sets++ }
func (o *fakeRollupObserver) ObserveIntervalsExpired(n int64) { o.gcd += n }

func TestRollupTickComputesPendingDays(t *testing.T) {
	from, _ := DayBounds(day)
	store := &fakeRollupStore{
		pending: []db.PendingRollupDay{{TeamID: "t", AppID: "a", Day: from}},
		intervals: map[string][]db.AllocationInterval{
			"t/a": {interval(from, 2*time.Hour, 250, 1<<30)},
		},
	}
	r := NewRollupper(store, &fakeRollupObserver{}, nil, func() time.Time { return from.AddDate(0, 0, 1) }, 0, 0)

	days, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if days != 1 || len(store.upserts) != 1 {
		t.Fatalf("days=%d upserts=%d, want 1/1", days, len(store.upserts))
	}
	if want := int64(250 * 7200); store.upserts[0].CPUMillicoreSeconds != want {
		t.Errorf("CPUMillicoreSeconds = %d, want %d", store.upserts[0].CPUMillicoreSeconds, want)
	}
}

// Re-running a day must land on the same row, because the bill for a
// past day cannot be allowed to change on a retry.
func TestRollupTickIsIdempotent(t *testing.T) {
	from, _ := DayBounds(day)
	store := &fakeRollupStore{
		pending: []db.PendingRollupDay{{TeamID: "t", AppID: "a", Day: from}},
		intervals: map[string][]db.AllocationInterval{
			"t/a": {interval(from, time.Hour, 100, 0)},
		},
	}
	r := NewRollupper(store, &fakeRollupObserver{}, nil, func() time.Time { return from.AddDate(0, 0, 1) }, 0, 0)

	for i := 0; i < 3; i++ {
		if _, err := r.Tick(context.Background()); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	if len(store.upserts) != 3 {
		t.Fatalf("want 3 upserts, got %d", len(store.upserts))
	}
	first := store.upserts[0]
	for i, got := range store.upserts[1:] {
		if got.CPUMillicoreSeconds != first.CPUMillicoreSeconds ||
			got.PodSeconds != first.PodSeconds ||
			got.MemoryByteSeconds.Cmp(first.MemoryByteSeconds) != 0 ||
			got.IntervalCount != first.IntervalCount {
			t.Fatalf("rerun %d drifted:\n%+v\n%+v", i+1, first, got)
		}
	}
}

func TestRollupReportsLagInDays(t *testing.T) {
	from, _ := DayBounds(day)
	store := &fakeRollupStore{oldest: from}
	obs := &fakeRollupObserver{}
	r := NewRollupper(store, obs, nil, func() time.Time { return from.AddDate(0, 0, 4) }, 0, 0)

	if _, err := r.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if obs.lag != 4 {
		t.Errorf("lag = %d days, want 4", obs.lag)
	}
}

func TestRollupReportsZeroLagWhenCaughtUp(t *testing.T) {
	store := &fakeRollupStore{}
	obs := &fakeRollupObserver{}
	r := NewRollupper(store, obs, nil, nil, 0, 0)

	if _, err := r.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if obs.sets == 0 || obs.lag != 0 {
		t.Errorf("caught-up ledger must report lag 0, got %d after %d sets", obs.lag, obs.sets)
	}
}

func TestRetentionCutoffIsThirteenMonths(t *testing.T) {
	nowT := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	store := &fakeRollupStore{gcRows: 5}
	obs := &fakeRollupObserver{}
	r := NewRollupper(store, obs, nil, func() time.Time { return nowT }, 0, 0)

	if _, err := r.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	age := nowT.Sub(store.gcCutoff)
	if days := age.Hours() / 24; days < 380 || days > 400 {
		t.Errorf("GC cutoff is %.0f days back, want roughly 13 months", days)
	}
	if obs.gcd != 5 {
		t.Errorf("GC count not reported: %d", obs.gcd)
	}
}

func TestRollupTickSurfacesStoreFailure(t *testing.T) {
	store := &fakeRollupStore{listErr: errors.New("db down")}
	r := NewRollupper(store, nil, nil, nil, 0, 0)

	if _, err := r.Tick(context.Background()); err == nil {
		t.Fatal("want the store failure to surface")
	}
}
