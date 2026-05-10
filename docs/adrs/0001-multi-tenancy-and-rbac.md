---
adr: "0001"
title: 多租戶模型與 RBAC
status: Accepted
date: 2026-05-09
tags:
  - tenancy
  - authorization
  - rbac
  - foundation
supersedes: []
superseded-by: []
---

# ADR-0001：多租戶模型與 RBAC

* Status：Accepted
* Date：2026-05-09
* 適用範圍：M0–M5；資料層、middleware、URL 路由設計之上游決策
* 來源：`docs/0ops-plan.md`「Auth & RBAC」「DB schema」「Tool catalog」三段已確定方向，本 ADR 將其正式化並補上被否決選項的理由

## 0. TL;DR（先讀本段）

採用以下五項組合決策，作為所有 backend / CLI / MCP 與未來 Web UI 的不可變前提：

1. **(a) 租戶一階單位**：`team` 為一階實體；`user` 透過 `team_membership` 加入並持有 role。
2. **(b) slug 唯一性**：`(team_id, slug)` 複合唯一；slug 不全域唯一。
3. **(c) 授權模型**：`team_membership.role` × `cli_token.scopes` 取交集；雙保險，缺一不放行。
4. **(d) Role 粒度**：固定四角 `owner / admin / member / viewer`；不開放 custom role。
5. **(e) URL routing**：path-based `/v1/teams/{team_slug}/...`；跨 team 查詢另設 `/v1/me/*` 端點。

行為與 schema 細節以 `docs/0ops-plan.md` 之「Auth & RBAC」「DB schema」章節為準，本 ADR 不重述。

## 1. Context and Problem Statement

0ops 是內部 PaaS 控制台，backend 不跑 LLM；同一組 REST/SSE API 同時被三條 client 取用：`0ops` CLI、`0ops-mcp`（給 claude code / codex / copilot）以及 v2 Web UI。在第一行業務程式碼下手前必須釘住四件事，否則 schema、middleware、URL 三層皆無法穩定收斂：

1. 租戶一階單位是什麼，以及單人使用者要不要被迫經過該抽象。
2. app slug 在哪個 scope 內唯一。
3. 授權如何計算：role-only、scope-only、role × scope、還是 ABAC。
4. role 粒度與 URL routing 形式。

這些決策一旦形成程式碼即難以局部修改：所有業務表都帶租戶 FK、所有 query 都鎖 `team_id`、所有 endpoint 都依 URL 形態做 routing。本 ADR 的目的是把方向釘成不可變前提，並標示什麼條件下需要 supersede。

## 2. Decision Drivers

* **DD1 強制資料隔離**：backend 同時被 CLI、MCP、Web 取用；任何 SQL 不得跨租戶洩漏，cross-tenant 防線需在 schema、query template、middleware 多層存在。
* **DD2 計費 / 配額單位**：`plan = free / starter / pro` 為租戶屬性；K3s ResourceQuota 以 namespace 對齊一個租戶；single-user dev 不應另立計費單位。
* **DD3 GitHub App install 綁定**：install 屬於一個共享單位（owner-only），離職 user 不應導致部署中斷。
* **DD4 Defense-in-depth**：query 模板鎖定（第一道）+ middleware 鏈（第二道）需各自獨立成立，不依賴對方。
* **DD5 三條 client 同構**：CLI、MCP、Web 必須使用同一條 URL 形態；team scope 應在 URL 上自描述，便於 logging、audit、dashboard。
* **DD6 Token 模型可表達 CI 自動化**：PAT 必須能限制於單一 team 並縮窄能力面，不得自動繼承發 token 者的全部 role 權限。

## 3. Considered Options

針對 (a) 與 (c) 做完整 alternative 比較；(b)(d)(e) 為局部技術選擇，列表帶過。

### 3.1 (a) 租戶一階單位

| Option | 描述 |
|---|---|
| **A1. Team as first-class**（採用） | team 為一階實體；user 透過 team_membership 加入；URL 帶 team_slug；單人 dev 自動建立 personal team |
| A2. User-as-tenant | 每 user 獨享 namespace，協作靠 collaborator 關係（GitHub legacy 風格） |
| A3. Org-only | 不分 personal / team，所有東西必須隸屬 org |
| A4. Project-as-tenant | project 為一階；多個 project 各自有 owner 與權限（GitLab 風格） |

### 3.2 (c) 授權模型

| Option | 描述 |
|---|---|
| **C1. Role × Scope intersection**（採用） | RBAC role + token scope 取交集；缺一不放行 |
| C2. Role-only | token 一旦發出即等同 actor 的全部 role 權限（GitHub PAT classic） |
| C3. Scope-only | 無 role 概念；token 自描述能力（AWS IAM 風格） |
| C4. ABAC | 屬性式判斷（team.plan、app.tier 等動態條件） |

