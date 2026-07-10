# tasks/todo.md

> 進度單一事實來源。歷史已完成項移至 `tasks/todo-archive.md`（凍結 2026-05-21）。
> 規則：進度只在本檔更新；docs/ 不得再新增 checkbox。

## 當前狀態（2026-05-21）

v1 範圍（M0-M6）全部 ship。M7 (Web UI) 為 post-v1，不阻擋 v1 上線。

| Milestone | 狀態 | 摘要 |
|---|---|---|
| M0-M5.6 | Done | 詳見 `tasks/task-status.md` + `tasks/todo-archive.md` |
| M6 app-source-ingestion | Done 2026-05-21 | PR #62-#84 + hotfix #85/#86 + sync #87；e2e 9 步全 PASS |
| M7 Web UI (post-v1) | Pending | Vue 3 + Vite + Tailwind + shadcn-vue；不在 v1 範圍 |

## 活躍 backlog

### M9 Trust（Compliance / Audit / Security）— 來源：`docs/trust-and-compliance/plan.md` § 5.1

> 登記於 task-list/task-status：PR #128（M9.0–M9.6）。文件層全封板：plan + 7 feature spec
> + ADR-0015/0016/0017 + business-plan 餵回（PR #126/#127）。實作層按依賴序逐切片跑 agent loop。

- [x] **M9.0 threat-model（STRIDE 系統威脅模型）** — Done 2026-06-28，PR #126（純文件）
- [x] **M9.1 audit append-only + tamper-evidence + export/verify** — Done 2026-06-29
  （`docs/features/audit-export-and-integrity/spec.md` / ADR-0015；切片化）
  - [x] **slice a**：hash-chain 核心（`chain.go`）+ migration `00013`(schema) + ADR-0015→Accepted
    + spec §4.3 配方對齊 — Done 2026-06-29，**PR #130**；13 單元測試（golden-vector / 0x1F 注入 /
    竄改偵測×8）、migration up/down/up 可逆驗證、CI `test` 綠
  - [x] **slice b**：寫入路徑交易（head-lock + `ON CONFLICT` upsert + hash + UPDATE head）—
    Done 2026-06-29，**PR #133**；5 整合測試（重算 / 跨 team 隔離 / 24-writer 並發無丟失 /
    unicode+大整數 jsonb / 非 canonical UUID）對真 postgres 綠、CI `test` 綠
  - [x] **slice b2（append-only role）** — Done 2026-06-29；migration `00014` 建
    `"0ops_app"`/`"0ops_migrate"`/`"0ops_archive"`（NOLOGIN，無密碼）+ `revoke UPDATE/DELETE`
    on audit_log（parent + 每 partition + archive，動態）+ `audit_chain_head` 保留 SELECT/INSERT/
    UPDATE（hard rule #1/#2）；`db.ConfigFromEnv` 優先讀 `APP_DATABASE_URL`；`CreateMonthlyPartition`
    對新 partition 補 revoke；連線切換落 compose（`provision-app-role` 服務 + `deploy/dev/`）+ .env；
    runbook `docs/runbooks/audit-append-only-role.md`。整合測試：app role 改/刪被拒 + chain_head 可改 +
    新 partition 被拒；migration up/down/up 可逆（Down 走 REASSIGN OWNED 防誤刪 migrate 擁有之表）。
  - [x] **slice c（export API）** — Done 2026-06-29；`GET .../audit/export`（CSV/JSON、串流游標、
    13mo 上限 422、since 必填）+ 新 scope `audit:export`（admin 專屬，與 `audit:read` 正交，hard rule #6）+
    integrity manifest（per-chain genesis/tip/row_count，CSV 走 `X-0ops-Audit-Integrity` header；hard rule #7）；
    export entries 帶 prev/row hash 供離線 verify；`ExportAuditLog`/`ListChainHeads` + handler + CLI `audit export`
    + backendclient。RBAC（缺 scope 403 / viewer 403）+ 全鏈竄改偵測整合測試。
  - [x] **slice d（verify CLI）** — Done 2026-06-29；`audit.VerifyChain` 重算 per-(team,month) 鏈
    （row_hash / linkage / row_count / tip 斷裂偵測，exit 1）；`0ops audit verify` 逐月完整抓取（避免
    部分視窗 false-BREAK）→ `verifyEnvelope` 重算；verify/export 皆不暴露 MCP（hard rule #9）。
