# Feature Spec：compliance-framework-mapping

> **狀態**：draft
> **來源**：`docs/trust-and-compliance/plan.md` § 3.1 / § 5.1（P1）/ § 1 框架優先序；消費 `docs/features/threat-model/spec.md` 之威脅清單做控制對應；`docs/0ops-business-plan.md` § 十一（合規/法務風險、資料落地敘事、line 344-347 PII 範圍）
> **適用範圍**：0ops 之合規/資料治理「對外可出示文件」——資料分類、資料盤點與資料流、台灣個資法（PDPA）對應、資料落地控制、SOC2 Type II TSC 控制矩陣、統一保留期矩陣；本 spec 為文件/政策交付，部分項目對應到既有程式機制（標明來源 spec/ADR/migration）
> **對應 Milestone**：Enterprise tier 前置（v2 / 2027 H1）；台灣段（PDPA + 資料落地）可在 design partner 階段先出
> **依賴**：`threat-model`（控制對應需先有威脅清單與資產表）；引用 `audit-log`、`auth-and-rbac`、`secrets-management`、`error-model`、`gitops-render-and-argocd`、`preview-confirm-gate`、`postgres-ha-and-dr`、`reconciler-and-incident`、`rate-limit-and-abuse`、`k3s-namespace-isolation`、`observability-skeleton`
> **讀法**：§ 1 結論 → § 3 狀態判定原則 → § 8 SOC2 矩陣 → § 6 PDPA 表 → § 14 不可違反硬性規則

## 1. 結論（先讀本段）

- 本文件是 0ops 的**單一合規控制對應事實來源**：把既有架構事實翻譯成審計員/採購可出示的「框架 → 0ops 控制 → 狀態 → 證據」矩陣，並補齊資料治理文件（分類、盤點、資料流、保留期）。本 spec **不新增程式能力**；它盤點、對應、釘死狀態。
- **框架優先序（採 plan § 1，釘死）**：台灣個資法（PDPA）/ 資料落地（P0，在地 moat，最低成本立即可主張）→ SOC2 Type II（P1，跨境企業/投資人門票）→ ISO 27001（P3 v3，國際擴張時，本 spec 不展開）。
- **每一條控制狀態三值**：`已具備 / 規劃中 / 不適用`，每條必填、不得留空或模糊（承 plan § 6 規則 3）。狀態判定依 § 3 原則：`已具備` 必對應到實際 spec / ADR / migration / 已 ship 架構事實；**不得宣稱未實作能力為已具備**（承 plan § 6 規則 1，避免合規造假）。
- **可主張的信任地基（plan § 1 四項已 ship 架構事實，本矩陣大量引用）**：① `preview → confirm` 後端強制；② GitOps 唯一真相（commit 可回溯）；③ backend 不跑 LLM（無 prompt injection 攻擊面）；④ `audit_log` 業務行為帳本（redact、13mo 保留、trace_id 串到底）。
- **主要缺口（標 `規劃中`，對應已排程 spec）**：SOC2 A1（可用性，HA/PITR 為 M5 spec）、tamper-evidence / audit export、token anomaly 偵測、SSO/集中撤權、SBOM/image provenance、正式 privacy policy 與 DPA（法務 dependency，§ 13）。
- **DPA + privacy policy 為法務 dependency**：標為待補，不在本 spec 產出（§ 13）。

## 2. 範圍

### 2.1 包含
- 資料分類標準（public / internal / customer / secret 四級）與每級處理規則（§ 4）。
- 資料盤點表 + 資料流圖（蒐集/處理哪些資料、存哪、保留多久、誰能存取；對應 `threat-model` § 4.2 資產表 C4/C11 等）（§ 5）。
- 台灣個資法（PDPA）對應表（蒐集告知、目的限定、當事人權利、跨境傳輸、安全維護義務 → 機制 → 狀態 → 證據）（§ 6）。
- 資料落地控制（self-host vs managed 差異，可驗證）（§ 7）。
- SOC2 Type II Trust Services Criteria 對應矩陣（CC1/CC2/CC5/CC6/CC7/CC8 + A1）（§ 8）。
- 統一保留期矩陣（audit / deploy_run / secret / PII / gitops）（§ 9）。

