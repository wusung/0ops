package reconciler

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakePartitionStore struct {
	months []time.Time
	err    error
}

func (f fakePartitionStore) ListPartitionMonths(context.Context) ([]time.Time, error) {
	return f.months, f.err
}

func months(vals ...string) []time.Time {
	out := make([]time.Time, 0, len(vals))
	for _, v := range vals {
		t, err := time.Parse("2006-01", v)
		if err != nil {
			panic(err)
		}
		out = append(out, t)
	}
	return out
}

func TestAuditPartitionScannerCountsConsecutiveFutureMonths(t *testing.T) {
	now := time.Date(2026, 9, 20, 11, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		given []time.Time
		want  int
	}{
		{
			name:  "current month missing counts zero even with later months present",
			given: months("2026-10", "2026-11"),
			want:  0,
		},
		{
			name:  "only the current month",
			given: months("2026-08", "2026-09"),
			want:  1,
		},
		{
			name:  "window as the rollover job leaves it",
			given: months("2026-08", "2026-09", "2026-10", "2026-11", "2026-12"),
			want:  4,
		},
		{
			name:  "a gap stops the count",
			given: months("2026-09", "2026-11", "2026-12"),
			want:  1,
		},
		{
			name:  "no partitions at all",
			given: nil,
			want:  0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &AuditPartitionScanner{Store: fakePartitionStore{months: tc.given}}
			got, err := s.Tick(context.Background(), now)
			if err != nil {
				t.Fatalf("Tick: %v", err)
			}
			if got != tc.want {
				t.Errorf("future months = %d, want %d", got, tc.want)
			}
		})
	}
}

// Partition months arrive as whatever instant the store parsed; the scanner
// must key on the month boundary so a mid-month timestamp still matches.
func TestAuditPartitionScannerNormalisesToMonthStart(t *testing.T) {
	now := time.Date(2026, 9, 20, 11, 0, 0, 0, time.UTC)
	given := []time.Time{
		time.Date(2026, 9, 17, 4, 30, 0, 0, time.UTC),
		time.Date(2026, 10, 2, 23, 59, 59, 0, time.FixedZone("x", 8*3600)),
	}

	s := &AuditPartitionScanner{Store: fakePartitionStore{months: given}}
	got, err := s.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got != 2 {
		t.Errorf("future months = %d, want 2", got)
	}
}

func TestAuditPartitionScannerPropagatesStoreError(t *testing.T) {
	sentinel := errors.New("boom")
	s := &AuditPartitionScanner{Store: fakePartitionStore{err: sentinel}}
	if _, err := s.Tick(context.Background(), time.Now()); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}

func TestAuditPartitionScannerThresholdDefaults(t *testing.T) {
	s := &AuditPartitionScanner{}
	if got := s.minFutureMonths(); got != defaultMinFutureMonths {
		t.Errorf("default threshold = %d, want %d", got, defaultMinFutureMonths)
	}
	s.MinFutureMonths = 5
	if got := s.minFutureMonths(); got != 5 {
		t.Errorf("configured threshold = %d, want 5", got)
	}
}

// The loop must classify the window, not just read it: prod alerting keys off
// these tick outcomes.
func TestRunnerAuditPartitionTickOutcomes(t *testing.T) {
	now := time.Now().UTC()
	cur := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		leader bool
		given  []time.Time
		err    error
		want   string
	}{
		{
			name:   "follower does not read the catalog",
			leader: false,
			want:   "audit_partition:skipped_not_leader",
		},
		{
			name:   "healthy window",
			leader: true,
			given:  []time.Time{cur, cur.AddDate(0, 1, 0), cur.AddDate(0, 2, 0)},
			want:   "audit_partition:ok",
		},
		{
			name:   "only the current month left",
			leader: true,
			given:  []time.Time{cur},
			want:   "audit_partition:window_low",
		},
		{
			name:   "current month already missing",
			leader: true,
			given:  []time.Time{cur.AddDate(0, -1, 0)},
			want:   "audit_partition:exhausted",
		},
		{
			name:   "store failure",
			leader: true,
			err:    errors.New("catalog unavailable"),
			want:   "audit_partition:error",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs := &recordingObserver{}
			runner := New(Config{
				Leader:   stubLeader(tc.leader),
				Observer: obs,
				AuditPartitionScanner: &AuditPartitionScanner{
					Store: fakePartitionStore{months: tc.given, err: tc.err},
				},
			})
			runner.runAuditPartition(context.Background())

			if len(obs.ticks) != 1 || obs.ticks[0] != tc.want {
				t.Fatalf("ticks = %v, want [%s]", obs.ticks, tc.want)
			}
		})
	}
}
