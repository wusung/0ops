# Feature Spec：security-hardening

> **狀態**：draft
> **來源**：`docs/trust-and-compliance/plan.md` § 3.3（Security 軸）/ § 5.1（P1）；`docs/features/threat-model/spec.md` § 5.1（AG1/AG2）、§ 5.2（AU1）、§ 5.5（TN2）、§ 5.6（SE2）、§ 6 缺口彙整
> **適用範圍**：安全強化 baseline 盤點、高風險動作差異化確認、token anomaly 偵測與 TTL 收斂、namespace 隔離強化盤點、secret 加密金鑰管理；不含個別緩解的底層實作（屬各自 spec：`preview-confirm-gate`、`auth-and-rbac`、`k3s-namespace-isolation`、`secrets-management`、`rate-limit-and-abuse`）
> **對應 Milestone**：P1（enterprise 前置；威脅模型已釘範圍，本 spec 補可出示的強化控制）
> **依賴**：`threat-model`（威脅單一事實來源）；強化對象 spec：`preview-confirm-gate`、`auth-and-rbac`、`k3s-namespace-isolation`、`secrets-management`、`rate-limit-and-abuse`、`audit-log`
> **讀法**：§ 1 結論 → § 4 baseline 盤點矩陣 → § 5 高風險動作 → § 11 驗證準則

## 1. 結論（先讀本段）

- 本 spec 是 `threat-model` 指派給 Security 軸的**強化交付**，解 AG1（confirm 疲勞 → 高風險動作差異化確認）、AG2/AU1（token 外洩爆炸半徑 → anomaly 偵測 + 短 TTL 預設）、TN2（namespace 隔離強化盤點）、SE2（secret 加密金鑰管理 / 輪替文件化）。
- **本 spec 的本質是「盤點 + 差異化 + 文件化」，不是重造輪子**。0ops 既有架構已壓制最危險威脅（preview→confirm 後端強制、team 隔離 + RBAC、redactor、簽章驗證、rate-limit、K3s namespace 隔離）。本 spec 的工作是：把既有控制盤成可出示的 baseline、對「高風險動作」加一層**額外**確認、補上 token 外洩後的偵測缺口、把隱性的 at-rest 加密金鑰管理寫成文件。
- **絕對紅線（承 threat-model AG4）**：高風險動作差異化確認是**疊加在既有 preview→confirm 後端強制之上的額外閘門**，不得繞過、不得弱化、不得取代 backend 對 `preview_id` 存在 / 未過期 / 未消費 / 單次性 / `actor_user_id` 一致的強制驗證。任何「差異化」只能是**更嚴**，不可更鬆。
- **已具備（對應實際機制）vs 本 spec 引入**，全程明確區分；§ 4 盤點矩陣每列標 `已具備 / 本 spec 引入 / 規劃中`，不得把未實作講成已具備（承 plan § 6 規則 1、threat-model § 11 規則 1）。
- 與 `rate-limit-and-abuse` 的分工：偵測**框架**（背景 goroutine、`access_log_aggregate` 聚合）屬 rate-limit-and-abuse；本 spec 定義 **token 維度的 anomaly 訊號語意、偵測後的安全反應（re-auth / 降速）、與短 TTL 預設政策**，反應動作復用既有 `abuse_detected` audit 通道，不另建平行偵測器。

## 2. 範圍

### 2.1 包含

- 安全強化 baseline checklist（現況盤點矩陣：已具備 vs 缺口 vs 本 spec 引入）（§ 4）。
- 高風險動作定義、`risk_level` 標記機制、preview 差異化呈現、typed confirmation 額外閘門（§ 5）。
- Token anomaly 偵測的訊號界定（v1 可得 vs 不可得）、偵測後反應、與 rate-limit-and-abuse 分工（§ 6）。
- Token TTL 預設收斂建議與可配置範圍（§ 7）。
- Namespace 隔離強化盤點（NetworkPolicy / ResourceQuota / LimitRange 現況確認 + 缺口）（§ 8）。
- Secret at-rest 加密金鑰的儲存、輪替策略文件化（§ 9）。

### 2.2 不包含