### 2.2 不包含
- DPA（資料處理協議）與 privacy policy 正式文本：法務 dependency，標待補（§ 13）。
- ISO 27001 逐條 Annex A 控制對應：P3 / v3，本 spec 不展開（僅在 § 8 註記延伸路徑）。
- 威脅清單本身：屬 `threat-model`，本 spec 只消費引用，不維護平行清單。
- 個別控制的實作（append-only / hash chain / export / SSO / SBOM）：屬各自下游 spec，本 spec 只標狀態與引用。
- 滲透測試報告、SBOM 產出物本身：屬後續 P1/P3 spec 與 runbook。

## 3. 狀態判定原則（釘死，供審計可追溯）

> 任何狀態欄只能填三值之一，且依下表判定。違反者 PR 不可合入（§ 14）。

| 狀態 | 判定條件 | 反例（不得標此狀態） |
|---|---|---|
| `已具備` | 對應到 plan § 1 四項已 ship 架構事實，**或** plan § 3 各軸「現況」清單已列、**且**有實際 spec / ADR / migration / 程式機制可驗 | 僅有 spec 設計但屬未來 milestone；僅口頭承諾；僅 business-plan 敘事而無對應機制 |
| `規劃中` | 已有對應 spec 設計或 plan 拆解清單登記，但屬未來 milestone（如 M5 HA/DR）或 plan 缺口清單尚未 ship | 無任何對應 spec / 拆解登記（→ 應入 Open issues 或先補 plan row） |
| `不適用` | 該控制與 0ops 架構/範圍本質無關，附理由 | 用「不適用」迴避實作缺口 |

**判定一致性規則**：

1. `已具備` 的證據欄必填 spec 路徑 / ADR 編號 / migration 編號 / 既有機制名稱之一；無可驗來源者不得標 `已具備`。
2. `規劃中` 的證據欄必填對應下游 spec 路徑（plan § 5.1 拆解清單登記者），並標其優先序（P0–P3）。
3. 本 spec 不得自行宣告某機制「已 ship」；以 plan § 1 / § 3 現況與既有 spec/ADR/migration 為唯一依據。

## 4. 資料分類標準

> 四級分類為 0ops 全系統資料治理基準，對應 `threat-model` § 4.2 資產分級欄（secret / customer / internal）並補 public 級。處理規則為硬性（§ 14 規則 2）。

### 4.1 四級定義與處理規則

| 級別 | 定義 | 可否出現於 log | 可否出現於 audit_log | at-rest 加密 | 傳輸加密 | 保留規則 |
|---|---|---|---|---|---|---|
| **public** | 公開即無損之資料：公開 repo URL、app slug、公開域名、文件 | 可（明文） | 可（明文） | 不要求 | TLS（一般） | 依業務，無下限 |
| **internal** | 內部營運資料，外洩造成有限影響：部署 desired state、deploy_run 狀態、metric label | 可（結構化、必要時 redact） | 可（redact 後） | 建議（DB at-rest） | TLS 強制 | 依 § 9 矩陣（gitops 永久 / deploy_run 90 天） |
| **customer** | 客戶資產與 PII：客戶 repo 原始碼/artifact、github_login、email | 僅 id / 摘要，不落原文 | 僅 id / redacted 欄位 | 強制 | TLS 強制 | PII 對齊 § 9；客戶 source 不長存於控制平面 |
| **secret** | 憑證與金鑰：PAT/device token、GitHub App token、Cloudflare token、app env secret | **禁止明文**（redactor 強制） | **禁止明文**（redactor 強制） | 強制（加密儲存 + token argon2 雜湊） | TLS 強制 | 不保留明文；rotation 兩段；redactor 共用 instance |

### 4.2 分類落地對應機制

