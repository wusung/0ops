-- Add preview.risk_level + preview.required_phrase for high-risk action
-- differentiated confirmation (security-hardening spec § 5.2 / § 5.4). The
-- backend chokepoint db.CreatePreview computes both from (action, args) via
-- internal/server/security.RiskLevel / RequiredPhrase and stores them; the
-- confirm path reads them to enforce the typed-confirmation phrase as an
-- AND condition on top of preview_id (spec hard rule #1).
--
-- Both kept nullable for migration safety (no ADD COLUMN NOT NULL DEFAULT in
-- one shot, matching 00012_preview_trace_id). GetPreview COALESCEs NULL on
-- read (risk_level -> 'normal', required_phrase -> ''), so pre-existing rows
-- and any producer that does not set them remain valid normal-risk previews.
--
-- No grant change: 0ops_app already holds table-level INSERT/UPDATE/SELECT on
-- preview (migration 00014), which covers new columns.

-- +goose Up

alter table preview add column if not exists risk_level text;
alter table preview add column if not exists required_phrase text;

-- +goose Down

alter table preview drop column if exists required_phrase;
alter table preview drop column if exists risk_level;