### 3.3 (b)(d)(e) 局部選擇

| 子決策 | 採用 | 否決選項 | 一句結論 |
|---|---|---|---|
| (b) slug 唯一性 | `UNIQUE (team_id, slug)`，citext | 全域唯一 / user-scoped | 全域唯一會跨 team leak 命名空間並衝突；user-scoped 與「team 為一階」語意矛盾 |
| (d) Role 粒度 | 固定四角 owner/admin/member/viewer | 三角（無 admin）/ 自訂 role | 三角無法分離「平台管理」與「日常開發」；自訂 role 為 v2 範圍，v1 不開 |
| (e) URL routing | path `/v1/teams/{slug}/...` | subdomain `acme.0ops.io` / header `X-0ops-Team` | subdomain 需 wildcard cert、額外 DNS 與 cookie scope 設計；header 在 path-only 工具（curl、log 顯示、瀏覽器網址）難用 |

## 4. Decision Outcome

採用 **A1 + C1**，搭配 (b) `(team_id, slug)` 唯一、(d) 4 角色固定、(e) path-based routing。

具體展開（細節以 `docs/0ops-plan.md` 為準，本 ADR 不重述 schema 與 matrix 內容）：

1. **Tenancy schema**：`team` / `team_membership` 為一階；每張業務表帶 `team_id` not-null FK。
2. **Slug 唯一性**：`UNIQUE (team_id, slug)`；`citext` 大小寫不敏感。
3. **Role 與 Scope**：role 為「人在 team 的身份」，scope 為「token 的能力面」；正交。
4. **有效權限**：`role 對應的最低能力 ∩ token scopes`；device flow token 視為持有所有 scope。
5. **URL routing**：寫入與讀取一律走 `/v1/teams/{team_slug}/...`；跨 team 查詢透過 `/v1/me/*`。
6. **Defense-in-depth**：
   * 第一道：sqlc query 模板強制 `WHERE team_id = $1`；不存在缺少 team_id 的 query。
   * 第二道：chi middleware chain `AuthBearer → ResolveTeam → CheckMembership → CheckTokenScope`。
7. **Personal team**：首次登入自動建立 `personal-{github_login}` team，user 為 owner。
8. **Enumeration 防範**：team A 的 user 查 team B 資源回 `404` 或空集合，不得回 `403`，避免 slug 列舉。

## 5. Pros and Cons of the Options

### 5.1 (a) 租戶一階單位

#### A1. Team as first-class（採用）

* Good：計費 / quota / GitHub install 三件事天然落在同一層，不需另立抽象。
* Good：「離職 user 不影響部署」可在 schema 層保證——所有資源 FK 指向 team 而非 user。
* Good：URL `/v1/teams/{slug}/...` 三條 client 同構，audit / log / dashboard 可直接攜帶 slug。
* Good：個人使用者透過 `personal-*` 自動 team，不必額外抽象。
* Bad：多一層 indirection；單人 dev 體感較重。
* Bad：每張業務表都帶 `team_id` not-null FK，初期 migration 重。

#### A2. User-as-tenant

* Good：個人使用最直觀（GitHub legacy 風格）。
* Bad：協作模型曖昧；collaborator 關係、權限繼承、轉讓流程都需另設。
* Bad：計費與配額需要另建抽象，最終會回到 team 概念，等於多繞一圈。
* Bad：user 離職時整個 namespace 需 transfer，運維風險大。

#### A3. Org-only

* Good：抽象最少，沒有 personal / team 雙模式。
* Bad：個人使用者必須先開 org 才能用，UX 摩擦大。
* Bad：對 free plan 單人 dev 不友善，違反「5 分鐘上手」目標。

#### A4. Project-as-tenant

* Good：細粒度，project 互不影響。
* Bad：計費邊界與 access control 拆兩層管理，複雜度高。
* Bad：跨 project 共用 GitHub App install 與 Cloudflare zone 困難，每 project 獨立 install 不可行。

### 5.2 (c) 授權模型

#### C1. Role × Scope intersection（採用）

* Good：Defense-in-depth；role 描述「人」，scope 描述「token」，兩者正交。
* Good：CI 場景可發 member 角色但 scope 限 `apps:read` 的 token，blast radius 受 scope 限制。
* Good：token 外洩時不會自動取得發 token 者的全部 role 能力。
* Bad：心智成本較高；錯誤訊息需區分 `forbidden_role` 與 `forbidden_scope`。
* Bad：role 矩陣與 scope 列舉需同步維護，新增能力時兩處都要動。

#### C2. Role-only

