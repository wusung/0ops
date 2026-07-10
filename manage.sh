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
cmd_lint_compose()    { podman compose config -q; }
cmd_lint_docker()     { hadolint src/cmd/*/Dockerfile src/migrations/Dockerfile; }
cmd_lint_go()         { ( cd src && golangci-lint run ); }
cmd_lint_prom_rules() { bash tasks/lint-prom-rules.sh; }

cmd_test()          { go -C src test ./...; }
cmd_contract_test() { go -C src test ./internal/server ./internal/cli ./internal/mcp/server; }
cmd_tidy()          { go -C src mod tidy; }

# --- end-to-end acceptance ---
# create_app preview → confirm → callback → public URL probe（CLI 互動式 / CLI --yes / MCP / public URL 四路徑）。
# 對齊 docs/features/create-app-flow/spec.md § 12「End-to-end happy path」。E2E_MODE=local|staging|production。
cmd_e2e_create_app()    { bash tasks/e2e-create-app.sh "$@"; }
# local file:// repo → pack build → registry push → live deploy 端到端（dev compose 必須先 healthy）。
# 對齊 docs/features/dev-environment/local-file-repo.md § 9.3。
cmd_e2e_local_build()   { bash tasks/local-build-e2e.sh; }
# upload-source ingestion → preview → confirm → JWT-signed archive fetch（compose 必須先 healthy）。
# 對齊 ADR-0013 / docs/features/app-source-ingestion/spec.md § 22；dev mode 不觸發實際 GHA workflow。
cmd_e2e_source_upload() { bash tasks/e2e-source-upload.sh; }
# SSO/OIDC (M9.5) 登入 dance + 集中撤權端到端（base + compose.e2e.yaml mock IdP overlay）。
# 對齊 docs/features/sso-saml/release/2026-06-30-oidc-login-and-e2e.md § 5；E2E_SSO_DOWN=1 收尾拆棧。
cmd_e2e_sso()           { bash tasks/e2e-sso.sh "$@"; }

# --- production bootstrap ---
# docs/features/production-deployment/spec.md — 一鍵 production 部署 / 卸載 / smoke。
cmd_prod_up()     { bash deploy/bootstrap/up.sh "$@"; }
cmd_prod_down()   { bash deploy/bootstrap/down.sh "$@"; }
cmd_prod_smoke()  { bash deploy/bootstrap/smoke.sh "$@"; }
cmd_prod_verify() { bash deploy/bootstrap/wait-for-sync.sh && bash deploy/bootstrap/smoke.sh; }
# docs/runbooks/production-oauth-setup.md — 互動式 OAuth App 註冊與驗證。
cmd_prod_setup_oauth()  { bash deploy/bootstrap/setup-oauth-app.sh "$@"; }
cmd_prod_verify_oauth() { bash deploy/bootstrap/verify-oauth.sh "$@"; }
# docs/features/self-hosted-runner/spec.md — production GHA self-hosted runner。
cmd_prod_install_runner()  { bash deploy/runner/install-runner.sh "$@"; }
cmd_prod_runner_status()   { bash deploy/runner/status.sh "$@"; }
cmd_prod_runner_validate() { bash tasks/m6-q1-runner-validate.sh "$@"; }
# docs/runbooks/production-acceptance.md — 一條指令串完所有 prod-* 達 HTTP 200。
cmd_prod_bootstrap_all() { bash deploy/bootstrap/all.sh "$@"; }

# --- ops drills ---
# 本機 Postgres PITR drill（podman compose 迷你 main → staging）；每次 chart 變更應跑一次。
# 對齊 docs/runbooks/postgres-restore-test.md。
cmd_pitr_drill() { bash deploy/postgres/scripts/pitr-drill.sh; }

# --- dev helpers ---
cmd_dev_example_init()   { bash examples/node-demo/bootstrap.sh; }
cmd_dev_create_example() { cmd_dev_example_init && bash tasks/local-build-e2e.sh; }
# 把 rootless podman socket 改 0666 讓 pack lifecycle container 可讀；host 重啟後跑一次。
# OPS_ENV=production 時拒絕執行。對齊 docs/features/dev-environment/local-file-repo.md § 14。
cmd_podman_socket_loosen() {
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

# --- marketing (build-in-public content lane) ---
cmd_mkt_next()    { bash tasks/mkt/next.sh "$@"; }
cmd_mkt_verify()  { bash tasks/mkt/verify.sh "$@"; }
cmd_mkt_publish() { bash tasks/mkt/publish.sh "$@"; }

# --- marketing landing site (docs/features/marketing-landing-site/spec.md) ---
cmd_mkt_site_build() { ( cd src && go run ./cmd/devtools/mkt-site "$@" ); }
cmd_mkt_site_serve() {
  local dir="docs/marketing/site/dist" port="${MKT_SITE_PORT:-8080}"
  [ -d "$dir" ] || die "no dist/ — run './manage.sh mkt-site-build' first"
  echo "serving $dir at http://localhost:$port (Ctrl-C to stop)"
  python3 -m http.server "$port" --directory "$dir"
}
cmd_mkt_site_deploy() { bash tasks/mkt/deploy-site.sh "$@"; }

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
  lint-prom-rules              用 podman + prom/prometheus 跑 promtool check rules
  test                         go test ./... (src/)
  contract-test                backend / cli / mcp contract path tests
  tidy                         go mod tidy (src/)

sqlc:
  sqlc                         產生 sqlc 程式碼

end-to-end acceptance (compose 必須先 healthy):
  e2e-create-app               create_app preview→confirm→callback→public URL probe
                               (E2E_MODE=local|staging|production；spec.md § 12)
  e2e-local-build              local file:// repo → pack build → registry → live deploy
  e2e-source-upload            upload-source ingestion → preview → confirm → JWT archive fetch
                               (ADR-0013；dev mode 不觸發實際 GHA workflow)
  e2e-sso                      SSO/OIDC 登入 dance + 集中撤權端到端 (mock IdP overlay)
                               (M9.5；E2E_SSO_DOWN=1 收尾拆棧)

production bootstrap (spec docs/features/production-deployment/spec.md):
  prod-up                      一鍵裝 K3s + ArgoCD + sealed-secrets + 套用 root app + smoke
                               (需 deploy/bootstrap/.env.prod；冪等)
  prod-down                    刪 ArgoCD root app + system-0ops / cloudflare-tunnel ns
                               (postgres ns + PVC 保留)
  prod-verify                  等 ArgoCD 全 Synced+Healthy + 跑 smoke
  prod-smoke                   單跑 HTTP 200 smoke（api / demo host）
  prod-setup-oauth             互動式註冊 production GitHub OAuth App + 寫入 .env.prod
                               (runbook docs/runbooks/production-oauth-setup.md)
  prod-verify-oauth            驗 GITHUB_OAUTH_CLIENT_ID 在 GitHub 端有效 + Device Flow 已啟用
  prod-install-runner          ssh 到 PROD_HOST 裝 GHA self-hosted runner + pack + podman
                               (spec docs/features/self-hosted-runner/spec.md)
  prod-runner-status           印 runner online 狀態 + 最近 N runs + vars.GHA_RUNNER_LABEL
  prod-runner-validate         端到端驗 self-hosted runner 鏈路（runner + vars + workflow + callback）
  prod-bootstrap-all           一條指令串完 setup-oauth → verify-oauth → up → smoke →
                               install-runner → runner-validate → e2e production，到 HTTP 200
                               (runbook docs/runbooks/production-acceptance.md)
                               flags: --skip-runner / --skip-e2e / --resume-from=N

ops drills:
  pitr-drill                   本機 Postgres PITR drill；每次 chart 變更應跑一次

dev helpers:
  dev-example-init             初始化 examples/node-demo 為 git repo
  dev-create-example           bootstrap node-demo 後跑 create_app at file:// → live
  podman-socket-loosen         把 rootless podman socket 改 0666 讓 pack lifecycle 可讀
                               (host 重啟後跑一次；OPS_ENV=production 拒絕執行)

task runner:
  task-list                    列出所有 task 與狀態
  task-next                    顯示下一個可執行 task
  task-run-all                 依序跑完所有 Pending task (中斷後再呼叫即 resume)
  task-run <ID>                跑指定 task (例: ./manage.sh task-run M2.5)
  task-rerun <ID>              強制重跑指定 task
  task-runner-test             跑 task runner 自身的 smoke 測試

marketing (build-in-public content lane; docs/features/build-in-public-engine/spec.md):
  mkt-next <cadence>           節奏產生器：讀 sources-ledger 挑下一個 available 素材、
                               註冊一筆 content task (cadence: weekly|monthly|quarterly)
  mkt-verify <post-path>       客觀內容驗收 gate G1–G6 (雙語/模板/工程錨點/邊界/帳本/長度)
  mkt-publish <queue.yaml>     dry-run 散佈器：印 FB 粉專 / Threads payload；不連網
                               (--publish 本輪被 guard，真發屬 MKT.2)

marketing landing site (docs/features/marketing-landing-site/spec.md):
  mkt-site-build [flags]       渲染 posts + landing → docs/marketing/site/dist/
                               (flags 透傳 renderer；例: -base-url https://0ops.sh)
  mkt-site-serve               本地靜態預覽 dist/ (MKT_SITE_PORT，預設 8080)
  mkt-site-deploy              部署 dry-run：印 wrangler pages deploy 指令；不連網
                               (--deploy 被 guard，需 CF_API_TOKEN + MKT_SITE_DEPLOY_CONFIRMED=1 → MKT.4)

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

    e2e-create-app)       cmd_e2e_create_app "$@" ;;
    e2e-local-build)      cmd_e2e_local_build "$@" ;;
    e2e-source-upload)    cmd_e2e_source_upload "$@" ;;
    e2e-sso)              cmd_e2e_sso "$@" ;;

    prod-up)              cmd_prod_up "$@" ;;
    prod-down)            cmd_prod_down "$@" ;;
    prod-verify)          cmd_prod_verify "$@" ;;
    prod-smoke)           cmd_prod_smoke "$@" ;;
    prod-setup-oauth)     cmd_prod_setup_oauth "$@" ;;
    prod-verify-oauth)    cmd_prod_verify_oauth "$@" ;;
    prod-install-runner)  cmd_prod_install_runner "$@" ;;
    prod-runner-status)   cmd_prod_runner_status "$@" ;;
    prod-runner-validate) cmd_prod_runner_validate "$@" ;;
    prod-bootstrap-all)   cmd_prod_bootstrap_all "$@" ;;

    pitr-drill)           cmd_pitr_drill "$@" ;;

    dev-example-init)     cmd_dev_example_init "$@" ;;
    dev-create-example)   cmd_dev_create_example "$@" ;;
    podman-socket-loosen) cmd_podman_socket_loosen "$@" ;;

    task-list)        cmd_task_list "$@" ;;
    task-next)        cmd_task_next "$@" ;;
    task-run-all)     cmd_task_run_all "$@" ;;
    task-run)         cmd_task_run "$@" ;;
    task-rerun)       cmd_task_rerun "$@" ;;
    task-runner-test) cmd_task_runner_test "$@" ;;

    mkt-next)     cmd_mkt_next "$@" ;;
    mkt-verify)   cmd_mkt_verify "$@" ;;
    mkt-publish)  cmd_mkt_publish "$@" ;;

    mkt-site-build)  cmd_mkt_site_build "$@" ;;
    mkt-site-serve)  cmd_mkt_site_serve "$@" ;;
    mkt-site-deploy) cmd_mkt_site_deploy "$@" ;;

    *)
      echo "unknown command: $cmd" >&2
      echo "run './manage.sh help' for usage" >&2
      exit 2
      ;;
  esac
}

main "$@"
