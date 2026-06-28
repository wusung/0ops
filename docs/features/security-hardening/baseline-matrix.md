# Security hardening — baseline 盤點矩陣（審計可出示版）

> **來源**：`docs/features/security-hardening/spec.md` § 4 / § 8。本檔是該節的可出示版本，與 spec § 4 同步。
> **狀態欄**：`已具備`（對應實際 spec / migration / 程式機制）、`本 spec 引入`（M9.3 交付）、`規劃中`（其他 spec / milestone）。
> **誠實規約**（承 plan § 6 規則 1、threat-model § 11 規則 1、spec § 13 #4）：`已具備` 必對應實作；不得把未實作講成已具備。

## 1. M9.3 實際交付（本 spec 引入）

| 控制 | 交付物 | 狀態 |
|---|---|---|
| 高風險動作 risk_level 標記 | `internal/server/security/risk.go`（白名單目錄純函式）；`db.CreatePreview` 於產 preview 時計算並存 `preview.risk_level`（migration `00015`） | 本 spec 引入 |
| 高風險 typed-confirmation 額外閘門 | `preview.required_phrase`（backend 生成、唯讀）+ confirm 端 `confirmation_phrase` AND 驗證（`deleteapp.Confirm`，不繞過既有 preview_id 強制）；CLI/MCP 透傳 | 本 spec 引入（delete_app 已接線；目錄其餘高風險動作待各自 preview/confirm 接線時沿用同機制） |
| token anomaly 訊號語意 + 反應政策 | `internal/server/security/anomaly.go`（評估純函式、`abuse_detected` 常數、v1 僅告警）；**不建偵測 goroutine**（歸 rate-limit-and-abuse，deferred） | 本 spec 引入（純模組） |
| token TTL 收斂政策 | `internal/server/security/policy.go`（`ResolvePATTTLDays`/`ResolveDeviceTTLDays` = min(req, teamCap, globalMax)）；**未接簽發路徑**（team_security_policy schema 屬 spec § 12 open） | 本 spec 引入（純函式） |
| at-rest 加密金鑰文件化 | `docs/runbooks/at-rest-encryption-key.md` + `deploy/security/encryption-config.example.yaml` 模板 | 本 spec 引入（文件 + 模板） |

## 2. 認證 / Token（spec § 4.1）

| 控制 | 現況 | 狀態 | 對應 |
|---|---|---|---|
| PAT / device token argon2id 雜湊儲存 | 明文不入 DB；`argon2id` 比對 | 已具備 | `auth-and-rbac` § 4.4 / migration 00003 |
| Token `expires_at` 強制 | device 30d 滾動、PAT 預設 90d / 最長 365d | 已具備（基底）+ 本 spec 引入（收斂政策純函式，§ 7） | `auth-and-rbac` § 4.3 / spec § 7 |
| Token scope 限定（RBAC） | PAT 綁單 team + scope 子集 | 已具備 | `auth-and-rbac` § 5 |
| Token 使用入帳 | `login` / `token_create` / `token_revoke` 入 audit | 已具備 | `audit-log` § 5.1 |
| Token 外洩 anomaly 偵測 | 訊號語意 + 反應政策（純模組）；偵測迴圈 deferred | 本 spec 引入（§ 6）+ 規劃中（偵測迴圈歸 rate-limit-and-abuse） | spec § 6 |
| 集中撤權（SSO） | 無 | 規劃中（P2） | `sso-saml` / ADR-0016 |

## 3. 寫入路徑 / Agent 攻擊面（spec § 4.2）

| 控制 | 現況 | 狀態 | 對應 |
|---|---|---|---|
| preview→confirm 後端強制 | write tool 無 `preview_id` 直接 4xx；單次性；actor 驗證 | 已具備（不得弱化） | `preview-confirm-gate` § 6 |
| preview 印 side_effects + 過期 | `Description / Resource / Reversible` + TTL 10min | 已具備 | `preview-confirm-gate` § 5 / § 8 |
| 高風險動作差異化確認 | risk_level 標記 + typed confirmation AND 閘門 | 本 spec 引入（§ 5） | spec § 5 |
| confused deputy | preview 顯示 subject；高風險 RISK 標頭 + `⚠ irreversible` | 已具備 + 本 spec 引入（§ 5.3） | `preview-confirm-gate` / spec § 5 |
| backend 不跑 LLM | 後端核心無 prompt injection 面 | 已具備 | `0ops-plan.md` § Runtime |

