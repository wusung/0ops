---
adr: "0016"
title: SSO and External Identity (OIDC-first)
status: Proposed
date: 2026-06-28
tags:
  - auth
  - sso
  - enterprise
  - security
supersedes: []
superseded-by: []
---

# ADR-0016：SSO and External Identity (OIDC-first)

* Status：Proposed（對應 spec 為 draft；尚未實作。承 `docs/trust-and-compliance/plan.md` § 6 規則 1，狀態誠實標示）
* Date：2026-06-28
* 適用範圍：team 級外部身分整合（SSO）；集中撤權；與既有 device flow 共存
* 來源：`docs/trust-and-compliance/plan.md` § 5.1（P2，Enterprise/Team tier 解鎖）；`docs/features/threat-model/spec.md` § 5.2 AU3（無集中撤權）；對應 spec [`docs/features/sso-saml/spec.md`](../features/sso-saml/spec.md)
* 上游依賴：[ADR-0001](0001-multi-tenancy-and-rbac.md)（team / role / membership 模型，SSO 不得另造）；[ADR-0011](0011-plan-tier-capability-matrix.md)（SSO 為 team tier 能力）；既有 [`docs/features/auth-login-flow/spec.md`](../features/auth-login-flow/spec.md)（device flow）、[`docs/features/auth-and-rbac/spec.md`](../features/auth-and-rbac/spec.md)

## 0. TL;DR（先讀本段）

採用以下五項組合決策：

1. **OIDC-first，SAML 後續**：v1 SSO 只做 OIDC（與既有 GitHub OAuth / device flow 同為 OAuth2 家族，整合面最小）；SAML 2.0 列後續 phase，由本 ADR 預留 `idp_config.protocol` 列舉（v1 CHECK 僅 `'oidc'`）與 nullable SAML 欄位但不實作。
2. **Team 級 IdP 綁定**：SSO 設定掛在 team 上（`idp_config.team_id` UNIQUE，一 team 一 IdP，v1）；僅 `owner` 可設定（scope `sso:manage`）；綁定 domain 須經 **DNS TXT 驗證**且 domain 全域唯一。
3. **JIT provisioning**：首次 SSO 登入自動建 user + membership；預設 role 由 `idp_config.jit_default_role`（CHECK `in (admin,member,viewer)`，預設 `member`）；IdP group → role 映射可選且**封頂 `admin`**——JIT 不得直接賦予 `owner`。
4. **集中撤權靠既有 middleware（核心，無平行旁路）**：deprovision = 停用 `team_membership`（`deactivated_at`）+ revoke 該 user 該 team 全部 `cli_token`（device 與 PAT 一併）。其後該員工任意 token 經既有鏈雙重失效（`AuthBearer` 撞 `revoked_at` → 401；`CheckMembership` 撞 `deactivated_at` → 404）。**單一 deprovision 涵蓋該 user 該 team 全部 token**，解 AU3 之「逐一 revoke」。禁用個人 PAT 為**可選政策** `idp_config.pat_policy=disallow`，非撤權前提。
5. **與 device flow 共存不破壞 agent UX**：SSO-enforced team 的 CLI / MCP 登入仍走 device flow，但 device 授權頁改走 IdP redirect；SSO-issued token **不 rolling refresh**、TTL ≤ `min(IdP session, 8h, 24h)`；agent 接線體驗（`0ops onboard`）不變，僅重登週期縮短。

行為與 schema/API 細節以 spec [`docs/features/sso-saml/spec.md`](../features/sso-saml/spec.md) 為準；本 ADR 釘住決策邊界，不重述 spec。

## 1. Context and Problem Statement

威脅模型 § 5.2 AU3：0ops v1 身分以 GitHub OAuth + 個人 PAT 為主；team 沒有集中身分管理。enterprise 採購的硬需求是「員工離職，IT 一處停用即全面斷 access」。現況下離職員工的 PAT 需逐一手動 revoke，且無法保證涵蓋所有 token —— 這是 enterprise design partner 與 SOC2 CC6（邏輯存取）會直接擋下的缺口。