| 處理規則 | 對應機制 | 狀態 | 證據 |
|---|---|---|---|
| secret 不落 log/audit/error 明文 | redactor 共用 instance（prefix `secret_`/`token`/`password`/`_signature` → `***`；webhook payload 全文不入） | 已具備 | `error-model` § 9；`audit-log` § 8 |
| secret at-rest 加密 + token argon2 雜湊 | 加密儲存 + argon2；`expires_at` 強制 | 已具備 | `secrets-management`；ADR-0001；migration 00003 |
| customer PII 於 audit 僅存 id / redacted | audit_log `actor_user_id`=UUID；github_login 為 PII 但經 redact 路徑 | 已具備 | `audit-log` § 4.1 / § 8；`threat-model` § 4.2 |
| 分類標籤強制套用於新寫入點 | 新增寫入點時 redactor 套用 + review heuristics | 規劃中 | `security-hardening`（P1，持續驗證新寫入點不漏 redact，承 `threat-model` SE1） |

## 5. 資料盤點與資料流

### 5.1 資料盤點表

> 對應 `threat-model` § 3.1 元件（C1–C11）與 § 4.2 資產表。保留期見 § 9。「誰能存取」依 `auth-and-rbac` role/scope。

| 資料 | 分類 | PII | 蒐集/處理目的 | 所在元件 | 保留期 | 存取者（RBAC） |
|---|---|---|---|---|---|---|
| 使用者 PAT / device token | secret | 否 | 認證 backend 請求 | C1（client config）、C4（雜湊）、C11 | 不保留明文；TTL `expires_at` | 本人（client）；backend 驗章 |
| GitHub App token | secret | 否 | 拉 repo / dispatch build | C11 | rotation 週期 | backend（system） |
| Cloudflare token | secret | 否 | Tunnel / 域名路由 | C11 | rotation 週期 | backend（system） |
| 客戶 repo 原始碼 / artifact | customer | 否 | build / 部署 | C5/C6/build tree（PVC） | build 期暫存；不長存控制平面 | build pipeline（system） |
| 部署 desired state（manifest） | internal | 否 | GitOps 同步 | C7（gitops repo） | 永久（git history） | backend（唯一 writer）；ArgoCD |
| `audit_log` | internal（取證） | 含 PII 欄位 | 業務行為帳本 / 取證 | C4 | 13 個月；`delete_app` 永久 archive | role ≥ admin 全 team；member 限 self（scope `audit:read`） |
| `deploy_run` 歷史 | internal | 否 | 部署狀態與回顧 | C4 | 90 天熱資料 + monthly aggregate 永存 | team 成員（依 scope） |
| github_login | customer（PII） | 是 | 身份顯示 / actor | C4（user_account） | 帳號生命週期；audit 內隨 13mo | team 成員（受限）；本人 |
| email | customer（PII） | 是 | 通知 / 帳號 | C4（user_account） | 帳號生命週期 | 本人；backend（system） |
| app env secrets | secret | 否 | 執行期注入 | C11 | rotation；不保留明文 | app owner/admin（scope 限定） |

### 5.2 資料流圖（落地點視角）

> 衍生自 `threat-model` § 3.2；本圖強調**資料落於何處（at-rest）**與 PII / secret 邊界，不重畫信任邊界（屬 `threat-model` § 3.3）。

```
使用者意圖 ─▶ [C1/C2 client]──(PAT, TLS)──▶ [C3 backend：不跑 LLM]
   寫入：preview ─▶ confirm(preview_id)
        ├─▶ audit_log [C4]        （internal；含 PII 欄位，redact 後落地，13mo）
        └─▶ commit  [C7 gitops]   （internal；desired state，git history 永久）
              └─▶ [C8 ArgoCD] ─▶ [C9 K3s per-team ns]
   build：[C3] ─(App token, 簽章)─▶ [C5 GHA] ─▶ image ─▶ [C6 GHCR]
        client source ─▶ build tree/PVC（customer；暫存，不長存控制平面）
   secret：PAT/App/CF/env  ──加密儲存 + argon2──▶ [C11 secrets store]（secret；明文不落 log/audit）
   對外：使用者請求 ─▶ [C10 Cloudflare TLS/Tunnel] ─▶ [C9 origin]
   PII：github_login / email ──▶ [C4 user_account]（customer/PII；帳號生命週期）
```

