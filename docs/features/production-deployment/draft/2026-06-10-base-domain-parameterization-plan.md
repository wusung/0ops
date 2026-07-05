# Base domain 參數化 + 切換 jesontech.com

> 狀態：Done（2026-06-10 落地）
> 決策（2026-06-10）：以參數化（根因修）取代逐字替換；平台 base domain 唯一事實來源為
> `OPS_DOMAIN_BASE` env（Go 端 default `jesontech.com`）。

## 1. 結論

deploy 層早已預留參數（`deploy/server/templates/configmap.yaml` 之 `OPS_DOMAIN_BASE`，
值來自 helm `config.domainBase`），但 src/ 從未讀取，8 處 Go/template hardcode `winshare.tw`。
本次把 Go 端接上該 env 並切換至 `jesontech.com`；下次換域名 = 改 deploy 參數，零 code change。

## 2. 設計

`internal/shared/runtime`（既有 env 設定 package）新增：

- `DomainBase() string` — 讀 `OPS_DOMAIN_BASE`（trim），空值 default `"jesontech.com"`
- `AppHostname(slug string) string` — `slug + "." + DomainBase()`

不另開 package；wildcard / reserved-suffix / email 等單點衍生由呼叫端自組。

## 3. 使用點改寫（8 處非測試）

| 檔案 | 原值 | 改為 |
|---|---|---|
| `createapp/service.go:241` | `fmt.Sprintf("%s.winshare.tw", slug)` | `opsruntime.AppHostname(slug)` |
| `redeploy/trigger.go:133` | `https://%s.winshare.tw` | `"https://" + opsruntime.AppHostname(...)` |
| `db/apps.go:327` | 同上 fmt | `opsruntime.AppHostname(params.Slug)` |
| `cloudflare/client.go:17` | const `*.winshare.tw` | func：`"*." + DomainBase()` |
| `cloudflare/client.go:123` | fmt | `AppHostname` |
| `domainverify/hostname.go:15` | const `.winshare.tw` | func：`"." + DomainBase()` |
| `gitops/service.go:34` | `ops-bot@winshare.tw` | `"ops-bot@" + DomainBase()` |
| `gitops/templates/ingress.yaml.tmpl:9` | `{{.AppSlug}}.winshare.tw` | `{{.AppSlug}}.{{.DomainBase}}`（render.go data 加欄位）|

## 4. 測試

- `runtime/env_test.go`：default = jesontech.com；`t.Setenv("OPS_DOMAIN_BASE","example.org")` override；AppHostname 組合。
- 既有斷言 `winshare.tw` 的測試（cloudflare/createapp/redeploy/domainverify/gitops/e2e/cli 等）字面改 `jesontech.com`（隨新 default）。
- gitops render 測試驗 ingress host 帶 DomainBase。

## 5. Deploy / scripts / docs 切換

- `deploy/gitops/argocd/apps/server.yaml`：ingress.host=0ops.jesontech.com、publicURL、domainBase
- `deploy/server/values.yaml`、`deploy/bootstrap/env.example`、`setup-oauth-app.sh`
- `deploy/chart/cloudflare-tunnel/Chart.yaml` 描述、`prometheus-alert-rules.yaml`
- `scripts/install.sh`、`tasks/e2e-create-app.sh`
- 操作性 docs（quickstart、production-acceptance、相關 runbook/spec 現行段落）→ jesontech.com
- `tasks/todo.md` 驗收項 `nextdemo.winshare.tw` → `nextdemo.jesontech.com`

## 6. 不在範圍

- `github.com/winshare`（Go module / GitHub org）、`ghcr.io/winshare|wusung`（image org）：組織名非域名，不動。
- 歷史紀錄（`tasks/todo-archive.md`、過往 release 紀錄、lessons）：不重寫歷史。
- feature 目錄名 `winshare-subdomain-and-tunnel` 與 runbook 檔名：保留（重命名屬檔案層 churn，內文現行指令段落會更新）。
- Cloudflare zone 實際建立（jesontech.com zone + wildcard CNAME + tunnel）：user 外部資源，屬 rollout 步驟。
