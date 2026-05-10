---
adr: "0011"
title: Plan Tier 與 Capability 矩陣
status: Accepted
date: 2026-05-10
tags:
  - plan
  - tier
  - capability
  - quota
  - billing
supersedes: []
superseded-by: []
---

# ADR-0011：Plan Tier 與 Capability 矩陣

* Status：Accepted
* Date：2026-05-10
* 適用範圍：M2–M5；`team.plan` 欄位之列舉、各 tier 啟用之 capability、降升級行為
* 來源：`docs/0ops-plan.md` DB schema `team.plan` 欄位、`docs/0ops-business-plan.md` §七 商業定價；ADR-0007 已預設 `pro` 才開 custom domain
* 上游依賴：[ADR-0001](0001-multi-tenancy-and-rbac.md)（team 為計費邊界）；[ADR-0007](0007-customer-domain-tls.md)（custom domain 為 pro 才開）；[ADR-0004](0004-k3s-role-and-orchestrator.md)（per-team ResourceQuota）

## 0. TL;DR（先讀本段）

採用以下五項組合決策：

1. **Tier 列舉**：`free` / `starter` / `pro` / `team`；free 為 personal team 預設、starter 為個人付費入門、pro 為小團隊、team 為企業；不採 enterprise（v2 評估）。
2. **Capability 矩陣 §3**：覆蓋 9 維度（apps 上限、custom hostname、ResourceQuota、build minutes、rate limit、member 數、audit retention、SSE 連線、support tier）；本 ADR 為各 spec 之上游 source。
3. **降級 grace**：tier 降級時既有資源（apps、custom domain）保留 7 天 grace；超過自動 release 至新 tier 上限（接續 `custom-domain-and-verify` § 9.3）。
4. **升級即時生效**：升級立即解鎖；無 grace、無 cooldown。
5. **Tier 變更 audit**：每次 `team.plan` 變動寫 audit_log；含 actor / 舊 tier / 新 tier / trace_id（接續 `audit-log` § 5.1）。

行為與 quota 數值細節以本 ADR § 3 表為準；變更須走 ADR 補丁。

## 1. Context and Problem Statement

`team.plan` 欄位於 plan.md DB schema 已存在（default `'free'`），但 v1 起未明確定義可能值與各 tier 啟用的 capability。多份 feature spec 已隱含參照 tier：

* `custom-domain-and-verify` § 9.1：`pro` 才開 custom domain（提案 5/team），`team` 20/team
* `k3s-namespace-isolation` § 5.1：ResourceQuota 各 tier 提案值
* `rate-limit-and-abuse` § 4.1：per-tier rate limit 提案值
* `build-pipeline-and-callback`：build minutes 配額（per-tier）
* `audit-log`：13 個月保留為合規最小值（不分 tier；本 ADR 不擴展）

需釘住三件事：

1. v1 起手有哪幾個 tier；命名是否與 business plan §七 對齊
2. 各 tier 啟用之 capability 維度與具體數值
3. 升降級之行為（即時 vs 7 天 grace）

ADR-0001 已將 team 設為計費邊界；本 ADR 在此邊界上釘 capability 結構。

## 2. Decision Drivers

* **DD1 商業定價對齊**：tier 與 business plan §七（Starter USD 19 / Pro USD 99 / Team USD 299）一致；需多一層 free 涵蓋 personal team
* **DD2 用戶自助升降級**：tier 變更為純 team owner 操作；不需 0ops ops 介入
* **DD3 配額為硬性安全閥**：ResourceQuota / build minutes 為 K3s / GHA 之硬上限；超出即拒，不靠軟提示
* **DD4 降級不應立即破壞既有資源**：grace 期讓 user 有時間 export / 升回；強制立即裁切會招致客訴
* **DD5 v1 範圍簡化**：不引入 add-on / metered billing（如「pro + 額外 5 hostname」）；tier 為 flat package
* **DD6 數值起手即可變**：v1 起手值為 educated guess；M2 上線後依實際使用調；變更走 ADR 補丁
* **DD7 Free tier 可持續**：free 非試用；不設 30 天到期；但配額需嚴控避免 abuse

## 3. Capability 矩陣

### 3.1 主表

