-- +goose Up
-- +goose StatementBegin
ALTER TABLE team_membership
    ADD COLUMN IF NOT EXISTS invited_by uuid REFERENCES user_account(id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE team_membership
    DROP COLUMN IF EXISTS invited_by;
-- +goose StatementEnd
