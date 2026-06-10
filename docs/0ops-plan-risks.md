## Risks & open

1. **Buildpack 偵測失敗 fallback**
   - Paketo 已涵蓋 Node/Python/Go/Java/Ruby/.NET；冷僻語言（Rust/Elixir）可能失敗
   - 對策：v1 偵測失敗時 CLI / MCP 提示「請提供 Dockerfile，下版本支援」；v1.1 加 Dockerfile mode

2. **客戶域名 TLS 邊緣終止**
   - 已採 Cloudflare for SaaS Custom Hostname API（見 ADR-0007）；TLS 在 Cloudflare edge 終止，cert 由 Cloudflare 自動簽發，0ops 不持 customer cert material
   - apex domain 需客戶 DNS 供應商支援 CNAME flattening / ALIAS / ANAME；不相容供應商於 24h verification window 內自然失敗
   - 殘餘風險：Cloudflare 服務中斷影響面、Cloudflare for SaaS 配額上限與訂閱費攤提（見 ADR-0007 Revisit Triggers）

3. **MCP 跨 CLI 相容性**
   - 已採官方 `modelcontextprotocol/go-sdk` v1.x（見 ADR-0003）；M0 spike 驗證 claude code / codex / copilot 三家 client 對 tool registry、preview/confirm、streaming 的互通行為；spike 矩陣不通過任一格觸發 fallback 至 `mark3labs/mcp-go`（ADR-0003 Revisit Trigger #1）
   - preview-then-confirm 是 SKILL 層級的約定，三家 LLM 是否一致遵守需測；違反時 backend 仍能阻擋（write tool 沒有 preview_id 直接 4xx）

4. **AI CLI confirm 流程不一致**
   - Claude Code 有 permission UI；codex / copilot 的互動方式不同
   - SKILL.md 必須清楚要求 LLM 呈現 side_effects 並等待明確同意；不能依賴 CLI 自動 prompt
   - 量測：`preview_consumption_rate` 與 `preview_to_confirm_latency` 為產品健康度紅旗指標

5. **GitOps repo 衝突**
   - 多人同時建立 app → git push 並發；用 retry + rebase 處理（最多 5 次）；超過則狀態機進 `compensating`

6. **Preview race**（**已透過設計處理**）
   - confirm 進入 transaction 內 `SELECT ... FOR UPDATE` 鎖 preview row；先決條件在同一 tx 重檢
   - 同 preview_id 重試由 `last_result` 回放，不重做副作用

7. **K3s 單 cluster failure domain**
   - 預設 SQLite datastore 在 namespace + workload 上 100 量級會卡，須切 PostgreSQL backend
   - 缺 backup / DR、namespace quota、Pod Security Admission baseline
   - **待 ADR-004 決議**：K3s 是 v1 stopgap 還是長期決策；v2 是否轉 EKS/GKE
   - v1 必補：etcd backup（每 6h）+ namespace ResourceQuota + PSA baseline

8. **Cloudflare Tunnel 單點**
   - 整個入口流量單通道；tunnel 重啟 / token 失效 / Cloudflare API rate-limit 影響所有 app
   - 對策：tunnel HA 用多 replica connector pool；Cloudflare API call 加退避（`0ops_cloudflare_api_calls_total{outcome=throttled}` 監控）
   - 待技術 spike：connector failover 行為

9. **單一 chi service 可擴展性**
   - goroutines 即可；長 task（DNS polling、reconciler）走獨立 goroutine + ticker
   - v1–M4 單實例；M5 升 2 replica + K8s Lease leader election（見 ADR-0008）；SSE 採 stateless cursor reconnection 任一 pod 接續，preview / reconciliation_job 為 DB-backed 自然 cluster-safe

10. **Cgo 與 cross-compile**
    - `pgx`、`client-go`、`go-github`、`modelcontextprotocol/go-sdk` 均為 pure Go；無 cgo 依賴
    - `goreleaser` 可一鍵產 linux/amd64、linux/arm64、darwin、windows 靜態 binary
    - 編譯時間預期 < 60s，CI 用 `actions/cache` 對 `~/go/pkg/mod` 與 `~/.cache/go-build`

11. **多 region / multi-AZ**（v1 不做）
    - jesontech.com 為台灣 region；latency / failover 在 v1 Non-goals
    - v2 視業務需求評估；先確保 stateless backend + DB 主從可水平擴展

> 已從 risks 升級為「設計章節已解」的項目（不再列為 open）：
> - 多租戶與 slug 唯一性 → 見《Auth & RBAC》、DB schema
> - Idempotency 與副作用補償 → 見「關鍵設計 #3 #4」、《Observability & SLO》Deploy 狀態機
> - Webhook replay → 見《Auth & RBAC》Webhook 安全
> - Build pipeline 可靠性 → 見《Build & deploy》HMAC callback
> - Observability 過晚 → M2 必上，見 Milestones
> - Domain verify TTL 過短 → 改 24h 可 extend

