# 0ops Trust 計畫：Compliance / Audit / Security

> **狀態**：plan（統籌路線圖 + 拆解清單）
> **日期**：2026-06-28
> **層級**：跨切面（cross-cutting）；非單一 feature，與 `docs/0ops-business-plan.md`、`docs/0ops-plan.md` 同根層級
> **對應**：`docs/0ops-business-plan.md`（商業定位）、`docs/features/audit-log/spec.md`（既有稽核基底）、`docs/adrs/0001`/`0002`/`0006`/`0007`/`0011`
> **讀法**：先讀 § 1 結論 → § 3 三軸路線圖 → § 5 拆解清單與下一步

---

## 1. 結論（先讀本段）

- 0ops 的 Compliance / Audit / Security **不是事後補的 checkbox，是架構先天屬性**。計畫的工作不是「補功能」，是把既有架構事實**翻譯成 enterprise 信任敘事**，再補齊「對外可出示」的缺口。
- 四個既有架構事實構成信任地基（已 ship，非規劃）：
  1. **`preview → confirm` 後端強制**：write tool 無 `preview_id` 直接 4xx，agent 無法繞過沉默副作用（`docs/features/preview-confirm-gate/spec.md`）。
  2. **GitOps 為唯一真相**：所有部署狀態可回溯到 Git commit，無黑盒（ADR-0004、`gitops-render-and-argocd`）。
  3. **Backend 不跑 LLM**：無 prompt injection 攻擊面；agent 邏輯在使用者端 CLI（`docs/0ops-plan.md` § Runtime）。
  4. **`audit_log` 業務行為帳本**：寫入/刪除/auth/secret/plan 全入帳、redact 後落地、13 個月保留、trace_id 串到底（`docs/features/audit-log/spec.md`）。
- **缺口集中在「對外可出示」**：既有機制是給內部與 reconciler 用的；要賣進 enterprise，需要 export、完整性證明(tamper-evidence)、框架對應矩陣、威脅模型、SSO、漏洞揭露政策這些「審計員/採購會問」的可交付物。
- **框架優先序（本計畫採用）**：台灣資料落地 / 個資法（在地 moat，最低成本，立即可主張）→ SOC2 Type II（跨境企業/投資人門票）→ ISO 27001（v3，國際擴張時）。
- **本輪交付**：僅本 `plan.md`（統籌路線圖 + 拆解清單 + 餵回 business-plan 清單）。子 feature spec 與 business-plan 實際修改為後續各自獨立 PR。

---

## 2. 商業定位層（Part A：餵回 `docs/0ops-business-plan.md`）

> 本輪**不直接改** business-plan；只在此列出待餵回清單，保持提交單一目的。

### 2.1 核心論述：信任是架構級 moat，不是 feature checklist

競品（Vercel / Railway / Render）讓 agent 操作只能走 **裸 API key 或 web scraping**——權限粒度粗、副作用不可預測、無稽核。0ops 的差異不在「也有 audit log」，而在**信任模型內建於寫入路徑**：每一次 production 變更都經 preview→confirm、落 audit_log、回溯到 Git commit。這是 agent 時代 enterprise 採購的核心顧慮（「我怎麼知道 AI 對我的 production 做了什麼、能不能擋、能不能查」），而競品的 Web-UI-first 架構天生答不了。

### 2.2 待餵回 business-plan 的修改清單