**落地點不變式**：secret 級資料只落 C11（加密）與 C4（雜湊參照）；明文永不入 C4 audit/一般欄、log、error envelope（redactor 強制）。customer source 不長存控制平面（build 期暫存於 PVC）。

## 6. 台灣個資法（PDPA）對應表

> 對應《個人資料保護法》核心義務。PII 範圍 v1 僅 github_login + email（business-plan line 347；`threat-model` § 4.2）。每條必標狀態。

| PDPA 義務 | 0ops 機制 | 狀態 | 證據 / 備註 |
|---|---|---|---|
| **蒐集告知**（§ 8 告知義務） | 正式 privacy policy 揭露蒐集項目、目的、期間、權利行使方式 | 規劃中 | privacy policy 為法務 dependency（§ 13）；business-plan line 347 列「明確 privacy policy」待補 |
| **目的限定**（§ 5 目的明確 / § 20 利用範圍） | PII 範圍最小化：v1 僅蒐集 github_login + email，僅用於認證/顯示/通知；無行銷再利用 | 已具備 | `threat-model` § 4.2；business-plan line 347（範圍最小化為既有事實）；**正式目的聲明文本** 屬 § 13 待補 |
| **當事人權利—查詢/閱覽**（§ 3 / § 10） | 本人可經 read API / `query_audit_log` 查自身 actor 紀錄；`get_app` / `list_*` 查自身資源 | 已具備（自助查詢）；正式 DSAR 流程 規劃中 | `audit-log` § 6.2（member 限 self）；`auth-and-rbac`；正式 DSAR 受理流程 → § 13 / `security-hardening` |
| **當事人權利—刪除/停止利用**（§ 3 / § 11） | app/資源刪除走 `delete-app-flow`；audit 對 delete 永久 archive | 規劃中 | 帳號/PII 刪除（user_account + email/github_login 移除）流程未釘定 → Open issues；`delete-app-flow` 僅涵蓋 app 維度 |
| **跨境傳輸**（§ 21） | self-host：客戶自有域名 + Tunnel，0ops 不存客戶資料（不出境）；managed：明確分區 | 已具備（self-host 不出境）；managed 分區 規劃中 | § 7；business-plan line 345；managed 多 region 拓樸 → Open issues |
| **安全維護義務**（§ 27 / 施行細則 § 12） | 加密儲存 + argon2；RBAC + team 隔離；audit_log 取證；redactor；rate-limit/abuse | 已具備 | `secrets-management`、ADR-0001、`audit-log`、`error-model` § 9、`rate-limit-and-abuse`；強化項（anomaly/tamper-evidence）見 § 8 規劃中列 |

## 7. 資料落地控制（self-host vs managed）

> 對應 business-plan line 345「客戶自有域名 + Tunnel 不存客戶資料；managed 版本明確分區」，並轉為**可驗證**之資料類別 × 落地差異表。

### 7.1 落地差異矩陣

| 資料類別 | self-host（客戶自有 infra + 域名 + Tunnel） | managed（0ops 託管） | 可驗證點 |
|---|---|---|---|
| 客戶 repo 原始碼 / artifact（customer） | 留在客戶 GitHub / 客戶 build 環境；0ops 控制平面不長存 | build 期暫存於 0ops PVC（team 子目錄）；不長存 | build tree 生命週期；`app-source-ingestion` PVC 清理 |
| 執行期 workload（customer/internal） | 跑在客戶自有 K3s / 自有域名 origin | 跑在 0ops managed K3s（per-team namespace 隔離） | `k3s-namespace-isolation`；Tunnel hostname 註冊 |
| app env secrets（secret） | 客戶 infra 內 | 0ops C11 加密儲存 | `secrets-management` 加密 + rotation |
| 控制平面 metadata（PII / audit / deploy_run）（customer/internal） | 仍於 0ops 控制平面 C4（認證/稽核必要） | 同左，managed 分區 | C4 為控制平面共用；分區拓樸 → Open issues |
| 對外流量（TLS） | 客戶自有域名 + Tunnel 加密 | 0ops 域名 / Cloudflare for SaaS | ADR-0007；`custom-domain-and-verify` |

