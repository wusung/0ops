package db

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrTooManyIntervals reports a window holding more interval rows than
// one response may carry. Callers narrow the window rather than receive
// a quietly truncated total.
var ErrTooManyIntervals = errors.New("usage: too many intervals in window")

// Close reasons recorded on usage_allocation_interval. Only
// ReasonReconciledMissing implies an estimated (under-billing) end time.
const (
	CloseReasonTerminated        = "terminated"
	CloseReasonDeleted           = "deleted"
	CloseReasonReconciledMissing = "reconciled_missing"
)

// AllocationInterval is one pod's resource allocation over its lifetime
// (resource-usage-metering spec § 4). StartedAt / EndedAt are always
// copied from the K8s object, never from the backend's clock.
type AllocationInterval struct {
	PodUID        string
	TeamID        string
	AppID         string
	Namespace     string
	PodName       string
	CPUMillicores int
	MemoryBytes   int64
	GPUCount      int
	GPUType       *string
	StartedAt     time.Time
	EndedAt       *time.Time
	LastSeenAt    time.Time
	Estimated     bool
	CloseReason   *string
}

// OpenIntervalRef is the projection the reconcile loop needs: enough to
// decide whether an interval should stay open, without hauling the full
// row across for every pod on the cluster.
type OpenIntervalRef struct {
	PodUID     string
	TeamID     string
	AppID      string
	Namespace  string
	PodName    string
	StartedAt  time.Time
	LastSeenAt time.Time
}

// DailyRollup is the per-(team, app, UTC day) integration of allocation
// intervals. MemoryByteSeconds is a big.Int because bytes x seconds
// overflows int64 once a team runs enough memory for long enough.
type DailyRollup struct {
	TeamID              string
	AppID               string
	Day                 time.Time
	CPUMillicoreSeconds int64
	MemoryByteSeconds   *big.Int
	GPUCountSeconds     int64
	PodSeconds          int64
	EstimatedSeconds    int64
	IntervalCount       int
}

// OpenAllocationInterval records a newly observed pod. It is idempotent:
// a pod already in the ledger is left untouched, so a watch event and a
// reconcile pass racing on the same pod cannot produce two intervals or
// move an existing StartedAt. Reports whether a row was actually created.
func (r *Repository) OpenAllocationInterval(ctx context.Context, in AllocationInterval) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
INSERT INTO usage_allocation_interval
    (pod_uid, team_id, app_id, namespace, pod_name,
     cpu_millicores, memory_bytes, gpu_count, gpu_type,
     started_at, last_seen_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (pod_uid) DO NOTHING`,
		in.PodUID, in.TeamID, in.AppID, in.Namespace, in.PodName,
		in.CPUMillicores, in.MemoryBytes, in.GPUCount, in.GPUType,
		in.StartedAt, in.LastSeenAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// CloseAllocationInterval stamps an end time on a still-open interval.
// The `ended_at IS NULL` guard makes a replayed DELETE event a no-op
// rather than a chance to overwrite an authoritative end time with a
// later, weaker one. Reports whether this call did the closing.
func (r *Repository) CloseAllocationInterval(ctx context.Context, podUID string, endedAt time.Time, estimated bool, reason string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
UPDATE usage_allocation_interval
   SET ended_at = $2, estimated = $3, closed_reason = $4,
       last_seen_at = GREATEST(last_seen_at, $2),
       closed_at = NOW()
 WHERE pod_uid = $1::uuid AND ended_at IS NULL`,
		podUID, endedAt, estimated, reason)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ReopenGuessedInterval undoes a close that was only ever a guess.