- preview→confirm gate 本體（TTL、GC、副作用框架、actor 驗證）：屬 `preview-confirm-gate`，本 spec 只疊加閘門。
- token 雜湊（argon2id）、device flow、PAT 簽發本體：屬 `auth-and-rbac`，本 spec 只調 TTL 預設政策。
- NetworkPolicy / ResourceQuota / PSA / ImagePullSecret 的 manifest 定義：屬 `k3s-namespace-isolation`，本 spec 只盤點 + 標缺口。
- secret rotation 程序（A–D 類）、K8s RBAC、Secret 清單：屬 `secrets-management`，本 spec 只補 at-rest 加密金鑰那一層。
- rate-limit 配額、429 envelope、abuse detector goroutine 與聚合表：屬 `rate-limit-and-abuse`。
- SBOM / image provenance（屬 `supply-chain-security`）、SSO（屬 `sso-saml`）、audit append-only / hash chain（屬 `audit-export-and-integrity`）。

## 3. 檔案結構

```
0ops/
├── internal/
│   └── server/
│       ├── security/
│       │   ├── risk.go               # 高風險動作目錄 + RiskLevel 判定（純函式，無副作用）
│       │   ├── anomaly.go            # token anomaly 訊號評估（消費 rate-limit-and-abuse 聚合輸入）
│       │   ├── policy.go             # TTL 預設政策 + team-level security policy 解析
│       │   └── doc.go
│       └── preview/
│           └── produce.go            # （既有）產 preview 時呼 security.RiskLevel() 標記 risk_level
└── docs/
    └── features/security-hardening/
        ├── spec.md                   # 本檔
        └── baseline-matrix.md        # § 4 盤點矩陣的可出示版本（審計交付；與本 spec § 4 同步）
```

> `security.go` 等為純判定 / 政策模組；不持有 DB 寫入路徑（audit 寫入仍走 `audit-log` 的 `audit.Log`，anomaly 偵測器仍走 `rate-limit-and-abuse` 的背景 goroutine）。本 spec 不新增獨立偵測迴圈。

## 4. 安全強化 baseline checklist

> 每列標狀態：`已具備`（對應實際 spec / migration / 程式機制）、`本 spec 引入`、`規劃中`（其他 spec / milestone）。承 threat-model § 11 規則 1：`已具備` 必對應實作，不得灌水。

### 4.1 認證 / Token

| 控制 | 現況 | 目標 | 狀態 | 對應 |
|---|---|---|---|---|
| PAT / device token argon2id 雜湊儲存 | 明文不入 DB；`argon2id(token)` 比對 | 維持 | 已具備 | `auth-and-rbac` § 4.4 / migration 00003 |
| Token `expires_at` 強制 | device 30d 滾動、PAT 預設 90d / 最長 365d | 收斂預設 + team policy cap | 已具備（基底）+ 本 spec 引入（收斂政策，§ 7） | `auth-and-rbac` § 4.3 / 本 spec § 7 |
| Token scope 限定（RBAC） | PAT 綁單 team + scope 子集；device 全 scope 受 role 限 | 維持 | 已具備 | `auth-and-rbac` § 5 |
| Token 使用入帳 | `login` / `token_create` / `token_revoke` 入 audit | 維持 | 已具備 | `audit-log` § 5.1 |
| Token 外洩 anomaly 偵測 | 無（僅手動 revoke） | 訊號偵測 + 自動反應 | 本 spec 引入（§ 6，框架復用 rate-limit-and-abuse） | 本 spec § 6 |
| 集中撤權（SSO） | 無 | OIDC/SAML | 規劃中（P2） | `sso-saml` |

### 4.2 寫入路徑 / Agent 攻擊面

| 控制 | 現況 | 目標 | 狀態 | 對應 |
|---|---|---|---|---|
| preview→confirm 後端強制 | write tool 無 `preview_id` 直接 4xx；單次性；actor 驗證 | 維持（不得弱化） | 已具備 | `preview-confirm-gate` § 6 / § 11 |
| preview 印 side_effects + 過期 | `Description / Resource / Reversible` 三欄 + TTL 10min | 維持 | 已具備 | `preview-confirm-gate` § 5 / § 8 |
| 高風險動作差異化確認 | 無（所有 confirm 同一強度） | risk_level 標記 + typed confirmation 額外閘門 | 本 spec 引入（§ 5） | 本 spec § 5 |
| confused deputy（誤 team/app） | preview 顯示 subject；team scope 強制 | 維持 + 高風險動作標紅 subject | 已具備 + 本 spec 引入（§ 5.3） | `preview-confirm-gate` / 本 spec § 5 |
| backend 不跑 LLM | 後端核心無 prompt injection 面 | 維持 | 已具備 | `docs/0ops-plan.md` § Runtime |