### 7.2 落地不變式

1. self-host 之**客戶執行期資料與 app secret 不進入 0ops 控制平面**；控制平面僅持有認證/稽核所需之 metadata（PII 範圍最小化）。
2. managed 之客戶 source 為 build 期暫存，不長存控制平面（§ 5.2 落地點不變式）。
3. 「不存客戶資料」之主張限定於**執行期資料與 secret**；控制平面 metadata（github_login/email/audit）之落地與保留，以 § 6（PDPA）與 § 9（保留矩陣）為準，不得對外宣稱「完全不存任何資料」（避免不可驗證聲明，§ 14 規則 4）。

## 8. SOC2 Type II Trust Services Criteria 對應矩陣

> 涵蓋 Common Criteria（CC1/CC2/CC5/CC6/CC7/CC8）+ Availability（A1）。每條 criteria → 0ops 機制 → 狀態 → 證據。每列必標狀態（§ 14 規則 1）。CC3（風險評估）/CC4（監控）/CC9 與其他 TSC（Confidentiality/Processing Integrity/Privacy）於 v3 SOC2 audit 啟動時補；本輪聚焦採購最常問之 CC + A1。

### 8.1 CC1 控制環境

| 機制 | 狀態 | 證據 |
|---|---|---|
| 貢獻規約 + ADR 決策流程（架構決策可追溯、schema/auth 變更先寫 ADR） | 已具備 | `AGENTS.md`；`docs/adrs/*`；plan § 6 規則 6 |
| 正式資安政策 / 角色職責 / 治理章程文件 | 規劃中 | `security-hardening`（P1）；治理 backlog（plan § 7 / `tasks/todo.md`） |

### 8.2 CC2 溝通與資訊

| 機制 | 狀態 | 證據 |
|---|---|---|
| 副作用對使用者明示：preview 印 `side_effects` + 過期；統一 error envelope | 已具備 | `preview-confirm-gate`；`error-model` |
| 內部威脅/控制資訊單一事實來源（威脅模型 + 本矩陣） | 已具備 | `threat-model`；本 spec |
| 對外漏洞揭露管道（security.txt / responsible disclosure） | 規劃中 | plan § 3.3 缺口；P2 文件 + `.well-known/security.txt` |

### 8.3 CC5 控制活動

| 機制 | 狀態 | 證據 |
|---|---|---|
| `preview → confirm` 兩階段寫入閘門（write tool 無 `preview_id` 直接 4xx） | 已具備 | `preview-confirm-gate`；ADR-0002 |
| RBAC role/scope middleware（router 端宣告） | 已具備 | `auth-and-rbac`；ADR-0001 |
| 兩階段寫入作為防誤刪安全網 | 已具備 | `preview-confirm-gate`；plan § 3.3 現況 |

### 8.4 CC6 邏輯與實體存取

| 機制 | 狀態 | 證據 |
|---|---|---|
| RBAC（owner/admin/member/viewer + scope）+ team 隔離 | 已具備 | `auth-and-rbac`；ADR-0001；`k3s-namespace-isolation` |
| token argon2 雜湊 + 加密儲存 + `expires_at` 強制 | 已具備 | `secrets-management`；migration 00003 |
| redactor：secret/token/webhook payload 不落地 | 已具備 | `error-model` § 9 |
| SSO / SAML 集中身份與離職集中撤權 | 規劃中 | `sso-saml`（P2）；承 `threat-model` AU3 |
| token 使用 anomaly 偵測（外洩後爆炸半徑控制） | 規劃中 | `security-hardening`（P1）；承 `threat-model` AG2/AU1 |

### 8.5 CC7 系統運作與偵測