- [x] **M9.2 compliance-framework-mapping（PDPA/SOC2 控制對應）** — Done 2026-06-29（控制狀態對齊已交付：A1 HA/PITR/SLO 升已具備 M5.4/M5.5/M2.6+ADR-0008、audit tamper-evidence/append-only/export/verify 升已具備 M9.1+ADR-0015、§3 範例改用 SSO M9.5/supply-chain M9.4；spec → accepted；`docs/0ops-plan-schema.md` 加資料分類+保留錨點）
- [x] **M9.3 security-hardening** — Done 2026-06-29（依 M9.0）
  - 落地：`internal/server/security/{risk,anomaly,policy}.go`（純模組）+ migration `00015`（`preview.risk_level`/`required_phrase` 可逆、COALESCE 讀）
    + confirm 端 typed-confirmation AND 驗證（`deleteapp.Confirm`，fail-closed；不繞過 preview_id，hard rule #1）+ DTO 唯讀欄（hard rule #2）
    + CLI（RISK 標頭 + 輸入 required_phrase）/ MCP（`confirmation_phrase`）typed confirmation + delete_app 全鏈必測（service / handler / db 整合 / cli）。
    §6 anomaly 純評估函式 + `abuse_detected` 常數（無 goroutine、無 geo 訊號）；§7 TTL `ResolveTTL` 純函式（未接簽發路徑）。
    §4 `baseline-matrix.md`；§8 default-deny-all + 跨-ns CI 明確 deferred（歸 k3s-namespace-isolation，hard rule #7）；
    §9 `docs/runbooks/at-rest-encryption-key.md` + `deploy/security/encryption-config.example.yaml`。`./manage.sh test` 綠（db 整合測對 v15 真 postgres 跑過）。
  - **已核准 v1 scope（直接執行、勿再停下問範圍）**：尊重 spec 的 deferred/open 邊界與 hard rule #4/#5/#7、§12。
    - **§5 高風險差異化確認 — 完整落地（本 task 主體）**：`security/risk.go` 純函式（risk_level 目錄）+ migration 加
      `preview.risk_level`/`required_phrase` 欄 + confirm 端 typed-confirmation AND 驗證（**不繞過既有 preview/confirm 後端強制**）+ DTO 唯讀欄 + CLI/MCP typed confirmation + 高風險動作必測。
    - **§6 token anomaly — 僅純模組**：`security/anomaly.go` 評估純函式 + 反應政策 + `abuse_detected` audit action 常數 + 單元測試（餵訊號→斷言 emit）。**不建偵測 goroutine**（歸 rate-limit-and-abuse，deferred；hard rule #5）。
    - **§7 TTL team policy — 僅純函式**：`security/policy.go` `ResolveTTL = min(req, teamCap, globalMax)` + 全域常數 + 單元測試。**不加 migration、不改簽發路徑**（team_security_policy schema 屬 §12 open，待 auth-and-rbac）。
    - **§4/§8/§9 — 文件**：`baseline-matrix.md`（盤點，審計可出示）、§8 default-deny-all manifest 與跨-ns CI 列明確 deferred（歸 k3s-namespace-isolation + 需 CI cluster）、§9 at-rest 金鑰 runbook。
    - **誠實**：spec §11 三條 end-to-end（TTL 簽發收斂 / anomaly→abuse_detected / 跨 ns 拒）降級為函式級單元測試 + 文件標 deferred 條件，不灌水講成已具備（hard rule #4）。
