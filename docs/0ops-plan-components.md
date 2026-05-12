## 核心元件設計

### Backend：preview gate（`internal/server/preview/preview.go`）
- 寫入/刪除類 endpoint 接受兩種模式：
  - `:preview` 後綴：執行 dry-run，計算 side_effects（不真的呼叫 Cloudflare / 不真的 push gitops），寫入 `preview` 表，回 PlanPreview
  - 主端點 + body 帶 `preview_id`：載入 preview、**SQL 強制 `WHERE team_id = $1 AND id = $2`**（不可跨 team）、檢查 actor_user_id 一致、未過期、未 consumed → 重做先決條件檢查（slug 是否仍可用、token 是否仍有效、role/scope 仍夠）→ 真正執行 → 標記 consumed 並把執行結果存進 `preview.last_result`
- **Idempotency**：
  - `preview_id` 兼 idempotency key；`(team_id, idempotency_key)` 唯一
  - 同一 `preview_id` 的 confirm 重試：若 `consumed_at != null`，直接回 `last_result`，**不重做副作用**
  - client 也可主動帶 `Idempotency-Key` header（若帶且與 preview_id 衝突 → 422）
- TTL：preview 10 分鐘；背景 goroutine 每 60s 清過期 row
- **race 條件**：confirm 進入後在 transaction 內 `SELECT ... FOR UPDATE` preview row，避免並發 confirm 同 preview；先決條件檢查在同一 tx 內
- **副作用順序**：reversible（gitops branch、Cloudflare DNS draft）先 → irreversible（image push、tunnel binding）後；任一步失敗轉狀態機 `compensating`，反向回滾 reversible 部分
- 安全：preview 不得跨 team 取用；query 模板鎖 `team_id`，handler 不接受用戶傳入 team_id（一律從 URL path resolve）

### CLI：互動式 confirm（`internal/cli/interactive/`）
- 預設行為：寫入/刪除指令先呼 `*:preview` → 用 `tablewriter` 印 action_summary + side_effects → `survey.Confirm` y/N → y 才呼主端點
- `--yes` / `-y`：跳過互動，直接 preview + 立刻 confirm（仍走兩階段，紀錄 audit）
- `--dry-run`：只呼 preview，印 PlanPreview 後結束
- `--output {table,json,yaml}`：適用所有讀取與最終結果

### MCP server（`cmd/mcp/` + `internal/mcp/`）
- 套件：官方 `github.com/modelcontextprotocol/go-sdk`；fallback 條件與相容性矩陣見 ADR-0003
- Transport：stdio；logging 走 `log/slog` + stderr
- Tool registry：在 `init()` 或啟動時註冊；每個 tool 提供 `Name()`, `Schema() json.RawMessage`, `Description() string`, `Call(ctx, args) (Result, error)`
- 寫入類拆兩個 tool：`<action>_preview` 與 `<action>`，後者必須帶前者回的 `preview_id`
- 認證：啟動時讀 `~/.config/0ops/auth.json`；無 token 時 tool 回錯誤訊息要使用者跑 `0ops auth login`
- 對 backend 的呼叫共用 `*http.Client`（含 timeout、retry middleware、429 處理）
- 對 SSE 類（logs follow）：優先採官方 SDK 的 streaming 能力；若 spike 驗證不足，退為分頁拉取 + cursor（見 ADR-0003）

#### Tool description 強制約定

Tool description 是 LLM 唯一能看見的「使用說明書」；三家 AI CLI 對 description 的遵守率差異大。所有 description **必須**符合下列規約：

**`<action>_preview` tool description 範本**：
```
Preview the side effects of <action>. Returns a PlanPreview with action_summary, side_effects[], and a preview_id valid for 10 minutes. ALWAYS call this BEFORE calling `<action>`. Show the user the action_summary and the FULL side_effects list and wait for explicit approval ("yes" / "go" / "確認") before invoking `<action>`. If the user does not explicitly approve, treat as rejection and abort.
```

**`<action>` tool description 範本**：
```
Execute <action> using a preview_id obtained from `<action>_preview`. NEVER call this tool without a fresh, user-approved preview_id. Calling without preview_id, with an expired preview_id, or without showing the user the side_effects first is a protocol violation; the backend will reject with 4xx. Idempotent on the same preview_id (returns last_result on retry).
```

**Read tool description**：簡述用途即可，無強制句式。

**強制機制**（不依賴 LLM 自律）：
- backend 的 `<action>` endpoint 無 `preview_id` 直接回 `400 missing_preview_id`
- preview 過期 `410 preview_expired`、跨 team `403 forbidden_team`、consumed 重試走 last_result 回放
- MCP server 啟動時 lint 自身 tool description：`*_preview` 必含 "ALWAYS call this BEFORE"；非 preview 寫入 tool 必含 "NEVER call this tool without"。違反則 `mcp-server` 啟動失敗並印明確錯誤
- skill packs `SKILL.md` 內也重述同一段 verbatim，雙保險

### Skill packs（`skills/`）
每個 pack 三件事：(a) 怎麼註冊 MCP server、(b) SKILL.md 描述使用情境與兩階段約定、(c) 安裝指引。