| 機制 | 狀態 | 證據 |
|---|---|---|
| `audit_log` 業務帳本（寫入/刪除/auth/secret/plan/abuse/reconciler 全入帳） | 已具備 | `audit-log`（plan § 3.2 現況） |
| rate-limit + abuse 偵測（`abuse_detected` 入 audit） | 已具備 | `rate-limit-and-abuse` |
| observability + trace_id 跨界落地 | 已具備 | `observability-skeleton`；`trace-id-end-to-end`；ADR-0006 |
| reconciler 收斂偵測 + incident（`failed_permanently` 入 audit） | 已具備 | `reconciler-and-incident`（plan § 3.2 現況 fallback） |
| audit tamper-evidence（per-row hash chain）+ append-only DB role | 規劃中 | `audit-export-and-integrity`（P0/P1 → ADR-0015）；承 `threat-model` AD1 |
| audit export（CSV/JSON）/ SIEM 串接（審計交付） | 規劃中 | `audit-export-and-integrity`（P1）/ SIEM（P3）；承 `threat-model` AD3 |

### 8.6 CC8 變更管理

| 機制 | 狀態 | 證據 |
|---|---|---|
| GitOps 唯一真相：所有部署狀態回溯到 commit、backend 為唯一 writer | 已具備 | `gitops-render-and-argocd`；ADR-0004 |
| 變更經 preview/confirm + audit（誰於何時改了什麼可查） | 已具備 | `preview-confirm-gate`；`audit-log` |
| ADR 決策流程：schema / auth path 變更先寫 ADR 再實作 | 已具備 | `AGENTS.md`；plan § 6 規則 6 |
| 供應鏈變更管理：SBOM + 依賴掃描 + image provenance/簽章 | 規劃中 | `supply-chain-security`（P1）；承 `threat-model` SC2/SC3 |

### 8.7 A1 可用性（Availability）

| 機制 | 狀態 | 證據 |
|---|---|---|
| reconciler 收斂自我修復（滯留偵測 + 重試，避免 silent 卡死） | 已具備 | `reconciler-and-incident`（plan § 3.2 現況） |
| backend HA（2 replica + leader election + graceful shutdown） | 規劃中 | `backend-ha-leader-election`（M5）；ADR-0008 |
| Postgres main + streaming replica + WAL archive + daily pg_dump + PITR | 規劃中 | `postgres-ha-and-dr`（M5）；ADR-0008；runbooks `postgres-pitr` / `postgres-failover` / `postgres-restore-test` |
| SLO/SLI 定義 + burn-rate alert（可用性目標監控） | 規劃中 | `slo-and-alerting`（M5）；ADR-0006（SLO 99.9%） |

> **ISO 27001（P3 / v3）延伸路徑**：本矩陣之 CC/A1 映射可作為 ISO 27001 Annex A（A.5 政策、A.8 資產管理、A.9 存取控制、A.12 運作安全、A.16 事故管理）之輸入；逐條對應於 v3 國際擴張時另開（plan § 4 P3）。本 spec 不展開。

## 9. 統一保留期矩陣

> 接 `audit-log` § 9。每類資料一個保留期 + 依據 + 狀態。

| 資料類別 | 分類 | 保留期 | 依據 | 狀態 |
|---|---|---|---|---|
| `audit_log` | internal（取證） | 13 個月；`delete_app` 對應永久移 `audit_log_archive` | 合規最小值；partition by month + drop | 已具備（`audit-log` § 9） |
| `deploy_run` | internal | 90 天熱資料 + monthly aggregate 永存 | 部署回顧需求 | 已具備（`docs/0ops-plan-schema.md`；`delete-app-flow`） |
| secret（PAT/App/CF/env） | secret | 不保留明文；TTL `expires_at` / rotation 兩段 | 最小暴露原則 | 已具備（`secrets-management`；migration 00003） |
| PII—audit 內（github_login/email 欄） | customer（PII） | 隨 audit_log 13 個月 | 對齊稽核保留 | 已具備（`audit-log` § 4.1） |
| PII—user_account（github_login/email） | customer（PII） | 帳號生命週期；刪除流程未釘定 | PDPA 刪除權（§ 6） | 規劃中（帳號/PII 刪除流程 → Open issues） |
| gitops desired state | internal | 永久（git history 不可變） | GitOps 唯一真相需可回溯 | 已具備（`gitops-render-and-argocd`；ADR-0004） |

## 10. 檔案結構（文件 vs 既有程式機制）