- [x] **M9.4 supply-chain-security** — Done 2026-06-29（依 M2.2/M2.3；ADR-0017）
  - 落地（可測，`./manage.sh test` 綠）：migration `00016`（`deploy_run.image_digest text` 可逆、COALESCE 寫）+ callback handler 解析 `image_digest`→`DeployCallbackParams.ImageDigest`（`normalizeImageDigest` 驗 sha256、malformed→nil）+ httptest（fakeStore 捕參）+ DB 整合測（真 v16 postgres 斷言落地與 nil-preserve）；gitops `DigestPinnedImageRef` 純函式 + `RenderInput.ImageDigest` → render 產 `<repo>@sha256:<digest>`（hard rule #6，render 測斷言不可變 digest、不回退 mutable tag）；`commit_authz.go` `ClassifyCommit` 純函式（signer/author≠ops-bot / 無 deploy_run_id → unauthorized）+ `gitops_unauthorized_commit` audit action 常數 + 表測。
  - config + 文件（deferred-validation，needs-CI/cluster，不由 `manage.sh test` 驗）：`ci.yml` govulncheck gate（順帶 bump `golang.org/x/net` v0.54→v0.55 修 GO-2026-5026 可達漏洞，否則 gate 立即 fail）；`release.yml` images job 加 syft(SBOM CycloneDX)/Trivy(`exit-code=1` enforce)/SLSA L3(`attest-build-provenance`)/cosign keyless sign+attest + `.goreleaser.yaml` sbom+signs（CLI binary）；`deploy/workflows/deploy-app.yml` 解 digest + syft + cosign sign + SLSA L2 provenance + callback 補帶 `image_digest`；`render-and-push-gitops.sh` 改 `@sha256` digest pin（缺 digest fail-closed）；`deploy/gitops/argocd/apps/policy-controller.yaml` + `deploy/gitops/policy/cluster-image-policy.yaml`（`mode: warn`）；`docs/runbooks/signing-key-rotation.md`（雙窗輪替）。
  - 邊界：未改 `threat-model` SC3 / `gitops-render-and-argocd` §4.3（§12.1 列合入前置依賴，本 task 不動）；未動 ADR-0017 狀態（spec 仍 draft）。誠實：app image L2（self-hosted runner 非 ephemeral，殘餘風險）、admission 首輪 warn、非 backend commit 僅偵測純函式不掛 reconciler，皆標 deferred。
  - **已核准 v1 scope（直接執行、勿再停下問範圍）**：尊重 spec §2.2/§13 hard rule #1–#10；CI/簽章/admission 類交付 config+文件並明標 deferred-validation，僅 backend Go 為可驗證 code。migration 取下一未用號（repo 已到 00014，起 `00015`）。
    - **§6 callback 補帶 `image_digest` → `deploy_run.image_digest` — 完整落地（可測）**：migration 加 `deploy_run.image_digest text` + callback handler 解析 payload 寫入 + DTO；httptest+DB 測斷言 digest 落地。
    - **§4.4/§12.1 gitops render 改 `@sha256` digest pin — 完整落地（可測）**：`src/internal/server/services/gitops/*` 之 ImageRef 由 commit_sha tag 改 `<repo>@sha256:<digest>`；render 測斷言不可變 digest（hard rule #6）。
    - **§7.2 非 backend commit 偵測 — 僅純函式**：commit 合法性判定純函式（signer/author≠ops-bot / 無 deploy_run_id → unauthorized）+ `gitops_unauthorized_commit` audit action 常數 + 單元測試。**不掛 reconciler goroutine/webhook**（deferred）。
    - **§4/§5 SBOM·govulncheck·Trivy·SLSA·cosign·admission policy — config+文件（deferred-validation, needs-CI/cluster）**：改 `.github/workflows/*`、`deploy/workflows/deploy-app.yml`、`deploy/gitops/argocd/apps/policy-controller.yaml`(mode warn)、`docs/runbooks/signing-key-rotation.md`；明標 `manage.sh test` 不驗（hard rule #2/#3/#4/#5/#9）。
    - **§12.1 回填 threat-model SC3 / gitops-render §4.3 — 列合入前置，本 task 不改該兩檔**。
    - **可驗證性**：僅 gitops digest pin + callback image_digest + 偵測純函式可被 `manage.sh test` 證明；其餘 CI/cluster config 標 deferred-validation。expected-path 命中 `deploy/**` + gitops。
