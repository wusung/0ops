-- Add deploy_run.image_digest for supply-chain SC3 mitigation
-- (supply-chain-security spec § 6 / § 4.4, ADR-0017 § 3.4).
--
-- The deploy callback (deploy-app.yml) resolves the immutable sha256 digest
-- of the pushed image and sends it as `image_digest`; the backend persists it
-- here so the audit / forensic query path can pull the cosign attestation
-- (SBOM + provenance) by `deploy_run.image_digest -> <repo>@<digest>`, and so
-- the three-way invariant "verified digest == deployed digest ==
-- deploy_run.image_digest" (spec hard rule #6) has a backend anchor.
--
-- Kept nullable for migration safety (no ADD COLUMN NOT NULL DEFAULT in one
-- shot, matching 00012_preview_trace_id / 00015_preview_risk_level). Callbacks
-- from older workflows that do not yet send image_digest leave it NULL and
-- remain valid; the column is read with COALESCE on the query side.
--
-- No grant change: 0ops_app already holds table-level INSERT/UPDATE/SELECT on
-- deploy_run (migration 00014), which covers new columns.

-- +goose Up

alter table deploy_run add column if not exists image_digest text;

-- +goose Down

alter table deploy_run drop column if exists image_digest;