> 本 spec 為文件/政策交付，無新增 package。下表標明哪些章節是**文件/政策**（本 spec 即落地），哪些**對應到既有程式機制**（引用，不在此實作）。

```
docs/features/compliance-framework-mapping/
└── spec.md                          # 本文件（單一事實來源：分類/盤點/PDPA/落地/SOC2/保留）
```

| 章節 | 性質 | 落地處 |
|---|---|---|
| § 4 資料分類 | 文件/政策 | 本 spec；落地機制引用 `error-model` § 9 redactor、`secrets-management` |
| § 5 資料盤點/資料流 | 文件/政策 | 本 spec；資產來源 `threat-model` § 3/§ 4 |
| § 6 PDPA 對應 | 文件/政策 | 本 spec；privacy policy/DPA 文本 → § 13 法務 |
| § 7 資料落地 | 文件/政策 | 本 spec；機制 `k3s-namespace-isolation`、ADR-0007、`app-source-ingestion` |
| § 8 SOC2 矩陣 | 文件/政策 | 本 spec；每列引用既有 spec/ADR/migration 或下游 spec |
| § 9 保留矩陣 | 文件/政策 | 本 spec；機制 `audit-log` § 9、`0ops-plan-schema.md` |
| `已具備` 列對應之程式機制 | 既有程式 | 各引用 spec/migration，**不在本 spec 修改** |
| `規劃中` 列對應之能力 | 未來程式 | 各下游 spec（plan § 5.1），**不在本 spec 實作** |

## 11. 與其他 spec 接合

| 接合 | spec / 文件 |
|---|---|
| 統籌計畫（本 spec 為其 P1） | `docs/trust-and-compliance/plan.md` § 3.1 / § 5.1 |
| 威脅清單 / 資產表（消費引用） | `docs/features/threat-model/spec.md` § 4.2 / § 5 / § 6 |
| 商業敘事（資料落地 / PII 範圍） | `docs/0ops-business-plan.md` § 十一（line 344-347） |
| audit 帳本 / 保留 / redaction | `audit-log` § 4 / § 8 / § 9 |
| RBAC / team 隔離 / 當事人查詢 | `auth-and-rbac`；ADR-0001 |
| secret 加密 / rotation / TTL | `secrets-management`；migration 00003 |
| redactor 共用 instance | `error-model` § 9 |
| GitOps 變更可追溯 | `gitops-render-and-argocd`；ADR-0004 |
| HA / PITR / DR（A1） | `postgres-ha-and-dr`、`backend-ha-leader-election`、`slo-and-alerting`（M5）；ADR-0008 |
| append-only / tamper-evidence / export | `audit-export-and-integrity`（→ ADR-0015，待寫） |
| SSO / anomaly / SBOM | `sso-saml`、`security-hardening`、`supply-chain-security`（待寫） |

## 12. 驗證準則

> 本 spec 為文件交付；「驗證」指文件完整性與可追溯性，非程式測試。

| 驗證項 | 通過條件 |
|---|---|
| 每條控制必標狀態 | § 4.2 / § 6 / § 8（CC1/CC2/CC5/CC6/CC7/CC8/A1）/ § 9 每列狀態 ∈ {已具備, 規劃中, 不適用}，無空白 |
| `已具備` 可驗 | 每條 `已具備` 證據欄含實際 spec 路徑 / ADR 編號 / migration 編號 / 既有機制（§ 3 規則 1），不得宣稱未實作機制 |
| `規劃中` 無孤兒 | 每條 `規劃中` 對應 plan § 5.1 已登記之下游 spec 並標優先序（§ 3 規則 2） |
| 框架優先序一致 | § 1 / § 2.1 之優先序與 plan § 1（PDPA→SOC2→ISO）一致 |
| 資料分類完整 | § 4.1 涵蓋 public/internal/customer/secret 四級且每級 log/audit/加密/保留規則齊全 |
| 盤點對應資產表 | § 5.1 每筆資料對應 `threat-model` § 4.2 資產（C4/C11 等）且分類/保留/存取齊全 |
| PDPA 五義務覆蓋 | § 6 涵蓋蒐集告知/目的限定/查詢/刪除/跨境/安全維護，每條有機制+狀態+證據 |
| 落地控制可驗證 | § 7 每列有具體可驗證點，且 § 7.2 不變式不含不可驗證之絕對宣稱 |
| SOC2 criteria 覆蓋 | § 8 至少涵蓋 CC1/CC2/CC5/CC6/CC7/CC8 + A1，每 criteria 至少一機制列 |
| 保留矩陣對齊 audit-log | § 9 audit 13mo 與 `audit-log` § 9 一致；deploy_run 90 天與 schema 一致 |
| 法務 dependency 標明 | § 13 DPA + privacy policy 標待補，不在本 spec 產出 |

