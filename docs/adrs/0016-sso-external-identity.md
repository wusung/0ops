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

1. **OIDC-first，SAML 後續**：v1 SSO 只做 OIDC（與既有 GitHub OAuth / device flow 同為 OAuth2 家族，整合面最小）；SAML 2.0 列後續 phase，由本 ADR 預留 `idp_protocol` 欄位但不實作。
2. **Team 級 IdP 綁定**：SSO 設定掛在 team 上（一 team 一 IdP，v1）；僅 `owner` 可設定；綁定前須通過 email domain 驗證。
3. **JIT provisioning**：首次 SSO 登入自動建 user + membership；預設 role 由 team 設定（預設 `member`）；IdP group → role 映射為可選。
4. **集中撤權為核心價值**：IdP 端停用使用者 → 0ops session 與 SSO 衍生 token 失效；SSO-enforced team 可選擇**禁用個人 PAT**，使「IdP 停用 = 全面斷access」成立（解 AU3）。
5. **與 device flow 共存不破壞 agent UX**：SSO-enforced team 的 CLI / MCP 登入仍走 device flow，但 device 授權頁改走 IdP redirect；agent 接線體驗（`0ops onboard`）不變。

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
* **DD6 漸進採用**：team 可選擇「SSO 可選」或「SSO 強制」；強制時才禁個人 PAT，避免一刀切影響既有使用者。

## 3. Decision Outcome

### 3.1 協定：OIDC-first

* v1：OIDC（Authorization Code + PKCE）。支援標準 OIDC discovery（`.well-known/openid-configuration`）。
* 預留 `idp_config.protocol ∈ {oidc, saml}`；`saml` 後續 phase 實作，v1 寫入即 422。

### 3.2 Schema（spec 定 migration 編號）

```sql
idp_config(
  id uuid pk,
  team_id uuid fk unique,          -- 一 team 一 IdP（v1）
  protocol text not null,          -- 'oidc'（v1）
  issuer text, client_id text,
  client_secret_ref text,          -- 指向 secrets store，不存明文
  allowed_email_domain text,       -- 綁定前驗證
  default_role text not null default 'member',
  group_role_map jsonb,            -- 可選 IdP group → role
  enforced bool not null default false,  -- true = 強制 SSO + 禁個人 PAT
  created_at timestamptz
)
```

`user_account` 補 `idp_subject text`（IdP 的 `sub`，與 team 綁定唯一）；`team_membership` 補 `provisioned_via text`（`manual` | `sso_jit`）。

### 3.3 集中撤權路徑（核心，DD1）

1. **Session/token 失效**：SSO 衍生的 0ops session token 帶短 TTL + refresh 須回 IdP 驗證；IdP 停用後 refresh 失敗 → access 斷。
2. **強制 SSO team 禁個人 PAT**：`enforced=true` 時，該 team scope 下的個人 PAT 建立被拒、既有 PAT 標記失效；所有 access 必經 SSO 身分 → IdP 停用即全斷。
3. **可選 SCIM**（後續）：即時 deprovision push；v1 靠 refresh-time 驗證 + 強制 SSO 達成「最終一致」撤權，SCIM 列 Open Question。

### 3.4 JIT provisioning

首次 SSO 登入（`sub` 未見過）且 email domain 符合 `allowed_email_domain` → 建 `user_account`（標 `idp_subject`）+ `team_membership`（role = `default_role` 或 `group_role_map` 命中值，`provisioned_via=sso_jit`）。不符 domain → 拒絕，不建 user。

### 3.5 與 device flow 共存（DD3）

* SSO-enforced team 的 `0ops auth login`（device flow）：device 授權頁不再用 GitHub OAuth，改 redirect 到 team 的 IdP；使用者在瀏覽器完成 IdP 登入後 device flow 領 token。
* agent 接線（`0ops onboard` / `0ops mcp setup`）流程與輸出**不變**；差別僅在授權頁背後的 IdP。
* 非 SSO team 維持既有 GitHub OAuth device flow，零變更。

### 3.6 稽核（DD5）

新增 audit action：`sso_login`、`sso_logout`、`sso_provision`（JIT 建 user/membership）、`sso_deprovision`、`idp_config_change`；source 依情境（user / system）。

## 4. 與既有 auth ADR / spec 之關係

* **ADR-0001（RBAC）不動**：SSO 決定身分，授權仍由 role / scope / team 隔離決定（DD2）。`group_role_map` 只是把 IdP group 映射到既有 role，不新增 role 類型。
* **auth-login-flow（device flow）擴充不取代**：device flow 框架保留；授權階段的 IdP 由 GitHub 擴為「team IdP」。
* **auth-and-rbac**：membership 來源新增 `sso_jit`；scope 模型不變。
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
* **個人 PAT 禁用之衝擊**：`enforced=true` team 內既有以 PAT 接線的 agent / CI 需改走 SSO 衍生 token 或 service account（後者 v1 未定義，列 Open Question）；遷移有摩擦。
* **IdP 設定錯誤之鎖死風險**：IdP 設錯可能把整 team 鎖在外；需 break-glass（owner 後門 / 平台支援）路徑（Open Question）。
* **refresh-time 撤權非即時**：無 SCIM 時，撤權在 token refresh 時才生效（TTL 內仍有效）；須把 SSO token TTL 收斂到可接受窗口。

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
