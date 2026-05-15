# 0ops 開發 target 收口（spec § 9）
# 容器引擎固定 podman + podman compose v2；禁用 docker / podman-compose v1。

SHELL := /bin/bash

VERSION ?= dev
LDFLAGS := -s -w -X github.com/winshare/zeroops/internal/shared.Version=$(VERSION)
SQLC_IMAGE ?= docker.io/sqlc/sqlc:1.31.1

.PHONY: help dev dev-down dev-clean dev-logs dev-shell migrate migrate-down migrate-lint \
        build-images lint-compose lint-docker lint-go lint-prom-rules test contract-test build tidy sqlc \
        m2-2-e2e-validation m2-2-check m2-6-promtool m2-8-e2e-acceptance m2-8-check \
        m5-4-pitr-drill \
        task-list task-next task-run-all task-run task-rerun task-runner-test

help:
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## --- dev stack ---

dev: ## 啟動 dev stack（db + migrate + server）
	podman compose up -d

dev-down: ## 停止 stack（保留 volume）
	podman compose down

dev-clean: ## 停止並刪除 volume
	podman compose down -v

dev-logs: ## 跟 server log
	podman compose logs -f server

dev-shell: ## 進入 server 容器（dev stage 有 sh）
	podman compose exec server sh

## --- migrations ---

migrate: ## 套用 migration up（idempotent）
	podman compose run --rm migrate up

migrate-down: ## 回滾一格
	podman compose run --rm migrate down

migrate-lint: ## 跑 spec § 10.1 migration 安全閘（CONCURRENTLY、ADD COLUMN NOT NULL 三步拆分）
	go test ./internal/server/db/migrationlint/...

## --- build ---

build-images: ## 三 binary runtime image + migrations image
	podman build --target runtime -f cmd/server/Dockerfile -t localhost/0ops-server:runtime --build-arg VERSION=$(VERSION) .
	podman build --target runtime -f cmd/cli/Dockerfile    -t localhost/0ops-cli:runtime    --build-arg VERSION=$(VERSION) .
	podman build --target runtime -f cmd/mcp/Dockerfile    -t localhost/0ops-mcp:runtime    --build-arg VERSION=$(VERSION) .
	podman build                  -f migrations/Dockerfile -t localhost/0ops-migrations:runtime .

build: ## 本機 host 編譯三 binary 至 ./bin
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/0ops-server ./cmd/server
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/0ops        ./cmd/cli
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/0ops-mcp    ./cmd/mcp

## --- lint / test ---

lint-compose: ## 驗證 compose schema
	podman compose config -q

lint-docker: ## 驗證 Dockerfile（需安裝 hadolint）
	hadolint cmd/*/Dockerfile migrations/Dockerfile

lint-go: ## golangci-lint
	golangci-lint run

lint-prom-rules: m2-6-promtool ## 別名：跑 M2.6 promtool 驗證

.PHONY: m2-6-promtool
m2-6-promtool: ## 用 podman + prom/prometheus 跑 promtool check rules 驗證 observability ConfigMap rules
	@bash tasks/m2-6-promtool-validate.sh

test: ## go test ./...
	go test ./...

contract-test: ## backend/cli/mcp contract path tests
	go test ./internal/server ./internal/cli ./internal/mcp/server

tidy: ## go mod tidy
	go mod tidy

## --- M2.2 e2e validation ---

.PHONY: m2-2-e2e-validation
m2-2-e2e-validation:
	@bash tasks/m2-2-e2e-validation.sh --dev

.PHONY: m2-2-check
m2-2-check: lint-go test
	@echo "✓ M2.2 code checks passed"

## --- M2.8 e2e acceptance ---

.PHONY: m2-8-e2e-acceptance
m2-8-e2e-acceptance: ## 跑 M2.8 端到端驗收腳本（預設 mode=local；E2E_MODE=staging|production 可覆蓋）
	@bash tasks/m2-8-e2e-acceptance.sh

.PHONY: m2-8-check
m2-8-check: lint-go test ## M2.8 程式檢查（lint + 單元/契約測試）
	@echo "✓ M2.8 code checks passed"

## --- M5.4 PITR drill ---

m5-4-pitr-drill: ## 跑 local PITR drill（podman compose；spec § 8.3 + § 16 hard rule #5）
	@bash deploy/postgres/scripts/pitr-drill.sh

sqlc: ## 產生 sqlc 程式碼
	podman run --rm --userns=keep-id -v $(CURDIR):/src -w /src $(SQLC_IMAGE) generate

## --- task runner ---

task-list: ## 列出所有 task 與狀態
	@bash tasks/run/show.sh

task-next: ## 顯示下一個可執行 task
	@bash tasks/run/next.sh

task-run-all: ## 依序跑完所有 Pending task（中斷後再呼叫即 resume）
	@bash tasks/run/run-all.sh

task-run: ## 跑指定 task：make task-run TASK=M2.5
	@test -n "$(TASK)" || (echo "usage: make task-run TASK=<ID>" >&2; exit 1)
	@bash tasks/run/run-one.sh $(TASK)

task-rerun: ## 強制重跑指定 task：make task-rerun TASK=M2.5
	@test -n "$(TASK)" || (echo "usage: make task-rerun TASK=<ID>" >&2; exit 1)
	@bash tasks/run/run-one.sh --force $(TASK)

task-runner-test: ## 跑 task runner 自身的 smoke 測試
	@bash tasks/run/test/run-tests.sh