同時 business-plan 之 Team tier（USD 299/月）已對外列 SSO 為賣點（ADR-0011 能力矩陣），但無對應設計。需要一個**不另造平行權限模型**、**不破壞既有 agent 接線 UX**、且**真能達成集中撤權**的 SSO 設計。

現況缺：無外部 IdP 整合、無 team↔IdP 綁定、無 JIT provisioning、無「IdP 停用 → token 失效」路徑。

## 2. Decision Drivers

* **DD1 集中撤權必須成立**：SSO 若無法讓「IdP 停用」實際切斷 access（含既有 PAT），就沒解到 AU3，只是登入糖衣。
* **DD2 不另造權限模型**：role / scope / team 隔離沿用 ADR-0001；SSO 只決定「你是誰」，不決定「你能做什麼」（後者仍 RBAC）。
* **DD3 agent UX 不退化**：CLI / MCP 走 device flow 接線是核心體驗；SSO 不得要求 agent 走瀏覽器互動式登入。
* **DD4 整合面最小先行**：先支援與既有 OAuth 同源的 OIDC，避免一開始就背 SAML 的 XML/憑證複雜度。
* **DD5 稽核完整**：SSO login / logout / provision / deprovision 必入 audit_log（接 audit-log § 5.1）。
* **DD6 漸進採用**：team 可選擇「SSO 可選」或「SSO 強制」（`enforce`）；個人 PAT 是否禁用為獨立 `pat_policy` 開關（預設 `allow`），避免一刀切中斷既有 CI；無論 PAT 是否禁用，deprovision 一律連帶撤該 user PAT。

## 3. Decision Outcome

### 3.1 協定：OIDC-first

* v1：OIDC（Authorization Code + PKCE）。支援標準 OIDC discovery（`.well-known/openid-configuration`）。
* 預留 `idp_config.protocol ∈ {oidc, saml}`；`saml` 後續 phase 實作，v1 寫入即 422。

### 3.2 Schema（三表 + 既有表補欄位；spec § 11 定完整 DDL 與 migration 編號）

* **`idp_config`**（一 team 一 IdP）：`team_id UNIQUE`、`protocol`（v1 CHECK `'oidc'`）、`issuer`、`client_id`、`client_secret_ref`（指 secrets store，不存明文）、`enforce`、`jit_default_role`（CHECK `admin/member/viewer`）、`group_claim`/`group_role_map`（可選）、`pat_policy`（`allow`/`disallow`）、`session_max_ttl_s`（預設 8h、上限 24h）、SAML 預留 nullable 欄位。
* **`idp_domain`**：`domain citext UNIQUE`（全域唯一防跨 team 劫持）、`verification_token`、`verified`——DNS TXT 驗證後綁定。
* **`idp_identity`**：`(idp_config_id, idp_subject) UNIQUE` ↔ `user_id`，`deactivated_at`（撤權標記）；OIDC `sub` 對應 0ops user，**不在 user_account 加欄位**。
* 既有表補欄位：`team_membership` 加 `auth_source('native'|'sso')` + `deactivated_at`；`cli_token` 加 `auth_source` + `idp_config_id`。`cli_token.kind` 不擴充（SSO token 仍 `kind='device'`，以 `auth_source='sso'` 區分）。

### 3.3 集中撤權路徑（核心，DD1）

deprovision（owner 手動 / back-channel logout / SCIM）於同一 transaction：

1. **`idp_identity.deactivated_at = now()`** + **`team_membership.deactivated_at = now()`**：`CheckMembership` 視 `deactivated_at != null` 為非 member → `404 team_not_found`（沿用 `auth-and-rbac` enumeration）。
2. **`UPDATE cli_token SET revoked_at = now() WHERE owner_user_id=$user AND team_id=$team`**：device 與 PAT 一併；`AuthBearer` 撞 `revoked_at` → `401 token_revoked`。
3. **invalidate token cache**（broadcast，防快取殘留）。
4. **短 TTL 保底**：SSO token 不 rolling refresh、TTL ≤ `min(IdP session, 8h)`；即便無 back-channel logout，最差 8h 內過期且無法重取（IdP 已停用）。
5. **SCIM**（即時 push）列後續；v1 以上述 + 短 TTL 達「最終一致」撤權。