| Capability | free | starter | pro | team |
|---|---|---|---|---|
| **月費**（USD / team / 月） | 0 | 19 | 99 | 299 |
| **可選對象** | personal team only | 任意 | 任意 | 任意 |
| **Apps 上限** | 1 | 3 | 20 | 200（軟上限） |
| **Members 上限** | 1（自己） | 3 | 10 | 50 |
| **Custom hostname** | ✗ | ✗ | ✓ 5 個 | ✓ 20 個 |
| **`*.winshare.tw` 子網域** | ✓ | ✓ | ✓ | ✓ |
| **ResourceQuota cpu requests** | 1 | 4 | 16 | 64 |
| **ResourceQuota memory requests** | 2Gi | 8Gi | 32Gi | 128Gi |
| **Pods 上限** | 5 | 30 | 120 | 300 |
| **PVC 上限** | 2 | 10 | 40 | 100 |
| **Build minutes / hour** | 5 | 20 | 100 | 300 |
| **Build minutes / month** | 50 | 500 | 2000 | 5000 |
| **Rate limit: per-token write** | 30/min | 60/min | 240/min | 600/min |
| **Rate limit: per-token read** | 300/min | 600/min | 2400/min | 6000/min |
| **Rate limit: per-team write 合計** | 60/min | 300/min | 1200/min | 3000/min |
| **Rate limit: preview-create per-team** | 10/min | 30/min | 120/min | 300/min |
| **Audit log 保留** | 13 個月 | 13 個月 | 13 個月 | 13 個月 |
| **SSE concurrent connections / team** | 5 | 20 | 100 | 500 |
| **Support tier** | 社群 | 社群 | email（48h response） | priority email + Slack（4h response）|
| **SLA** | 無承諾 | 無承諾 | best effort（無合約） | 99.9%（合約 SLA；v2 含補償條款） |

> Build minutes 數值校準依 GitHub Actions 配額：team 5000 min/月對應 GitHub Team 付費方案（3000 min × 配額升級或 self-hosted runner fallback）；pro 2000 min/月落於 free tier 規模；starter / free 為輕量試用級。
>
> SLA 之差異：pro 為 best effort（系統 SLO 自然涵蓋；無合約補償）；team 為合約 SLA（v2 接 billing 後補補償條款）。

### 3.2 v2+ 範圍（不在本 ADR）

- Enterprise tier（self-host license + on-prem 支援）：屬 business plan 範圍
- Add-on（額外 hostname / member / build minutes）：v2 評估
- Annual billing discount（如年付 -15%）：屬 billing spec
- 學術 / OSS 折扣：v1.1 評估

### 3.3 與既有 spec 之對齊狀態

| Spec | 對齊欄位 | 狀態 |
|---|---|---|
| `custom-domain-and-verify` § 9.1 | custom hostname 配額 | 已對齊（5 / 20） |
| `k3s-namespace-isolation` § 5.1 | ResourceQuota | 對齊；該 spec 列出 starter 為 8/16Gi 但 free 缺；本 ADR 補 free=1/2Gi |
| `rate-limit-and-abuse` § 4.1 | per-tier rate limit | 對齊（提案值與該 spec 一致） |
| `build-pipeline-and-callback` | build minutes | 該 spec 未明列數值；本 ADR 為 source |
| `auth-and-rbac` | members 上限 | 該 spec 未明列；本 ADR 補（free=1 等於 personal team） |

> 上述 spec 之提案數值若與本 ADR 不符，**以本 ADR 為準**（ADR 為上游）；spec 端應於 user 拍板後同步更新。

## 4. Decision Outcome

採用 § 3.1 之矩陣值；落地對應如下：

1. **DB 列舉常數**：`internal/shared/rbac/plan.go` 加入 `Plan` typed string + `PlanFree | PlanStarter | PlanPro | PlanTeam`
2. **DB CHECK constraint**：`team.plan` 必為四列舉之一；`free` 必對應 personal team（slug prefix `personal-`）
3. **K8s namespace label**：`team-<slug>` namespace 之 `app.0ops.io/plan` label 隨 `team.plan` 變動 patch
4. **ResourceQuota 動態 render**：`gitops-render-and-argocd` 之 `teams/<slug>/resourcequota.yaml` 依 plan tier 渲染；plan 變動觸發新 commit
5. **Rate limit bucket rebuild**：`rate-limit-and-abuse` § 4.4 之 plan 變動 invalidate 既有 bucket；下次 request 重建
6. **Audit log**：`team.plan` 變動寫 audit_log（action=`plan_change`）
7. **API endpoint**（v1 提供，billing 接 v2）：
   - `POST /v1/teams/{slug}/plan:preview` + `POST /v1/teams/{slug}/plan` (preview-confirm；owner only；scope `members:manage`)
   - `GET /v1/teams/{slug}/plan` (含當前 quota 使用率；viewer)
   - 對應 CLI：`0ops teams plan get|set` （preview 印出 capability 變更摘要）
   - 對應 MCP tool：`get_plan` / `change_plan_preview` + `change_plan`

