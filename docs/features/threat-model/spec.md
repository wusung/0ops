# Feature Spec：threat-model

> **狀態**：draft
> **來源**：`docs/trust-and-compliance/plan.md` § 3.3 / § 5.1（P0 第一支）；STRIDE 方法
> **適用範圍**：0ops 系統級威脅模型——元件分解、信任邊界、資產、STRIDE 威脅清單、既有緩解與缺口對應；不含個別 spec 的實作細節
> **對應 Milestone**：P0（純文件，解鎖後續 `compliance-framework-mapping` 控制對應）
> **依賴**：無（盤點性質）；下游 `compliance-framework-mapping`、`security-hardening`、`audit-export-and-integrity` 依賴本文件
> **讀法**：§ 1 結論 → § 5 STRIDE 表 → § 6 缺口彙整

## 1. 結論（先讀本段）

- 本文件是 0ops 的**單一系統級威脅模型**，採 STRIDE。目的不是窮舉所有 CVE，是建立「攻擊面 → 既有緩解 → 缺口 → 對應 spec」的可追溯對應，供 `compliance-framework-mapping`（SOC2/個資法控制對應）與安全 review 引用。
- 0ops 的威脅模型與一般 PaaS 的關鍵差異是**agent 攻擊面**：操作者可能是 LLM，指令來源可能被污染（惡意 repo 內容、惡意 MCP tool 描述）。最高優先威脅是「被污染的 agent 對 production 發出破壞性寫入」。
- **既有架構已壓制最危險的威脅**：
  - `preview → confirm` 後端強制 → 任何寫入/刪除都有人類閘門，prompt injection 無法靜默生效。
  - Backend 不跑 LLM → 後端核心無 prompt injection 攻擊面。
  - GitOps 唯一真相 + audit_log → 篡改/抵賴可被偵測與回溯。
  - Team 隔離 + RBAC scope → 越權與資訊揭露受限。
- **主要殘留缺口**（→ 後續 spec）：audit 可被 DB 權限者竄改（→ append-only）、無 tamper-evidence、token 外洩後的爆炸半徑控制（scope/TTL 已有但無 anomaly 偵測）、supply chain provenance 未證明、無正式漏洞揭露管道。

## 2. 範圍

### 2.1 包含
- 系統元件分解與信任邊界（§ 3、§ 4）。
- 資產清單與敏感度分級（§ 4.2）。
- STRIDE 六類威脅，依攻擊面分組（§ 5）。
- 每條威脅的既有緩解、缺口、對應下游 spec（§ 5、§ 6）。
- 殘餘風險與明示接受項（§ 7）。

### 2.2 不包含
- 個別緩解的實作細節（屬各自 spec：`audit-export-and-integrity`、`security-hardening`、`sso-saml`）。
- 基礎設施 CVE patching 流程（屬 ops runbook）。
- 滲透測試報告（屬後續 P3 runbook）。
- 合規框架的逐條控制對應（屬 `compliance-framework-mapping`）。

## 3. 系統分解（Components & Data Flow）

### 3.1 元件

| # | 元件 | 位置 / 信任層級 | 說明 |
|---|---|---|---|
| C1 | AI CLI（claude code / codex）+ `0ops-mcp` | **使用者機器**；半信任 | stdio MCP server，持有使用者 PAT；指令可能由 LLM 生成 |
| C2 | `0ops` 人類 CLI | 使用者機器；半信任 | 互動式 preview/confirm |
| C3 | `0ops-server` backend | **信任核心**；受控 | REST/SSE，不跑 LLM；強制 preview/confirm |
| C4 | PostgreSQL（HA） | 信任核心 | tokens、apps、audit_log、secrets refs |
| C5 | GitHub App + GitHub Actions | 外部；半信任 | 拉 repo、`pack build`、push image |
| C6 | GHCR | 外部；半信任 | image registry |
| C7 | `0ops-gitops` repo | 受控；唯一真相 | desired state（manifests + commits） |
| C8 | ArgoCD | 信任核心 | 同步 gitops → K3s |
| C9 | K3s cluster（per-team namespace） | 信任核心 | runtime workloads |
| C10 | Cloudflare Tunnel + edge | 外部邊界 | TLS 終止、對外路由 |
| C11 | Secrets store（加密 token） | 信任核心 | argon2 雜湊 + 加密儲存 |