撤權不開任何平行旁路；完全靠既有 middleware 鏈與 token revoke 落地。

### 3.4 JIT provisioning

首次 SSO callback（`sub` 未見於 `idp_identity`）且 `email` domain 屬該 team 之 `verified` domain → upsert `user_account`（沿用既有，不另造 user 表）+ `INSERT idp_identity` + 若無 active membership 則 `INSERT team_membership(auth_source='sso', role=...)`。role = `jit_default_role` 或 `group_role_map` 命中值（多 group 取最高、**封頂 `admin`**）。domain 不符 → `403 sso_domain_mismatch` + audit failure，不建 user。

### 3.5 與 device flow 共存（DD3）

* SSO-enforced team 的 `0ops auth login`（device flow）：device 授權頁不再用 GitHub OAuth，改 redirect 到 team 的 IdP；使用者在瀏覽器完成 IdP 登入後 device flow 領 token。
* agent 接線（`0ops onboard` / `0ops mcp setup`）流程與輸出**不變**；差別僅在授權頁背後的 IdP。
* 非 SSO team 維持既有 GitHub OAuth device flow，零變更。

### 3.6 稽核（DD5）

新增 audit action：`sso_login`、`sso_logout`、`sso_provision_user`（JIT 建 user/membership）、`sso_deprovision_user`、`sso_config_create`/`sso_config_update`/`sso_config_delete`、`sso_domain_verify`；IdP-initiated 來源（back-channel/SCIM）`source='system'` 且 `actor_user_id` 為 NULL。client secret / id_token / logout token / DNS token 不入 args/result。

## 4. 與既有 auth ADR / spec 之關係

* **ADR-0001（RBAC）不動**：SSO 決定身分，授權仍由 role / scope / team 隔離決定（DD2）。`group_role_map` 只是把 IdP group 映射到既有 role，不新增 role 類型。
* **auth-login-flow（device flow）擴充不取代**：device flow 框架保留；授權階段的 IdP 由 GitHub 擴為「team IdP」。
* **auth-and-rbac**：membership 標 `auth_source='sso'`；撤權靠既有 `CheckMembership`（`deactivated_at`）+ `cli_token.revoked_at`；scope 模型不變，僅加 owner-only `sso:manage`。
* **secrets-management**：`client_secret_ref` 走既有 secret store，不存明文（接 redact / 加密）。

## 5. Pros and Cons of the Options

| 方案 | 描述 | 採用 |
|---|---|---|
| **A. OIDC-first，SAML 後續（本 ADR）** | v1 OIDC，預留 SAML | ✅ |
| B. SAML-first | 先做 SAML 2.0（多數 legacy enterprise IdP） | ✗（v1） |
| C. OIDC + SAML 同時 | 一次兩協定 | ✗ |

### A（採用）
**Pros**：與既有 OAuth2/device flow 同源，整合面最小（DD4）；現代 IdP（Google Workspace / Okta / Azure AD）皆原生 OIDC；PKCE 與既有 device flow 共用心智模型。
**Cons**：部分 legacy enterprise 只給 SAML；這類客戶 v1 無法服務（列 Revisit）。

### B（否決，列 Revisit）
SAML 覆蓋更多 legacy IdP，但 XML 簽章 / metadata / 憑證輪替複雜度高；與既有 OAuth 心智模型斷裂；v1 投資報酬低。待首批 design partner 明確要 SAML 再做。

### C（否決）
同時兩協定使 v1 範圍翻倍、攻擊面加大；違反 DD4「整合面最小先行」。

## 6. Consequences

