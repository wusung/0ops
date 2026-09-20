package reconciler

import (
	"context"
	"log/slog"
	"time"
)

// AuditPartitionStore is the read-only slice of db.Repository the detector
// needs. It issues a catalog SELECT only, so it works under the restricted
// "0ops_app" role the server runs as (migration 00014). Creating partitions
// needs DDL privileges the server deliberately does not hold — that is the
// audit-rollover CronJob's job. This loop only reports.
type AuditPartitionStore interface {
	ListPartitionMonths(ctx context.Context) ([]time.Time, error)
}

// defaultMinFutureMonths is how many months of audit_log partitions must
// exist ahead of the current month before the window counts as healthy.
// The rollover job keeps [now-1, now+3], so anything below 2 means it has
// missed at least one run and the cliff is approaching.
const defaultMinFutureMonths = 2

// AuditPartitionScanner reports how far ahead audit_log is partitioned.
//
// Migration 00007 seeded a fixed window that ran out on 2026-09-01 because
// nothing called audit.Rollover, and every audit write failed with
// "no partition of relation audit_log found for row" until 00019 bootstrapped
// a fresh window. The failure was silent until inserts started rejecting, so
// this scanner exists to make the window observable *before* it runs out.
type AuditPartitionScanner struct {
	Store           AuditPartitionStore
	Logger          *slog.Logger
	MinFutureMonths int
}

// Tick returns the number of consecutive monthly partitions that exist
// starting at the current month. A return of 0 means the current month has
// no partition — audit writes are already failing.
func (s *AuditPartitionScanner) Tick(ctx context.Context, now time.Time) (int, error) {
	months, err := s.Store.ListPartitionMonths(ctx)
	if err != nil {
		return 0, err
	}

	present := make(map[time.Time]struct{}, len(months))
	for _, m := range months {
		present[monthStart(m)] = struct{}{}
	}

	count := 0
	for cursor := monthStart(now); ; cursor = cursor.AddDate(0, 1, 0) {
		if _, ok := present[cursor]; !ok {
			break
		}
		count++
	}
	return count, nil
}

// minFutureMonths resolves the configured threshold, falling back to the
// package default when unset.
func (s *AuditPartitionScanner) minFutureMonths() int {
	if s.MinFutureMonths > 0 {
		return s.MinFutureMonths
	}
	return defaultMinFutureMonths
}

// monthStart truncates t to the first instant of its month in UTC, matching
// the boundaries audit_log partitions are cut on.
func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
