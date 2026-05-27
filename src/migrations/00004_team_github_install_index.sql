-- +goose Up
-- +goose StatementBegin

-- Index lookups by github installation id used by the installation webhook
-- handler (github-app-install-flow spec § 3 / § 7.1).
create index if not exists team_github_install_id_idx
    on team (github_install_id)
    where github_install_id is not null;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop index if exists team_github_install_id_idx;
-- +goose StatementEnd
