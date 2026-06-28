-- +goose NO TRANSACTION
-- M9.6 (audit-event-notification): outbound webhook outbox.
--
-- docs/features/audit-event-notification/spec.md § 4. Adds the per-team
-- subscription table and the partitioned delivery (outbox) table. The unique
-- event source stays audit_log; enqueue rides the audit insert transaction
-- (db.Repository.InsertAuditLog) so audit-success implies delivery-enqueued
-- (hard rule #3).
--
-- CREATE INDEX CONCURRENTLY cannot run inside a transaction (migrationlint R1 /
-- postgres-ha-and-dr spec § 16 hard rule #7), and PostgreSQL forbids
-- CONCURRENTLY on a partitioned PARENT. Both constraints are satisfied by
-- running NO TRANSACTION (like 00009 / 00011 / 00017) and creating the
-- delivery indexes per-partition (a partition child is an ordinary table, so
-- CONCURRENTLY is legal there and every CREATE INDEX carries the keyword the
-- linter requires). All statements are individually idempotent (IF NOT EXISTS),
-- so a mid-file failure is safe to re-run.
--
-- The partition seed is intentionally small (2026-06..2026-08) plus a DEFAULT
-- catch-all; the monthly rollover + 90-day retention drop job (spec § 9) is
-- deferred, and DEFAULT guarantees inserts never fail for out-of-window dates.

-- +goose Up

-- webhook_subscription: per-team outbound webhook config (spec § 4.1).
-- secret_ref points at the signing-key store; secret_material holds the v1
-- backing bytes. DEFERRED: at-rest envelope encryption is pending the
-- secrets-management module (hard rule #10 encryption face); v1 stores the
-- base64 key in secret_material and the SecretStore interface is the swap seam.
-- The plaintext key is never returned by any GET path (write-only reveal).
create table if not exists webhook_subscription (
    id                   uuid primary key default gen_random_uuid(),
    team_id              uuid not null references team(id) on delete cascade,
    url                  text not null,
    events               text[] not null check (cardinality(events) > 0),
    secret_ref           text not null,
    secret_material      text not null,
    description          text,
    active               boolean not null default true,
    disabled_reason      text check (disabled_reason in ('auto_circuit_breaker','manual')),
    consecutive_failures int not null default 0,
    last_delivery_at     timestamptz,
    created_by           uuid references user_account(id),
    created_at           timestamptz not null default now(),
    updated_at           timestamptz not null default now()
);

create index concurrently if not exists webhook_subscription_team
    on webhook_subscription (team_id);
create index concurrently if not exists webhook_subscription_team_active
    on webhook_subscription (team_id) where active;
create index concurrently if not exists webhook_subscription_events_gin
    on webhook_subscription using gin (events);

-- webhook_delivery: outbox / delivery record, monthly RANGE partition on
-- created_at (spec § 4.2). No FK to webhook_subscription so records survive
-- subscription deletion. The (id, created_at) PK carries the partition key.
create table if not exists webhook_delivery (
    id              uuid not null default gen_random_uuid(),
    subscription_id uuid not null,
    team_id         uuid not null,
    audit_log_id    bigint not null,
    event           text not null,
    payload         jsonb not null,
    status          text not null default 'pending'
                      check (status in ('pending','delivering','succeeded','failed','dropped')),
    attempt         int not null default 0,
    max_attempts    int not null default 6,
    next_attempt_at timestamptz not null default now(),
    response_status int,
    response_ms     int,
    error           text,
    created_at      timestamptz not null default now(),
    delivered_at    timestamptz,
    primary key (id, created_at)
) partition by range (created_at);

create table if not exists webhook_delivery_2026_06 partition of webhook_delivery
    for values from ('2026-06-01') to ('2026-07-01');
create table if not exists webhook_delivery_2026_07 partition of webhook_delivery
    for values from ('2026-07-01') to ('2026-08-01');
create table if not exists webhook_delivery_2026_08 partition of webhook_delivery
    for values from ('2026-08-01') to ('2026-09-01');
create table if not exists webhook_delivery_default partition of webhook_delivery default;

-- Per-partition indexes (CONCURRENTLY-legal on partition children). dedup is
-- the spec § 4.2 idempotency guard: one delivery per (subscription, audit
-- event) — enqueue treats unique_violation (23505) as a no-op.
create unique index concurrently if not exists webhook_delivery_2026_06_dedup
    on webhook_delivery_2026_06 (subscription_id, audit_log_id, created_at);
create index concurrently if not exists webhook_delivery_2026_06_due
    on webhook_delivery_2026_06 (next_attempt_at) where status in ('pending','failed','delivering');
create index concurrently if not exists webhook_delivery_2026_06_sub
    on webhook_delivery_2026_06 (subscription_id, created_at desc);
create index concurrently if not exists webhook_delivery_2026_06_team
    on webhook_delivery_2026_06 (team_id, created_at desc);

create unique index concurrently if not exists webhook_delivery_2026_07_dedup
    on webhook_delivery_2026_07 (subscription_id, audit_log_id, created_at);
create index concurrently if not exists webhook_delivery_2026_07_due
    on webhook_delivery_2026_07 (next_attempt_at) where status in ('pending','failed','delivering');
create index concurrently if not exists webhook_delivery_2026_07_sub
    on webhook_delivery_2026_07 (subscription_id, created_at desc);
create index concurrently if not exists webhook_delivery_2026_07_team
    on webhook_delivery_2026_07 (team_id, created_at desc);

create unique index concurrently if not exists webhook_delivery_2026_08_dedup
    on webhook_delivery_2026_08 (subscription_id, audit_log_id, created_at);
create index concurrently if not exists webhook_delivery_2026_08_due
    on webhook_delivery_2026_08 (next_attempt_at) where status in ('pending','failed','delivering');
create index concurrently if not exists webhook_delivery_2026_08_sub
    on webhook_delivery_2026_08 (subscription_id, created_at desc);
create index concurrently if not exists webhook_delivery_2026_08_team
    on webhook_delivery_2026_08 (team_id, created_at desc);

create unique index concurrently if not exists webhook_delivery_default_dedup
    on webhook_delivery_default (subscription_id, audit_log_id, created_at);
create index concurrently if not exists webhook_delivery_default_due
    on webhook_delivery_default (next_attempt_at) where status in ('pending','failed','delivering');
create index concurrently if not exists webhook_delivery_default_sub
    on webhook_delivery_default (subscription_id, created_at desc);
create index concurrently if not exists webhook_delivery_default_team
    on webhook_delivery_default (team_id, created_at desc);

-- +goose Down

drop table if exists webhook_delivery;
drop table if exists webhook_subscription;
