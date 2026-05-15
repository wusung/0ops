package audit

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeMaintainer struct {
	created  []time.Time
	dropped  []time.Time
	archived map[string]int64
	existing []time.Time
	failCreate bool
}

func (f *fakeMaintainer) CreateMonthlyPartition(_ context.Context, month time.Time) error {
	if f.failCreate {
		return errors.New("boom")
	}
	month = time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	for _, m := range f.created {
		if m.Equal(month) {
			return nil
		}
	}
	f.created = append(f.created, month)
	return nil
}

func (f *fakeMaintainer) ArchiveDeleteAppRows(_ context.Context, month time.Time) (int64, error) {
	if f.archived == nil {
		f.archived = map[string]int64{}
	}
	moved := int64(1)
	f.archived[PartitionLabel(month)] = moved
	return moved, nil
}

func (f *fakeMaintainer) DropMonthlyPartition(_ context.Context, month time.Time) error {
	f.dropped = append(f.dropped, time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC))
	return nil
}

func (f *fakeMaintainer) ListPartitionMonths(context.Context) ([]time.Time, error) {
	out := make([]time.Time, len(f.existing))
	copy(out, f.existing)
	return out, nil
}

func TestRolloverCreatesLookaheadPartitions(t *testing.T) {
	t.Parallel()
	m := &fakeMaintainer{}
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)

	plan, err := Rollover(context.Background(), m, now, 3)
	if err != nil {
		t.Fatalf("Rollover() error = %v", err)
	}
	if len(plan.Created) != 5 {
		t.Fatalf("expected 5 partitions created (prev + current + 3 future), got %d", len(plan.Created))
	}
	if !plan.Created[0].Equal(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("first created should be 2026-04, got %v", plan.Created[0])
	}
}

func TestRolloverDropsRowsOlderThanRetention(t *testing.T) {
	t.Parallel()
	m := &fakeMaintainer{
		existing: []time.Time{
			time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), // 28 months back — must drop
			time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC), // exactly 13 months back — boundary, must stay
			time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC), // inside window
		},
	}
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	plan, err := Rollover(context.Background(), m, now, 3)
	if err != nil {
		t.Fatalf("Rollover() error = %v", err)
	}
	if len(plan.Dropped) != 1 || !plan.Dropped[0].Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("expected the 2024-01 partition to drop, got %#v", plan.Dropped)
	}
	if plan.Archived["2024_01"] == 0 {
		t.Fatalf("expected archive count for 2024-01, got %#v", plan.Archived)
	}
}

func TestRolloverPropagatesCreateError(t *testing.T) {
	t.Parallel()
	m := &fakeMaintainer{failCreate: true}
	_, err := Rollover(context.Background(), m, time.Now(), 1)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestPartitionLabelRoundtrip(t *testing.T) {
	t.Parallel()
	t1 := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	label := PartitionLabel(t1)
	if label != "2026_05" {
		t.Fatalf("PartitionLabel = %q", label)
	}
	parsed, err := ParsePartitionLabel(label)
	if err != nil {
		t.Fatalf("ParsePartitionLabel: %v", err)
	}
	if !parsed.Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("ParsePartitionLabel = %v", parsed)
	}
}
