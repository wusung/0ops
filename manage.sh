#!/usr/bin/env bash
# 0ops manage.sh — dev / build / test / lint / e2e 統一入口（取代 Makefile）。
#
# 用法：
#   ./manage.sh <command> [args...]
#   ./manage.sh help
#
# 設計準則：
# - 對外 command 名稱與舊 Makefile target 一致，方便文件/腳本逐步搬遷。
# - .env 若存在會被 auto-export（與 Makefile -include + export 等價）。
# - Go 相關指令走 `go -C src ...` 或 `cd src && ...`（src/ 為 module root）。
# - 容器引擎固定 podman + podman compose v2；禁用 docker / podman-compose v1。

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

# 載入 .env（dev 用）；production 不存在 .env，set -a 不影響。
if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . .env
  set +a
fi

VERSION="${VERSION:-dev}"
LDFLAGS="-s -w -X github.com/winshare/zeroops/internal/shared.Version=${VERSION}"
SQLC_IMAGE="${SQLC_IMAGE:-docker.io/sqlc/sqlc:1.31.1}"

# ----- command groups -----

# --- dev stack ---
cmd_dev()       { podman compose up -d; }
cmd_dev_down()  { podman compose down; }
cmd_dev_clean() { podman compose down -v; }
cmd_dev_logs()  { podman compose logs -f server; }
cmd_dev_shell() { podman compose exec server sh; }

# --- migrations ---
cmd_migrate()      { podman compose run --rm migrate "$DATABASE_URL" up; }
cmd_migrate_down() { podman compose run --rm migrate "$DATABASE_URL" down; }
cmd_migrate_lint() { go -C src test ./internal/server/db/migrationlint/...; }