### 3.2 主要資料流

```
使用者意圖 → [C1/C2] → (auth token) → [C3 backend]
  寫入路徑：preview → confirm(preview_id) → audit_log[C4]
                                          → commit [C7] → [C8 ArgoCD] → [C9 K3s]
  build：[C3] → workflow_dispatch → [C5 GHA] → image → [C6 GHCR] → callback(簽章) → [C3]
  對外：使用者請求 → [C10 Cloudflare] → [C9 origin]
  webhook：GitHub push → (簽章) → [C3] → redeploy
```

### 3.3 信任邊界（trust boundary）

| TB | 邊界 | 跨界內容 | 主要威脅類別 |
|---|---|---|---|
| TB1 | 使用者機器 ↔ backend | auth token、tool call、preview/confirm | S, T, E（偽冒、竄改、提權） |
| TB2 | backend ↔ GitHub（App / webhook） | App token、webhook payload + 簽章 | S, T, R |
| TB3 | backend ↔ gitops repo | commit 寫入權 | T, R, E |
| TB4 | GHA build ↔ backend callback | image digest + 簽章 | T, S（供應鏈） |
| TB5 | tenant ↔ tenant | team_id 範圍查詢 | I, E（越權、資訊揭露） |
| TB6 | backend ↔ Postgres | DML 權限 | T, R（含 audit 竄改） |
| TB7 | Cloudflare edge ↔ origin | TLS 流量、hostname 路由 | S, D, I |
| TB8 | LLM 語意層 ↔ 0ops tool（agent 特有） | 自然語言意圖 → tool call | T, E（污染指令→破壞性操作） |

## 4. 資產與敏感度

### 4.1 攻擊者模型

| 攻擊者 | 能力 | 動機 |
|---|---|---|
| A1 被污染的 agent | 可發 tool call；指令來源被惡意 repo / tool 描述污染（prompt injection） | 破壞 production、外洩資料 |
| A2 外部未授權者 | 網路可達 API / 對外域名 | 越權存取、資料竊取、DoS |
| A3 越權租戶 | 合法 team A 成員，企圖碰 team B | 橫向移動、資訊揭露 |
| A4 內部高權限者 / DB 存取者 | 持 DB 連線或 infra 權限 | 竄改/刪除 audit、抵賴 |
| A5 供應鏈攻擊者 | 污染依賴、build 環境、image | 植入後門 |

### 4.2 資產分級

| 資產 | 分類 | 敏感度 | 所在 |
|---|---|---|---|
| 使用者 PAT / device token | secret | 極高 | C1 config、C4（雜湊）、C11 |
| GitHub App token | secret | 極高 | C11 |
| Cloudflare token | secret | 極高 | C11 |
| 客戶 repo 原始碼 / artifact | customer | 高 | C5/C6/build tree |
| 部署 desired state | internal | 中 | C7 |
| `audit_log` | internal（取證） | 高（完整性） | C4 |
| PII（github_login、email） | customer | 中（個資法/GDPR） | C4 |
| App secrets（env） | secret | 極高 | C11 |

## 5. STRIDE 威脅分析

> 格式：威脅 → STRIDE 類別 → 攻擊面(TB) → 既有緩解 → 缺口 → 對應 spec。
> 風險評級：H/M/L（影響 × 可能性）。

### 5.1 Agent / MCP 特有（最高優先）