### 4.3 租戶隔離 / 執行環境

| 控制 | 現況 | 目標 | 狀態 | 對應 |
|---|---|---|---|---|
| per-team namespace | `team-<slug>` 固定命名 | 維持 | 已具備 | `k3s-namespace-isolation` § 4 |
| NetworkPolicy 預設拒跨 team | ingress default-deny（限 traefik/tunnel/同 ns）；egress 封 RFC1918 | 維持 + 補顯式 default-deny-all + 常態化跨 ns 拒絕驗證 | 已具備（基底）+ 本 spec 引入（盤點 + 驗證，§ 8） | `k3s-namespace-isolation` § 6 / 本 spec § 8 |
| ResourceQuota / LimitRange | 依 plan tier；建立時同 transaction apply | 維持 + 盤點防資源耗盡逃逸覆蓋率 | 已具備 + 本 spec 引入（盤點，§ 8） | `k3s-namespace-isolation` § 5 / 本 spec § 8 |
| PSA | `enforce=baseline / warn=restricted` | v2 升 restricted | 已具備 + 規劃中（v2） | `k3s-namespace-isolation` § 7 |

### 4.4 Secrets / 資料

| 控制 | 現況 | 目標 | 狀態 | 對應 |
|---|---|---|---|---|
| redactor 共用 instance | secret/token/webhook payload 不落 log/audit/error | 維持 | 已具備 | `error-model` § 9 / `audit-log` § 8 |
| secret rotation（A–D 類） | 雙 window / 週期化 | 維持 | 已具備 | `secrets-management` § 5 |
| Secret K8s RBAC `resourceNames` 限定 | backend 僅可讀列舉 secret | 維持 | 已具備 | `secrets-management` § 6 |
| at-rest 加密金鑰管理 / 輪替 | 未文件化（K8s native Secret 落 kine/Postgres datastore） | 文件化：金鑰所在、輪替、接合 | 本 spec 引入（§ 9） | 本 spec § 9 |
| webhook/callback 簽章驗證 | HMAC 驗章（push / callback） | 維持 | 已具備 | `webhook-and-redeploy` / `build-pipeline-and-callback` |

### 4.5 速率 / 濫用

| 控制 | 現況 | 目標 | 狀態 | 對應 |
|---|---|---|---|---|
| per-token / per-team rate limit | token bucket 已落地（M4.2） | 維持 | 已具備 | `rate-limit-and-abuse` § 4（Implementation note） |
| abuse 偵測器框架 | 設計完成；`access_log_aggregate` 待建（deferred） | 復用為 token anomaly 載體 | 規劃中（rate-limit-and-abuse deferred）+ 本 spec 引入訊號語意 | `rate-limit-and-abuse` § 6 / 本 spec § 6 |
| `abuse_detected` audit 通道 | 已定義入帳 | 復用為 anomaly 反應落地 | 已具備 | `audit-log` § 5.1 |

## 5. 高風險動作差異化確認

### 5.1 高風險動作目錄

> 「高風險」= 不可逆或爆炸半徑大、誤觸成本顯著高於一般寫入。判定為**白名單**（明列），不採黑名單推斷；新增高風險動作須更新本表 + `security.go` 的 `riskCatalog`。

| Action | risk_level | 理由 | 主要對應 spec |
|---|---|---|---|
| `delete_app` | **critical** | 不可逆刪除 + audit 永久 archive | `delete-app-flow` |
| `token_revoke`（全域 / 全 team token 批次撤銷）| **critical** | 立即斷所有自動化存取 | `auth-and-rbac` |
| `plan_change`（**降級**） | **high** | 觸發 quota 收縮、新 pod 被擋 | `k3s-namespace-isolation` § 5.3 |
| `custom_domain_unbind` | **high** | 對外服務立即中斷 | `custom-domain-and-verify` |
| `remove_member`（owner / admin）| **high** | 移除高權限成員、可能自鎖 | `auth-and-rbac` |
| `uninstall_github_app` | **high** | 斷 build / image pull credential 源 | `github-app-install-flow` |

