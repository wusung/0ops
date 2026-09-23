package usage

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/wusung/0ops/internal/server/db"
)

const (
	defaultRollupBatch = 200
	// Intervals are kept for 13 months, matching audit retention: long
	// enough to answer a billing dispute, after which the permanent
	// record is the daily rollup.
	defaultIntervalRetention = 13 * 30 * 24 * time.Hour
)

// RollupStore is the ledger access the rollup pass needs.
type RollupStore interface {
	ListPendingRollupDays(ctx context.Context, limit int) ([]db.PendingRollupDay, error)
	ListAllocationIntervalsOverlapping(ctx context.Context, teamID, appID string, from, to time.Time) ([]db.AllocationInterval, error)
	UpsertDailyRollup(ctx context.Context, ru db.DailyRollup) error
	OldestPendingRollupDay(ctx context.Context) (time.Time, error)
	DeleteAllocationIntervalsBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// RollupObserver reports rollup progress. Counts only.
type RollupObserver interface {
	ObserveRollupLagDays(days int)
	ObserveIntervalsExpired(n int64)
}

// NopRollupObserver discards everything.
type NopRollupObserver struct{}

func (NopRollupObserver) ObserveRollupLagDays(int)      {}
func (NopRollupObserver) ObserveIntervalsExpired(int64) {}

// Rollupper integrates closed days into permanent per-day totals and
// expires the raw intervals behind them.
type Rollupper struct {
	store     RollupStore
	obs       RollupObserver
	log       *slog.Logger
	now       func() time.Time
	batch     int
	retention time.Duration
}

// NewRollupper wires a rollup pass. batch and retention fall back to the
// spec defaults when zero.
func NewRollupper(store RollupStore, obs RollupObserver, log *slog.Logger, now func() time.Time, batch int, retention time.Duration) *Rollupper {
	if obs == nil {
		obs = NopRollupObserver{}
	}
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if batch <= 0 {
		batch = defaultRollupBatch
	}
	if retention <= 0 {
		retention = defaultIntervalRetention
	}
	return &Rollupper{store: store, obs: obs, log: log, now: now, batch: batch, retention: retention}
}

// Tick rolls up pending days and expires old intervals. Returns how many
// days were computed.
func (r *Rollupper) Tick(ctx context.Context) (days int, err error) {
	pending, err := r.store.ListPendingRollupDays(ctx, r.batch)
	if err != nil {
		return 0, fmt.Errorf("list pending rollup days: %w", err)
	}
	for _, p := range pending {
		if ctx.Err() != nil {
			return days, ctx.Err()
		}
		if err := r.rollupOne(ctx, p); err != nil {
			return days, err
		}
		days++
	}

	if err := r.observeLag(ctx); err != nil {
		return days, err
	}
	if err := r.gc(ctx); err != nil {
		return days, err
	}
	return days, nil
}

func (r *Rollupper) rollupOne(ctx context.Context, p db.PendingRollupDay) error {
	from, to := DayBounds(p.Day)
	intervals, err := r.store.ListAllocationIntervalsOverlapping(ctx, p.TeamID, p.AppID, from, to)
	if err != nil {
		return fmt.Errorf("list intervals for %s/%s on %s: %w", p.TeamID, p.AppID, p.Day.Format(time.DateOnly), err)
	}
	// Recomputed from scratch every time, so a retry cannot double count.
	ru := RollupForDay(p.TeamID, p.AppID, p.Day, intervals)
	if err := r.store.UpsertDailyRollup(ctx, ru); err != nil {
		return fmt.Errorf("upsert rollup for %s: %w", p.Day.Format(time.DateOnly), err)
	}
	return nil
}

func (r *Rollupper) observeLag(ctx context.Context) error {
	oldest, err := r.store.OldestPendingRollupDay(ctx)
	if err != nil {
		return fmt.Errorf("oldest pending rollup day: %w", err)
	}
	if oldest.IsZero() {
		r.obs.ObserveRollupLagDays(0)
		return nil
	}
	lag := int(r.now().UTC().Sub(oldest).Hours() / 24)
	if lag < 0 {
		lag = 0
	}
	r.obs.ObserveRollupLagDays(lag)
	return nil
}

func (r *Rollupper) gc(ctx context.Context) error {
	cutoff := r.now().UTC().Add(-r.retention)
	n, err := r.store.DeleteAllocationIntervalsBefore(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("expire intervals before %s: %w", cutoff.Format(time.DateOnly), err)
	}
	if n > 0 {
		r.obs.ObserveIntervalsExpired(n)
		r.log.Info("usage: expired allocation intervals past retention",
			slog.Int64("rows", n), slog.Time("cutoff", cutoff))
	}
	return nil
}
