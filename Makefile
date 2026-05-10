# 0ops 開發 target 收口（spec § 9）
# 容器引擎固定 podman + podman compose v2；禁用 docker / podman-compose v1。

SHELL := /bin/bash

VERSION ?= dev
LDFLAGS := -s -w -X github.com/winshare/zeroops/internal/shared.Version=$(VERSION)

.PHONY: help dev dev-down dev-clean dev-logs dev-shell migrate migrate-down \
        build-images lint-compose lint-docker lint-go test build tidy

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

test: ## go test ./...
	go test ./...

tidy: ## go mod tidy
	go mod tidy