**Claude Code**（`skills/claude-code/0ops/`）
- `mcp-config.json`：
  ```json
  {
    "mcpServers": {
      "0ops": {
        "command": "0ops-mcp",
        "args": []
      }
    }
  }
  ```
  使用者跑 `claude mcp add` 或手動貼到 `~/.claude.json`
- `SKILL.md`（frontmatter + body）：
  - 觸發場景：使用者問「部署 X」「加網域」「看 Y log」「重新部署」
  - 工具列表與用途
  - **強制約定**：寫入/刪除前必呼 `*_preview` → 把 `action_summary` 與 `side_effects` 完整呈現給使用者 → 等使用者明確同意（"yes" / "go" / "確認"）→ 才呼主 tool；若使用者未明確同意，視為拒絕
  - 範例對話腳本

**Codex CLI**（`skills/codex/0ops/`）
- `codex-config.toml.snippet`：`[mcp_servers.0ops]` 段落貼到 `~/.codex/config.toml`
- `SKILL.md`：等價內容、強調 codex 的互動方式

**Copilot CLI**（`skills/copilot/0ops/`）
- 若 Copilot CLI 原生支援 MCP：與 Claude/Codex 共用同一份 server，提供其專屬 config
- 若不支援：退路為包一層 shell extension wrap CLI binary（`0ops apps create ...`），LLM 透過 shell tool 呼叫；preview/confirm 由 CLI 互動式處理
- **TBD**：實際支援度

### Build & deploy（`deploy/workflows/deploy-app.yml`）
GitHub Actions workflow，五階段：GHCR 登入 → `pack build` → trivy scan → render manifest 並 commit 到 gitops repo → callback backend。

```yaml
jobs:
  deploy:
    steps:
      - uses: actions/checkout@v4

      - name: Login GHCR
        run: echo "$GHCR_TOKEN" | docker login ghcr.io -u "$ACTOR" --password-stdin

      - name: Pack build
        run: |
          pack build $IMAGE_REF \
            --builder paketobuildpacks/builder-jammy-base \
            --path . \
            --publish \
            --cache-image $IMAGE_REF-cache

      - name: Trivy scan
        uses: aquasecurity/trivy-action@v0
        with:
          image-ref: ${{ env.IMAGE_REF }}
          severity: HIGH,CRITICAL
          exit-code: '0'   # v1 觀察、v1.1 改 '1' 強制阻擋

      - name: Render & commit gitops
        run: ./scripts/render-and-push-gitops.sh
        # push 衝突時 retry + rebase（最多 5 次）

      - name: Callback backend (always)
        if: always()
        env:
          BACKEND: ${{ secrets.OPS_BACKEND_URL }}
          SECRET:  ${{ secrets.OPS_CALLBACK_SECRET }}
          RUN_ID:  ${{ env.DEPLOY_RUN_ID }}
        run: |
          PAYLOAD=$(jq -n \
            --arg run_id "$RUN_ID" \
            --arg status "${{ job.status }}" \
            --arg trace_id "$TRACE_ID" \
            --arg image "$IMAGE_REF" \
            '{run_id:$run_id,status:$status,trace_id:$trace_id,image:$image}')
          TS=$(date +%s)
          SIG=$(echo -n "${TS}.${PAYLOAD}" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $2}')
          curl -fsS -X POST "$BACKEND/internal/deploy-runs/$RUN_ID/callback" \
            -H "Content-Type: application/json" \
            -H "X-0ops-Timestamp: $TS" \
            -H "X-0ops-Signature: sha256=$SIG" \
            --data "$PAYLOAD"
```

**Callback over polling**：backend 不主動 poll workflow_run，由 GHA 完成後（含 `failure` / `cancelled`）發 HMAC 簽章 callback。Backend 驗 signature + timestamp window ±5min + nonce（`run_id` 入 `webhook_dedup`）。
**Polling 為退路**：背景 reconciler 對 `deploy_run.status='building'` 滯留 > 30min 主動拉 GitHub API workflow_run 收斂，避免 callback 永遠不來。

**Build secret 注入**：team 級 `secret_binding` 表，GHA 透過 `repository_dispatch` payload 帶 short-lived token，backend 簽發 20min 過期。
**Promote 機制**（dev → prod 跨 ref 升版）：v2 規劃。

### GitOps target（`deploy/gitops/`）
新 repo `0ops-gitops`：
```
apps/<slug>/
  ├── deployment.yaml
  ├── service.yaml
  ├── ingress.yaml
  └── kustomization.yaml
argo/
  └── applicationset.yaml
```
ApplicationSet 採 list/git generator 模式，每個 app 對應 `apps/<slug>/` 子目錄。

### Domain verify（`internal/server/services/domainverify/`）
- 客戶自有域名：產生 `verification_token` (32-byte hex)，要求加 CNAME + TXT
- 背景 goroutine 每 30s 用 `net.DefaultResolver` 對 pending `domain_binding` 做 DNS 查詢
- 同時通過 → 標記 verified → 呼 Cloudflare client 註冊 hostname → 觸發 ingress yaml render
- TTL：**`domain_binding.expires_at` 預設 24h**（涵蓋客戶內部 IT 跨日審批）；CLI/MCP 可呼 `0ops domains verify <slug> <host> --extend` 把 TTL 再延 24h，最多兩次（總 72h）
- 過期後保留 row 7 天供使用者重啟（重發 token），之後 hard delete

