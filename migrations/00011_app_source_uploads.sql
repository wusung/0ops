-- +goose Up
-- +goose StatementBegin

create table if not exists app_source_uploads (
    id              text primary key,
    team_id         uuid not null references team(id) on delete cascade,
    actor_user_id   uuid not null references user_account(id),
    size_bytes      bigint not null,
    sha256          text not null,
    archive_format  text not null,
    status          text not null default 'received',
    pinned_at       timestamptz,
    expires_at      timestamptz not null,
    received_at     timestamptz not null default now(),
    gc_at           timestamptz,
    constraint app_source_uploads_status_chk
        check (status in ('received','pinned','expired','gc''d')),
    constraint app_source_uploads_format_chk
        check (archive_format in ('tar.zst','tar.gz'))
);

create index if not exists idx_app_source_uploads_team_status
    on app_source_uploads (team_id, status);

create index if not exists idx_app_source_uploads_expires
    on app_source_uploads (expires_at)
    where status in ('received','pinned');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

drop table if exists app_source_uploads;

-- +goose StatementEnd
