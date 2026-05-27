-- +goose Up
-- +goose StatementBegin

-- M5.3: reconciler-and-incident spec § 4 + § 6.3 + § 9.1
-- 1. Extend reconciliation_job with status + trace_id (spec § 4 hard
--    rule #7: status enumeration is fixed; spec § 16 #7).
-- 2. Add deploy_run CHECK constraint to enforce failure_classification
--    non-null on final-failure states (spec § 6.3, § 16 hard rule #1).
-- 3. Create incident table per spec § 9.1 with its two indexes.

-- Step 1: extend reconciliation_job. status replaces the old implicit
-- "completed_at != NULL" convention so the workers can claim rows and
-- mark them failed_permanently after the §16 #4 backoff cap.
alter table reconciliation_job
    add column if not exists status text not null default 'pending'
        check (status in ('pending', 'in_progress', 'completed', 'failed_permanently'));

alter table reconciliation_job
    add column if not exists trace_id text;

-- Backfill: legacy rows whose completed_at is set should be 'completed';
-- the rest remain 'pending' so existing cleanup_residue retries continue.
update reconciliation_job
   set status = 'completed'
 where completed_at is not null
   and status = 'pending';

-- Hot-path index for the worker that scans the queue every tick.
drop index if exists recon_pending;
create index if not exists recon_pending
    on reconciliation_job (status, next_attempt_at)
    where status = 'pending';

create index if not exists recon_subject
    on reconciliation_job (subject_type, subject_id);

create index if not exists recon_team
    on reconciliation_job (team_id);

-- Step 2: deploy_run failure_classification CHECK constraint. The
-- reconciler MUST surface a classification before final-failure states
-- become visible to operators (spec § 7.2 + § 16 #1).
alter table deploy_run
    drop constraint if exists failure_classification_required;
alter table deploy_run
    add constraint failure_classification_required
    check (
        status not in ('failed', 'rolled_back', 'failed_permanently')
        or failure_classification is not null
    );

-- Step 3: incident table. severity is a fixed enum; closed_at = NULL
-- means open. Indexes match the two access paths in spec § 9.3-§ 9.4:
-- open-incident dashboard scan and per-team chronological list.
create table if not exists incident (
    id            uuid primary key default gen_random_uuid(),
    team_id       uuid not null references team(id) on delete cascade,
    subject_type  text not null,
    subject_id    uuid not null,
    kind          text not null,
    severity      text not null default 'medium'
        check (severity in ('low', 'medium', 'high', 'critical')),
    description   text,
    trace_id      text,
    opened_at     timestamptz not null default now(),
    closed_at     timestamptz,
    closed_by     uuid references user_account(id),
    closed_note   text
);

create index if not exists incident_open
    on incident (opened_at)
    where closed_at is null;

create index if not exists incident_team
    on incident (team_id, opened_at desc);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

drop index if exists incident_team;
drop index if exists incident_open;
drop table if exists incident;

alter table deploy_run drop constraint if exists failure_classification_required;

drop index if exists recon_team;
drop index if exists recon_subject;
drop index if exists recon_pending;
create index if not exists recon_pending
    on reconciliation_job (next_attempt_at)
    where completed_at is null;

alter table reconciliation_job drop column if exists trace_id;
alter table reconciliation_job drop column if exists status;

-- +goose StatementEnd