| 位置 | 修改 | 理由 |
|---|---|---|
| § 一 核心差異化表（line 24-30） | 新增「信任 / 可稽核性」維度 row：0ops=寫入路徑內建稽核+preview 攔截；競品=裸 API key 無稽核 | 目前差異化表缺信任維度，這是 enterprise 最在意項 |
| § 五 護城河建構順序（line 160） | 補一條：「架構級信任（preview/confirm + GitOps + audit）難以被 Web-UI-first 競品事後補上」 | moat 敘事缺架構不可逆性論證 |
| § 七 收入結構 Enterprise（line 210） | 「合規與稽核」展開為具體承諾：資料落地選項、audit export、SOC2 roadmap、DPA、SSO | 目前僅一行字，無法支撐 $30K+ 定價 |
| § 十一 合規/法務風險（line 344-347） | 從「風險緩解」升級為「主動信任資產」：列出已具備的控制（argon2、加密儲存、稽核日誌、13mo 保留）對應到框架 | 既有內容偏防守；應轉為賣點 |
| 新增附錄 D | 「信任與合規一頁表」：框架 → 0ops 控制 → 狀態（已具備/規劃中），供 design partner / 投資人 due diligence | 採購與 DD 會直接問框架對應 |

---

## 3. 產品能力路線圖（Part B：三軸）

每軸格式：**現況（已 ship）→ 缺口 → 待補能力 → 優先序 → 對應 milestone / 新 spec**。

### 3.1 Compliant（合規）

**現況**
- 13 個月 audit 保留（合規最小值，`audit-log` spec § 9）。
- 資料落地敘事：客戶自有域名 + Cloudflare Tunnel 不存客戶資料；managed 版本分區（business-plan line 345）。
- PII 範圍小：v1 僅蒐集 GitHub login + email（business-plan line 347）。
- Token 加密儲存 + argon2 雜湊（`docs/0ops-plan-schema.md`、ADR-0001）。

**缺口**
- 無框架對應矩陣（審計員/採購要的 control mapping）。
- 無正式資料落地控制文件（哪些資料、存哪、保留多久、誰能存取）。
- 無 DPA（資料處理協議）範本、無 privacy policy 正式版。
- 個資法（台灣 PDPA）對應未盤點。

**待補能力**
| 能力 | 優先 | 對應產出 |
|---|---|---|
| SOC2 Type II CC 控制 ↔ 0ops 機制對應矩陣 | P1 | new spec `compliance-framework-mapping` |
| 台灣個資法 / 資料落地控制盤點 + 資料流圖 | P0 | 同上 spec § 台灣段 |
| 保留期政策統一文件（audit 13mo / deploy_run / secret / PII） | P1 | `compliance-framework-mapping` § 保留矩陣 |
| DPA + privacy policy 範本 | P2 | 法務協作，spec 標 dependency |
| 資料分類（public / internal / customer / secret）標準 | P1 | spec § 資料分類 |

**對應 milestone**：Enterprise tier 前置（v2 / 2027 H1）；台灣段可在 design partner 階段先出。

### 3.2 Audit（稽核）

**現況**（`docs/features/audit-log/spec.md` 已完整）
- 業務帳本：寫入/刪除 preview+confirm、auth、github install、plan change、secret rotation、abuse、reconciler failed 全入帳。
- 結構：`{actor, subject, action, args, result, preview_id, trace_id, source, outcome, http_status}`，redact 後寫入。
- Partition by month + 13mo drop；`delete_app` 永久移 `audit_log_archive`。
- Query API + CLI (`0ops audit list/get`) + MCP `query_audit_log`，RBAC `audit:read`。
- trace_id 跨 5 段鏈路落地（已 e2e 驗證，見 `tasks/todo.md` trace_id 段）。

**缺口（對外可出示）**
- 無 export（CSV/JSON dump）：`audit-log` spec § 14 列 v1.1 未做。審計交付需要。
- 無完整性證明：audit row 可被有 DB 權限者竄改而無痕跡。SOC2/取證需 tamper-evidence。
- 無對外通知：重要事件（delete_app）無 webhook/email（spec § 14 列 v1.1）。
- 無 SIEM 串接：enterprise 要把 audit 餵進自家 SIEM（Splunk/Datadog）。
- completeness 保證：寫入失敗 fallback 已設計（reconciliation_job 重寫）但無「保證無漏」的對外聲明與驗證。

