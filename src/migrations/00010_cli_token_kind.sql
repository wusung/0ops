-- +goose Up
-- +goose StatementBegin

alter table if exists cli_token
    add column if not exists kind text;

update cli_token
set kind = 'device'
where kind is null;

alter table cli_token
    alter column kind set not null;

alter table cli_token
    add constraint cli_token_kind_check check (kind in ('device', 'pat'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

alter table cli_token
    drop constraint if exists cli_token_kind_check;

alter table cli_token
    drop column if exists kind;

-- +goose StatementEnd