//
// Reconcile closes an interval when the cluster stops reporting its pod.
// That is right when the pod is gone, and wrong when it was a transient
// blind spot — an API hiccup, or an app row briefly disappearing during
// a rename. Because pod_uid is the primary key, a re-open is the only
// way back: a second INSERT hits the conflict clause and does nothing,
// silently dropping the rest of that pod's life.
//
// Only intervals closed by inference are eligible. A close backed by a
// K8s timestamp is a fact and is never reopened.
func (r *Repository) ReopenGuessedInterval(ctx context.Context, podUID string, seenAt time.Time) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
UPDATE usage_allocation_interval
   SET ended_at = NULL, closed_reason = NULL, estimated = false,
       closed_at = NULL, last_seen_at = GREATEST(last_seen_at, $2)
 WHERE pod_uid = $1::uuid AND closed_reason = $3`,
		podUID, seenAt, CloseReasonReconciledMissing)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// TouchAllocationIntervals advances last_seen_at for pods still present
// on the cluster. last_seen_at is the fallback end time for a pod that
// disappears while the backend is down, so it must be persisted rather
// than held in memory: a restart would otherwise lose the only evidence
// of how long the pod was known to be alive.
func (r *Repository) TouchAllocationIntervals(ctx context.Context, podUIDs []string, seenAt time.Time) error {
	if len(podUIDs) == 0 {
		return nil
	}
	// GREATEST, not plain assignment: the watcher stamps its own clock
	// while the reconcile loop stamps an injected one, and the two
	// goroutines interleave. A rewind here would shorten the fallback
	// end time of a pod that later vanishes.
	_, err := r.pool.Exec(ctx, `
UPDATE usage_allocation_interval
   SET last_seen_at = GREATEST(last_seen_at, $2)
 WHERE ended_at IS NULL AND pod_uid = ANY($1::uuid[])`,
		podUIDs, seenAt)
	return err
}

// ListOpenAllocationIntervals returns every interval still open, across
// all teams.
//
// This is deliberately not team-scoped: it backs the cluster-wide
// reconcile pass, whose whole job is to compare the ledger against the
// cluster. Tenant-facing reads (ListAllocationIntervalsOverlapping and
// the rollup queries) are team-scoped without exception.
func (r *Repository) ListOpenAllocationIntervals(ctx context.Context) ([]OpenIntervalRef, error) {
	rows, err := r.pool.Query(ctx, `
SELECT pod_uid::text, team_id::text, app_id::text, namespace, pod_name,
       started_at, last_seen_at
