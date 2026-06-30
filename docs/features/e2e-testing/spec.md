# e2e-testing — 端到端測試標準

> 來源：AGENTS.md「Testing」§「每個 feature 必須有 e2e test」之落地規格。
> 本檔定義「e2e test 在 0ops 是什麼、怎麼建、怎麼跑、CI 怎麼接」，是所有 feature
> e2e 的單一事實來源。各 feature 的**個別** e2e 設計寫在
> `docs/features/{FEATURE}/`（spec 或 release/），不在本檔重述。

## 1. 結論（先讀）

- **每個 feature 必須有一條 e2e**，對「真實組裝起來的系統」行使該 feature 的招牌保證，
  而非僅單元/handler 層的隔離測試。
- e2e 分兩層，依 feature 性質擇一或併用：
  1. **composition test（Go）**：in-process `httptest.NewServer(NewRouter(...))`，可對真
     postgres（`TEST_DATABASE_URL`/`DATABASE_URL`，缺則 `t.Skip`）。檔名 `e2e_*_test.go`，
     package `server`。適合：協定層 happy path、簽章、狀態轉移。範本：
     `src/internal/server/e2e_create_app_test.go`。
  2. **compose-stack e2e（shell）**：對 `podman compose` 拉起的真服務棧（真 server 容器 +
     真 db + 必要的 mock 依賴）跑驗收腳本。檔名 `tasks/e2e-{feature}.sh`，由
     `manage.sh e2e-{feature}` 驅動。適合：跨容器、跨進程、外部協定（OIDC/webhook/callback）、
     權限與撤權鏈這類「只有真組裝才證得了」的保證。範本：`tasks/e2e-create-app.sh`。
- **硬規約（lessons L001）**：compose-stack e2e 一律經 `OPS_HOST` 打 compose stack 或 staging
  端點；**不可**在 host 直接跑 `./bin/0ops-server` 取代 stack；CLI/MCP 以 `podman run` 對應
  runtime image 驅動，保留 distroless non-root 邊界。

## 2. compose e2e 測試棧

### 2.1 棧的組成

e2e 棧 = root `compose.yaml`（dev 拓樸：`db → migrate → provision-app-role → server`）
**疊加** feature 專屬的 `compose.e2e.yaml` overlay（加入 mock 依賴與 e2e 專用設定）：

```
podman compose -f compose.yaml -f compose.e2e.yaml up -d --wait
```

- overlay **只新增**測試依賴服務（如 mock IdP、mock receiver）與覆寫 e2e 專用 env；
  **不得**改 image / build target / 既有 healthcheck 等 root 契約（與 compose.override 同規）。
- overlay 服務必須有 healthcheck，並讓 server `depends_on` 它 `service_healthy`，使
  `--wait` 能等到全棧 ready。
- e2e 專用、可重現：overlay 不掛 host podman socket、不依賴外部網路；mock 依賴自帶測試資料。

### 2.2 mock 外部依賴

外部協定（OIDC IdP、webhook receiver、registry…）以**自帶 in-repo mock** 服務提供，置於
`src/cmd/devtools/{mock-*}/`，由 overlay 以 `build:` 就地建。理由與既有「自實作 OIDC 驗證器
免新依賴」一致：可控、可離線、CI 無需外網。mock 僅供 dev/e2e，production compose 永不含它。

### 2.3 driver 腳本契約（tasks/e2e-{feature}.sh）

比照 `tasks/e2e-create-app.sh`：

- `set -euo pipefail`；頂部註解寫明對齊的 `docs/features/{FEATURE}/` 章節。
- 支援 `--phase=<name>`（單跑某階段）與 `E2E_MODE`（`local` 預設 / `staging` / `production`）。
- preflight 檢查工具（podman/curl/openssl/jq）缺則 exit 2。
- 退出碼穩定：`0` 全過或合法 SKIP；`2` 缺工具；`3` 斷言失敗；`4` 子程序非預期退出；
  `64` 用法錯誤。`E2E_REQUIRE_PASS=1` 時全 SKIP 視為失敗（exit 6），防「全 SKIP 假綠」。
- 對 backend 一律經 `OPS_HOST`（local 預設 `http://127.0.0.1:${OPS_HOST_PORT:-8080}`）。
- fixture 植入：允許以 `podman compose exec -T db psql` 寫入「非本 feature 招牌保證」的前置資料
  （如他層已單測覆蓋的設定列），但**招牌保證本身必須經 live HTTP 行使**，不可用 SQL 偽造結果。

## 3. CI 接線

- composition test 隨 `manage.sh test`（`go -C src test ./...`）跑；CI 已備 postgres service
  + migrations（見 v1 收尾 #114），真 DB 測在 GHA 真跑。
- compose-stack e2e 由 `manage.sh e2e-{feature}` 觸發；CI job 先 `compose ... up -d --wait`
  再跑腳本，結束 `compose down -v`。`E2E_REQUIRE_PASS=1` 確保不因全 SKIP 假綠。

## 4. 「feature 完成」的 e2e 門檻

一個 feature 視為完成，其 e2e 必須：

1. 行使該 feature 文件（spec/plan）所宣稱的**招牌保證**至少一條 end-to-end。
2. 在「真組裝」層級行使（composition 或 compose-stack，依 §1 擇定），非僅 mock 掉相依的隔離測試。
3. 誠實標明 deferred：凡因環境（真 IdP/cluster/外部資源）無法在 e2e 行使者，於 feature 文件
   明列 deferred 條件與替代覆蓋層，不得灌水講成已端到端（對齊 AGENTS.md「文件先寫結論再寫限制」）。

## 5. 現況盤點（roll-out）

本標準為**前向規約**：自即日起新 feature 必附 e2e。既有 feature 的 e2e 補齊為獨立 backlog，
依風險排序（撤權/權限/簽章/狀態轉移類優先）。已具 e2e 者：

- `create-app-flow`：`e2e_create_app_test.go` + `tasks/e2e-create-app.sh`
- `app-source-ingestion`：`tasks/e2e-source-upload.sh`
- ADR-0012 local-build：`tasks/local-build-e2e.sh`
- `trace-id-end-to-end`：`trace_propagation_test.go`
- `sso-saml`（M9.5）：`tasks/e2e-sso.sh` + compose mock IdP（見
  `docs/features/sso-saml/release/2026-06-30-oidc-login-and-e2e.md`）