# --- build ---
cmd_build_images() {
  podman build --target runtime -f src/cmd/server/Dockerfile -t localhost/0ops-server:runtime --build-arg VERSION="$VERSION" src
  podman build --target runtime -f src/cmd/cli/Dockerfile    -t localhost/0ops-cli:runtime    --build-arg VERSION="$VERSION" src
  podman build --target runtime -f src/cmd/mcp/Dockerfile    -t localhost/0ops-mcp:runtime    --build-arg VERSION="$VERSION" src
  podman build                  -f src/migrations/Dockerfile -t localhost/0ops-migrations:runtime src
}
cmd_build() {
  mkdir -p bin
  ( cd src && CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS" -o ../bin/0ops-server ./cmd/server )
  ( cd src && CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS" -o ../bin/0ops        ./cmd/cli )
  ( cd src && CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS" -o ../bin/0ops-mcp    ./cmd/mcp )
}

# --- lint / test ---
cmd_lint_compose() { podman compose config -q; }
cmd_lint_docker()  { hadolint src/cmd/*/Dockerfile src/migrations/Dockerfile; }
cmd_lint_go()      { ( cd src && golangci-lint run ); }
cmd_lint_prom_rules() { cmd_m2_6_promtool; }

cmd_test()          { go -C src test ./...; }
cmd_contract_test() { go -C src test ./internal/server ./internal/cli ./internal/mcp/server; }
cmd_tidy()          { go -C src mod tidy; }

# --- M2.x ---
cmd_m2_2_e2e_validation() { bash tasks/m2-2-e2e-validation.sh --dev; }
cmd_m2_2_check()          { cmd_lint_go && cmd_test && echo "✓ M2.2 code checks passed"; }
cmd_m2_6_promtool()       { bash tasks/m2-6-promtool-validate.sh; }
cmd_m2_8_e2e_acceptance() { bash tasks/m2-8-e2e-acceptance.sh; }
cmd_m2_8_check()          { cmd_lint_go && cmd_test && echo "✓ M2.8 code checks passed"; }

# --- M5.x ---
cmd_m5_4_pitr_drill() { bash deploy/postgres/scripts/pitr-drill.sh; }

cmd_dev_example_init()  { bash examples/node-demo/bootstrap.sh; }
cmd_dev_create_example() { cmd_dev_example_init && bash tasks/local-build-e2e.sh; }
cmd_m5_6_local_build_e2e() { bash tasks/local-build-e2e.sh; }
cmd_m5_6_podman_socket_loosen() {
  local sock="/run/user/$(id -u)/podman/podman.sock"
  if [ ! -S "$sock" ]; then
    echo "socket not found at $sock — 先跑 systemctl --user start podman.socket" >&2
    exit 1
  fi
  if [ "${OPS_ENV:-}" = "production" ]; then
    echo "refusing to loosen socket perms with OPS_ENV=production" >&2
    exit 1
  fi
  chmod 666 "$sock"
  ls -la "$sock"
  echo "ok — pack lifecycle container 可讀 socket 至下次 podman.socket 重啟"
}

# --- M6 app-source-ingestion (ADR-0013) ---
cmd_m6_source_upload_e2e() { bash tasks/m6-source-upload-e2e.sh; }

# --- sqlc codegen ---
cmd_sqlc() {
  podman run --rm --userns=keep-id -v "$ROOT_DIR/src:/src" -w /src "$SQLC_IMAGE" generate
}

# --- task runner ---
cmd_task_list()    { bash tasks/run/show.sh; }
cmd_task_next()    { bash tasks/run/next.sh; }
cmd_task_run_all() { bash tasks/run/run-all.sh; }
cmd_task_run() {
  local task="${1:-}"
  if [ -z "$task" ]; then
    echo "usage: ./manage.sh task-run <ID>" >&2
    exit 1
  fi
  bash tasks/run/run-one.sh "$task"
}
cmd_task_rerun() {
  local task="${1:-}"
  if [ -z "$task" ]; then
    echo "usage: ./manage.sh task-rerun <ID>" >&2
    exit 1
  fi
  bash tasks/run/run-one.sh --force "$task"
}
cmd_task_runner_test() { bash tasks/run/test/run-tests.sh; }

# ----- help -----

usage() {
  cat <<'EOF'
0ops manager — 開發 / 建置 / 測試 / 驗證統一入口

用法：
  ./manage.sh <command> [args...]
  ./manage.sh help

dev stack:
  dev                          啟動 dev stack (db + migrate + server)
  dev-down                     停止 stack (保留 volume)
  dev-clean                    停止並刪除 volume
  dev-logs                     跟 server log
  dev-shell                    進入 server 容器 (dev stage 有 sh)

migrations:
  migrate                      套用 migration up (idempotent)
  migrate-down                 回滾一格
  migrate-lint                 spec § 10.1 migration 安全閘 (CONCURRENTLY + ADD COLUMN NOT NULL 三步拆分)

build:
  build                        本機 host 編譯三 binary 至 ./bin
  build-images                 三 binary runtime image + migrations image

lint / test:
  lint-compose                 驗證 compose schema
  lint-docker                  驗證 Dockerfile (需安裝 hadolint)
  lint-go                      golangci-lint
  lint-prom-rules              別名：跑 M2.6 promtool 驗證
  test                         go test ./... (src/)
  contract-test                backend / cli / mcp contract path tests
  tidy                         go mod tidy (src/)

sqlc:
  sqlc                         產生 sqlc 程式碼

M2.x:
  m2-2-e2e-validation          M2.2 e2e validation (dev mode)
  m2-2-check                   M2.2 程式檢查 (lint + 測試)
  m2-6-promtool                用 podman + prom/prometheus 跑 promtool check rules
  m2-8-e2e-acceptance          M2.8 端到端驗收 (E2E_MODE=local|staging|production)
  m2-8-check                   M2.8 程式檢查 (lint + 單元/契約測試)

M5.x:
  m5-4-pitr-drill              local PITR drill (spec § 8.3 + § 16 hard rule #5)
  dev-example-init             初始化 examples/node-demo 為 git repo
  dev-create-example           跑一次 create_app at file:// → live (要求 compose healthy)
  m5-6-local-build-e2e         M5.6 端到端驗收腳本 (compose 必須先 healthy)
  m5-6-podman-socket-loosen    把 rootless podman socket 改 0666 讓 pack lifecycle 可讀

M6:
  m6-source-upload-e2e         M6 T22 端到端驗收腳本 (upload 路徑；compose 必須先 healthy)

task runner:
  task-list                    列出所有 task 與狀態
  task-next                    顯示下一個可執行 task
  task-run-all                 依序跑完所有 Pending task (中斷後再呼叫即 resume)
  task-run <ID>                跑指定 task (例: ./manage.sh task-run M2.5)
  task-rerun <ID>              強制重跑指定 task
  task-runner-test             跑 task runner 自身的 smoke 測試

misc:
  help, -h, --help             顯示本說明
EOF
}

# ----- dispatch -----

main() {
  local cmd="${1:-help}"
  shift || true
  case "$cmd" in
    help|-h|--help) usage ;;

    dev)             cmd_dev "$@" ;;
    dev-down)        cmd_dev_down "$@" ;;
    dev-clean)       cmd_dev_clean "$@" ;;
    dev-logs)        cmd_dev_logs "$@" ;;
    dev-shell)       cmd_dev_shell "$@" ;;

    migrate)         cmd_migrate "$@" ;;
    migrate-down)    cmd_migrate_down "$@" ;;
    migrate-lint)    cmd_migrate_lint "$@" ;;

    build)           cmd_build "$@" ;;
    build-images)    cmd_build_images "$@" ;;

    lint-compose)    cmd_lint_compose "$@" ;;
    lint-docker)     cmd_lint_docker "$@" ;;
    lint-go)         cmd_lint_go "$@" ;;
    lint-prom-rules) cmd_lint_prom_rules "$@" ;;

    test)            cmd_test "$@" ;;
    contract-test)   cmd_contract_test "$@" ;;
    tidy)            cmd_tidy "$@" ;;

    sqlc)            cmd_sqlc "$@" ;;

    m2-2-e2e-validation) cmd_m2_2_e2e_validation "$@" ;;
    m2-2-check)          cmd_m2_2_check "$@" ;;
    m2-6-promtool)       cmd_m2_6_promtool "$@" ;;
    m2-8-e2e-acceptance) cmd_m2_8_e2e_acceptance "$@" ;;
    m2-8-check)          cmd_m2_8_check "$@" ;;

    m5-4-pitr-drill)            cmd_m5_4_pitr_drill "$@" ;;
    dev-example-init)           cmd_dev_example_init "$@" ;;
    dev-create-example)         cmd_dev_create_example "$@" ;;
    m5-6-local-build-e2e)       cmd_m5_6_local_build_e2e "$@" ;;
    m5-6-podman-socket-loosen)  cmd_m5_6_podman_socket_loosen "$@" ;;

    m6-source-upload-e2e) cmd_m6_source_upload_e2e "$@" ;;

    task-list)        cmd_task_list "$@" ;;
    task-next)        cmd_task_next "$@" ;;
    task-run-all)     cmd_task_run_all "$@" ;;
    task-run)         cmd_task_run "$@" ;;
    task-rerun)       cmd_task_rerun "$@" ;;
    task-runner-test) cmd_task_runner_test "$@" ;;

    *)
      echo "unknown command: $cmd" >&2
      echo "run './manage.sh help' for usage" >&2
      exit 2
      ;;
  esac
}

main "$@"
