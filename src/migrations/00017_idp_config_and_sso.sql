-- +goose NO TRANSACTION
-- M9.5 (sso-saml): SSO and external identity (OIDC-first).
--
-- ADR-0016 + docs/features/sso-saml/spec.md § 11. Adds the three IdP tables
-- (idp_config / idp_domain / idp_identity) and augments team_membership /
-- cli_token with the auth_source + deactivation / linkage columns that the
-- central-revocation path (spec § 7.2) relies on.
--
-- CREATE INDEX CONCURRENTLY cannot run inside a transaction (migrationlint R1 /
-- postgres-ha-and-dr spec § 16 hard rule #7), so this file runs in autocommit
-- mode like 00009 / 00011. The other statements are individually idempotent
-- (IF NOT EXISTS / IF EXISTS), so a mid-file failure is safe to re-run.
--
-- auth_source is added via the nullable → backfill → SET NOT NULL split
-- (migrationlint R2 / hard rule #8); the CHECK constraint is attached after.

-- +goose Up

-- IdP configuration: one team one IdP (v1). protocol CHECK is v1-only 'oidc';
-- the SAML reservation columns (metadata_url / sp_entity_id / idp_cert) are
-- nullable and unused until the SAML phase (ADR-0016 § 3.1, hard rule #10).
-- client_secret_ref points at the secrets store; the plaintext secret never
-- lands in this table (hard rule #7).
create table if not exists idp_config (
    id                 uuid primary key default gen_random_uuid(),
    team_id            uuid not null unique references team(id) on delete cascade,
    protocol           text not null default 'oidc'
                         check (protocol in ('oidc')),
    display_name       text,
    issuer             text not null,
    discovery_url      text,
    client_id          text not null,
    client_secret_ref  text not null,
    scopes             text[] not null default '{openid,email,profile}',
    enforce            bool not null default false,
    jit_default_role   text not null default 'member'
                         check (jit_default_role in ('admin','member','viewer')),
    group_claim        text,
    group_role_map     jsonb,
    pat_policy         text not null default 'allow'
                         check (pat_policy in ('allow','disallow')),
    session_max_ttl_s  int not null default 28800
                         check (session_max_ttl_s > 0 and session_max_ttl_s <= 86400),
    -- SAML reservation (v1 nullable, unused).
    metadata_url       text,
    sp_entity_id       text,
    idp_cert           text,
    created_by         uuid references user_account(id),
    created_at         timestamptz not null default now(),
    updated_at         timestamptz
);

-- IdP-bound domain. domain is globally UNIQUE so one domain cannot be claimed
-- by two teams (login hijack guard, spec § 5.2). verification_token is the DNS
-- TXT value; it is redacted and never enters audit args (hard rule #7).
create table if not exists idp_domain (
    id                 uuid primary key default gen_random_uuid(),
    idp_config_id      uuid not null references idp_config(id) on delete cascade,
    team_id            uuid not null references team(id) on delete cascade,
    domain             citext not null unique,
    verification_token text not null,
    verified           bool not null default false,
    verified_at        timestamptz,
    created_at         timestamptz not null default now()
);

-- IdP identity mapping (OIDC sub ↔ 0ops user). deactivated_at is the central
-- revocation marker (spec § 7.2). No column is added to user_account.
create table if not exists idp_identity (
    idp_config_id  uuid not null references idp_config(id) on delete cascade,
    user_id        uuid not null references user_account(id) on delete cascade,
    idp_subject    text not null,
    email          citext,
    last_login_at  timestamptz,
    deactivated_at timestamptz,
    created_at     timestamptz not null default now(),
    primary key (idp_config_id, user_id),
    unique (idp_config_id, idp_subject)
);

-- team_membership: tag JIT origin + soft-deactivation (revocation). auth_source
-- via the three-step split; deactivated_at is plain nullable.
alter table team_membership
    add column if not exists auth_source text;

update team_membership
set auth_source = 'native'
where auth_source is null;

alter table team_membership
    alter column auth_source set not null;

-- SET DEFAULT is a separate statement from the ADD COLUMN (migrationlint R2 only
-- forbids the combined ADD COLUMN ... NOT NULL DEFAULT), so existing insert paths
-- that omit auth_source keep working with the 'native' default (spec § 11.2).
alter table team_membership
    alter column auth_source set default 'native';

-- ADD CONSTRAINT has no IF NOT EXISTS; guard it so a mid-file failure + re-run
-- (this file runs NO TRANSACTION) does not abort on 42710 already-exists.
-- +goose StatementBegin
do $$ begin
    if not exists (select 1 from pg_constraint where conname = 'team_membership_auth_source_check') then
        alter table team_membership
            add constraint team_membership_auth_source_check
                check (auth_source in ('native','sso'));
    end if;
end $$;
-- +goose StatementEnd

alter table team_membership
    add column if not exists deactivated_at timestamptz;

-- cli_token: tag SSO origin + link to idp_config (revocation batch locate).
-- kind stays CHECK in ('device','pat'); SSO bearer is kind='device' with
-- auth_source='sso' (spec § 11.2 note).
alter table cli_token
    add column if not exists auth_source text;

update cli_token
set auth_source = 'native'
where auth_source is null;

alter table cli_token
    alter column auth_source set not null;

alter table cli_token
    alter column auth_source set default 'native';

-- +goose StatementBegin
do $$ begin
    if not exists (select 1 from pg_constraint where conname = 'cli_token_auth_source_check') then
        alter table cli_token
            add constraint cli_token_auth_source_check
                check (auth_source in ('native','sso'));
    end if;
end $$;
-- +goose StatementEnd

alter table cli_token
    add column if not exists idp_config_id uuid references idp_config(id);

create index concurrently if not exists idp_domain_config
    on idp_domain (idp_config_id);
create index concurrently if not exists idp_identity_user
    on idp_identity (user_id);
create index concurrently if not exists idp_identity_active
    on idp_identity (idp_config_id) where deactivated_at is null;
create index concurrently if not exists cli_token_idp_revoke
    on cli_token (owner_user_id, team_id) where auth_source = 'sso';
create index concurrently if not exists team_membership_active
    on team_membership (team_id, user_id) where deactivated_at is null;

-- +goose Down

drop index concurrently if exists team_membership_active;
drop index concurrently if exists cli_token_idp_revoke;
drop index concurrently if exists idp_identity_active;
drop index concurrently if exists idp_identity_user;
drop index concurrently if exists idp_domain_config;

alter table cli_token drop constraint if exists cli_token_auth_source_check;
alter table cli_token drop column if exists idp_config_id;
alter table cli_token drop column if exists auth_source;

alter table team_membership drop constraint if exists team_membership_auth_source_check;
alter table team_membership drop column if exists deactivated_at;
alter table team_membership drop column if exists auth_source;

drop table if exists idp_identity;
drop table if exists idp_domain;
drop table if exists idp_config;