| ID | 威脅 | STRIDE | TB | 既有緩解 | 缺口 | 風險 | 對應 |
|---|---|---|---|---|---|---|---|
| AG1 | prompt injection 經 MCP（惡意 repo README / tool 描述）誘使 agent 對 production 發破壞性寫入/刪除 | T,E | TB8 | **preview→confirm 後端強制**；write tool 無 `preview_id` 直接 4xx；backend 不跑 LLM；preview 印 side_effects + 過期 | 人類確認疲勞（盲按 confirm）；無「高風險動作」差異化告警 | **H** | preview-confirm-gate（既有）；`security-hardening` 補高風險動作二次確認 |
| AG2 | 被污染 agent 讀取 MCP config 竊取 PAT 並外傳 | I,S | TB1 | scoped token + TTL；audit 記 token 使用；redactor 不落明文 | 無 token 使用 anomaly 偵測；外洩後僅靠手動 revoke | **M** | `security-hardening`（anomaly + 短 TTL 預設）；audit-log（既有使用記錄） |
| AG3 | agent 被誘導對「錯誤 team/app」操作（confused deputy） | E | TB8,TB5 | preview 顯示 subject；team scope 強制 | preview 摘要可能被 agent 略過呈現給人類 | M | preview-confirm-gate；MCP host 呈現規約 |
| AG4 | agent 嘗試自行偽造 confirm 繞過 preview | E | TB1 | backend 驗 `preview_id` 存在/未過期/未消費；單次性 | — | L（已壓制） | preview-confirm-gate（既有硬規則） |

### 5.2 認證 / Token（TB1, TB2）

| ID | 威脅 | STRIDE | 既有緩解 | 缺口 | 風險 | 對應 |
|---|---|---|---|---|---|---|
| AU1 | PAT / device token 竊取後重放 | S | argon2 雜湊儲存；`expires_at` 強制（migration 00003）；scope 限定 | 無 IP/裝置綁定；無 anomaly | M | `security-hardening` |
| AU2 | device flow 授權碼攔截 | S,I | 短時效 code + PKCE 風格交換 | — | L | auth-login-flow（既有） |
| AU3 | 缺 SSO 導致企業無集中撤權 | S,E | — | 無 SAML/OIDC，離職員工 token 需逐一 revoke | M | `sso-saml`（P2） |
| AU4 | webhook 偽造（假 GitHub push 觸發 redeploy） | S,T | webhook 簽章驗證（HMAC） | 簽章金鑰輪替流程未文件化 | M | secrets-management；webhook-and-redeploy（既有驗章） |
| AU5 | GHA callback 偽造（假 image digest） | S,T | callback 簽章驗證 | 同上輪替 | M | build-pipeline-and-callback（既有） |

### 5.3 Backend / API（TB1, TB6）

| ID | 威脅 | STRIDE | 既有緩解 | 缺口 | 風險 | 對應 |
|---|---|---|---|---|---|---|
| BE1 | 越權 API 呼叫（提權至 admin 動作） | E | RBAC role + scope middleware；router 端宣告 | scope 矩陣需全整合測試覆蓋（AGENTS.md 列必測） | M | auth-and-rbac |
| BE2 | 注入（SQL / 參數） | T | 參數化查詢；DTO 驗證 | — | L | shared-dto-and-contract |
| BE3 | DoS / 資源耗盡（大量 preview / create） | D | rate-limit + abuse 偵測；abuse_detected 入 audit | 全域配額 vs per-team 上限需明確 | M | rate-limit-and-abuse |
| BE4 | 錯誤訊息洩漏內部細節 | I | error-model 統一 envelope；redactor § 9 | — | L | error-model |

### 5.4 GitOps / 供應鏈（TB3, TB4）

| ID | 威脅 | STRIDE | 既有緩解 | 缺口 | 風險 | 對應 |
|---|---|---|---|---|---|---|
| SC1 | gitops repo 被惡意 commit（繞過 backend 改 desired state） | T,E | backend 為唯一 writer；commit 可回溯 | repo write 權限收斂與分支保護未文件化 | M | `supply-chain-security`；gitops-render-and-argocd |
| SC2 | 依賴/build 環境污染植入後門 image | T | self-hosted runner 隔離 | 無 SBOM、無 image provenance 證明、無依賴掃描 | **H** | `supply-chain-security`（SBOM + SLSA provenance + scan） |
| SC3 | image digest 在 GHCR↔ArgoCD 間被替換 | T | callback 帶 digest；GitOps pin digest | 無 image 簽章驗證（cosign/policy） | M | `supply-chain-security` |
| SC4 | buildpack 偵測誤判導致非預期 runtime | T | preview 顯示偵測結果 | — | L | app-source-ingestion |

