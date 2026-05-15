package audit

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// retentionMonths is the spec § 9.1 hard retention window for audit_log
// partitions; partitions older than this are dropped (except those whose
// rows have been moved to audit_log_archive — see ArchiveDeleteApp).
const retentionMonths = 13

// PartitionMaintainer drives the monthly rollover lifecycle: create
// the next future partition, archive `delete_app` rows from the
// soon-to-be-dropped partition, and drop the partition itself.
// Implementations live in the db layer; tests substitute a fake.
type PartitionMaintainer interface {
	CreateMonthlyPartition(ctx context.Context, month time.Time) error
	ArchiveDeleteAppRows(ctx context.Context, month time.Time) (int64, error)
	DropMonthlyPartition(ctx context.Context, month time.Time) error
	ListPartitionMonths(ctx context.Context) ([]time.Time, error)
}

// RolloverPlan describes the actions a single rollover invocation took.
// It is returned so callers / tests / metrics can inspect the result
// without scraping logs.
type RolloverPlan struct {
	Created  []time.Time
	Archived map[string]int64
	Dropped  []time.Time
}

// Rollover advances the partition window: every month within
// [now-1, now+lookaheadMonths] is created if missing, and every
// partition whose month is older than retentionMonths is archived +
// dropped. The function is idempotent: running it twice in a row is a
// no-op on the second pass.
func Rollover(ctx context.Context, m PartitionMaintainer, now time.Time, lookaheadMonths int) (RolloverPlan, error) {
	if m == nil {
		return RolloverPlan{}, fmt.Errorf("audit: nil maintainer")
	}
	if lookaheadMonths < 1 {
		lookaheadMonths = 3
	}

	plan := RolloverPlan{Archived: map[string]int64{}}
	current := truncateToMonth(now.UTC())
	for offset := -1; offset <= lookaheadMonths; offset++ {
		month := current.AddDate(0, offset, 0)
		if err := m.CreateMonthlyPartition(ctx, month); err != nil {
			return RolloverPlan{}, fmt.Errorf("audit: create partition %s: %w", PartitionLabel(month), err)
		}
		plan.Created = append(plan.Created, month)
	}

	cutoff := current.AddDate(0, -retentionMonths, 0)
	existing, err := m.ListPartitionMonths(ctx)
	if err != nil {
		return RolloverPlan{}, fmt.Errorf("audit: list partitions: %w", err)
	}
	for _, month := range existing {
		if !month.Before(cutoff) {
			continue
		}
		moved, err := m.ArchiveDeleteAppRows(ctx, month)
		if err != nil {
			return RolloverPlan{}, fmt.Errorf("audit: archive %s: %w", PartitionLabel(month), err)
		}
		plan.Archived[PartitionLabel(month)] = moved
		if err := m.DropMonthlyPartition(ctx, month); err != nil {
			return RolloverPlan{}, fmt.Errorf("audit: drop %s: %w", PartitionLabel(month), err)
		}
		plan.Dropped = append(plan.Dropped, month)
	}
	return plan, nil
}

// PartitionLabel returns the canonical YYYY_MM string used to name a
// monthly partition (e.g. "2026_05"). Used by both the DDL helpers and
// the metrics labels.
func PartitionLabel(t time.Time) string {
	t = truncateToMonth(t.UTC())
	return fmt.Sprintf("%04d_%02d", t.Year(), int(t.Month()))
}

// ParsePartitionLabel is the inverse of PartitionLabel. Used by the
// reader to map partition names back into time ranges.
func ParsePartitionLabel(raw string) (time.Time, error) {
	parts := strings.Split(strings.TrimSpace(raw), "_")
	if len(parts) != 2 || len(parts[0]) != 4 || len(parts[1]) != 2 {
		return time.Time{}, fmt.Errorf("audit: invalid partition label %q", raw)
	}
	year, err := time.Parse("2006", parts[0])
	if err != nil {
		return time.Time{}, fmt.Errorf("audit: invalid year in %q", raw)
	}
	month, err := time.Parse("01", parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("audit: invalid month in %q", raw)
	}
	return time.Date(year.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC), nil
}

func truncateToMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