- [x] **M9.5 sso-saml（OIDC + 集中撤權）** — Done 2026-06-29（merged PR #141；task-status.md 已 Done）
  - **e2e 補完 2026-06-30**（branch `feat/m9.5-sso-e2e`）：補 `GET .../sso/{slug}/authorize` OIDC 登入入口
    （state+PKCE 簽發 + 302→IdP；`authorize.go` + 2 handler 測）+ in-repo mock IdP（`cmd/devtools/mock-idp`）
    + `compose.e2e.yaml` overlay + `tasks/e2e-sso.sh`（`./manage.sh e2e-sso`）。對真 compose 棧跑通**完整 OIDC
    dance + 集中撤權端到端**（authorize→mock IdP→callback→JIT→bearer→GET /apps 200→deprovision→401 +
    DB/audit 斷言全綠）。設計：`docs/features/sso-saml/release/2026-06-30-oidc-login-and-e2e.md` +
    跨切面標準 `docs/features/e2e-testing/spec.md`；AGENTS.md 加「每 feature 必備 e2e」。
    窄化 deferred：multi-replica HA 之 durable StateStore（spec § 19.2）。
  - **已核准 v1 scope（直接執行、勿再停下問範圍）**：v1 **OIDC-only**；SAML 欄位 nullable 預留、`protocol` CHECK 僅 `'oidc'`（hard rule #10）；不另造平行權限模型，集中撤權靠既有 `CheckMembership`+`cli_token.revoked_at`（hard rule #1/#2/#5）。migration 取下一未用號（`00015+`；與 M9.6 併行須各取不同號）。
    - **§11 schema — 完整落地（可測）**：migration 建 `idp_config`/`idp_domain`/`idp_identity` + `team_membership`(auth_source/deactivated_at) + `cli_token`(auth_source/idp_config_id)；CHECK 落地 + DB 測。
    - **§7.2 集中撤權 deprovision — 完整落地（核心，可測）**：同 tx membership.deactivated_at + 該 user 該 team 全部 cli_token.revoked_at + cache invalidate + audit；httptest+DB 斷言 device→401、PAT→404、一次覆蓋全部（hard rule #5）。
    - **§6 JIT — 完整落地（可測）**：upsert user+membership+idp_identity；role 解析**封頂 admin、不給 owner**（hard rule #3）；mock OIDC callback 測。
    - **§3/§5/§12 OIDC 路徑+端點 — 完整落地（可測）**：oidc/authorize/callback/domain(DNS TXT,resolver 可注入)/config(owner-only)/enforce/backchannel + routers；scope `sso:manage`(owner-only) 入 rbac；RBAC/enumeration(跨 team→404) httptest。
    - **§7.3 SSO token 不 rolling refresh — 完整落地（可測）**；**§9 audit + §13 CLI（`0ops sso status/deprovision`）— 完整落地（可測）**：新 audit action（IdP-initiated source=system/actor NULL）+ redactor 蓋 secret/token。
    - **§3.2 SAML — 僅 schema 預留不實作**；**§16 open（SCIM/多 IdP/Web UI/service account/break-glass/group 降級/批次 revoke）— deferred**。
    - **可驗證性**：幾乎全 backend Go+DB+CLI，絕大多數可被 `manage.sh test` 證明（mock IdP/httptest）；無 CI/cluster deferred。expected-path 命中 `src/internal/server/auth/sso/**` + `src/migrations/**` + `src/internal/cli/**`。