### 5.5 租戶隔離（TB5）

| ID | 威脅 | STRIDE | 既有緩解 | 缺口 | 風險 | 對應 |
|---|---|---|---|---|---|---|
| TN1 | team A 讀/寫 team B 資源 | I,E | 全查詢帶 team_id 範圍；ADR-0001 多租戶邊界 | 需 RBAC 全矩陣整合測試持續驗證 | M | auth-and-rbac；k3s-namespace-isolation |
| TN2 | namespace 逃逸（workload 影響他 team） | E | K3s per-team namespace 隔離 | NetworkPolicy / resource quota 強化程度待盤點 | M | `security-hardening`；k3s-namespace-isolation |
| TN3 | 跨 team audit 資訊揭露 | I | audit query RBAC（admin 全 team / member 限 self） | — | L | audit-log（既有） |

### 5.6 Secrets（TB6, C11）

| ID | 威脅 | STRIDE | 既有緩解 | 缺口 | 風險 | 對應 |
|---|---|---|---|---|---|---|
| SE1 | secret 明文落入 log / audit / error | I | redactor 共用 instance（error-model § 9）；webhook payload 全文不入 | 需持續驗證新寫入點不漏 redact | M | secrets-management；audit-log |
| SE2 | secret at-rest 外洩 | I | 加密儲存 + argon2 | 加密金鑰管理 / 輪替策略待文件化 | M | secrets-management |
| SE3 | secret rotation 期間舊值仍可用 | T,E | rotation start/finalize 兩段（secrets-management § 9） | — | L | secrets-management |

### 5.7 完整性 / 抵賴（TB6 — audit）

| ID | 威脅 | STRIDE | 既有緩解 | 缺口 | 風險 | 對應 |
|---|---|---|---|---|---|---|
| AD1 | 高權限者竄改/刪除 audit_log row 掩蓋行為 | R,T | audit 全入帳；redact；13mo 保留；delete_app 永久 archive | **app DB role 仍可 UPDATE/DELETE audit_log**；無 tamper-evidence | **H** | `audit-export-and-integrity`（append-only + hash chain）→ ADR-0015 |
| AD2 | audit 寫入失敗導致行為無紀錄（抵賴） | R | 寫失敗 log warn + 進 reconciliation_job 重寫（不 silent） | 「無漏」對外保證與驗證缺 | M | audit-log（既有 fallback）；`audit-export-and-integrity` § completeness |
| AD3 | 缺對外可出示證據（審計交付） | R | query API + CLI | 無 export（CSV/JSON）、無 SIEM 串接 | M | `audit-export-and-integrity`；`audit-event-notification` |

### 5.8 基礎設施 / 邊界（TB7）

| ID | 威脅 | STRIDE | 既有緩解 | 缺口 | 風險 | 對應 |
|---|---|---|---|---|---|---|
| IN1 | 對外 DoS | D | Cloudflare edge 防護；rate-limit | origin 直連保護程度待盤點 | M | rate-limit-and-abuse |
| IN2 | hostname 路由錯置（A 域名打到 B origin） | S,I | Tunnel hostname 註冊 + DNS verify | — | L | custom-domain-and-verify |
| IN3 | 中間人（origin 段明文） | I,T | Cloudflare TLS；Tunnel 加密 | — | L | ADR-0007 |

## 6. 缺口彙整 → 下游 spec

| 高優先缺口 | STRIDE ID | 對應 spec | 計畫優先 |
|---|---|---|---|
| audit 非 append-only、可被竄改 | AD1 | `audit-export-and-integrity`（→ ADR-0015） | **P0** |
| 無 SBOM / provenance / 依賴掃描 | SC2, SC3 | `supply-chain-security` | P1 |
| 無 token anomaly 偵測、高風險動作無差異化確認 | AG1, AG2, AU1 | `security-hardening` | P1 |
| 無集中撤權（SSO） | AU3 | `sso-saml` | P2 |
| 無對外 audit export / 通知 | AD3 | `audit-export-and-integrity` / `audit-event-notification` | P1/P2 |
| namespace 隔離強化程度未盤點 | TN2 | `security-hardening` / k3s-namespace-isolation | P1 |

