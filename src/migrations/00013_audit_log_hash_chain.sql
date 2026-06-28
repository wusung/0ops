-- +goose Up
-- +goose StatementBegin

-- M9.1 (slice a): audit-export-and-integrity spec § 4 — hash-chain schema.
-- Adds per-row tamper-evidence columns and the per-(team, month) anchor
-- table. The append-only DB role split (spec § 5.1) and the write-path that
-- populates these columns (spec § 4.4) land in later M9.1 slices; until then
-- existing inserts leave prev_hash / row_hash NULL — pre-chain rows that
-- verify treats as existence-only (spec § 4.1).

alter table audit_log
    add column if not exists prev_hash bytea,
    add column if not exists row_hash  bytea;

-- Archive table mirrors the hash columns so delete_app rows keep their
-- single-row integrity proof after their partition is dropped (spec § 5.2).
alter table audit_log_archive
    add column if not exists prev_hash bytea,
    add column if not exists row_hash  bytea;

-- Per-(team, partition_month) chain anchor. One row per chain; the row is
-- never dropped, so verify can attest "this month had N rows, tip = H" even
-- after the partition itself is dropped at 13 months (spec § 4.2, § 8).
-- genesis_hash is derived purely from the chain coordinates (spec § 4.3) so a
-- third party can recompute it; tip_hash / row_count advance on every INSERT.
create table if not exists audit_chain_head (
    team_id         uuid not null references team(id) on delete cascade,
    partition_month date not null,
    genesis_hash    bytea not null,
    tip_hash        bytea not null,
    row_count       bigint not null default 0,
    first_row_id    bigint,
    last_row_id     bigint,
    updated_at      timestamptz not null default now(),
    primary key (team_id, partition_month)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

drop table if exists audit_chain_head;

alter table audit_log_archive
    drop column if exists prev_hash,
    drop column if exists row_hash;

alter table audit_log
    drop column if exists prev_hash,
    drop column if exists row_hash;

-- +goose StatementEnd