- [x] **M9.6 audit-event-notification（outbox webhook）** — Done 2026-06-29（依 M9.1）
  - **已核准 v1 scope（直接執行、勿再停下問範圍）**：通知唯一源 `audit_log`，transactional outbox 同 tx enqueue、fire-and-retry 非阻塞（hard rule #1/#3/#4/#5）；v1 generic webhook，不做原生 SIEM、不開 MCP write tool。migration 取下一未用號（`00015+`；與 M9.5 各取不同號）。
    - **§4 schema — 完整落地（可測）**：migration 建 `webhook_subscription` + `webhook_delivery`（月 partition、dedup unique）+ DB 測。
    - **§7.1 outbox enqueue — 完整落地（核心，可測）**：與 `audit.Log()` 同 tx 比訂閱 INSERT delivery（ON CONFLICT DO NOTHING）；DB 測斷言 audit 成功⇒delivery 落地、rollback⇒一併、enqueue panic 以 defer-recover 隔離不影響 audit commit。
    - **§7.2–7.4 dispatcher — 完整落地（可測）**：`FOR UPDATE SKIP LOCKED` poll + 指數退避+jitter + max_attempts drop + 連續失敗熔斷（寫 `webhook_subscription_disabled` audit）；httptest mock receiver 斷言狀態機。
    - **§6 payload·sign·SSRF — 完整落地（純函式，可測）**：白名單 redact（無 args/result/secret/token）、HMAC over `ts.body` 三 header、https-only + 拒私網/loopback/metadata；單元測試簽章/redact/SSRF→422。
    - **§5/§10/§3/§7.6 catalog+RBAC+router+redeliver — 完整落地（可測）**：action→event 映射、owner/admin + scope `webhook:read|write`(走既有 preview-confirm)、DTO；投遞不入 audit、config/熔斷/redeliver 入 audit。
    - **§8 簽章金鑰 — 部分（at-rest deferred）**：≥32B、write-only reveal、secret_ref + interface；**DB at-rest envelope 加密依賴 secrets-management（repo 尚無本體）→ deferred**。**§9 retention drop / §11 native SIEM v3 / §16 MCP write tool — deferred**。
    - **可驗證性**：幾乎全 backend Go+DB，絕大多數可被 `manage.sh test` 證明；唯 §8 at-rest 加密接點 + retention 排程 deferred。expected-path 命中 `src/internal/server/services/audit/notify/**` + `src/migrations/**`。

### M6 follow-up（來源：`docs/features/app-source-ingestion/spec.md` § 16-17）

- [ ] **Q1 — production CI workflow 驗證**（waiting on user-side resources）
  - 目標：`deploy/workflows/deploy-app-from-upload.yml` 對 self-hosted runner 在 production GHA 跑通；JWT fetch 路徑端到端驗
  - 工程封裝完成 2026-06-06：`docs/features/self-hosted-runner/spec.md` + workflow opt-in
    (`runs-on: ${{ vars.GHA_RUNNER_LABEL || 'ubuntu-latest' }}`) + `deploy/runner/`（install-runner.sh、
    status.sh、systemd unit template、values.yaml）+ `manage.sh prod-{install-runner,runner-status,
    runner-validate}` + `tasks/m6-q1-runner-validate.sh` + `docs/runbooks/gha-self-hosted-runner.md`
  - 剩下動工：user 端
    1. `prod-up` 完成、`api.<domain>/health` 200（已工程封裝；user 端外部資源到位後）
    2. `./manage.sh prod-install-runner`（會自動安裝 actions-runner + pack + podman、註冊 + systemd）
    3. `gh variable set GHA_RUNNER_LABEL --repo wusung/0ops --body 0ops-builder`
    4. `./manage.sh prod-runner-validate` 全綠
- [ ] **Q3 — OCI artifact registry ADR-0014**
  - 目標：寫 ADR 評估「OCI artifact 取代本地 ingest tree」之 trade-off
  - 動工條件：v2 多 region / artifact promotion 需求浮現；v1 不採
- [x] **Q6 — `repo_url` github alias 移除（M8）** — Done 2026-06-09
  - 範圍決策（窄刪）：移除 github-via-`repo_url` alias（API/CLI/MCP），刪 server normalize shim，
    MCP `create_app_preview` 補 `source` 欄位。github 一律走 `source`；`repo_url` 僅留 ADR-0012
    dev `file://`（spec § 2.2 不改動，非「完全移除 repo_url」）。
  - 落地：`internal/server/apps.go`（reject github repo_url）、`createapp/service.go`（service 層同步）、
    `internal/cli/root.go`（`--repo-url github` 硬錯導向 `--source`）、`internal/mcp/server/server.go`
    （`source` 欄位 + 透傳）、`dto/apps.go`（comment）。測試全綠（36 套件）。文件：spec § 16/§ 17 +
    release migration doc「M8 更新」段。
  - 殘留（非本次範圍）：dev `file://` 之最終移除須待 ADR-0012 supersede，屬獨立決策。

