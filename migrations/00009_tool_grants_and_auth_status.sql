-- +goose NO TRANSACTION
-- CREATE INDEX CONCURRENTLY cannot run inside a transaction (spec § 16 hard
-- rule #7). The other statements in this file are individually atomic; goose
-- runs them sequentially in autocommit mode.

-- +goose Up

ALTER TABLE user_account
    ADD COLUMN IF NOT EXISTS auth_status TEXT DEFAULT 'authorized'
        CHECK (auth_status IN ('authorized', 'expired', 'revoked')),
    ADD COLUMN IF NOT EXISTS auth_scopes JSONB DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS auth_expires_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS tool_grants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id UUID NOT NULL REFERENCES team(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    tool_id TEXT NOT NULL,
    allowed BOOLEAN NOT NULL DEFAULT false,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    granted_by_actor_id UUID REFERENCES user_account(id) ON DELETE SET NULL,
    UNIQUE (team_id, user_id, tool_id)
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS tool_grants_team_user_allowed_idx
    ON tool_grants (team_id, user_id, allowed)
    WHERE allowed = true;

CREATE INDEX CONCURRENTLY IF NOT EXISTS tool_grants_team_granted_at_idx
    ON tool_grants (team_id, granted_at DESC);

-- +goose Down

DROP TABLE IF EXISTS tool_grants;

ALTER TABLE user_account
    DROP COLUMN IF EXISTS auth_status,
    DROP COLUMN IF EXISTS auth_scopes,
    DROP COLUMN IF EXISTS auth_expires_at;
