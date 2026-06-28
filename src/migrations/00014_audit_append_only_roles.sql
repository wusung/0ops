-- +goose Up
-- +goose StatementBegin

-- M9.1 (slice b2): audit-export-and-integrity spec § 5.1 / ADR-0015 § 3.1 —
-- append-only DB role split. Enforces hard rules #1/#2 at the Postgres
-- privilege layer (not application self-discipline): the application runtime
-- role cannot UPDATE or DELETE audit_log, so a leaked runtime credential
-- (threat-model AD1) cannot rewrite or erase the ledger.
--
-- Roles (created NOLOGIN — pure privilege/group roles; a per-environment
-- LOGIN identity becomes a member of "0ops_app" and inherits its envelope,
-- so no secret ever lives in this migration):
--   "0ops_app"      runtime connection envelope — SELECT/INSERT on audit_log
--                   (NO UPDATE/DELETE), full DML elsewhere, tip maintenance on
--                   audit_chain_head.
--   "0ops_migrate"  DDL identity (schema evolution; not a runtime writer).
--   "0ops_archive"  rollover / archive identity — may DELETE audit_log rows and
--                   DROP partitions; never used by the runtime (spec § 5.2).
--
-- Role names follow the spec verbatim; the leading digit means every
-- reference is a quoted identifier.

DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '0ops_app') THEN
        CREATE ROLE "0ops_app" NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '0ops_migrate') THEN
        CREATE ROLE "0ops_migrate" NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '0ops_archive') THEN
        CREATE ROLE "0ops_archive" NOLOGIN;
    END IF;
END $$;

-- Schema usage for all three roles.
GRANT USAGE ON SCHEMA public TO "0ops_app", "0ops_archive";
GRANT USAGE, CREATE ON SCHEMA public TO "0ops_migrate";

-- "0ops_app": full DML across the schema, then the append-only carve-out.
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO "0ops_app";
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO "0ops_app";

-- Append-only carve-out: revoke change/erase on audit_log (parent) and every
-- existing partition (spec § 5.1 — direct-to-partition access bypasses the
-- parent ACL, so each partition is revoked explicitly), plus the archive table.
REVOKE UPDATE, DELETE ON audit_log FROM "0ops_app";
REVOKE UPDATE, DELETE ON audit_log_archive FROM "0ops_app";
DO $$
DECLARE
    part regclass;
BEGIN
    FOR part IN
        SELECT inhrelid::regclass
        FROM pg_inherits
        WHERE inhparent = 'audit_log'::regclass
    LOOP
        EXECUTE format('REVOKE UPDATE, DELETE ON %s FROM "0ops_app"', part);
    END LOOP;
END $$;

-- audit_chain_head: the runtime advances tip_hash / row_count on every INSERT,
-- so it keeps SELECT/INSERT/UPDATE but never DELETE (spec § 5.1 table).
REVOKE DELETE ON audit_chain_head FROM "0ops_app";

-- "0ops_archive": move + drop authority for the rollover job (spec § 5.2).
-- DELETE on audit_log so it can relocate delete_app rows into the archive;
-- partition DROP authority comes from table ownership / a SECURITY DEFINER
-- helper provisioned per environment (spec § 12 Open issue), out of scope here.
GRANT SELECT, INSERT, DELETE ON audit_log, audit_log_archive TO "0ops_archive";
GRANT SELECT, UPDATE ON audit_chain_head TO "0ops_archive";
DO $$
DECLARE
    part regclass;
BEGIN
    FOR part IN
        SELECT inhrelid::regclass
        FROM pg_inherits
        WHERE inhparent = 'audit_log'::regclass
    LOOP
        EXECUTE format('GRANT SELECT, INSERT, DELETE ON %s TO "0ops_archive"', part);
    END LOOP;
END $$;

-- "0ops_migrate": full authority to evolve the schema.
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO "0ops_migrate";
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO "0ops_migrate";

-- Future objects created by the migration owner inherit the same envelope so
-- the single runtime connection keeps working as new feature tables land. This
-- intentionally departs from spec § 5.1's literal `FOR ROLE 0ops_migrate ...
-- REVOKE`: dev runs migrations as the owner (not "0ops_migrate"), and a blanket
-- default-revoke would strip UPDATE/DELETE from every future app table.
--
-- Append-only is NOT weakened by this: the runtime always writes via the parent
-- (INSERT INTO audit_log ...), and Postgres checks the PARENT's ACL for
-- via-parent access — and the parent has no UPDATE/DELETE for "0ops_app". The
-- per-partition REVOKE below (and in db.CreateMonthlyPartition for future
-- partitions) is defense-in-depth against an attacker addressing a partition
-- table DIRECTLY (spec § 5.1). INVARIANT: any future migration that
-- pre-creates an audit_log partition MUST `REVOKE UPDATE, DELETE ON it FROM
-- "0ops_app"`, and rollover must run under a privileged role, not "0ops_app".
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "0ops_app";
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO "0ops_app";
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "0ops_migrate";
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO "0ops_migrate";

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER DEFAULT PRIVILEGES IN SCHEMA public
    REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM "0ops_app";
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    REVOKE USAGE, SELECT, UPDATE ON SEQUENCES FROM "0ops_app";
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM "0ops_migrate";
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    REVOKE USAGE, SELECT, UPDATE ON SEQUENCES FROM "0ops_migrate";

-- Drop the roles cleanly. REASSIGN OWNED first moves any objects the role owns
-- to the current (migration) user — critical for "0ops_migrate", which in
-- production is the migration identity and therefore OWNS the schema's tables:
-- a bare DROP OWNED would delete them. After reassigning objects, DROP OWNED
-- strips the role's remaining grants / default privileges so DROP ROLE succeeds.
DO $$
DECLARE
    role_name text;
BEGIN
    FOREACH role_name IN ARRAY ARRAY['0ops_app', '0ops_archive', '0ops_migrate'] LOOP
        IF EXISTS (SELECT FROM pg_roles WHERE rolname = role_name) THEN
            EXECUTE format('REASSIGN OWNED BY %I TO CURRENT_USER', role_name);
            EXECUTE format('DROP OWNED BY %I', role_name);
            EXECUTE format('DROP ROLE %I', role_name);
        END IF;
    END LOOP;
END $$;

-- +goose StatementEnd