## 13. 法務 dependency（不在本 spec 產出）

| 文件 | 性質 | 狀態 | 接合 |
|---|---|---|---|
| Privacy Policy（隱私權政策正式文本） | 法務協作 | 待補 | PDPA 蒐集告知（§ 6）；business-plan line 347；plan § 5.1 P2 |
| DPA（資料處理協議範本） | 法務協作 | 待補 | Enterprise 採購（plan § 2.2 § 七）；plan § 5.1 P2 |

> 本 spec 僅標 dependency 與接合點；文本撰寫屬法務工作流，不在本 feature 產出（避免技術 spec 越界產出法律文件）。

## 14. Open issues

- 帳號 / PII 刪除流程（user_account + github_login/email 移除，含 audit 內 PII 之處置）：v1 未釘定；`delete-app-flow` 僅涵蓋 app 維度。對應 PDPA § 11 刪除權，需獨立 spec 或併入 `security-hardening`。
- managed 版本「明確分區」之實際拓樸（台灣 zone vs 多 region）：依 managed cloud 上線時程（plan § 7 Open issue）。
- 個資法盤點是否需外部法律意見：design partner 簽約前確認（plan § 7 Open issue）。
- SOC2 audit 事務所與啟動時點：founder/商業決策（plan § 7）；本矩陣為其前置文件。
- CC3（風險評估）/ CC4（監控）/ CC9 及 Confidentiality/Processing Integrity/Privacy 其餘 TSC：本輪未展開，SOC2 audit 啟動前補。
- ISO 27001 Annex A 逐條對應：P3 / v3，待國際擴張觸發。
- 「目的限定」與「跨境傳輸 managed 分區」之狀態升級為 `已具備`：需 privacy policy 文本（§ 13）與 managed 分區拓樸落地後重評。

## 15. 不可違反的硬性規則

> 違反以下任一項，引用或修改本文件之 PR 不可合入。

1. SOC2 / PDPA / 保留矩陣中**每一條控制必標狀態** `已具備 / 規劃中 / 不適用`，不得留空或模糊（承 plan § 6 規則 3）。
2. **不得宣稱未實作能力為 `已具備`**：`已具備` 必對應實際 spec / ADR / migration / 已 ship 架構事實，證據欄必填可驗來源（承 plan § 6 規則 1，避免合規造假）。
3. 資料分類（§ 4）之 secret 級處理規則為硬性：secret 明文不得入 log / audit / error envelope；customer PII 於 audit 僅存 id / redacted。
4. 對外信任聲明不得包含**不可驗證之絕對句**（如「完全不存任何客戶資料」）；落地主張限定於可驗證範圍（§ 7.2）。
5. 框架優先序固定為 PDPA / 資料落地（P0）→ SOC2 Type II（P1）→ ISO 27001（P3）；不得在本 spec 擅自調整（承 plan § 1）。
6. 本 spec 為合規控制對應單一事實來源；不得在其他 spec 維護平行控制矩陣，只能引用本文件；威脅清單只引用 `threat-model`，不另維護。
7. `規劃中` 之每條控制必對應 plan § 5.1 已登記之下游 spec；新增無對應 spec 之控制前，須先補 plan 拆解 row（承 plan § 6 規則 5）。
8. DPA 與 privacy policy 為法務 dependency，本 spec 只標待補與接合點，不得在本 feature 產出法律文本。
