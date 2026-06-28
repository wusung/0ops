package cli

import (
	"testing"
	"time"
)

// TestMonthsInRangeCoversTouchedMonths guards the slice-d completeness fix: a
// narrow window must still resolve to the FULL set of (team, month) chains it
// touches, so verify recomputes each chain from genesis over the whole month
// rather than a partial slice (which would false-BREAK).
func TestMonthsInRangeCoversTouchedMonths(t *testing.T) {
	cases := []struct {
		name         string
		since, until time.Time
		want         []string
	}{
		{
			name:  "sub-month window still yields its month",
			since: time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC),
			until: time.Date(2026, 6, 15, 17, 0, 0, 0, time.UTC),
			want:  []string{"2026-06"},
		},
		{
			name:  "window spanning three months yields all three",
			since: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC),
			until: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			want:  []string{"2026-05", "2026-06", "2026-07"},
		},
		{
			name:  "year boundary",
			since: time.Date(2025, 12, 31, 23, 0, 0, 0, time.UTC),
			until: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC),
			want:  []string{"2025-12", "2026-01"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := monthsInRange(c.since, c.until)
			if len(got) != len(c.want) {
				t.Fatalf("got %d months %v, want %v", len(got), labels(got), c.want)
			}
			for i, m := range got {
				if m.Format("2006-01") != c.want[i] {
					t.Fatalf("month[%d] = %s, want %s", i, m.Format("2006-01"), c.want[i])
				}
			}
		})
	}
}

func labels(ms []time.Time) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Format("2006-01")
	}
	return out
}
