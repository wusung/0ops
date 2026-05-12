## Verification

### Smoke
- 啟本機開發環境（`make dev` → `podman compose up -d`，含 db + migrate + server；詳見 `docs/features/dev-environment/spec.md`）
- mock GitHub App + Cloudflare API（`net/http/httptest` + `h2non/gock` 或自寫 fake server）
- 端到端：`0ops apps create` → confirm → 模擬 build success（含 callback HMAC）→ 看 manifest 寫入測試 gitops repo
- MCP：以 stdio 直接餵 `initialize` + `tools/list` + 呼 `list_apps`，驗證輸出符合 MCP schema

### Contract
- CLI / MCP 的 backend client DTO 由 backend OpenAPI spec 自動生成（`oapi-codegen`），CI 上鎖 schema drift
- MCP tool input/output JSON Schema 對 backend handler 跑 contract test：每 tool 一條 `golden` fixture
- Webhook payload（GitHub + 內部 callback）的 fixture 集存 `internal/server/auth/testdata/`

### 整合
- 真連 GitHub App + Cloudflare API（staging zone `*.staging.winshare.tw`）
- 真 sample repo（FastAPI/Express/Go HTTP server 各一）跑全流程
- `testing` + `testcontainers-go` Postgres
- 把 MCP server 註冊到本機 claude code，跑 5 條代表性自然語言指令，驗證 LLM 是否遵守 preview-then-confirm 約定（錄成 deterministic transcript fixture，CI 重放）

### 邊界
- **Preview / Idempotency**：preview 過期、consumed 二次 confirm（須回 last_result 不重做副作用）、跨 team 偷拿 preview（SQL 鎖定須 reject）、同 slug race（`SELECT ... FOR UPDATE` + 唯一索引）
- **Authorization**：viewer 呼 write、wrong scope token、PAT 跨 team、token 過期、role 在 confirm 之間被降級
- **Webhook**：HMAC 簽章錯誤、timestamp 過期、replay（同 delivery_id 重送）、payload 過大
- **Compensation**：image push 成功但 gitops push 失敗、Cloudflare DNS 寫入後 ArgoCD sync 失敗（reconciler 須收斂、狀態機進 `compensating`）
- **External failure**：repo 私有未授權、Cloudflare API 5xx / 429、DNS 永遠不通、buildpack 偵測失敗、GHA timeout（callback 永遠不來，靠 polling fallback 收斂）
- **AI CLI 違規**：跳過 preview 直接呼 write tool（backend 必須 reject：write tool 必須帶 preview_id）
- **Multi-tenant isolation**：team A 的 user 列出 team B 的 app（須 0 結果而非 403，避免 enumeration）

