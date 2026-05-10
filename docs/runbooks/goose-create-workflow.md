# Goose Create Workflow

## Goal

標準化新增 migration 的流程，避免改寫歷史、漏掉 `sqlc` 重生、或缺測試就提交。

## Create a migration

Run:

```bash
goose create add_team_plan_check sql
```

## Edit the generated file

- 補完整 `-- +goose Up`
- 補完整 `-- +goose Down`
- 變更必須單一目的
- 不可修改既有 migration 檔去覆蓋歷史

## Verify locally

1. `make migrate`
2. 若 schema 或 query shape 有變：直接跑 container 版 `sqlc`

```bash
podman run --rm --userns=keep-id -v "$PWD":/src -w /src docker.io/sqlc/sqlc generate
```

3. `go test ./internal/server/db -v`
4. 若 schema 影響其他 package：`go test ./...`

## Rules

- 不可提交只有 `Up` 沒有 `Down` 的 migration
- 不可修改歷史 migration 來修新問題
- 只要 schema 或 query signature 變更，就必須重生 `sqlc`
- 只要 query 行為改變，就必須補或更新測試
