-- Add preview.trace_id so the preview → confirm → deploy_run chain
-- carries the same trace through every row, per ADR-0006 § 4 point 5.
-- The default sentinel matches `missingTraceSentinel` in
-- src/internal/server/services/audit/log.go:131 — it is a backfill
-- placeholder for legacy rows; CreatePreview overwrites it with the
-- real trace_id read from ctx (Task 2).

-- +goose Up

alter table preview
    add column if not exists trace_id text not null default '00000000000000000000000000000000';

-- +goose Down

alter table preview drop column if exists trace_id;