**待補能力**
| 能力 | 優先 | 對應產出 |
|---|---|---|
| Audit export（CSV/JSON，RBAC + 範圍限定） | P1 | new spec `audit-export-and-integrity` § export |
| Tamper-evidence（per-row hash chain，prev_hash 串接） | P1 | 同 spec § 完整性 |
| Append-only 強化（DB role 撤 UPDATE/DELETE on audit_log） | P0 | 同 spec § append-only；低成本高信任 |
| 重要事件對外 webhook（delete_app / token_revoke / abuse） | P2 | new spec `audit-event-notification` |
| SIEM 串接（syslog / JSON push） | P3 | v3；spec 標 future |

**對應 milestone**：append-only + export 可在 v2 早期；hash chain 與 SIEM 為 enterprise GA 前。

### 3.3 Security（安全）

**現況**
- secret 加密儲存 + rotation 流程（`docs/features/secrets-management/spec.md`）。
- RBAC：owner/admin/member/viewer + scope（`audit:read` 等），team 隔離（ADR-0001、`auth-and-rbac`）。
- Webhook/callback 簽章驗證（AGENTS.md 列高風險必測項）。
- Rate limit + abuse 偵測（`rate-limit-and-abuse`）。
- Redactor 共用 instance（`error-model` § 9）：secret/token/webhook payload 不落地。
- K3s namespace 隔離（`k3s-namespace-isolation`）。
- 兩階段寫入 = 防誤刪安全網。

**缺口**
- 無正式威脅模型（STRIDE）文件：審計與安全 review 第一個要的。
- 無 SBOM / supply chain 證明：image provenance、依賴掃描未文件化。
- 無漏洞揭露政策（security.txt / responsible disclosure）。
- 無滲透測試紀錄或計畫。
- 無 SSO / SAML：enterprise 採購硬需求（business-plan Team tier 已列 SSO 但無 spec）。
- 無安全強化 baseline 盤點（現有控制 vs 待補的 checklist）。

**待補能力**
| 能力 | 優先 | 對應產出 |
|---|---|---|
| 威脅模型（STRIDE，涵蓋 agent 攻擊面 / token / GitOps repo / tunnel） | P0 | new spec `threat-model` |
| 安全強化 baseline checklist（現況盤點 + 缺口） | P1 | `threat-model` § baseline 或獨立 `security-hardening` |
| SBOM + 依賴掃描 + image provenance | P1 | new spec `supply-chain-security`（接 CI release pipeline） |
| 漏洞揭露政策 + security.txt | P2 | 文件 + repo `.well-known/security.txt` |
| SSO / SAML（OIDC IdP 整合） | P2 | new spec `sso-saml`（Team/Enterprise tier 解鎖） |
| 滲透測試計畫（範圍 + 週期） | P3 | runbook |

**對應 milestone**：威脅模型立即可寫（純文件）；SSO 隨 Enterprise tier；supply-chain 接既有 CI。

---

## 4. 跨軸依賴與排序

```
P0（立即，純文件/低成本，最高信任槓桿）
  ├─ threat-model（STRIDE）                    ← 安全 review 入口
  ├─ audit append-only（撤 DB UPDATE/DELETE）   ← 低成本高信任
  └─ 台灣個資法 / 資料落地盤點 + 資料流圖        ← 在地 moat，design partner 可用

P1（v2 早期，enterprise 前置）
  ├─ compliance-framework-mapping（SOC2 CC matrix）
  ├─ audit-export-and-integrity（export + hash chain）
  ├─ security-hardening baseline
  └─ supply-chain-security（SBOM + provenance）

P2（Enterprise tier 解鎖）
  ├─ sso-saml
  ├─ audit-event-notification（webhook）
  └─ DPA / privacy policy（法務協作）

P3（v3 / 國際擴張）
  ├─ ISO 27001 對應
  ├─ SIEM 串接
  └─ 滲透測試週期化
```

