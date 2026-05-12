-- +goose Up
-- +goose StatementBegin

-- Extend user_account with auth status
ALTER TABLE user_account
    ADD COLUMN IF NOT EXISTS auth_status TEXT DEFAULT 'authorized'
        CHECK (auth_status IN ('authorized', 'expired', 'revoked')),
    ADD COLUMN IF NOT EXISTS auth_scopes JSONB DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS auth_expires_at TIMESTAMPTZ;

-- MCP tool grants: user selects which tools are allowed per team
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

-- Index for fast lookup during tool invocation
CREATE INDEX IF NOT EXISTS tool_grants_team_user_allowed_idx
    ON tool_grants (team_id, user_id, allowed) 
    WHERE allowed = true;

-- Index for audit / grant history
CREATE INDEX IF NOT EXISTS tool_grants_team_granted_at_idx
    ON tool_grants (team_id, granted_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS tool_grants;

ALTER TABLE user_account
    DROP COLUMN IF EXISTS auth_status,
    DROP COLUMN IF EXISTS auth_scopes,
    DROP COLUMN IF EXISTS auth_expires_at;

-- +goose StatementEnd
