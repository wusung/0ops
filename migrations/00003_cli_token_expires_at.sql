-- +goose Up
-- +goose StatementBegin

alter table if exists cli_token
    add column if not exists expires_at timestamptz;

update cli_token
set expires_at = created_at + interval '30 day'
where expires_at is null;

alter table cli_token
    alter column expires_at set not null;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
alter table cli_token
    alter column expires_at drop not null;

alter table cli_token
    drop column if exists expires_at;

-- +goose StatementEnd