> 一般寫入（`create_app`、`redeploy`、新增 domain、邀請 member）為 `normal`，走既有 preview/confirm，不加額外閘門。`plan_change` **升級** 為 `normal`（reversible side_effect）。

### 5.2 risk_level 標記機制

- backend 在 **產 preview 時**（`preview/produce.go`）呼 `security.RiskLevel(action, args) → normal | high | critical`，把結果寫入 preview 輸出（DTO 新增唯讀欄位 `risk_level`，預設 `normal`）。
- `risk_level` 為 backend 計算之**唯讀**屬性；CLI / MCP 不得自填或竄改；confirm 端不讀 client 傳入的 risk_level（防 client 自降風險繞過）。
- 判定為純函式（無 DB / 無副作用）；以 `action` + 必要 `args`（如 `plan_change` 需判升 / 降級）決定。
- 對既有 preview/confirm 契約**僅加欄位**（additive），不改 `preview_id` 語意、不改 TTL、不改 actor 驗證、不改副作用框架。

### 5.3 preview 差異化呈現

對 `risk_level >= high` 之 preview：

- side_effects 中 `Reversible == false` 之項**標紅 / 標記 `⚠ irreversible`**（CLI 終端與 MCP 文字呈現皆然）。
- 顯式標出 subject（team slug + app/resource），降低 confused deputy（threat-model AG3）。
- preview 摘要頭部加 `RISK: high|critical` 標頭，要求 host 端不得折疊省略（MCP host 呈現規約，接 `end-user-onboarding` MCP-hosts 段）。

### 5.4 typed confirmation 額外閘門

對 `risk_level >= high` 之 confirm，在**既有 preview_id 強制之上**疊加：

- **CLI**：要求使用者輸入指定 typed token（如 app slug 或 `DELETE <slug>`）才送出 confirm；輸入不符則本地中止，不發 confirm 請求。
- **MCP**：tool schema 對高風險動作要求 `confirmation_phrase` 參數，且其值須等於 backend 在 preview 輸出回傳的 `required_phrase`；backend confirm 端**額外驗證** `confirmation_phrase == preview.required_phrase`，不符回 `400 confirmation_phrase_mismatch`（envelope）。
- 此 `confirmation_phrase` 檢查是 **AND** 條件，與既有 `preview_id` 驗證並存；任一不過即拒。**不得**以 phrase 通過取代 preview_id 驗證，反之亦然（承 AG4）。
- `required_phrase` 由 backend 於 preview 產出時生成並存於 preview row（隨 preview 一併過期 / 消費）；不可由 client 提供。

### 5.5 更短過期（選用，受 ADR 約束）

- 任務要求「更短過期」作為高風險動作的可選強化。**但** `preview-confirm-gate` § 8 / 硬性規則 9 已釘「TTL 10 分鐘為常數；變更需 ADR 補丁；不得個別 endpoint 自行覆寫」。
- 因此本 spec **不**在 application 層為高風險動作私自縮短 TTL。若要對 `risk_level >= critical` 採更短 TTL（如 2 分鐘），須先補 `preview-confirm-gate` 的 ADR，由該 spec 以 `risk_level → ttl` 對照表統一管理，再由本 spec 引用。
- v1 採 typed confirmation（§ 5.4）為主要強化手段；更短 TTL 列 Open issue（§ 12），不在 v1 落地。**此為跨 spec 不一致的明示處置。**

## 6. Token anomaly 偵測

### 6.1 訊號（明確區分 v1 可得 / 不可得）

