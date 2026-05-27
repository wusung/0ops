-- +goose Up
-- +goose StatementBegin

create extension if not exists "pgcrypto";
create extension if not exists "citext";

-- 租戶與身份
create table if not exists user_account (
    id          uuid primary key default gen_random_uuid(),
    github_login citext unique,
    email        citext,
    created_at   timestamptz not null default now()
);

create table if not exists team (
    id                 uuid primary key default gen_random_uuid(),
    slug               citext not null unique,
    name               text   not null,
    plan               text   not null default 'free',
    github_install_id  bigint,
    created_at         timestamptz not null default now(),
    archived_at        timestamptz
);

create table if not exists team_membership (
    team_id    uuid not null references team(id) on delete cascade,
    user_id    uuid not null references user_account(id) on delete cascade,
    role       text not null check (role in ('owner','admin','member','viewer')),
    invited_at timestamptz,
    joined_at  timestamptz,
    primary key (team_id, user_id)
);

-- 主要 entity（slug 在 team 內唯一）
create table if not exists app (
    id                  uuid primary key default gen_random_uuid(),
    team_id             uuid not null references team(id) on delete cascade,
    slug                citext not null,
    name                text,
    repo_url            text,
    repo_default_branch text,
    image_ref           text,
    builder             text,
    created_by          uuid references user_account(id),
    status              text,
    created_at          timestamptz not null default now(),
    updated_at          timestamptz not null default now(),
    unique (team_id, slug)
);

create table if not exists domain_binding (
    id                  uuid primary key default gen_random_uuid(),
    app_id              uuid not null references app(id) on delete cascade,
    team_id             uuid not null references team(id) on delete cascade,
    hostname            citext not null unique,
    kind                text   check (kind in ('primary','extra')),
    verified            boolean not null default false,
    verification_token  text,
    cf_hostname_id      text,
    cf_dns_record_id    text,
    expires_at          timestamptz,
    created_at          timestamptz not null default now(),
    verified_at         timestamptz
);

-- Deploy 狀態機 + 失敗分類 + metering 預埋
create table if not exists deploy_run (
    id                     uuid primary key default gen_random_uuid(),
    app_id                 uuid not null references app(id) on delete cascade,
    team_id                uuid not null references team(id) on delete cascade,
    commit_sha             text,
    ref                    text,
    workflow_run_id        bigint,
    status                 text not null,
    failure_classification text,
    trace_id               text,
    events                 jsonb not null default '[]'::jsonb,
    build_minutes          numeric(10,2),
    image_size_bytes       bigint,
    started_at             timestamptz,
    finished_at            timestamptz,
    error_summary          text
);

create index if not exists deploy_run_team_app_started_idx
    on deploy_run (team_id, app_id, started_at desc);

-- 用量採樣（v1 寫入，v2 暴露 query）
create table if not exists usage_sample (
    id            bigserial primary key,
    team_id       uuid not null references team(id) on delete cascade,
    app_id        uuid references app(id) on delete cascade,
    sampled_at    timestamptz not null default now(),
    cpu_millicores int,
    memory_bytes  bigint,
    active        boolean,
    egress_bytes  bigint
);

-- 兩階段寫入 + idempotency
create table if not exists preview (
    id              uuid primary key default gen_random_uuid(),
    team_id         uuid not null references team(id) on delete cascade,
    actor_user_id   uuid not null references user_account(id),
    action          text not null,
    args            jsonb not null default '{}'::jsonb,
    action_summary  text,
    side_effects    jsonb not null default '[]'::jsonb,
    idempotency_key text not null,
    last_result     jsonb,
    expires_at      timestamptz not null,
    consumed_at     timestamptz,
    created_at      timestamptz not null default now(),
    unique (team_id, idempotency_key)
);

-- 認證
create table if not exists cli_token (
    id            uuid primary key default gen_random_uuid(),
    owner_user_id uuid not null references user_account(id) on delete cascade,
    team_id       uuid not null references team(id) on delete cascade,
    token_hash    text not null,
    name          text,
    scopes        text[] not null,
    last_used_at  timestamptz,
    created_at    timestamptz not null default now(),
    revoked_at    timestamptz
);

-- Webhook 防重放
create table if not exists webhook_dedup (
    provider     text not null,
    delivery_id  text not null,
    received_at  timestamptz not null default now(),
    primary key (provider, delivery_id)
);

-- 稽核
create table if not exists audit_log (
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

-- Reconciler 收斂工作
create table if not exists reconciliation_job (
    id              uuid primary key default gen_random_uuid(),
    team_id         uuid not null references team(id) on delete cascade,
    subject_type    text not null,
    subject_id      uuid,
    kind            text not null,
    attempts        int  not null default 0,
    next_attempt_at timestamptz,
    payload         jsonb,
    last_error      text,
    created_at      timestamptz not null default now(),
    completed_at    timestamptz
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table if exists reconciliation_job;
drop table if exists audit_log;
drop table if exists webhook_dedup;
drop table if exists cli_token;
drop table if exists preview;
drop table if exists usage_sample;
drop table if exists deploy_run;
drop table if exists domain_binding;
drop table if exists app;
drop table if exists team_membership;
drop table if exists team;
drop table if exists user_account;
-- +goose StatementEnd
