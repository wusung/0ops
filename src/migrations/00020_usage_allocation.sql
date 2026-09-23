-- +goose NO TRANSACTION
-- CREATE INDEX CONCURRENTLY cannot run inside a transaction. Same pattern
-- as 00009 / 00011.

-- resource-usage-metering spec § 5.
--
-- The ledger stores one row per pod, not one row per sample: the billing
-- basis is (declared allocation) x (lifetime in seconds), and both factors
-- are known constants, so integration is closed-form and accurate to the
-- second regardless of how often the backend polls. Timestamps are always
-- copied from the K8s object (spec § 14 rule #1), never taken from the
-- backend's wall clock, which is what makes a restart able to backfill an
-- interval's start without losing precision.

-- +goose Up

create table if not exists usage_allocation_interval (
    pod_uid         uuid primary key,
    team_id         uuid not null references team(id) on delete cascade,
    app_id          uuid not null references app(id) on delete cascade,
    namespace       text not null,
    pod_name        text not null,
    cpu_millicores  integer not null,
    memory_bytes    bigint  not null,
    gpu_count       integer not null default 0,
    gpu_type        text,
    started_at      timestamptz not null,
    ended_at        timestamptz,
    last_seen_at    timestamptz not null,
    -- true when ended_at is a conservative fallback (last_seen_at) rather
    -- than a K8s-authoritative timestamp. Such intervals always under-bill.
    estimated       boolean not null default false,
    closed_reason   text,
    created_at      timestamptz not null default now(),
    constraint usage_alloc_span_chk
        check (ended_at is null or ended_at >= started_at),
    constraint usage_alloc_closed_reason_chk
        check (closed_reason is null
               or closed_reason in ('terminated','deleted','reconciled_missing')),
    -- An open interval cannot carry a close reason, and a closed one must.
    constraint usage_alloc_closed_consistency_chk
        check ((ended_at is null) = (closed_reason is null)),
    constraint usage_alloc_nonneg_chk
        check (cpu_millicores >= 0 and memory_bytes >= 0 and gpu_count >= 0)
);

create index concurrently if not exists idx_usage_alloc_team_app_started
    on usage_allocation_interval (team_id, app_id, started_at desc);

-- Hottest path: every reconcile tick scans every still-open interval.
create index concurrently if not exists idx_usage_alloc_open
    on usage_allocation_interval (last_seen_at)
    where ended_at is null;

create table if not exists usage_daily_rollup (
    team_id                uuid not null references team(id) on delete cascade,
    app_id                 uuid not null references app(id) on delete cascade,
    day                    date not null,
    cpu_millicore_seconds  bigint        not null default 0,
    memory_byte_seconds    numeric(30,0) not null default 0,
    gpu_count_seconds      bigint        not null default 0,
    pod_seconds            bigint        not null default 0,
    -- of pod_seconds, how much came from intervals closed by fallback
    estimated_seconds      bigint        not null default 0,
    interval_count         integer       not null default 0,
    computed_at            timestamptz   not null default now(),
    primary key (team_id, app_id, day),
    constraint usage_rollup_estimated_chk
        check (estimated_seconds >= 0 and estimated_seconds <= pod_seconds)
);

-- +goose Down

drop table if exists usage_daily_rollup;
drop table if exists usage_allocation_interval;
