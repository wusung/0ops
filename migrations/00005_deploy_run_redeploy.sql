-- +goose Up
-- +goose StatementBegin

-- M4.1: webhook-and-redeploy spec § 12 schema diff
-- deploy_run gains the trigger-source attribution needed for webhook vs
-- user vs reconciler distinction, plus a foreign key back to the actor for
-- audit traceability when redeploys are user-initiated.
alter table deploy_run
    add column if not exists source              text   not null default 'user',
    add column if not exists webhook_delivery_id text,
    add column if not exists actor_user_id       uuid   references user_account(id);

-- Constrain source enum so a future writer cannot smuggle in a free-form
-- attribution string (spec § 12 point 3).
alter table deploy_run
    drop constraint if exists deploy_run_source_check;
alter table deploy_run
    add constraint deploy_run_source_check
        check (source in ('user', 'webhook', 'reconciler'));

-- In-flight check ("is there a non-terminal deploy_run for this app?") is
-- run on every webhook push event; index keeps it within p95 budget.
create index if not exists deploy_run_app_status_idx
    on deploy_run (app_id, status)
    where status not in ('live', 'failed', 'canceled', 'rolled_back');

-- Lookup by webhook delivery id is used by reconciler / audit replay to
-- correlate a webhook event with the deploy_run it spawned.
create index if not exists deploy_run_webhook_delivery_idx
    on deploy_run (webhook_delivery_id)
    where webhook_delivery_id is not null;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop index if exists deploy_run_webhook_delivery_idx;
drop index if exists deploy_run_app_status_idx;
alter table deploy_run drop constraint if exists deploy_run_source_check;
alter table deploy_run
    drop column if exists actor_user_id,
    drop column if exists webhook_delivery_id,
    drop column if exists source;
-- +goose StatementEnd