| 訊號 | 說明 | v1 可得性 | 來源 |
|---|---|---|---|
| 頻率突增 | 單 token 短時間內請求量遠超基線 | **部分可得**：rate-limit token bucket 觸發 + `0ops_rate_limit_triggered_total` metric 已存在；anomaly 為「持續觸頂」而非單次 429 | `rate-limit-and-abuse` § 4 / § 8（已落地） |
| scope 異常使用 | token 使用了其 scope 邊緣 / 罕用組合（如唯讀 token 持續觸發 403 write） | **部分可得**：可由 audit / middleware 403 計數推導；無歷史基線表 v1 為粗略門檻 | `audit-log` + `auth-and-rbac` middleware |
| 地理 / IP 跳變 | 同 token 短時間跨 ASN / 地理 | **v1 不可得**：依賴 `access_log_aggregate`（含 `client_asn`，由 Cloudflare header 反查），該表 v1 不存在（rate-limit-and-abuse Implementation note 標 deferred） | `rate-limit-and-abuse` § 6.2（deferred） |

> 硬性：地理 / IP 跳變訊號**不得**在 `access_log_aggregate` 落地前宣稱為已具備（threat-model § 11 規則 1）。v1 token anomaly 以「頻率持續觸頂 + 異常 403 比例」兩訊號為限；地理跳變列 v1.1，隨 rate-limit-and-abuse 聚合管線一併解鎖。

### 6.2 偵測後反應

偵測命中（由 rate-limit-and-abuse 背景 goroutine 評估，本 spec 提供訊號判定與反應政策）：

1. **入帳**：寫既有 `abuse_detected` audit 事件（`actor=system:anomaly`、`subject_type=token`、`subject_id=token_id`）；走 `audit-log` § 5.1 既有通道，不另開事件類別。
2. **自動降速（選用）**：對該 token 套更嚴 rate-limit bucket（復用 rate-limit-and-abuse limiter，不新增機制）；v1 預設**僅告警不自動降速**（對齊 rate-limit-and-abuse § 14 規則 7：abuse v1 不自動阻擋），自動降速列 v1.1。
3. **要求 re-auth（選用）**：對 device token 可標記 `require_reauth`，下次請求回引導重 login；v1 列 v1.1（需 auth-and-rbac 配合 token 狀態欄位）。

### 6.3 與 rate-limit-and-abuse 分工

| 職責 | 歸屬 |
|---|---|
| 偵測背景 goroutine + 5min ticker | `rate-limit-and-abuse` § 6.1 |
| `access_log_aggregate` 聚合表 + ASN 反查 | `rate-limit-and-abuse`（deferred） |
| rate-limit bucket / 429 / 自動降速機制 | `rate-limit-and-abuse` § 4 |
| **token 維度 anomaly 訊號語意 + 門檻** | **本 spec § 6.1** |
| **偵測後安全反應政策（re-auth / 降速觸發條件）** | **本 spec § 6.2** |
| `abuse_detected` audit 落地 | `audit-log` § 5.1（既有通道） |

> 不重複造偵測器：本 spec 不新增獨立偵測迴圈，只提供 `security.anomaly` 評估函式供 rate-limit-and-abuse 偵測器呼用。

## 7. Token TTL 預設強化

### 7.1 現況與收斂建議

| Token 類型 | 現況預設 | 現況上限 | 本 spec 收斂建議 | 可配置範圍 |
|---|---|---|---|---|
| device flow token | 30 天滾動更新（每 > 24h 自動換發） | — | 維持 30d 滾動；敏感 team 可降至 14d | team policy：7d–30d |
| PAT（一般 scope） | 90 天 | 365 天 | 預設維持 90d | 1d–365d（不變） |
| PAT（含寫入 / 高權限 scope） | 90 天 | 365 天 | 建議預設收斂至 **30d**，由 team security policy 決定 | team policy cap ≤ 90d |

### 7.2 機制

- 引入 **team-level security policy**（`policy.go`）：team owner 可設 `max_pat_ttl_days`、`max_device_ttl_days`，作為簽發時的**硬上限**（取 min(使用者要求, team cap, 全域 max)）。
- 預設值不破壞 `auth-and-rbac` 既有契約：未設 policy 的 team 維持 90d/365d 現況；policy 僅能**收緊**不可放寬超過全域上限。
- 任何預設值調整須同步更新 `auth-and-rbac` § 4.4 文件（避免兩處漂移）。
- TTL 上限變更不影響既有已簽發 token（不追溯縮短；下次簽發生效）。