## 4. 租戶隔離 / 執行環境（spec § 4.3 / § 8）

| 控制 | 現況 | 狀態 | 對應 |
|---|---|---|---|
| per-team namespace | `team-<slug>` 固定命名 | 已具備 | `k3s-namespace-isolation` § 4 |
| NetworkPolicy 預設拒跨 team | ingress default-deny；egress 封 RFC1918 | 已具備 | `k3s-namespace-isolation` § 6 |
| 顯式 default-deny-all（ingress+egress）基線 | ingress 已 default-deny；egress 為 allow-with-except | **規劃中（deferred）**：manifest 單一事實來源為 `k3s-namespace-isolation`（spec § 13 #7，本 spec 不重定義） | `k3s-namespace-isolation` § 6 |
| 跨 namespace 拒絕常態化驗證 | 無 CI cluster；無常態 integration | **規劃中（deferred）**：需 CI cluster；本 spec 標需求，不灌水講成已具備（spec § 13 #4/#8） | `k3s-namespace-isolation` |
| ResourceQuota / LimitRange | 依 plan tier；建立時同 transaction apply | 已具備 | `k3s-namespace-isolation` § 5 |
| PSA | `enforce=baseline / warn=restricted` | 已具備 + 規劃中（v2 升 restricted） | `k3s-namespace-isolation` § 7 |

> **§ 8 誠實處置**：default-deny-all 顯式化與跨-ns 拒絕 CI 常態驗證是 M9.3 盤出的缺口，
> 但 manifest 歸 `k3s-namespace-isolation`、CI 常態跑需 CI cluster，故 v1 標 **deferred**，
> 不在 M9.3 落地，也不宣稱已具備（spec § 5 §11 三條 end-to-end 之「跨 ns 拒」降級為文件標記）。

## 5. Secrets / 資料（spec § 4.4 / § 9）

| 控制 | 現況 | 狀態 | 對應 |
|---|---|---|---|
| redactor 共用 instance | secret/token/webhook payload 不落 log/audit/error | 已具備 | `error-model` § 9 / `audit-log` § 8 |
| secret rotation（A–D 類） | 雙 window / 週期化 | 已具備 | `secrets-management` § 5 |
| Secret K8s RBAC `resourceNames` 限定 | backend 僅可讀列舉 secret | 已具備 | `secrets-management` § 6 |
| at-rest 加密金鑰管理 / 輪替 | runbook + EncryptionConfiguration 模板 | 本 spec 引入（§ 9） | `docs/runbooks/at-rest-encryption-key.md` |
| webhook/callback 簽章驗證 | HMAC 驗章 | 已具備 | `webhook-and-redeploy` / `build-pipeline-and-callback` |

> **at-rest 誠實**：不得宣稱 K8s native `Secret`（base64）為「加密儲存」（spec § 13 #4）。

## 6. 速率 / 濫用（spec § 4.5）

| 控制 | 現況 | 狀態 | 對應 |
|---|---|---|---|
| per-token / per-team rate limit | token bucket 已落地（M4.2） | 已具備 | `rate-limit-and-abuse` § 4 |
| abuse 偵測器框架 + `access_log_aggregate` | 設計完成；聚合表待建 | 規劃中（deferred）+ 本 spec 引入訊號語意 | `rate-limit-and-abuse` § 6 / spec § 6 |
| `abuse_detected` audit 通道 | 已定義入帳 | 已具備（本 spec 復用） | `audit-log` § 5.1 |

> **anomaly 誠實**：地理 / IP 跳變訊號依賴 `access_log_aggregate`（v1 不存在），
> 在其落地前不得宣稱已具備（spec § 6.1 / § 13 #4）。v1 `anomaly.go` 不含 `client_asn` 依賴路徑。