## 7. 殘餘風險與明示接受

| 殘餘風險 | 為何接受（v1/v2） | 重新評估觸發 |
|---|---|---|
| confirm 疲勞盲按（AG1 殘餘） | 人類閘門仍優於無閘門；高風險動作差異化確認列 P1 | 出現誤刪 production 事件 |
| token 外洩後爆炸半徑（AG2/AU1 殘餘） | scope + TTL 已壓制；anomaly 偵測列 P1 | enterprise design partner 要求 |
| 無 image 簽章驗證（SC3 殘餘） | self-hosted runner 隔離 + digest pin；cosign policy 列 P1 | 供應鏈事件或 SOC2 要求 |
| webhook/callback 金鑰輪替未自動化（AU4/AU5） | 簽章驗證已防偽造；輪替為營運強化 | 金鑰疑似外洩 |

## 8. 驗證準則

> 本 spec 為文件交付；「驗證」指文件完整性與可追溯性，非程式測試。

| 驗證項 | 通過條件 |
|---|---|
| 元件覆蓋 | § 3.1 涵蓋所有 binary + 外部依賴 + 信任核心元件 |
| 信任邊界覆蓋 | 每條 TB 至少對應一條 STRIDE 威脅 |
| Agent 攻擊面 | § 5.1 涵蓋 prompt injection、token 外洩、preview 繞過、confused deputy（plan § 6 規則 4） |
| 缺口可追溯 | 每條「缺口」非空者對應一支下游 spec（§ 6 無孤兒缺口） |
| 既有緩解可驗 | 每條「既有緩解」對應到實際 spec / ADR / migration，不得宣稱未實作機制 |
| 風險評級 | 每條威脅有 H/M/L 且 H 級必有對應 P0/P1 spec |

## 9. 與其他 spec 接合

| 接合 | spec |
|---|---|
| 統籌計畫（本 spec 為其 P0 第一支） | `docs/trust-and-compliance/plan.md` § 5.1 |
| 控制對應矩陣（消費本威脅清單） | `compliance-framework-mapping`（待寫） |
| append-only + tamper-evidence | `audit-export-and-integrity`（待寫，→ ADR-0015） |
| 安全強化緩解 | `security-hardening`（待寫） |
| 供應鏈緩解 | `supply-chain-security`（待寫） |
| preview/confirm 閘門 | `preview-confirm-gate` |
| RBAC / team 隔離 | `auth-and-rbac`、ADR-0001 |
| redactor | `error-model` § 9 |
| 既有稽核帳本 | `audit-log` |

## 10. Open issues

- prompt injection 的「高風險動作」清單界線（哪些動作需二次/強化確認）：`security-hardening` 決定。
- token anomaly 偵測的訊號（地理、頻率、scope 異常）：`security-hardening` 與 rate-limit-and-abuse 協調。
- image 簽章採 cosign keyless vs key-based：`supply-chain-security` 決定。
- gitops repo 分支保護與 write 收斂的具體設定：`supply-chain-security` / runbook。
- 是否需要 DREAD / 量化風險評分取代 H/M/L：v1 採 H/M/L；SOC2 audit 時若要求量化再升級。

## 11. 不可違反的硬性規則

> 違反任一項，引用本文件的 PR 不可合入。

1. 每條「既有緩解」必對應到實際 spec / ADR / migration / 程式機制；不得宣稱未實作的緩解（避免合規造假，承 plan § 6 規則 1）。
2. agent 特有攻擊面（§ 5.1）為本模型一級對象，任何 STRIDE 修訂不得移除或弱化 AG1–AG4。
3. 每條 H 級威脅必對應到已排程的 P0/P1 spec；新增 H 級威脅而無對應 spec 時，須同步更新 `plan.md` § 5.1。
4. 缺口（§ 5 缺口欄）不得留「無對應 spec」的孤兒；新缺口須在 § 6 與 plan 拆解清單同步登記。
5. 本文件為威脅模型單一事實來源；其他 spec 不得各自維護平行威脅清單，只能引用本文件。