## 8. Namespace 隔離強化盤點

> 本節為**盤點 + 缺口確認 + 驗證常態化**，非重新定義 manifest。manifest 的單一事實來源為 `k3s-namespace-isolation`。

### 8.1 現況確認（已具備）

| 控制 | 現況（對應 k3s-namespace-isolation） | 對 TN2 的作用 |
|---|---|---|
| 跨 team ingress | `default-deny-ingress`：僅允 traefik / cloudflare-tunnel / 同 namespace（§ 6.2） | 阻 team A → team B 之 ingress |
| 跨 team egress | `default-egress`：`0.0.0.0/0` except RFC1918（封 cluster-internal pod/服務 CIDR）（§ 6.2） | 阻 team A pod → team B pod 之直連 |
| 資源耗盡 | ResourceQuota（cpu/mem/pods/pvc/svc）+ LimitRange，依 plan tier（§ 5） | 限單 team 資源耗盡逃逸 |
| workload 提權 | PSA `enforce=baseline`：拒 privileged / hostPath / hostNetwork 等（§ 7.2） | 壓 namespace 逃逸面 |

### 8.2 缺口與本 spec 引入

| 缺口 | 強化 | 狀態 |
|---|---|---|
| 無顯式 `default-deny-all`（ingress 已 default-deny，egress 為 allow-with-except） | 補一條顯式 `default-deny-all`（ingress+egress）基線 policy 置於每 team namespace，再以既有 allow policy 疊加；確保「未明列即拒」語意明確 | 本 spec 引入（manifest 落於 k3s-namespace-isolation，本 spec 標需求） |
| 跨 namespace 拒絕無常態化驗證 | 把「team A pod → team B service 連線失敗」納入 CI 整合測試常態跑（AGENTS.md：team 隔離為高風險必測） | 本 spec 引入（驗證，§ 11） |
| PSA restricted 未啟 | v2 升 `enforce=restricted` | 規劃中（v2，k3s-namespace-isolation § 7.3） |

> 本 spec **不**重寫 NetworkPolicy / ResourceQuota / LimitRange；既有控制標 `已具備`。新增僅為 default-deny-all 顯式化與驗證常態化。

## 9. Secret 加密金鑰管理

> 解 threat-model SE2：secret at-rest 外洩——加密金鑰管理 / 輪替策略待文件化。`secrets-management` 已涵蓋 secret **內容**的 rotation（A–D 類）；本節補 **at-rest 加密層的金鑰**這一缺口層。

### 9.1 現況（精確陳述）

- application 端 PAT / device token：明文不入 DB，存 `argon2id` 雜湊（已具備，非可逆加密）。
- 共享 / 外部 secret：以 **K8s native `Secret`** 儲存（base64，**非加密**），落於 K3s datastore（kine on Postgres，ADR-0004）。
- 因此 secret 的 at-rest 保護**取決於 datastore 層**：K8s `EncryptionConfiguration`（對 `secrets` 資源加密寫入 etcd/kine）與 / 或 datastore（Postgres）卷加密。**此層的加密金鑰目前未文件化**（即 SE2 缺口）。

> 硬性：不得宣稱 K8s native Secret 為「加密儲存」。threat-model § 5.6 既有緩解所述「加密儲存」精確指 PAT 的 argon2id 雜湊與 datastore 層加密；K8s Secret base64 本身非加密。本節文件化的正是補足 datastore 層 at-rest 加密金鑰管理。

### 9.2 本 spec 引入（文件化目標）

| 項目 | 規範 |
|---|---|
| at-rest 加密啟用 | K3s 啟用 `EncryptionConfiguration` 對 `secrets` 資源（aescbc / secretbox provider）；或 datastore Postgres 卷層加密；二擇一或並用，須於 deployment runbook 明列實際採用 |
| 加密金鑰所在 | 加密金鑰**不得**與被加密資料同一信任域；存於 cluster 外（如 ops 管理之 KMS / 受控檔案 + 嚴格 OS 權限）；金鑰不入 git、不入 K8s Secret 自身 |
| 輪替策略 | 加密金鑰輪替週期 90 天，採 K8s `EncryptionConfiguration` 多 key（新 key 置首、舊 key 保留供解密）+ `kubectl get secrets -A -o yaml | kubectl replace -f -` 重寫全 secret 後移除舊 key |
| 與 secrets-management 接合 | secret **內容** rotation 屬 `secrets-management` § 5（A–D 類）；**at-rest 加密金鑰** rotation 屬本節；兩者正交，不得混為一談 |
| 審計 | 加密金鑰輪替入帳：`secret_rotate_start` / `secret_rotate_finalize`（subject 為 `system:at-rest-key`），復用 `secrets-management` § 9.1 既有 action |

