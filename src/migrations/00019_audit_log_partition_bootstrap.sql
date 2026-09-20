-- Bootstrap audit_log partitions relative to the time the migration runs.
--
-- 00007 seeded a fixed window of monthly partitions ending at
-- audit_log_2026_08 (covering up to 2026-09-01), on the assumption that the
-- rollover job would extend it. That job exists (audit.Rollover +
-- db.Repository.CreateMonthlyPartition) but has no production caller, so the
-- window was never extended: from 2026-09-01 onward every INSERT into
-- audit_log fails with
--
--     ERROR: no partition of relation "audit_log" found for row (SQLSTATE 23514)
--
-- which takes down audit writes in any environment and every DB-backed audit
-- test in CI.
--
-- A fixed list of months would only move the same cliff later, so this
-- migration computes the window from now(): the previous month through three
-- months ahead, the same [now-1, now+3] span audit.Rollover uses. A database
-- migrated at any date therefore starts with a usable window. Re-running is a
-- no-op (CREATE TABLE IF NOT EXISTS), so this stays safe under goose replay
-- and under two leader candidates racing during failover.
--
-- No DEFAULT partition is added on purpose: rows landing in a DEFAULT
-- partition make the later CREATE of that month's real partition fail until
-- the rows are moved out, which converts a loud, immediate error into a
-- silent one that surfaces weeks later.
--
-- 00014 INVARIANT: any migration that pre-creates an audit_log partition must
-- REVOKE UPDATE, DELETE on it FROM "0ops_app" (defense-in-depth against an
-- attacker addressing a partition directly). The REVOKE is best effort — an
-- environment that has not provisioned the role yet raises undefined_object
-- (42704) and must not fail the migration, matching the tolerance in
-- db.Repository.CreateMonthlyPartition.

-- +goose Up
-- +goose StatementBegin

do $$
declare
    month_start date := (date_trunc('month', now() at time zone 'utc') - interval '1 month')::date;
    window_end  date := (date_trunc('month', now() at time zone 'utc') + interval '3 months')::date;
    part_name   text;
begin
    while month_start <= window_end loop
        part_name := 'audit_log_' || to_char(month_start, 'YYYY_MM');

        execute format(
            'create table if not exists %I partition of audit_log for values from (%L) to (%L)',
            part_name,
            month_start,
            (month_start + interval '1 month')::date
        );

        begin
            execute format('revoke update, delete on %I from "0ops_app"', part_name);
        exception
            when undefined_object then
                null;
        end;

        month_start := (month_start + interval '1 month')::date;
    end loop;
end
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Intentionally empty. The Up path only creates partitions that audit_log
-- needs in order to accept writes; dropping them would delete audit rows,
-- which the append-only guarantee (00014) forbids. 00007's Down already drops
-- the whole partitioned table when the partitioning scheme itself is rolled
-- back.
select 1;

-- +goose StatementEnd
