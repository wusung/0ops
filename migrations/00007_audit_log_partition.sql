-- +goose Up
-- +goose StatementBegin

-- M5.2: audit-log spec § 4 schema diff
-- Convert audit_log into a monthly-partitioned table; add the
-- source/outcome/http_status attribution columns; create the
-- spec § 4.2 indexes and the audit_log_archive table that holds
-- delete_app rows beyond the 13-month rolling retention window
-- (spec § 9.2, § 15 hard rule #7).

-- Step 1: snapshot existing rows before the destructive rebuild.
-- audit_log has no incoming foreign keys (see 00001_init.sql), so
-- dropping it is safe; deploy_run.audit_id etc. do not exist.
create table if not exists audit_log_v1_backup as
    select * from audit_log;

drop table if exists audit_log;

-- Step 2: re-create audit_log as a PARTITIONED table by created_at month.
-- PostgreSQL requires the partition key to be part of the primary key,
-- so the PK widens to (id, created_at). Lookups by id alone (e.g.
-- /v1/teams/{slug}/audit/{id}) still hit indexed scans because every
-- partition carries an index on (id) via the inherited primary key.
create table audit_log (
    id            bigserial,
    team_id       uuid not null references team(id) on delete cascade,
    actor_user_id uuid references user_account(id),
    subject_type  text,
    subject_id    uuid,
    action        text not null,
    args          jsonb,
    result        jsonb,
    preview_id    uuid,
    trace_id      text,
    created_at    timestamptz not null default now(),
    source        text not null default 'user'
        check (source in ('user', 'webhook', 'reconciler', 'system')),
    outcome       text not null default 'success'
        check (outcome in ('success', 'failure', 'idempotent_replay')),
    http_status   int,
    primary key (id, created_at)
) partition by range (created_at);

-- Step 3: pre-create five partitions — one prior month, the current
-- month, and three future months. The ops audit-rollover job extends
-- this window; M5.2 ships the seed so the table is usable on day one.
create table if not exists audit_log_history partition of audit_log
    for values from (minvalue) to ('2026-01-01');
create table if not exists audit_log_2026_01 partition of audit_log
    for values from ('2026-01-01') to ('2026-02-01');
create table if not exists audit_log_2026_02 partition of audit_log
    for values from ('2026-02-01') to ('2026-03-01');
create table if not exists audit_log_2026_03 partition of audit_log
    for values from ('2026-03-01') to ('2026-04-01');
create table if not exists audit_log_2026_04 partition of audit_log
    for values from ('2026-04-01') to ('2026-05-01');
create table if not exists audit_log_2026_05 partition of audit_log
    for values from ('2026-05-01') to ('2026-06-01');
create table if not exists audit_log_2026_06 partition of audit_log
    for values from ('2026-06-01') to ('2026-07-01');
create table if not exists audit_log_2026_07 partition of audit_log
    for values from ('2026-07-01') to ('2026-08-01');
create table if not exists audit_log_2026_08 partition of audit_log
    for values from ('2026-08-01') to ('2026-09-01');

-- Step 4: spec § 4.2 indexes — replicated on the partitioned parent so
-- every partition inherits them automatically.
create index if not exists audit_log_team_created_idx
    on audit_log (team_id, created_at desc);
create index if not exists audit_log_team_action_idx
    on audit_log (team_id, action, created_at desc);
create index if not exists audit_log_team_actor_idx
    on audit_log (team_id, actor_user_id, created_at desc);
create index if not exists audit_log_trace_idx
    on audit_log (trace_id);

-- Step 5: backfill from the snapshot. Defaults populate source/outcome
-- so legacy rows survive without rewriting prior callers.
insert into audit_log (
    id, team_id, actor_user_id, subject_type, subject_id, action,
    args, result, preview_id, trace_id, created_at
)
select id, team_id, actor_user_id, subject_type, subject_id, action,
       args, result, preview_id, trace_id, created_at
from audit_log_v1_backup;

-- Resync the implicit sequence so future bigserial allocations do not
-- collide with backfilled ids.
select setval(
    pg_get_serial_sequence('audit_log', 'id'),
    coalesce((select max(id) from audit_log), 0) + 1,
    false
);

drop table if exists audit_log_v1_backup;

-- Step 6: archive table for permanent retention of delete_app rows
-- (spec § 9.2). Same shape as audit_log; not a partition of audit_log
-- so the rolling-drop policy never touches it. Operators move rows
-- via the audit-rollover job; query API never reads from this table.
create table if not exists audit_log_archive (
    id            bigint primary key,
    team_id       uuid not null,
    actor_user_id uuid,
    subject_type  text,
    subject_id    uuid,
    action        text not null,
    args          jsonb,
    result        jsonb,
    preview_id    uuid,
    trace_id      text,
    created_at    timestamptz not null,
    source        text not null,
    outcome       text not null,
    http_status   int,
    archived_at   timestamptz not null default now()
);

create index if not exists audit_log_archive_team_created_idx
    on audit_log_archive (team_id, created_at desc);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

drop table if exists audit_log_archive;

drop table if exists audit_log;

-- Restore the pre-partition shape from 00001_init.sql so a Down +
-- replay sequence remains consistent.
create table audit_log (
    id            bigserial primary key,
    team_id       uuid not null references team(id) on delete cascade,
    actor_user_id uuid references user_account(id),
    subject_type  text,
    subject_id    uuid,
    action        text not null,
    args          jsonb,
    result        jsonb,
    preview_id    uuid,
    trace_id      text,
    created_at    timestamptz not null default now()
);

-- +goose StatementEnd