## 10. 與其他 spec 接合

| 接合 | spec |
|---|---|
| 威脅單一事實來源（本 spec 解 AG1/AG2/AU1/TN2/SE2）| `threat-model` § 5、§ 6 |
| 統籌計畫（本 spec 為 Security 軸 P1）| `docs/trust-and-compliance/plan.md` § 3.3 / § 5.1 |
| preview/confirm 後端強制（不得弱化；疊加閘門）| `preview-confirm-gate` § 6 / § 8 / 硬規則 9 |
| risk_level 標記寫入 preview 產出 | `preview-confirm-gate` § 5.2（side_effects_jsonb 鄰欄）|
| typed confirmation MCP schema | `mcp-tool-permissions`、`end-user-onboarding` MCP-hosts |
| token TTL 預設 / team policy | `auth-and-rbac` § 4.3 / § 4.4 |
| anomaly 偵測框架 + 聚合表 | `rate-limit-and-abuse` § 6（deferred）|
| `abuse_detected` 入帳 | `audit-log` § 5.1 |
| namespace NetworkPolicy / Quota / PSA | `k3s-namespace-isolation` § 5–7 |
| secret 內容 rotation + K8s RBAC | `secrets-management` § 5 / § 6 |
| redactor（secret 不落地）| `error-model` § 9 |

## 11. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| risk_level 標記 | 產 `delete_app` preview | 輸出 `risk_level=critical`；`create_app` preview 為 `normal` |
| risk_level 唯讀不可竄改 | client confirm 傳入偽 `risk_level=normal` | backend 忽略 client 值，仍以 backend 計算判定 |
| 高風險動作強化確認被強制 | MCP confirm `delete_app` 不帶 `confirmation_phrase` | `400 confirmation_phrase_mismatch`（envelope）|
| phrase 與 preview_id 為 AND | 帶正確 phrase 但偽 / 過期 `preview_id` | 仍拒（preview_id 驗證不被 phrase 取代）|
| phrase 不取代 preview_id（反向）| 帶正確 preview_id 但錯 phrase（高風險）| 仍拒（phrase 不被 preview_id 取代）|
| 既有 preview/confirm 契約不破 | 跑 `create_app`（normal）preview→confirm | 行為與 preview-confirm-gate 既有測試一致；無新增必填 |
| irreversible 標紅 | 高風險 preview 含不可逆 side_effect | CLI / MCP 呈現標 `⚠ irreversible` |
| anomaly 觸發入 audit | mock token 持續觸頂 rate-limit | audit `abuse_detected`、`subject_type=token`、`actor=system:anomaly` |
| anomaly v1 不自動阻擋 | 同上 | v1 僅告警；token 未被自動降速 / revoke |
| 地理跳變訊號不誤宣告 | 檢查 v1 程式 | 無 `client_asn` 依賴路徑被啟用（聚合表不存在時 graceful skip）|
| TTL 預設生效 | team 設 `max_pat_ttl_days=30`，簽 PAT 要求 90d | 簽出 token `expires_at = now+30d`（取 min）|
| TTL policy 僅收緊 | team 設 `max_pat_ttl_days=400` | 上限仍夾在全域 365d，不放寬 |
| TTL 不追溯 | 設更嚴 policy 後查既有 token | 既有 token `expires_at` 不變 |
| NetworkPolicy 跨 namespace 被拒 | team A pod → team B service 連線 | 連線失敗（CI 常態跑）|
| default-deny-all 基線存在 | `kubectl get netpol -n team-<slug>` | 含顯式 default-deny-all + 既有 allow 疊加 |
| ResourceQuota 防耗盡 | team 嘗試超 quota 起 pod | 新 pod 被擋；audit 記 `quota_exceeded` |
| at-rest 加密金鑰文件化 | 查 deployment runbook | 明列 EncryptionConfiguration / datastore 加密之實採方案與金鑰所在 |
| 加密金鑰輪替入帳 | 跑一次 at-rest key 輪替 | audit 含 `secret_rotate_start` + `secret_rotate_finalize`（subject=`system:at-rest-key`）|

