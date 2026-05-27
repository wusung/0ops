-- +goose Up
-- +goose StatementBegin

-- M5.1: delete-app-flow spec § 11 schema diff
-- app.status was an untyped text column with implicit values ('live',
-- 'paused'). delete_app introduces 'deleting' and 'delete_compensated';
-- harden the column with a CHECK constraint and backfill defaults so the
-- saga can rely on the lifecycle enum (spec § 13 hard rule #1).
update app set status = 'live' where status is null;

alter table app
    alter column status set default 'live',
    alter column status set not null;

alter table app
    drop constraint if exists app_status_check;
alter table app
    add constraint app_status_check
        check (status in ('queued', 'live', 'paused', 'deleting', 'delete_compensated'));
-- 'queued' kept for backwards compatibility with the M2.1 create_app saga
-- which inserts new app rows in 'queued' before the first deploy callback
-- transitions them to 'live'. delete_app refuses to operate on 'queued' or
-- 'deleting' rows (spec § 4.2 already_deleting; § 5.1 precondition_changed).

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

alter table app drop constraint if exists app_status_check;
alter table app alter column status drop not null;
alter table app alter column status drop default;

-- +goose StatementEnd