### v1 收尾殘留（不阻擋 ship）

- [ ] **`nextdemo.jesontech.com` 真實外部 HTTP 200**（waiting on user-side resources）
  - 來源：M2 驗收基準（`tasks/todo-archive.md` § 驗收基準）
  - 工程封裝**全部完成**（2026-06-06，PR #107 / #108 / #109 / #110 + 本次）：
    - 部署層：`docs/features/production-deployment/spec.md` + `deploy/bootstrap/` + `deploy/gitops/argocd/` + server ingress/config
    - OAuth：`./manage.sh prod-setup-oauth` / `prod-verify-oauth` + `docs/runbooks/production-oauth-setup.md`
    - End-user：`scripts/install.sh` + `0ops mcp setup <host>` + `docs/quickstart.md`
    - Self-hosted runner：workflow opt-in + `./manage.sh prod-install-runner` + `prod-runner-validate`
    - One-shot：`./manage.sh prod-bootstrap-all` + `docs/runbooks/production-acceptance.md`
    - Runbook：`winshare-route-failure.md` § 2-5 全填
  - 剩下純 user 動作：
    1. 備齊外部資源：Cloudflare zone + wildcard CNAME + tunnel token + K3s host + `kubeseal`/`gh`/`gh auth login`
    2. `cp deploy/bootstrap/env.example deploy/bootstrap/.env.prod`，填值
    3. `./manage.sh prod-bootstrap-all`
  - 驗收：`docs/runbooks/production-acceptance.md` § 9 三條 curl 全 200
- [x] **trace_id 全鏈路驗證**
  - 結果：C1（middleware ctx 注入）/ C2（preview.trace_id 欄位）/ C3（callback handler 補 audit.Log）三個 fix + redeploy/create-app/delete-app confirm 都改讀 `audit.TraceIDFromContext` + e2e composition test 已 ship。
  - 既已驗：HTTP header `X-Trace-ID` → middleware ctx → preview.trace_id → deploy_run.trace_id → workflow_dispatch payload trace_id → callback payload trace_id → audit_log.trace_id 同一 trace 串到底。
  - 範圍排除（follow-up）：M1 `reconciliation_job.trace_id` 欄位、M2 slog `ContextHandler` 自動注入、`requestTrace` middleware 抽到 shared package、`apps.go:554` slog `*string` 列印 ptr 位址而非值（pre-existing bug）。
- [x] **runbook winshare 細節補完** — 2026-06-06，隨 `feat/production-deployment` PR 收。§ 6 「待落地」段移除；§ 2-5 已補 kubectl / cloudflared / argocd 具體指令。

### Bug fixes / hardening（2026-06-09，end-user UX 驗證連帶）

> 起點：真實在 Claude Code 內用 `0ops onboard` + MCP `delete_app` 驗證 end-user UX 時連環暴露。
> 全部對 dev compose backend 端到端驗證，非僅單元測試。

- [x] **delete_app 永遠卡 `deleting`（P0）** — PR #117 + #118，2026-06-09
  - Root cause：`cmd/server/main.go` 給 reconciler 傳空 `HandlerRegistry`，`cleanup_residue`
    job 撞 `unknown job kind` → 8 次 retry 後 `failed_permanently` → app row 永遠 `deleting`。
    `deleteapp.HandleResidue` 早寫好且有測試，只是從沒接上 M5.3 runner。
  - **#117**（修因）：`deleteapp.ResidueJobKind` 常數 + `Service.ResidueHandler()` adapter +
    `server.RegisterReconcilerHandlers` 可測 wiring；`cmd/server` 改呼叫它而非傳空 registry。
    紅綠驗證 + live 驗證（`hello` app 自動收斂消失）。
  - **#118**（修果）：`0ops admin retry-delete --team-slug --app-slug` + `db.RetryStuckDelete`
    （驗 app 仍 `deleting`，re-enqueue fresh job）+ `docs/runbooks/delete-app-residue.md`
    （spec § 6.3 標「待 M5 撰寫」那份）。清掉卡 10 天的 node-demo / node-demo-2（live 驗證收斂）。
  - 連帶修復（同期暴露）：
    - **#113** `CreateCLIToken` 漏 `expires_at`（migration 00003 NOT NULL）→ device flow login 在套了
      00003 的 DB 會 fail；verification 跑 `manage.sh test` 才暴露。
    - **#112** host-side DB test DSN translation（`@db:5432` → `@127.0.0.1:15432`）讓 `manage.sh test`
      在 host 端能跑 DB integration（先前全 SKIP）。
    - **#114** CI 加 postgres service + migrations，讓 `internal/server/db` smoke tests 在 GHA 真跑
      （root cause：CI 從沒設 DATABASE_URL → 上述 schema-vs-query 不一致沒被攔下）。

