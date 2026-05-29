-- +goose Up

alter table preview
    add column trace_id text not null default '00000000000000000000000000000000';

-- +goose Down

alter table preview drop column trace_id;
