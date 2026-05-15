## Milestones

| M | 範圍 | 完成標準 |
|---|---|---|
| **M0** | Module scaffold + dev env + ADR 定稿 | 三 binary 都 `go run ./cmd/...` 起得來；`/health` 200；`0ops --version`；MCP server 回應 `initialize`；`golangci-lint run` 全綠；`go test ./...` 通過；**ADR-001（多租戶/RBAC）、ADR-002（idempotency/補償）、ADR-006（observability baseline）已寫定** |
| **M1** | Read-only：API + CLI + MCP + RBAC + observability skeleton | `0ops teams list / use / apps list / get / repo inspect / deploys status / deploys logs / domains list` 通；MCP read tools（`list_teams/list_apps/get_app/inspect_repo/get_deploy_status/tail_logs/list_domains`）在 claude code 端到端跑通；middleware chain（AuthBearer → ResolveTeam → CheckMembership → CheckTokenScope）就位；`/metrics` 暴露 HTTP histogram + request counter（含 `team_bucket`）；trace_id propagation 端到端 |
| **M1.5** | Identity bootstrap + team member provisioning | ✅ 已完成：production 首位 owner 之 bootstrap flow（一次性）可執行且可審計；`members invite/list/remove` 與 `members:manage` scope 上線；管理者建立/邀請成員採 preview/confirm 兩階段；cross-team 保持 `404 team_not_found`；CLI + MCP + backend contract test 通過 |
| **M2** | `create_app` + 兩階段 preview/confirm + idempotency + winshare 子網域 + observability GA + 隔離模型 | nextdemo.winshare.tw 端到端通；CLI 互動式 + `--yes`；MCP 透過 SKILL 約定 LLM 遵守 preview-then-confirm；preview_id 重試冪等驗收；deploy_run 狀態機完整；GHA HMAC callback 上線；Prometheus metrics 含 preview/deploy/cf 指標；SLO dashboard + burn-rate alert 上線；team namespace + ResourceQuota + LimitRange + NetworkPolicy + PSA baseline 上線 |
| **M3** | 客戶自有域名 + DNS verify（24h TTL + extend） + GitHub App install 流程 | 真實 example domain 驗證通過；`domains verify --watch` / `--extend` 通；過期 grace 7 天可重啟；`teams github install/uninstall` CLI/MCP 端到端通 |
| **M4** | Webhook auto redeploy + manual redeploy + replay protection + rate limit | push 觸發 + CLI redeploy 都通；webhook_dedup 表生效；同 delivery_id 重送 200 不重做；preview_consumption_rate 上 dashboard；per-token / per-team rate limit 上線並回 429 + Retry-After |
| **M5** | `delete_app` + audit + reconciler GA + incident table + Postgres HA + DR 演練 | 安全刪除（含資源清理 runbook）；audit_log 含 preview_id + trace_id 鏈路；`audit list` CLI/MCP 通；reconciliation_job 收斂滯留 deploy；MTTR 量測機制就位；`failure_classification` 強制非 null（unknown < 5%）；Postgres replica + WAL archive 就位；PITR 恢復演練通過（RPO 5min/RTO 30min）；backend 升級為 2 replica + leader election |
| **M6** (post-v1) | Web UI | Vue 3 + Vite + Tailwind + shadcn-vue；登入、team 切換、app dashboard、log viewer |

---

## 立即下一步（執行階段）
1. **撰寫 feature spec**（M0–M5 阻擋項，先於程式碼）：以 `docs/features/dev-environment/spec.md` 為格式範本，逐 feature 落地於 `docs/features/{FEATURE}/spec.md`。
   - ADR-0001..0008 已全部定稿，作為各 spec 之不可變前提
   - 待補上游 ADR：migrations image 策略（dev-environment spec §12 待解項）、CLI 套件分發策略、plan tier→capability 矩陣
2. 起 `M0` scaffold：
   - `go mod init github.com/winshare/zeroops`
   - 建立 `cmd/server/main.go`、`cmd/cli/main.go`、`cmd/mcp/main.go`
   - `.golangci.yml`、`.goreleaser.yaml`、`Makefile`、`.dockerignore`、`.env.example`
   - `compose.yaml`（root）起 db + migrate + server；三 binary 各自之 `cmd/{server,cli,mcp}/Dockerfile`；詳見 `docs/features/dev-environment/spec.md`
   - `goose create init sql` 建初始 schema（含 team / team_membership / app / preview / deploy_run / cli_token / webhook_dedup / audit_log / reconciliation_job）
   - server `/health` + `/metrics`；CLI `--version`；MCP 回 `initialize`
3. 寫第一條 read-only chain：backend `GET /v1/teams/{team}/apps` → CLI `apps list` → MCP `list_apps`（經 RBAC middleware）
4. 同步建 `0ops-gitops` 空 repo + ArgoCD ApplicationSet 雛型

---

## TBD（執行前需 user 確認）
- Repo 主機位置（自建 git server、GitHub org、其他）
- **Copilot CLI / Codex CLI 與官方 Go SDK 相容性矩陣**：M0 spike 驗證 tool registry、preview/confirm、streaming fallback
- **Copilot CLI 是否原生支援 MCP**（影響 skill pack 形式：MCP 共用 / 退路 wrap CLI）
- **Codex / Copilot skill metadata 精確格式**（v1 起手時驗證）
- Backend 是否需要 SSE → MCP streaming（官方 Go SDK 若支援不足，則改分頁拉取）
> 已從 TBD 移除（上游已決議）：
> - 專案名稱：`0ops`（agents-guide §2、dev-environment spec）
> - Module path：`github.com/winshare/zeroops`（agents-guide §3.2）
> - K3s 長期定位：stopgap-acceptable（ADR-0004 第 6 點）
> - Go 版本：1.25（dev-environment spec §6.1；M0 scaffold 期由 1.23 上修為 1.25 以符 MCP go-sdk v1.6 之最低版本）
> - DB 存取層：`sqlc + pgx`（agents-guide §3.1）
> - migrations image 策略：自建 multi-stage distroless（ADR-0009）
> - CLI 套件分發：goreleaser + brew tap + go install + 24h 自更新通知（ADR-0010）
> - Plan tier→capability 矩陣：4 tier × 18 維度（ADR-0011）

> 已從 TBD 移除（本次設計補強已決議）：
> - 租戶模型 / RBAC：team 為一階，四角色 + scope 矩陣（ADR-0001）
> - Idempotency 模型：preview_id 兼 key，consumed 後 last_result 回放（ADR-0002）
> - MCP SDK：採官方 `modelcontextprotocol/go-sdk` v1.x（ADR-0003）
> - K3s 角色：v1 stopgap-acceptable + PostgreSQL via kine（ADR-0004）
> - Build pipeline：GHA + HMAC SHA256 callback + 20min ephemeral token（ADR-0005）
> - Observability：M2 必上，含 SLO/SLI、metrics、trace、burn-rate alert（ADR-0006）
> - 客戶自有域名 TLS：Cloudflare for SaaS Custom Hostname；apex 走 ALIAS / ANAME / flattening（ADR-0007）
> - Backend HA：v1 single → M5 K8s Lease + 2 replica；SSE 走 stateless cursor（ADR-0008）
> - Auth：GitHub OAuth device flow + PAT（綁 team + scopes）
> - Preview TTL：10 分鐘
> - Domain verify TTL：24h，可 extend 兩次
> - Webhook replay：webhook_dedup + ±5min timestamp window