## 5. 升降級行為

### 5.1 升級

- 立即生效；無 cooldown
- backend 偵測 `team.plan` 變動 → patch namespace label → render & push gitops（updated quota）→ ArgoCD sync
- audit_log 記錄
- 升級**不**影響既有 deploy_run / build；下個 request 起套用新 quota

### 5.2 降級

- 7 天 grace（與 `custom-domain-and-verify` § 9.3 同步）
- grace 期間：
  - quota 暫不裁切（保留現量 + 新量上限為**舊 tier**）
  - custom hostname 暫不解綁
  - build minutes 立即套新 tier 上限（避免 abuse）
- grace 結束後：
  - 超出新 tier 上限之 hostname 自動 release（按 `kind=extra` 之 `created_at` 由舊至新保留）
  - 超出新 tier 上限之 apps 進 `paused` 狀態（按 `created_at` 由新至舊 paused）
  - quota 套新 tier 上限；既有 pod 不殺，新 pod 受限
- 降級 grace 觸發 audit + owner 通知（v1 為 stdout，v1.1 為 email / webhook）

### 5.3 降級舉例

`pro → starter`：
- 既有 7 個 custom hostname → 7 天 grace → 期滿保留 0 個（starter 不開 custom domain）→ 全部 release
- 既有 5 個 apps → 7 天 grace → 期滿保留前 3 個（starter 上限）→ 第 4、5 paused
- ResourceQuota 從 16 cpu / 32Gi 降至 4 cpu / 8Gi → 期滿降低；既有 pod 不變，新 pod 受新 quota
- audit + owner 通知

### 5.4 升降級 API

詳見 § 4 第 7 點。v1 不提供 self-service billing；升降級為「permission grant」性質（owner 自選 tier，capability 立即生效），計費對接屬 v2 商業範圍；ops 在 v1 期間透過手動結帳 + DB 確認後解鎖。

### 5.5 Free tier 限制

- `team.plan = 'free'` 必滿足 `team.slug LIKE 'personal-%'`（DB CHECK + backend handler 雙保險）
- 非 personal team 無法選 free；最低 starter
- 防範用 organization team 規避付費

### 5.6 Personal team 預設

- 首次 device flow login 自動建之 personal team（`auth-and-rbac` § 5）：`plan = 'free'`
- user 可主動升級 personal team 至 starter+（如為個人付費 power user）
- 升降級路徑與一般 team 相同

## 6. Pros and Cons of the Options

### 6.1 採用：4-tier flat package

* Good：與 business plan §七 三付費 tier 對齊；多一個 free 涵蓋 personal team。
* Good：tier 為 flat（不細分 add-on）；onboarding 心智成本低。
* Good：每 tier 對應一組固定 quota / capability；ResourceQuota / rate limit 為靜態 manifest，運維簡單。
* Good：降級 7 天 grace 提供 user buffer；不立即破壞資源。
* Good：升級即時，符合 user 預期。
* Bad：flat tier 對「只多要一個 hostname」的 user 不友善；強迫升整 tier。
* Bad：4 個 tier 數值需運維端維護一致（chart / spec / Go 常數三處）。

### 6.2 否決：Metered / pay-as-you-go

* Good：精準計費；user 用多少付多少。
* Bad：v1 規模 over-engineering；計量管線 + billing 整合屬 v2 商業範圍。
* Bad：對 user 預測成本困難。

### 6.3 否決：完全自定 quota

* Good：靈活。
* Bad：無 tier 概念；商業定價無法 anchored。
* Bad：自定 quota 等於每客戶議價；scaleable 性差。

## 7. Consequences

### 7.1 Positive

- Tier 結構清晰；user 對應商業定價無歧義
- 各 spec 之 quota 數值有單一上游 source
- 升降級行為確定；ops / user 雙方對 grace 期有共識
- ResourceQuota / rate limit 為靜態 chart manifest；運維簡單

### 7.2 Negative