**關鍵依賴**
- `compliance-framework-mapping` 依賴 `threat-model`（控制對應需先有威脅清單）。
- `audit-export-and-integrity` 依賴既有 `audit-log` schema（append-only 改造須先於 hash chain）。
- `sso-saml` 依賴 `auth-and-rbac` 現有 role/scope 模型。
- SOC2 audit 啟動前須先有 P0+P1 全部文件 + 至少一輪 internal review。

---

## 5. 拆解清單與下一步（Part C）

### 5.1 要新增的 feature specs（各自獨立 PR，走 AGENTS.md feature 流程）

| 新 spec | 路徑 | 優先 | 依賴 | 是否需新 ADR |
|---|---|---|---|---|
| `threat-model` | `docs/features/threat-model/spec.md` | P0 | 無 | 否（盤點性質） |
| `audit-export-and-integrity` | `docs/features/audit-export-and-integrity/spec.md` | P0/P1 | `audit-log` | 是（append-only + hash chain 為 schema/安全決策 → ADR-0015） |
| `compliance-framework-mapping` | `docs/features/compliance-framework-mapping/spec.md` | P1 | `threat-model` | 否（對應文件） |
| `security-hardening` | `docs/features/security-hardening/spec.md` | P1 | `threat-model` | 視盤點結果 |
| `supply-chain-security` | `docs/features/supply-chain-security/spec.md` | P1 | CI release pipeline | 視是否改 release 流程 |
| `sso-saml` | `docs/features/sso-saml/spec.md` | P2 | `auth-and-rbac` | 是（新 auth path → ADR-0016） |
| `audit-event-notification` | `docs/features/audit-event-notification/spec.md` | P2 | `audit-log` | 否 |

### 5.2 business-plan 餵回（獨立 docs PR）

依 § 2.2 清單修改 `docs/0ops-business-plan.md`；單一目的提交（`docs: add trust dimension to business plan`）。

### 5.3 立即下一步（建議順序）

1. 本 `plan.md` 合入（建立單一事實來源）。
2. P0 第一支：`threat-model` spec（純文件，解鎖後續控制對應）。
3. P0 第二支：audit `append-only` 改造（撤 DB 寫權限 + migration + 測試；低成本高信任）。
4. business-plan § 2.2 餵回（趕 design partner / 投資人材料）。
5. P1 依 § 4 排序逐支推進。

---

## 6. 不可違反的硬性規則（本計畫範圍）

> 違反任一項，相關 PR 不可合入。

1. 任何「對外信任聲明」必對應到**可驗證的既有控制或已排程的 spec**；不得宣稱未實作的能力（避免合規造假）。
2. audit `append-only` 一旦上線，application DB role 不得保有 `audit_log` 的 UPDATE/DELETE 權限。
3. 框架對應矩陣（SOC2/個資法）每一條控制必標狀態：`已具備 / 規劃中 / 不適用`，不得留空或模糊。
4. 威脅模型必涵蓋 agent 特有攻擊面（prompt injection 經 MCP、token 外洩、preview 繞過嘗試），不得只寫傳統 web 威脅。
5. 子 feature spec 動工前，本 `plan.md` 的對應 row 須先存在（plan 為拆解單一事實來源）。
6. 涉及 schema / auth path 變更（append-only、hash chain、SSO）必先寫對應 ADR 再實作。

---

## 7. Open issues

- SOC2 audit 由哪家事務所、何時啟動：屬 founder/商業決策（接 `tasks/todo.md` 治理 backlog）。
- hash chain 的 anchor 策略（純 DB 內 prev_hash vs 定期外部 anchor 到 transparency log）：`audit-export-and-integrity` spec 決定。
- SSO 先支援哪些 IdP（Google Workspace / Okta / Azure AD）：依首批 enterprise design partner 需求。
- 資料落地「分區」對 managed 版本的實際拓樸（台灣 zone vs 多 region）：依 managed cloud 上線時程。
- supply-chain 的 provenance 等級（SLSA L2 vs L3）：接 `comfyui-ltx-shorts` 既有 image-format/provenance 經驗評估成本。
- 個資法盤點是否需外部法律意見：design partner 簽約前確認。
