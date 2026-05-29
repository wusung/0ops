-- Add preview.trace_id so the preview → confirm → deploy_run chain
-- carries the same trace through every row, per ADR-0006 § 4 point 5.
-- Kept nullable for migration safety (spec § 16 hard rule #8 forbids
-- ADD COLUMN NOT NULL DEFAULT in one shot). The app layer guarantees
-- sentinel '00000000000000000000000000000000' (= missingTraceSentinel
-- in src/internal/server/services/audit/log.go:131) on INSERT via
-- audit.TraceIDFromContext, and GetPreview COALESCEs NULL on read.

-- +goose Up

alter table preview add column if not exists trace_id text;

-- +goose Down

alter table preview drop column if exists trace_id;