* Good：心智簡單（GitHub PAT classic 模型）。
* Bad：token 等同 user 全權限；CI token 拿到 = 能 delete app。
* Bad：無法在不變更 role 前提下，臨時收緊 token 能力。

#### C3. Scope-only

* Good：token 完全自描述。
* Bad：沒有「人」的角色；invite / transfer ownership 等流程無自然位置。
* Bad：scope 列舉爆炸；owner-only 操作（install GitHub App、邀請 member）都需獨立 scope，難管理。

#### C4. ABAC

* Good：表達力最強，可依 team.plan / app.tier 等屬性動態決策。
* Bad：v1 規模 over-engineering；policy 引擎、debug、稽核成本高。
* Bad：static 分析較難；對 query 模板鎖定（第一道防線）幫助有限。

## 6. Consequences

### 6.1 Positive

* sqlc codegen 後不再需要 review「query 是否漏鎖 team_id」；缺 team_id 的 query 不存在。
* Middleware 鏈固定，新 endpoint 加入只需宣告所需 role 與 scope。
* GitHub App install 綁 team 後，user 異動完全不影響部署。
* URL 自描述（`/v1/teams/acme/apps/foo`）對 logs、audit、dashboard、support 工單都友善。
* PAT 模型支援「member 發 token 給 CI 但只有 read」的常見需求，無須升降 role。

### 6.2 Negative

* 個人 dev 需經一層 `personal-{github_login}` team；首次登入需自動 provisioning。
* role × scope 兩個維度的 403 錯誤碼需區分清楚（`forbidden_role` / `forbidden_scope` / `not_member`），中介層 error 設計負擔較大。
* 每張業務表都帶 `team_id` not-null FK，初期 schema 與 migration 較重。
* Personal team 命名 `personal-{github_login}` 與 GitHub login 重命名後的同步策略需另行決議。

### 6.3 Neutral

* 4 角色為現階段精度；未來若需 custom role 由獨立 ADR supersede 本 ADR 之 (d) 段，不影響 (a)(b)(c)(e)。
* Path-based routing 對 v2 Web UI 客戶端 SDK 友善；若採 subdomain 將需獨立 ADR。
* `team_bucket = hash(team_id) mod 64` 的 metric label 策略屬於 Observability 範疇，不在本 ADR。

## 7. Revisit Triggers

下列任一條件觸發時，應重新評估本 ADR 對應段：

1. **org / team 雙層需求**：客戶要求「org 下多個 team」（GitHub-style 兩層）→ supersede (a)。
2. **Custom role**：4 角色不足（如要分離 `billing-admin` 或 `security-admin`）→ supersede (d)。
3. **跨 team 共享資源**：app 需 cross-team 共讀或共寫 → 重審 (b)(c)。
4. **Scope 爆炸**：scope 列舉超過 ~30 條，代表 RBAC 模型已扭曲 → 評估 ABAC，重審 (c)。
5. **Subdomain 必要性**：客戶端要求 cookie-based auth 並需 origin 隔離，或需 per-team CSP → supersede (e)。
6. **Personal team 摩擦**：使用者反饋對「強制 personal-* team」明顯不耐 → 評估 user-as-tenant 混合模型，重審 (a)。

## 8. More Information

* 詳細 schema 與 middleware 行為：`docs/0ops-plan.md`「Auth & RBAC」與「DB schema」章節。
* Idempotency 與 preview 的跨 team 隔離行為：見 ADR-0002（待寫）；其關鍵約束（`preview.team_id` not null、`SELECT ... FOR UPDATE` 跨 team 拒收）受本 ADR (a)(b) 約束。
* Authentication 機制（GitHub OAuth device flow、PAT lifecycle）：規劃為獨立 ADR；本 ADR 僅約束「PAT 必須綁 team 且持 scope」。
* GitHub App install 綁 team 的具體流程（state HMAC、callback、uninstall paused）：規劃為獨立 ADR。
* Observability metric 中 team-bucket label 設計：見 ADR-0006（待寫）。

## 9. Open Questions

下列問題不阻擋本 ADR 通過，但需在 M0 結束前敲定：

1. Personal team 的 lifecycle：能否 rename、能否 delete、GitHub login 改名後如何同步。
2. Team archival 語意：`team.archived_at` 設定後，是否仍可 query 已建立的 app（read-only）；archived 後 PAT 是否立即失效。
3. Membership invite 狀態：`team_membership.invited_at` 與 `joined_at` 的中間狀態（pending invite）是否需要獨立 query 端點。
4. Token scope 枚舉的 source of truth：常數定義在 `internal/shared/rbac/`，但 OpenAPI spec 是否需要同步暴露；新增 scope 是否需 ADR 補丁。