> AGENTS.md「高風險區域必測」對應：preview/confirm（§ 5）、team 隔離（§ 8）、role/scope（§ 7 token policy）、簽章 / 確認閘門（§ 5.4）皆列必測。

## 12. Open issues

- 高風險動作的 `critical` 級是否採更短 preview TTL：須先補 `preview-confirm-gate` ADR（TTL 為常數，硬規則 9）；v1 不做，以 typed confirmation 替代（§ 5.5）。
- typed confirmation 在純自動化（CI PAT）情境的可用性：CI 無互動輸入；高風險動作於 CI 是否該直接禁止或要 out-of-band 核可，待 `mcp-tool-permissions` 協調。
- token anomaly 的「基線」建模（per-token 歷史 vs 全域門檻）：v1 採固定門檻；歷史基線待 `access_log_aggregate` 上線後評估。
- 地理 / IP 跳變訊號解鎖時程：綁 rate-limit-and-abuse 聚合管線（deferred）。
- team-level security policy 的 schema 落點（`team` 表新欄 vs 獨立 `team_security_policy` 表）：待 `auth-and-rbac` 拍板；涉 migration。
- at-rest 加密採 EncryptionConfiguration vs datastore 卷加密 vs 並用：依實際 K3s datastore 拓樸（接 `postgres-ha-and-dr`）決定；runbook 落地。
- 高風險動作目錄是否納入 `invite_member`（owner 邀請）/ `redeploy`（prod）：v1 不納；依事故回饋擴充。

## 13. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. 高風險動作差異化確認**只能更嚴、不可更鬆**；不得繞過、弱化或取代既有 preview→confirm 後端強制（承 threat-model AG4）。`confirmation_phrase` 與 `preview_id` 為 AND 條件，任一不過即拒。
2. `risk_level` 與 `required_phrase` 為 backend 計算 / 生成之唯讀屬性；confirm 端**不得**採信 client 傳入的 risk_level 或自帶 phrase 來源。
3. 不得在 application 層為任何動作私自縮短 / 變更 preview TTL；TTL 變更須走 `preview-confirm-gate` ADR（承其硬規則 9）。
4. `已具備` 狀態必對應實際 spec / migration / 程式機制；不得把未實作講成已具備（承 plan § 6 規則 1、threat-model § 11 規則 1）。特別地：K8s native Secret 不得宣稱為「加密儲存」；地理 / IP anomaly 訊號在 `access_log_aggregate` 落地前不得宣稱已具備。
5. token anomaly 偵測不得新增獨立偵測迴圈；必復用 `rate-limit-and-abuse` 偵測框架與 `abuse_detected` audit 通道。v1 偵測後**僅告警不自動阻擋**（對齊 rate-limit-and-abuse 硬規則 7）。
6. team security policy 對 TTL **只能收緊不可放寬**；簽發 TTL 取 `min(使用者要求, team cap, 全域上限)`；不得超過 `auth-and-rbac` 全域上限（PAT 365d）。
7. 本 spec **不**重新定義 NetworkPolicy / ResourceQuota / LimitRange / Secret manifest；manifest 單一事實來源為 `k3s-namespace-isolation` 與 `secrets-management`；本 spec 僅盤點、標缺口、加驗證。
8. namespace 跨 team 拒絕（pod→pod、pod→svc）必納入 CI 整合測試常態跑（AGENTS.md team 隔離必測）；不得僅靠人工抽驗。
9. at-rest 加密金鑰**不得**與被加密資料同信任域、不得入 git、不得入 K8s Secret 自身；輪替必入 audit。
10. 任何 token TTL 預設 / 上限調整，必同步更新 `auth-and-rbac` 對應段落，不得兩處漂移。