- 4 tier 數值需在 7 維度上維護；變更需 ADR 補丁 + 多 spec 同步
- 降級 7 天 grace 對 K3s 配額有累積壓力（雙倍 quota 並存）；M5 後若 churn 高需重審
- Free tier 配額（5 pods、60 build min/月）對「只想試試看」之 user 可能太緊；M2 上線後依使用調

### 7.3 Neutral

- Annual billing discount 屬商業 spec
- 學術 / OSS 折扣屬 v1.1 商業決策
- Self-hosted edition 屬 business plan §七 第三階段（2027+）

## 8. Revisit Triggers

下列任一條件觸發時，重新評估本 ADR：

1. **某 tier 配額被廣泛抱怨「不夠用」或「過度浪費」**：> 30% team 持續超 80% quota 或 < 10% 使用率 → 重審該維度數值
2. **特定 capability 之 add-on 需求高**：> 20% pro tier 用戶要求額外 hostname → 評估 add-on 模型
3. **降級 grace 7 天造成配額壓力**：M5 後 K3s capacity 受 grace 期累積影響 → 重審 grace 期長度或配額重疊機制
4. **新 capability 加入**：v2 引入 new feature 需歸入 tier → ADR 補丁
5. **Enterprise tier 商業需求**：M5 後特定大客戶要求 → 新增 enterprise tier，可能含 custom quota（破打 flat tier）
6. **Build minutes 撞 GitHub Actions 配額**：team tier `10000 min/月` 接近 GitHub free tier 限制 → 升 GitHub paid plan 或調整數值

## 9. More Information

- Business plan §七 定價：`docs/0ops-business-plan.md` 第 198–227 行
- Custom domain 之 pro tier 限制：[ADR-0007 客戶自有域名 TLS](0007-customer-domain-tls.md) 第 4 節第 7 點
- ResourceQuota 配置：[`docs/features/k3s-namespace-isolation/spec.md`](../features/k3s-namespace-isolation/spec.md) § 5
- Rate limit 配額：[`docs/features/rate-limit-and-abuse/spec.md`](../features/rate-limit-and-abuse/spec.md) § 4
- Build pipeline：[`docs/features/build-pipeline-and-callback/spec.md`](../features/build-pipeline-and-callback/spec.md)
- 降級 grace（custom domain）：[`docs/features/custom-domain-and-verify/spec.md`](../features/custom-domain-and-verify/spec.md) § 9.3
- Plan change audit：[`docs/features/audit-log/spec.md`](../features/audit-log/spec.md) § 5.1

## 10. Open Questions

下列問題不阻擋本 ADR 通過，但需在 v1 GA 前 / v2 規劃時敲定：

1. **Self-hosted edition 對 tier 之關係**：business plan §七提到 USD 5K–15K / 年的 Business license；屬 v2 後分離 ADR；本 ADR 僅涵蓋 managed cloud tier
2. **降級 grace 7 天 vs 月度 billing 週期**：若 v2 接 Stripe 之月底結算，billing 週期與 grace 期之精確語意需釐清（提前降 vs 月底自動降）；屬 v2 billing spec
3. **Build minutes 超限後行為**：v1 採「硬擋 + 提示升級」；v1.1 評估「自動升 tier」或「pay-as-you-go overage」
4. **「Custom team 配額」**：是否允許 enterprise tier 自定 quota（破打 flat tier）；屬 v2 enterprise 範圍
5. **學術 / OSS 折扣**：是否提供 `pro` tier 對 OSS 專案免費？v1.1 商業決策
6. **Region pricing**：v2 multi-region 後，台灣外 region 是否獨立定價？
7. **降級 grace 期之 audit trail 格式**：grace 觸發 → 7 天內每日提醒 → 期滿 release 三段事件之 audit_log 詳細欄位

> 已決議（user 全權委託，2026-05-10 採提案值並落地）：
> - Tier 命名：採 `free` / `starter` / `pro` / `team`（與 business plan §七 對齊）
> - § 3.1 數值：採提案值；build minutes 校準至 GitHub Actions 可承受範圍（50/500/2000/5000）
> - 降級 grace：7 天，與 custom domain 一致
> - 升降級 API：v1 提供 CLI / MCP（preview-confirm）；billing 接 v2
> - Free tier 限制：僅 personal team 可選；非 personal team 最低 starter
> - 與 billing 解耦：v1 「DB 變動 = capability 變動」；v2 才接 Stripe
> - SLA 區分：pro = best effort（無合約）；team = 99.9%（合約 SLA + 補償條款 v2）