### end-user onboarding（2026-06-08/09）

- [x] **一條 curl install + login + AI CLI 接線** — PR #115，2026-06-08
  - `scripts/install.sh` 設 `OPS_HOST` 後自動跑 `0ops onboard`（device flow login + 偵測 claude/codex
    + 寫 MCP config）。`0ops mcp setup <host>` + `0ops onboard <host>` 子命令。
  - 端到端驗證：v0.1.2 release binary `curl|sh` → onboard → MCP `tools/list` 24 tools →
    `tools/call list_apps` 回真實資料。

### 治理 / 商業（文件層 backlog）

> 不是工程任務；user / founder 決策範疇。

- [ ] 公司法律主體
- [ ] 領投人選與時程
- [ ] Open source 範圍決策（v1 全閉源 vs v1 OSS core）
- [ ] Managed cloud 上線時程（建議與 v2 Web UI 同步）
- [ ] AI CLI 廠商合作 outreach 順序
- [ ] Repo 主機位置最終定案（自建 vs GitHub org）
- [ ] Copilot CLI / Codex CLI 與官方 Go SDK 相容性矩陣（v1 起手時驗證）
- [ ] Backend SSE → MCP streaming 評估（官方 Go SDK 若不足則分頁拉取）
- [ ] 啟動 build-in-public 行銷引擎 go/no-go（每週決策 / 每月失敗 / 每季路徑；gated on §九 團隊 credibility 與 design partner 時機；引擎 bootstrap 見 task `MKT.0`。決策更新：出刊走既有 task-runner loop（`mkt-next` 觸發 → `task-run` 產出 → `mkt-verify` gate），非人工編輯日曆；排程自動化屬 MKT.2 且 gated）

### MKT.0 — Build-in-public engine bootstrap
- [ ] `docs/marketing/` scaffold：README、sources-ledger、editorial-calendar、published-ledger、三模板、posts/queue（見 plan Task 1）
- [ ] `tasks/mkt/{lib,verify,next,publish}.sh` + `tasks/mkt/test/`，`./manage.sh mkt-next/mkt-verify/mkt-publish` 接線（plan Task 2–6）
- [ ] verify gate G1–G6 有測試佐證（雙語 / 模板結構 / 工程錨點 / 邊界 / 帳本 / Threads 長度）
- [ ] First proof：`./manage.sh mkt-next weekly` → `task-run MKT.W1` 由 loop 產出 ADR-0002 週更長文，`mkt-verify` PASS（plan Task 8）
- [ ] 全程改動只落 `docs/marketing/**`、`tasks/mkt/**`、`manage.sh` 與 registry 三檔

### MKT.1 — Social distribution lane (dry-run)
- [ ] 由 canonical 長文衍生 `docs/marketing/queue/<post>.yaml`（fb + threads 變體，Threads ≤500 字）
- [ ] `./manage.sh mkt-publish <queue>` dry-run 印 fb/threads 兩通道 payload 且不連網
- [ ] `--publish` 被 guard 擋下（需 Meta creds + `MKT_PUBLISH_CONFIRMED=1`，本輪不接真 token）
- [ ] `published-ledger.md` dedup key 冪等：重跑已發項目跳過
- [ ] 明確不含：真實 Meta API 發文、排程器（屬 MKT.2）