FROM usage_allocation_interval
WHERE ended_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OpenIntervalRef
	for rows.Next() {
		var o OpenIntervalRef
		if err := rows.Scan(&o.PodUID, &o.TeamID, &o.AppID, &o.Namespace, &o.PodName,
			&o.StartedAt, &o.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// AppRefRow maps a pod's labels back to the ids the ledger stores.
type AppRefRow struct {
	TeamID   string
	AppID    string
	TeamSlug string
	AppSlug  string
}

// ListAppRefs returns every live app with the slugs its pods are labelled
// with. Like ListOpenAllocationIntervals this is cluster-wide on purpose:
// the reconcile pass has to attribute pods from every team in one go.
func (r *Repository) ListAppRefs(ctx context.Context) ([]AppRefRow, error) {
	rows, err := r.pool.Query(ctx, `
SELECT a.id::text, a.team_id::text, t.slug::text, a.slug::text
FROM app a
JOIN team t ON t.id = a.team_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AppRefRow
	for rows.Next() {
		var ref AppRefRow
		if err := rows.Scan(&ref.AppID, &ref.TeamID, &ref.TeamSlug, &ref.AppSlug); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// MaxIntervalRows caps a single interval read. One row per pod means a
// team that redeploys often accumulates tens of thousands over the
// retention window, and the whole result is held in memory and
// serialised at once.
const MaxIntervalRows = 10000

// ListAllocationIntervalsOverlapping returns intervals for one team that
// overlap [from, to), newest first, capped at MaxIntervalRows. Still-open
// intervals are included; callers clamp them at query time. appID is
// optional ("" = every app in the team).
//
// The app filter passes NULL rather than an empty string: an empty
// string would have to be cast to uuid, and PostgreSQL does not promise
// to short-circuit OR, so a team-wide read could fail on a cast that was
// never meant to run.
func (r *Repository) ListAllocationIntervalsOverlapping(ctx context.Context, teamID, appID string, from, to time.Time) ([]AllocationInterval, error) {
	rows, err := r.pool.Query(ctx, `
SELECT pod_uid::text, team_id::text, app_id::text, namespace, pod_name,
       cpu_millicores, memory_bytes, gpu_count, gpu_type,
       started_at, ended_at, last_seen_at, estimated, closed_reason
FROM usage_allocation_interval
WHERE team_id = $1::uuid
  AND ($2::uuid IS NULL OR app_id = $2::uuid)
  AND started_at < $4
  AND (ended_at IS NULL OR ended_at > $3)
ORDER BY started_at
LIMIT $5`, teamID, nullableUUID(appID), from, to, MaxIntervalRows+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AllocationInterval
	for rows.Next() {
		var in AllocationInterval
		if err := rows.Scan(&in.PodUID, &in.TeamID, &in.AppID, &in.Namespace, &in.PodName,
			&in.CPUMillicores, &in.MemoryBytes, &in.GPUCount, &in.GPUType,
			&in.StartedAt, &in.EndedAt, &in.LastSeenAt, &in.Estimated, &in.CloseReason); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > MaxIntervalRows {
		// Truncating silently would understate usage without saying so.
		return nil, ErrTooManyIntervals
	}
	return out, nil
}

// UpsertDailyRollup replaces one (team, app, day) row wholesale. Rollups
// are always recomputed from scratch and never incremented, so re-running
// a day is a no-op rather than a double count (spec § 14 rule #5).
func (r *Repository) UpsertDailyRollup(ctx context.Context, ru DailyRollup) error {
	mem, err := bigIntToNumeric(ru.MemoryByteSeconds)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
INSERT INTO usage_daily_rollup
    (team_id, app_id, day, cpu_millicore_seconds, memory_byte_seconds,
     gpu_count_seconds, pod_seconds, estimated_seconds, interval_count, computed_at)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, NOW())
ON CONFLICT (team_id, app_id, day) DO UPDATE SET
    cpu_millicore_seconds = EXCLUDED.cpu_millicore_seconds,
    memory_byte_seconds   = EXCLUDED.memory_byte_seconds,
    gpu_count_seconds     = EXCLUDED.gpu_count_seconds,
    pod_seconds           = EXCLUDED.pod_seconds,
    estimated_seconds     = EXCLUDED.estimated_seconds,
    interval_count        = EXCLUDED.interval_count,
    computed_at           = NOW()`,
		ru.TeamID, ru.AppID, ru.Day, ru.CPUMillicoreSeconds, mem,
		ru.GPUCountSeconds, ru.PodSeconds, ru.EstimatedSeconds, ru.IntervalCount)
	return err
}

// ListDailyRollups returns closed-day rollups for one team over
// [fromDay, toDay]. appID is optional ("" = every app in the team).
func (r *Repository) ListDailyRollups(ctx context.Context, teamID, appID string, fromDay, toDay time.Time) ([]DailyRollup, error) {
	rows, err := r.pool.Query(ctx, `
SELECT team_id::text, app_id::text, day,
       cpu_millicore_seconds, memory_byte_seconds, gpu_count_seconds,
       pod_seconds, estimated_seconds, interval_count
FROM usage_daily_rollup
WHERE team_id = $1::uuid
  AND ($2::uuid IS NULL OR app_id = $2::uuid)
  AND day >= $3 AND day <= $4
ORDER BY day, app_id`, teamID, nullableUUID(appID), fromDay, toDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DailyRollup
	for rows.Next() {
		var ru DailyRollup
		var mem pgtype.Numeric
		if err := rows.Scan(&ru.TeamID, &ru.AppID, &ru.Day,
			&ru.CPUMillicoreSeconds, &mem, &ru.GPUCountSeconds,
			&ru.PodSeconds, &ru.EstimatedSeconds, &ru.IntervalCount); err != nil {
			return nil, err
		}
		ru.MemoryByteSeconds = numericToBigInt(mem)
		out = append(out, ru)
	}
	return out, rows.Err()
}

// UsageSample is one instantaneous reading of an app's consumption.
//
// Observation only. ADR-0018 keeps this out of every metering path: it
// is a gauge, so it says nothing about the interval between readings.
type UsageSample struct {
	TeamID        string
	AppID         string
	SampledAt     time.Time
	CPUMillicores int
	MemoryBytes   int64
	Active        bool
}

// ObservedUsage summarises samples for one app over one UTC day.
// Averages describe typical load; peaks are what a limit has to cover.
type ObservedUsage struct {
	TeamID           string
	AppID            string
	Day              time.Time
	CPUMillicoresAvg int
	CPUMillicoresMax int
	MemoryBytesAvg   int64
	MemoryBytesMax   int64
	SampleCount      int
}

// InsertUsageSamples writes a batch of observations.
func (r *Repository) InsertUsageSamples(ctx context.Context, samples []UsageSample) error {
	if len(samples) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, s := range samples {
		batch.Queue(`
INSERT INTO usage_sample (team_id, app_id, sampled_at, cpu_millicores, memory_bytes, active)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)`,
			s.TeamID, s.AppID, s.SampledAt, s.CPUMillicores, s.MemoryBytes, s.Active)
	}
	results := r.pool.SendBatch(ctx, batch)
	defer results.Close()
	for range samples {
		if _, err := results.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// SummarizeObservedUsage aggregates samples per app per UTC day over
// [from, to). Days outside the sample retention window simply have no
// rows; callers must not read that as "used nothing".
func (r *Repository) SummarizeObservedUsage(ctx context.Context, teamID, appID string, from, to time.Time) ([]ObservedUsage, error) {
	rows, err := r.pool.Query(ctx, `
SELECT team_id::text, app_id::text,
       (date_trunc('day', sampled_at AT TIME ZONE 'UTC'))::date AS day,
       ROUND(AVG(cpu_millicores))::int, MAX(cpu_millicores),
       ROUND(AVG(memory_bytes))::bigint, MAX(memory_bytes),
       COUNT(*)::int
FROM usage_sample
WHERE team_id = $1::uuid
  AND ($2::uuid IS NULL OR app_id = $2::uuid)
  AND app_id IS NOT NULL
  AND sampled_at >= $3 AND sampled_at < $4
GROUP BY team_id, app_id, 3
ORDER BY 3, 2`, teamID, nullableUUID(appID), from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ObservedUsage
	for rows.Next() {
		var o ObservedUsage
		if err := rows.Scan(&o.TeamID, &o.AppID, &o.Day,
			&o.CPUMillicoresAvg, &o.CPUMillicoresMax,
			&o.MemoryBytesAvg, &o.MemoryBytesMax, &o.SampleCount); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// DeleteUsageSamplesBefore drops observations past their retention.
func (r *Repository) DeleteUsageSamplesBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM usage_sample WHERE sampled_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ListAppSlugsByTeam maps app ids to slugs for one team, so usage
// responses can name apps without a join in every query.
func (r *Repository) ListAppSlugsByTeam(ctx context.Context, teamID string) (map[string]string, error) {
	rows, err := r.pool.Query(ctx, `
SELECT id::text, slug::text FROM app WHERE team_id = $1::uuid`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var id, slug string
		if err := rows.Scan(&id, &slug); err != nil {
			return nil, err
		}
		out[id] = slug
	}
	return out, rows.Err()
}

// PendingRollupDay is one (team, app, UTC day) still awaiting rollup.
type PendingRollupDay struct {
	TeamID string
	AppID  string
	Day    time.Time
}

// ListPendingRollupDays finds closed UTC days whose rollup is missing or
// stale, oldest first.
//
// Days are derived by expanding each interval across the days it spans,
// so an interval that straddles midnight makes both days pending. Only
// days strictly before today are returned: today is still accumulating
// and is computed live instead (spec § 6).
//
// "Stale" is the important half. A rollup computed before an interval
// reached its final shape is wrong in whichever direction the change
// went — a day rolled up while a pod was still open records a clamped
// full day and would keep that figure after the real end time arrived.
// Comparing computed_at against COALESCE(closed_at, created_at) brings
// such a day back for recomputation; since rollups are recomputed whole
// rather than incremented, revisiting one is free of double counting.
func (r *Repository) ListPendingRollupDays(ctx context.Context, limit int) ([]PendingRollupDay, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
SELECT DISTINCT i.team_id::text, i.app_id::text, d.day::date
FROM usage_allocation_interval i
CROSS JOIN LATERAL generate_series(
        date_trunc('day', i.started_at AT TIME ZONE 'UTC'),
        date_trunc('day', COALESCE(i.ended_at, NOW()) AT TIME ZONE 'UTC'),
        interval '1 day') AS d(day)
WHERE d.day < date_trunc('day', NOW() AT TIME ZONE 'UTC')
  AND i.started_at >= NOW() - interval '14 months'
  AND NOT EXISTS (
        SELECT 1 FROM usage_daily_rollup ru
         WHERE ru.team_id = i.team_id
           AND ru.app_id = i.app_id
           AND ru.day = d.day::date
           AND ru.computed_at >= COALESCE(i.closed_at, i.created_at))
ORDER BY 3, 1, 2
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PendingRollupDay
	for rows.Next() {
		var p PendingRollupDay
		if err := rows.Scan(&p.TeamID, &p.AppID, &p.Day); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// OldestPendingRollupDay returns the oldest closed day still awaiting a
// rollup, or zero time if the ledger is caught up. Drives the lag gauge.
func (r *Repository) OldestPendingRollupDay(ctx context.Context) (time.Time, error) {
	days, err := r.ListPendingRollupDays(ctx, 1)
	if err != nil || len(days) == 0 {
		return time.Time{}, err
	}
	return days[0].Day, nil
}

// DeleteAllocationIntervalsBefore drops closed intervals that ended before
// cutoff. Open intervals are never touched regardless of age — an interval
// open for a year means a pod alive for a year, not a stale row.
func (r *Repository) DeleteAllocationIntervalsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
DELETE FROM usage_allocation_interval
 WHERE ended_at IS NOT NULL AND ended_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// nullableUUID turns the "" sentinel used for "every app" into a real
// NULL, so the uuid cast in the filter is never evaluated for it.
func nullableUUID(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

func bigIntToNumeric(v *big.Int) (pgtype.Numeric, error) {
	n := new(big.Int)
	if v != nil {
		n.Set(v)
	}
	if n.Sign() < 0 {
		return pgtype.Numeric{}, fmt.Errorf("memory_byte_seconds must not be negative, got %s", n)
	}
	return pgtype.Numeric{Int: n, Exp: 0, Valid: true}, nil
}

func numericToBigInt(n pgtype.Numeric) *big.Int {
	if !n.Valid || n.Int == nil {
		return big.NewInt(0)
	}
	out := new(big.Int).Set(n.Int)
	// numeric(30,0) never carries a fractional part, but a non-zero Exp is
	// still representable on the wire; scale it rather than silently
	// returning a value off by orders of magnitude.
	if n.Exp > 0 {
		out.Mul(out, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n.Exp)), nil))
	} else if n.Exp < 0 {
		out.Quo(out, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-n.Exp)), nil))
	}
	return out
}
