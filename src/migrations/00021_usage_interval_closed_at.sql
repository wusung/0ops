-- +goose NO TRANSACTION

-- resource-usage-metering: make rollups recomputable.
--
-- ListPendingRollupDays picked days by "no rollup row exists yet", which
-- made every day a one-shot: once computed, never revisited. Two real
-- failures followed from that, in opposite directions:
--
--   over-bill  A pod dies at 10:00 while the backend is out of contact.
--              The rollup runs at midnight with the interval still open,
--              clamps it to the day boundary and records a full 24h.
--              Reconcile later stamps the real end time — and the day
--              stays at 24h forever.
--
--   under-bill A backend restart backfills an interval into a day that
--              already had a rollup row. Those seconds never land.
--
-- ended_at cannot distinguish these: it is the pod's clock, not ours.
-- closed_at records when *the ledger* learned the interval ended, which
-- is what a rollup has to be compared against to know it is stale.

-- +goose Up

alter table usage_allocation_interval
    add column if not exists closed_at timestamptz;

comment on column usage_allocation_interval.closed_at is
    'When the ledger closed this interval (wall clock), as opposed to ended_at, which is the pod''s own timestamp. Used to detect rollups computed before this interval reached its final shape.';

-- Backfill: existing closed intervals predate every rollup that could be
-- affected, so created_at is a safe stand-in and keeps COALESCE simple.
update usage_allocation_interval
   set closed_at = created_at
 where ended_at is not null and closed_at is null;

create index concurrently if not exists idx_usage_alloc_closed_at
    on usage_allocation_interval (closed_at)
    where ended_at is not null;

-- +goose Down

drop index concurrently if exists idx_usage_alloc_closed_at;
alter table usage_allocation_interval drop column if exists closed_at;