### MKT.3 — Social landing site + build-in-public blog
- [ ] `./manage.sh mkt-site-build -base-url https://0ops.sh` 成功產出 `docs/marketing/site/dist/`（S1）
- [ ] 每個 `docs/marketing/posts/*.md` 都有對應 `blog/<slug>.html`，另有 `index.html`、`blog/index.html`（S2）
- [ ] 每篇 blog 頁中英雙語皆非空（S3）
- [ ] dist 內無殘留 `{{…}}` 佔位符 / 死鏈（S4）；無內部代號 `ADR-XXXX` / `file.go:line`（S5）
- [ ] `publish.sh` dry-run 把 `{{canonical_url}}` 換成 `<base>/blog/<slug>`（`MKT_SITE_BASE_URL` 預設 `https://0ops.sh`）
- [ ] `cd src && go test ./cmd/devtools/mkt-site/...` 綠 + `bash tasks/mkt/test/run-tests.sh` 綠（S6）
- [ ] `tasks/mkt/deploy-site.sh` dry-run 印 `wrangler pages deploy` 指令且不連網
- [ ] `dist/` 由 `docs/marketing/site/.gitignore` 排除，不進版控

### MKT.4 — Real Cloudflare Pages deploy (gated)
- [ ] 真實 `wrangler pages deploy`：需 CF Pages 專案 + `CF_API_TOKEN` + `MKT_SITE_DEPLOY_CONFIRMED=1`（本輪不接）
- [ ] 網域 `0ops.sh` 綁定（CF DNS，與產品同帳號）
- [ ] 依賴 MKT.3 完成；屬對外動作，gated

## Governance Guide

> 本區不是進度追蹤，不用 checkbox。

### docs/adr-reading-strategy.md 套用要點

- 修改涉及的模組或概念是否已在 ADR 明確提及
- 修改是否違反 TL;DR 的前三項決策；若無明確提及，改為 **Read**
- Context 中提及的問題是否仍然適用
- Decision 中的選項取捨是否影響本次修改
- Consequences 中列舉的限制是否與本次修改衝突；若超出預期，改為 **Deep**
- Consequences 中列舉的長期影響是否已納入本次修改評估
- Revisit Triggers 中是否有會被本次修改觸發的條件
- Open Questions 是否暗示未來不確定性會影響本次設計
- 若需變更已決策項，應新增 ADR，而非直接修改既有決策

### 執行順序

- 識別相關 ADR
- 執行讀取
- 記錄發現
- 發現違反時停止擴寫實作，先回到 ADR decision / consequences

### 交付前檢查

- 涉及新 API 或 schema 時，確認 ADR 深度足夠
- 做架構決策檢查
- 做文件同步檢查
- 做測試完整性檢查

### MKT.W1 — Build-in-public weekly post from 0002-idempotency-and-compensation.md
- [ ] 依 `docs/features/build-in-public-engine/spec.md` §4 由 docs/adrs/0002-idempotency-and-compensation.md 產出 weekly 中英雙語 canonical 長文至 `docs/marketing/posts/`
- [ ] front-matter 含 `cadence: weekly`、`source: docs/adrs/0002-idempotency-and-compensation.md`
- [ ] 通過 `./manage.sh mkt-verify <post>`（G1–G6）
- [ ] sources-ledger 標 docs/adrs/0002-idempotency-and-compensation.md consumed；editorial-calendar 加列

### MKT.W2 — Build-in-public weekly post from 0015-audit-log-append-only-and-tamper-evidence.md
- [ ] 讀 `docs/marketing/WRITING-PRINCIPLES.md`（對外推廣、用戶視角、零內部代號、含 CTA），依 `templates/weekly-promo.md` 由種子 docs/adrs/0015-audit-log-append-only-and-tamper-evidence.md 產出 weekly 中英雙語**推廣文案**至 `docs/marketing/posts/`
- [ ] front-matter 含 `cadence: weekly`、`source: docs/adrs/0015-audit-log-append-only-and-tamper-evidence.md`
- [ ] 通過 `./manage.sh mkt-verify <post>`（G1–G6；G3 擋內部代號並要求 CTA）
- [ ] sources-ledger 標 docs/adrs/0015-audit-log-append-only-and-tamper-evidence.md consumed；editorial-calendar 加列