### 6.1 正面
* AU3 解除：`enforced=true` + 禁個人 PAT 後，IdP 停用即全面斷 access。
* 解鎖 Team / Enterprise tier 對外承諾（ADR-0011）。
* 滿足 SOC2 CC6（邏輯存取集中管理）關鍵控制（接 `compliance-framework-mapping`）。

### 6.2 負面
* **`pat_policy=disallow` 之衝擊**：選擇禁個人 PAT 的 team，其 CI / agent 需改走 owner 維護之 token；service account 為更乾淨解但 v1 未定義（列 Open Question）。預設 `allow` 不受影響。
* **IdP 設定錯誤之鎖死風險**：IdP 設錯可能把整 team 鎖在外；需 break-glass（owner 後門 / 平台支援）路徑（Open Question），且 break-glass 本身須入 audit。
* **手動 deprovision 為即時、IdP 端被動停用非即時**：owner deprovision 或 back-channel logout 即時撤（token revoke）；但若 IdP 端僅停用而無 back-channel logout，撤權靠短 TTL 過期（最差 8h）兜底，非即時。SCIM 列後續以補即時性。

### 6.3 中性
* SAML 預留欄位存在但 v1 不實作；寫 `saml` 即 422。
* `group_role_map` 可選；不設則全員 `default_role`。

## 7. Revisit Triggers

* **design partner 要求 SAML**：首批 enterprise 若有 legacy SAML-only IdP → 啟動 SAML phase（沿用本 ADR 預留欄位）。
* **即時 deprovision 需求**：若客戶要求停用即時生效（非 refresh-time）→ 引入 SCIM（獨立 spec / 可能新 ADR）。
* **service account 需求浮現**：`enforced` team 的非人類（CI / agent）身分需要 PAT 替代品 → 定義 team-scoped service account。
* **多 IdP per team**：若大型 team 需多 IdP（併購情境）→ 放寬一 team 一 IdP 限制。

## 8. More Information

* **Feature spec**：[`docs/features/sso-saml/spec.md`](../features/sso-saml/spec.md)（schema、IdP 設定流程、JIT、撤權路徑、驗證準則以本檔為準）
* **威脅模型**：[`docs/features/threat-model/spec.md`](../features/threat-model/spec.md) § 5.2 AU3
* **統籌計畫**：[`docs/trust-and-compliance/plan.md`](../trust-and-compliance/plan.md) § 5.1
* **ADR-0001**：[0001-multi-tenancy-and-rbac.md](0001-multi-tenancy-and-rbac.md)（role/scope/team；SSO 不另造）
* **ADR-0011**：[0011-plan-tier-capability-matrix.md](0011-plan-tier-capability-matrix.md)（SSO 為 team tier 能力）
* **auth-login-flow**：[`docs/features/auth-login-flow/spec.md`](../features/auth-login-flow/spec.md)（device flow，SSO 擴充其授權階段）

## 9. Open Questions

1. **Service account**：`enforced` team 的 CI / agent 在禁個人 PAT 後如何持有憑證？team-scoped service account 為可能解，v1 未定義。
2. **Break-glass**：IdP 設定錯誤把 team 鎖死時的緊急回復路徑（owner 後門 token？平台支援？）須在 spec 定義且本身入 audit。
3. **SCIM**：是否需即時 deprovision；v1 靠 refresh-time + 強制 SSO 達「最終一致」撤權；SCIM 列後續。
4. **SSO token TTL**：撤權即時性與使用者體驗的權衡；TTL 設多短由 spec 給預設 + 可配置範圍。
5. **跨 team 使用者**：同一人在 SSO team A 與非 SSO team B 的身分如何並存（`idp_subject` 綁 team，但 `user_account` 為全域）；spec 須定義 user 與多 membership 的身分解析。
6. **SAML phase 範圍**：預留欄位足夠承載 SAML metadata / 憑證輪替嗎？啟動 SAML 時再評估是否需 schema 擴充。
