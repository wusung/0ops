# Feature Spec：sso-saml

> **狀態**：draft
> **來源**：`docs/trust-and-compliance/plan.md` § 3.3（Security 軸 SSO/SAML）、§ 5.1（拆解清單 P2）；`docs/features/threat-model/spec.md` § 5.2 AU3（無集中撤權）、§ 6（缺口 → 本 spec）；`docs/0ops-business-plan.md`（Team tier 已列 SSO，line 205）
> **適用範圍**：企業 IdP 整合（v1 OIDC）、Team ↔ IdP 綁定、JIT provisioning、集中撤權、與既有 device flow 共存、SSO 稽核寫入點；不含 Web UI 登入頁（v2）、不含 SCIM 自動同步（v1.1）、不含 SAML 2.0 binding 實作（v1.1/v2）
> **對應 Milestone**：P2（Enterprise / Team tier 解鎖；`trust-and-compliance/plan.md` § 4 排序）
> **依賴**：`auth-and-rbac`（沿用 role/scope/team 隔離模型，不另造）、`auth-login-flow`（沿用 device flow 與 OAuth2 authcode + PKCE 路徑）、`audit-log`（沿用 § 5.1 寫入點與 redactor）、`secrets-management`（IdP client secret 加密儲存）
> **引用 ADR**：ADR-0016（新 auth path 之架構決策；由他人另寫，本 spec 僅引用編號與其釘定之 scope 列舉擴充）
> **讀法**：§ 1 結論 → § 3 協定選擇 → § 7 集中撤權 → § 14 驗證準則 → § 17 硬性規則

## 1. 結論（先讀本段）

- 解的核心威脅是 `threat-model` § 5.2 **AU3**：無 SSO 時企業無集中撤權，離職員工的 device token 與 PAT 必須**逐一 revoke**，爆炸半徑與遺漏風險高。本 spec 提供「IdP 端停用一次 → 該員工在該 team 的所有 0ops token 立即失效」的單一撤權路徑。
- **協定選擇：v1 只做 OIDC**。理由：既有 device flow 與 OAuth2 authcode + PKCE（`auth-login-flow` § 3.2、§ 4.3）本就是 OAuth2/OIDC 形狀，IdP 整合可直接複用 PKCE、authorization-code 交換、`GET /user` 之等價 `userinfo`/`id_token` 解析；首批目標 IdP（Google Workspace / Okta / Azure AD）皆原生暴露 OIDC discovery。**SAML 2.0 延後**（XML assertion 解析、簽章驗證、redirect/POST binding 為獨立攻擊面與獨立工程量），列 v1.1/v2，待出現「僅支援 SAML 的 design partner」再開（§ 3.2）。
- **沿用既有權限模型，不另造平行體系**：SSO 不引入新 role；JIT 建立的 membership 仍是 `owner/admin/member/viewer` 四角色之一；SSO 設定為 **owner-only** 動作，透過 `auth-and-rbac` § 6.2 之 `CheckTokenScope` 宣告 `MinRole=owner` + scope `sso:manage`（scope 列舉擴充由 ADR-0016 釘定，常數同步 `internal/shared/rbac/scope.go`）。
- **集中撤權靠既有 middleware 達成（無平行旁路）**：deprovision = 移除/停用 `team_membership` + 將該 user 在該 team 的所有 `cli_token`（device 與 pat）標 `revoked_at`。其後該員工任何 token 走既有鏈即雙重失效——`AuthBearer` 撞 `revoked_at` 回 `401 token_revoked`、`CheckMembership` 撞缺 membership 回 `404 team_not_found`（`auth-and-rbac` § 6.2、§ 7.1）。
- **與 device flow 共存，不破壞 agent 接線**：SSO-enforced team 的 `0ops auth login` 仍是 device-flow poll loop UX；差異僅在 backend 把 verification 導向該 team 的 IdP authorize URL 而非 GitHub。CLI/MCP 仍只讀寫 `~/.config/0ops/auth.json`，MCP 不寫 token（`auth-and-rbac` § 8.2 硬規則 10 不變）。
- **一 team 一 IdP（v1）**：`idp_config.team_id` UNIQUE；domain 經 DNS TXT 驗證後綁定；enforce 為 per-team 開關。
- **不弱化稽核**：SSO login/logout/provision/deprovision/config 變更全部入 `audit_log`，接 `audit-log` § 5.1 寫入點並新增 action（§ 9）；client secret 不入 args/result（沿用 redactor）。

## 2. 範圍

### 2.1 包含
- `internal/server/auth/sso/` package：OIDC discovery、authorize URL 構造、callback/code 交換、id_token 驗章、JIT provisioning、deprovision、back-channel logout 接收。
- `idp_config`、`idp_domain`、`idp_identity` 三張新表；`team_membership`、`cli_token` 補欄位。
- Team ↔ IdP 綁定流程（owner 設定 + domain 驗證 + enforce 開關）。
- JIT provisioning（首次 SSO 登入建 `user_account` + `team_membership`；預設 role；IdP group → 0ops role 映射，可選）。
- 集中撤權路徑（IdP 停用 → session/token 失效）與既有 PAT 在 SSO enforce 下的處理。
- 與 device flow / OAuth2 authcode 共存的 CLI/MCP 登入路徑。
- SSO 相關 audit 寫入點（接 `audit-log` § 5.1）。
- migration `00013_idp_config_and_sso.sql`。

### 2.2 不包含
- SAML 2.0 實作（v1.1/v2；§ 3.2 列觸發條件與保留欄位）。
- SCIM 2.0 自動 provisioning/deprovisioning（v1.1；v1 以 back-channel logout + 短 TTL 重驗 + owner 手動 deprovision 達成撤權）。
- Web UI 登入頁（v2；屬 M6 Web UI）。
- 一 team 多 IdP、IdP federation（v2）。
- PAT 之 lifecycle 與 argon2id 細節（屬 `auth-and-rbac` § 4.4；本 spec 只規範 SSO enforce 下的政策）。
- 錯誤 envelope 結構（屬 `error-model`）、scope 列舉之 source-of-truth 程式定義（屬 `shared-dto-and-contract` § 6、ADR-0016）。

## 3. 協定選擇

### 3.1 OIDC vs SAML 2.0 取捨

| 維度 | OIDC（v1 採用） | SAML 2.0（延後） |
|---|---|---|
| 與既有架構同源 | **高**：device flow / OAuth2 authcode + PKCE 已是 OAuth2 形狀，code 交換、`userinfo`/`id_token` 解析可複用 `auth-login-flow` 既有路徑 | 低：XML assertion、redirect/POST binding 為全新 code path |
| 簽章/驗章 | JWKS（`jwks_uri`）+ JWT 驗章，函式庫成熟、無 XML canonicalization 雷 | XML-DSig + canonicalization，歷史漏洞面大（XSW 攻擊） |
| 首批 IdP 覆蓋 | Google Workspace / Okta / Azure AD 皆原生 OIDC | 多為 legacy 企業強制項；Okta/Azure 亦支援但非首選 |
| Discovery | `.well-known/openid-configuration` 自動取 endpoint | metadata XML，需手動或上傳 |
| 工程量 | 小（複用既有 OAuth2 基礎） | 大（獨立 assertion 解析 + 驗章 + 攻擊面） |

### 3.2 決策

- **v1 只做 OIDC**：與 GitHub OAuth/device flow 同源，整合成本最低、攻擊面最小、首批 design partner（`trust-and-compliance/plan.md` § 7 Open issue「先支援哪些 IdP」）皆可由 OIDC 覆蓋。
- **SAML 2.0 延後至 v1.1/v2**，觸發條件：出現「IdP 僅支援 SAML、無 OIDC endpoint」之簽約中 enterprise design partner。屆時 `idp_config.protocol` 由 `'oidc'` 擴充 `'saml'`，新增 `internal/server/auth/sso/saml/`，schema 預留欄位（`metadata_url`、`sp_entity_id`、`idp_cert`）以 nullable 加入，不破壞 v1 OIDC 路徑。
- 本 spec 全文以 OIDC 為實作對象；SAML 僅在 schema 與 `protocol` 列舉預留位置，**不在 v1 實作**（避免 placeholder：預留欄位為 nullable 且 `protocol` CHECK v1 僅允許 `'oidc'`）。

## 4. IdP 範圍

### 4.1 首批支援（v1，皆走 OIDC discovery）

| IdP | discovery | 備註 |
|---|---|---|
| Google Workspace | `https://accounts.google.com/.well-known/openid-configuration` | `hd` claim 可作 domain 佐證 |
| Okta | `https://{org}.okta.com/.well-known/openid-configuration` | group claim 需在 Okta 端 scope 設定 |
| Azure AD（Entra ID） | `https://login.microsoftonline.com/{tenant}/v2.0/.well-known/openid-configuration` | `groups` claim 為 object id，映射需 group_role_map |
| 通用 OIDC | 任意符合 OIDC discovery 之 IdP | 由 owner 填 `issuer`；backend 拉 discovery 自動取 endpoint |

### 4.2 可配置性

- 依 enterprise design partner 需求，IdP 為 **per-team 可配置**：owner 提供 `issuer` + `client_id` + `client_secret`（存 secrets store，不入 DB 明文）+ 可選 `group_claim`/`group_role_map`。
- backend 啟動時不硬編任何 IdP；全部由 `idp_config` 驅動 + runtime 拉 `.well-known/openid-configuration`（快取 + 定期刷新 JWKS）。
- 標記為**可配置**（`trust-and-compliance/plan.md` § 7：先支援哪些 IdP 依首批 design partner 決定）。

## 5. Team ↔ IdP 綁定

### 5.1 誰可設定

- **僅 `owner`**：設定/修改/刪除 IdP、驗證 domain、開關 enforce，皆需 `MinRole=owner` + scope `sso:manage`（經 `auth-and-rbac` § 6.2 `CheckTokenScope`；scope 由 ADR-0016 列舉）。
- `admin` 可**讀** `idp_config`（不含 secret），不可改（`sso:manage` 為 owner-only）。

### 5.2 Domain 驗證後綁定

1. owner 呼 `POST /v1/teams/{slug}/sso/domains {domain}` → backend 產 `verification_token`，回 DNS TXT 記錄（`_0ops-sso.{domain} = 0ops-verify={token}`）。
2. owner 在 DNS 設 TXT → 呼 `POST /v1/teams/{slug}/sso/domains/{id}/verify` → backend 查 TXT 命中則 `idp_domain.verified=true`。
3. `idp_domain.domain` **全域 UNIQUE**：一個 domain 不可同時綁兩 team（防跨 team 劫持登入）。
4. 未驗證 domain 不可開 enforce；enforce 開啟前 backend 檢查至少一個 `verified=true` domain。

### 5.3 一 team 一 IdP（v1）

- `idp_config.team_id` UNIQUE：v1 每 team 至多一個 IdP。
- 多 IdP / 一 team 多 domain 跨 IdP：v1 僅支援一 IdP 多 domain（同 `idp_config_id`）；多 IdP 列 v2（§ 16）。

## 6. JIT provisioning

### 6.1 首次 SSO 登入自動建立

SSO callback 成功（id_token 驗章通過、`email`/`sub` 取得）後，於同一 transaction：

1. 以 id_token `sub` 查 `idp_identity(idp_config_id, idp_subject)`；命中則取既有 `user_id`。
2. 未命中：以 `email` 之 domain 確認屬該 team 之 `verified` domain（不符 → `403 sso_domain_mismatch`，入 audit failure）。
3. 以 `email`/`github_login`（若可得）upsert `user_account`（沿用既有 `user_account`，**不另造 user 表**）。
4. `INSERT idp_identity(idp_config_id, user_id, idp_subject, email, last_login_at)`。
5. 若該 user 在該 team 無 active membership → `INSERT team_membership(team_id, user_id, role, auth_source='sso', joined_at=now())`，role = § 6.2 解析結果。
6. 簽發 0ops bearer token（`cli_token`，kind=`device`、`auth_source='sso'`、`idp_config_id` 綁定、TTL 見 § 7.3）。

### 6.2 預設 role 與 group 映射

- 預設 role：`idp_config.jit_default_role`（CHECK `in ('admin','member','viewer')`，預設 `member`；**不允許 JIT 直接給 `owner`**——owner 僅能由既有 owner 手動指派，防 IdP 端誤配提權）。
- 可選 group 映射：若設 `idp_config.group_claim`，backend 從 id_token 取該 claim（陣列）→ 依 `idp_config.group_role_map`（`{idp_group: 0ops_role}` jsonb）解析；多 group 命中取**最高權限**（owner 除外，封頂 `admin`）。
- 映射查無 → fallback `jit_default_role`。
- group → role 映射只影響 JIT 建立**當下**之 role；後續 IdP group 變動之同步屬 SCIM（v1.1，§ 16）。v1 之再評估：每次 SSO 登入若 group 映射結果高於現有 role，**不自動降級、只記 audit**（避免登入即改權限的副作用）；升級與否列 v1.1 政策。

### 6.3 與 personal team 的關係

- SSO 簽發之 token 綁定**該 enterprise team**，不觸發 `auth-and-rbac` § 5 之 personal team auto-provisioning（personal team 僅 GitHub device flow 首登時建立）。
- 同一 GitHub 身份既有 personal team 與 SSO team 並存；`auth.json` 以 `default_team_slug` 區分（沿用 `auth-login-flow` § 5.1）。

## 7. 集中撤權（核心價值）

> 解 AU3：IdP 端一次停用 → 0ops 端該員工該 team 全部 token 失效，取代逐一 revoke。

### 7.1 撤權觸發來源

| 來源 | 機制 | v1 |
|---|---|---|
| OIDC back-channel logout | IdP 推 logout token 至 `POST /v1/teams/{slug}/sso/backchannel-logout`（驗 IdP 簽章）→ 撤該 `sub` session | 支援（若 IdP 提供） |
| Owner 手動 deprovision | `POST /v1/teams/{slug}/sso/deprovision {user}` 或既有 `0ops team members remove` | 支援 |
| 短 TTL 重驗 | SSO token TTL = `min(IdP session, 8h)`；過期後 CLI 重登 → IdP 已停用則登入失敗 | 支援（baseline 保證） |
| SCIM deprovision | IdP 推 SCIM `active=false` | v1.1（§ 16） |

### 7.2 撤權落地（靠既有 middleware，無平行旁路）

deprovision 動作於同一 transaction：

1. `idp_identity.deactivated_at = now()`。
2. 移除或軟停用 `team_membership`（v1：`team_membership.deactivated_at = now()`；`CheckMembership` 視 `deactivated_at != null` 為**非 member**，回 `404 team_not_found`，與 `auth-and-rbac` § 7.1 enumeration 規則一致）。
3. `UPDATE cli_token SET revoked_at = now() WHERE owner_user_id = $user AND team_id = $team`（device 與 pat 一併）。
4. invalidate `auth-and-rbac` § 10 之 argon2id token cache（broadcast；防快取殘留）。
5. 寫 audit `sso_deprovision_user`（§ 9）。

撤權後該員工任意 token 走既有五段鏈即雙重失效：`AuthBearer` 撞 `revoked_at` → `401 token_revoked`；即便快取競態，`CheckMembership` 撞 `deactivated_at` → `404 team_not_found`。**單一 deprovision 動作覆蓋該 user 在該 team 的全部 token**，解 AU3 之「逐一 revoke」。

### 7.3 SSO token TTL

- SSO-issued `cli_token`（`auth_source='sso'`）TTL = `min(IdP id_token/session 過期, 8h)`，可由 `idp_config.session_max_ttl_s` 覆寫（上限 24h）。
- 過期後 CLI 重登須再過 IdP；IdP 已停用之員工無法重新取得 token（即便無 back-channel logout，最差 8h 內失效）。
- 不沿用 `auth-and-rbac` § 4.3 device token 30 天 rolling refresh：SSO token **不 rolling refresh**（rolling refresh 會繞過 IdP 再驗，破壞集中撤權保證）。`X-0ops-Token-Refreshed` 對 `auth_source='sso'` token **不發**。

### 7.4 既有 PAT 在 SSO enforce 下的處理

- **PAT 不禁用，但受 membership 撤權涵蓋**：PAT 綁單一 team（`cli_token.team_id`，`auth-and-rbac` § 4.4），deprovision 之 § 7.2 步驟 3 已 `revoked_at` 全部含該 user 之 PAT；步驟 2 之 membership 停用使 PAT 同遭 `CheckMembership` 404。離職員工的長壽 PAT 因此**自動連帶失效**，無需逐一處理。
- **可選政策 `idp_config.pat_policy`**：
  - `allow`（預設）：仍允許 SSO-active 成員建立 PAT（CI/自動化需求）；PAT 受上述撤權涵蓋。
  - `disallow`：SSO-enforced team 禁止**個人** PAT 建立（`POST .../tokens` 回 `403 sso_pat_disabled`）；CI 改用 owner 簽發之 team service token（`auth_source='sso'` 不適用，service token 由 owner 維護、deprovision 不影響——因其 `owner_user_id` 為 owner 本人）。
- enforce 開啟時，既有由**非 owner 成員**持有之 PAT：標記為待重驗；v1 採「保留但下次該成員 SSO 登入時校驗其 idp_identity 仍 active，否則 § 7.2 撤權」。enforce 開啟**不**即時批次 revoke 既有 PAT（避免中斷 CI），但 deprovision 任一成員時其 PAT 必撤。

## 8. 與既有 device flow 共存

### 8.1 SSO-enforced team 的 CLI 登入

- UX 不變：`0ops auth login --team={slug}`（或 `--sso`）仍走 device-flow poll loop（`auth-login-flow` § 3.1）。
- 差異：`POST /v1/auth/device/start` 帶 `team_slug` → backend 查該 team `idp_config.enforce`：
  - `enforce=true`：回應之 `verification_uri` 指向**該 team IdP 的 authorize URL**（OIDC authorization-code + PKCE，沿用 `auth-login-flow` § 4.3 PKCE 構造），而非 GitHub `login/device`。
  - `enforce=false`：維持 GitHub device flow（既有路徑不變）。
- CLI 開瀏覽器 → 使用者在 IdP 認證 → IdP redirect 回 `GET /v1/auth/sso/{slug}/callback` → backend 交換 code、驗 id_token、JIT（§ 6）、簽 token → CLI poll loop 取得 `bearer_token`（沿用 `auth-login-flow` § 4.2 poll 200 形狀）。
- headless/無瀏覽器：複用 OIDC device authorization grant（若 IdP 支援）；否則指示使用者於有瀏覽器環境完成。

### 8.2 不破壞 agent 接線

- CLI/MCP 仍只讀寫 `~/.config/0ops/auth.json`（perm 0600）；token 解析順序不變（`auth-login-flow` § 5.2）。
- MCP server 仍**只讀不寫**（`auth-and-rbac` 硬規則 10）；SSO token 過期時 MCP 回既有 `unauthorized` 引導訊息「請先在 terminal 執行 0ops auth login，並重啟 MCP host」。
- 因 SSO token 不 rolling refresh（§ 7.3），使用者最差每 8h（或 `session_max_ttl_s`）跑一次 `0ops auth login`；agent 接線 UX 與既有「30 天重登」同形狀，僅週期縮短。

### 8.3 非 SSO 路徑共存

- 非 SSO-enforced team 之成員與 personal team：完全走既有 GitHub device flow / OAuth2 authcode，不受本 spec 影響。
- 同一使用者可同時持有 SSO team token 與 GitHub personal team token（`auth.json` 多 host/team entry 並存）。

## 9. 稽核（接 audit-log § 5.1）

### 9.1 新增 audit action

| Action | source | subject_type | 寫入時機 |
|---|---|---|---|
| `sso_login` | user | `team` | SSO callback 成功簽發 token（success）/ 驗章或 domain 不符（failure） |
| `sso_logout` | user / system | `team` | RP-initiated logout 或 back-channel logout |
| `sso_provision_user` | system | `membership` | JIT 首次建 user + membership |
| `sso_deprovision_user` | user / system | `membership` | § 7.2 撤權（owner 手動=user；back-channel/SCIM=system） |
| `sso_config_create` | user | `idp_config` | owner 建 IdP |
| `sso_config_update` | user | `idp_config` | owner 改 IdP（含 enforce 開關、pat_policy） |
| `sso_config_delete` | user | `idp_config` | owner 刪 IdP |
| `sso_domain_verify` | user | `idp_config` | domain DNS TXT 驗證通過/失敗 |

- 全部經 `audit-log` § 5.3 `audit.Log(ctx, entry)`；**client secret、id_token、logout token、DNS token 不入 args/result**（沿用 `audit-log` § 8 / `error-model` § 9 redactor；欄位 `client_secret`/`*_token`/`id_token` → `***`）。
- `outcome` 用 `success`/`failure`；back-channel/SCIM 來源 `source` 必為 `system` 且 `actor_user_id` 為 NULL（`audit-log` 硬規則 4）。
- CLI/MCP 端不寫 audit（`audit-log` 硬規則 8）；只 backend 寫。

## 10. 檔案結構

```
0ops/
├── internal/
│   └── server/
│       ├── auth/
│       │   └── sso/
│       │       ├── oidc.go              # discovery 拉取 + JWKS 快取 + id_token 驗章
│       │       ├── authorize.go         # 構造 IdP authorize URL（authcode + PKCE）
│       │       ├── callback.go          # GET .../sso/{slug}/callback：code 交換 + 驗 id_token
│       │       ├── jit.go               # JIT provisioning（upsert user + membership + idp_identity）
│       │       ├── deprovision.go       # 集中撤權（membership 停用 + token revoke + cache invalidate）
│       │       ├── backchannel.go       # POST .../sso/backchannel-logout 接收 + 驗章
│       │       ├── domain.go            # domain DNS TXT 驗證
│       │       ├── config.go            # idp_config CRUD（owner-only）
│       │       ├── enforce.go           # device/start 端 enforce 判斷與 IdP 導向
│       │       ├── metrics.go
│       │       └── doc.go
│       └── routers/
│           └── sso.go                   # /v1/teams/{slug}/sso/*、/v1/auth/sso/{slug}/callback
└── migrations/
    └── 00013_idp_config_and_sso.sql     # idp_config / idp_domain / idp_identity + membership/cli_token 補欄位
```

## 11. Schema

### 11.1 新表

```sql
-- IdP 設定（一 team 一 IdP，v1）
idp_config(
  id uuid pk,
  team_id uuid fk not null unique,                         -- 一 team 一 IdP（v1）
  protocol text not null default 'oidc'
            check(protocol in ('oidc')),                   -- v1 僅 oidc；'saml' 待 v1.1 擴充 CHECK
  display_name text,
  issuer text not null,                                    -- OIDC issuer URL
  discovery_url text,                                      -- .well-known/openid-configuration（可由 issuer 推導）
  client_id text not null,
  client_secret_ref text not null,                         -- 指向 secrets store；DB 不存明文（secrets-management）
  scopes text[] not null default '{openid,email,profile}',
  enforce bool not null default false,                     -- true = 該 team 強制走 SSO
  jit_default_role text not null default 'member'
            check(jit_default_role in ('admin','member','viewer')),  -- 不允許 JIT 給 owner
  group_claim text,                                        -- id_token 內 group claim 名稱（可選）
  group_role_map jsonb,                                    -- {idp_group: 0ops_role}（可選）
  pat_policy text not null default 'allow'
            check(pat_policy in ('allow','disallow')),     -- SSO enforce 下個人 PAT 政策
  session_max_ttl_s int not null default 28800,            -- SSO token TTL 上限（預設 8h；上限 86400）
  -- SAML 預留（v1 nullable 不使用）
  metadata_url text, sp_entity_id text, idp_cert text,
  created_by uuid fk references user_account(id),
  created_at timestamptz default now(), updated_at timestamptz
)

-- IdP 綁定之 domain（DNS TXT 驗證）
idp_domain(
  id uuid pk,
  idp_config_id uuid fk not null references idp_config(id) on delete cascade,
  team_id uuid fk not null,
  domain citext not null unique,                           -- 全域唯一：一 domain 不可綁兩 team
  verification_token text not null,                        -- DNS TXT 值；redact，不入 audit
  verified bool not null default false,
  verified_at timestamptz,
  created_at timestamptz default now()
)

-- IdP 身份對應（OIDC sub ↔ 0ops user）
idp_identity(
  idp_config_id uuid fk not null references idp_config(id) on delete cascade,
  user_id uuid fk not null references user_account(id),
  idp_subject text not null,                               -- OIDC 'sub'（IdP-stable）
  email citext,
  last_login_at timestamptz,
  deactivated_at timestamptz,                              -- IdP 停用 → 集中撤權標記
  created_at timestamptz default now(),
  primary key(idp_config_id, user_id),
  unique(idp_config_id, idp_subject)
)
```

### 11.2 既有表補欄位

```sql
-- team_membership：標記 JIT 來源 + 軟停用（撤權用）
alter table team_membership
  add column auth_source text not null default 'native'
        check(auth_source in ('native','sso')),
  add column deactivated_at timestamptz;                   -- CheckMembership 視 != null 為非 member（404）

-- cli_token：標記 SSO 來源 + 綁 idp_config（撤權批次定位用）
alter table cli_token
  add column auth_source text not null default 'native'
        check(auth_source in ('native','sso')),
  add column idp_config_id uuid references idp_config(id);  -- SSO-issued token 連結；native 為 null
```

> `cli_token.kind` 維持既有 CHECK `in ('device','pat')`（migration `00010`）；SSO-issued 0ops bearer 仍為 `kind='device'`，以 `auth_source='sso'` 區分，不擴充 `kind` 列舉。

### 11.3 索引

```sql
CREATE INDEX idp_domain_config       ON idp_domain (idp_config_id);
CREATE INDEX idp_identity_user       ON idp_identity (user_id);
CREATE INDEX idp_identity_active     ON idp_identity (idp_config_id) WHERE deactivated_at IS NULL;
CREATE INDEX cli_token_idp_revoke    ON cli_token (owner_user_id, team_id) WHERE auth_source = 'sso';
CREATE INDEX team_membership_active  ON team_membership (team_id, user_id) WHERE deactivated_at IS NULL;
```

## 12. 端點規格

| Method | Path | Role/Scope | 行為 |
|---|---|---|---|
| POST | `/v1/teams/{slug}/sso` | owner + `sso:manage` | 建 `idp_config`（secret 進 secrets store，回不含 secret） |
| GET | `/v1/teams/{slug}/sso` | admin + `sso:manage`（讀）/ owner | 讀 IdP 設定（不含 secret） |
| PATCH | `/v1/teams/{slug}/sso` | owner + `sso:manage` | 改設定（含 `enforce`、`pat_policy`、`jit_default_role`） |
| DELETE | `/v1/teams/{slug}/sso` | owner + `sso:manage` | 刪 IdP（enforce 須先關） |
| POST | `/v1/teams/{slug}/sso/domains` | owner + `sso:manage` | 加 domain，回 DNS TXT 指示 |
| POST | `/v1/teams/{slug}/sso/domains/{id}/verify` | owner + `sso:manage` | 查 TXT，命中設 `verified` |
| GET | `/v1/auth/sso/{slug}/callback` | 無（IdP redirect；state/PKCE 驗） | code 交換 + 驗 id_token + JIT + 簽 token |
| POST | `/v1/teams/{slug}/sso/backchannel-logout` | 無（驗 IdP logout token 簽章） | 撤該 sub session |
| POST | `/v1/teams/{slug}/sso/deprovision` | owner + `sso:manage` | 手動集中撤權（§ 7.2） |

- 所有 team-scoped endpoint 走 `auth-and-rbac` § 6.1 五段鏈；非 member/team 不存在一律 `404 team_not_found`（§ 7.1 enumeration 防範，SSO 不繞過）。
- `callback`/`backchannel-logout` 不經 `AuthBearer`（無 0ops token），但必驗 `state`+PKCE（callback）/ IdP 簽章（back-channel），失敗回 4xx 並入 audit failure。

## 13. CLI / MCP

### 13.1 CLI

```
$ 0ops auth login --team=acme-corp           # acme-corp enforce=true
Opening your IdP to sign in...
Open https://acme.okta.com/oauth2/v1/authorize?... and complete sign-in
logged in as alice@acme.com on https://api.0ops.tw (team acme-corp, via SSO)

$ 0ops sso status --team=acme-corp            # owner/admin 可看
PROTOCOL  ISSUER                        ENFORCE  DOMAINS(verified)   PAT_POLICY
oidc      https://acme.okta.com         true     acme.com(✓)         disallow

$ 0ops sso deprovision --team=acme-corp --user=bob@acme.com   # owner
deprovisioned bob@acme.com: 1 membership deactivated, 3 tokens revoked
```

- SSO 設定/deprovision 命令需 owner token；非 owner 回 `403 forbidden_role`。
- 不顯示 client secret、id_token、DNS token 明文。

### 13.2 MCP

- v1 **不**新增 SSO 設定類 write tool（IdP 設定為高敏感 owner 動作，走 CLI/Web）。
- 既有 read tool（如 `list_members`）回應之 membership 含 `auth_source` 欄位（DTO 擴充）；deprovision 不開 MCP write tool（避免 agent 誤撤權）。

## 14. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| JIT 建 user + membership | mock OIDC callback（新 sub） | DB 新增 `user_account` + `team_membership(auth_source='sso')` + `idp_identity` |
| JIT 預設 role | callback 無 group 映射 | membership.role = `jit_default_role`（不為 owner） |
| group → role 映射 | callback id_token 帶映射命中 group | membership.role = 映射結果（封頂 admin） |
| domain 不符拒登 | callback email domain 非 verified | `403 sso_domain_mismatch` + audit `sso_login` failure |
| **IdP 停用 → token 失效** | deprovision 後立即用該 user device token + PAT 呼 API | device token `401 token_revoked`；PAT 經 `CheckMembership` `404 team_not_found` |
| 撤權覆蓋全部 token | user 持 2 device + 1 PAT，deprovision 一次 | 3 個 `cli_token` 全 `revoked_at != null`；membership `deactivated_at != null` |
| back-channel logout | mock IdP logout token（驗章通過） | 對應 sub 之 SSO token 撤銷 + audit `sso_logout` source=system |
| SSO token 不 rolling refresh | SSO token 距上次更新 > 24h 的請求 | **無** `X-0ops-Token-Refreshed` header |
| SSO token TTL 上限 | 設 `session_max_ttl_s` | 簽發 token `expires_at - issued_at` ≤ min(IdP session, 設定值, 24h) |
| **team scope 不被 SSO 繞過** | SSO team A 成員以 SSO token 呼 `/v1/teams/teamB/...` | `404 team_not_found`（沿用 `auth-and-rbac` § 7.1） |
| RBAC 不被 SSO 繞過 | JIT member 呼 owner-only `POST .../sso` | `403 forbidden_role` |
| owner-only 設定 | admin token 改 `idp_config` | `403 forbidden_role`（scope `sso:manage` owner-only） |
| PAT policy disallow | enforce + `pat_policy=disallow` 下建 PAT | `403 sso_pat_disabled` |
| 一 domain 不綁兩 team | team B 驗證 team A 已綁 domain | UNIQUE 衝突 → `409 domain_taken` |
| enforce 前置 | 無 verified domain 開 enforce | `400 sso_no_verified_domain` |
| **SSO login 入 audit** | 完成 SSO 登入 | `audit_log` 含 `sso_login` success；args 不含 id_token/secret 明文 |
| secret 不落地 | 建 idp_config 後查 audit/log | `client_secret`/DNS token = `***`；DB `client_secret_ref` 為參照非明文 |
| device flow 共存 | 非 enforce team `0ops auth login` | 維持 GitHub device flow，不受影響 |
| MCP 不寫 token | SSO token 過期後 MCP call | 回 `unauthorized` 引導訊息，`auth.json` 未被 MCP 改寫 |

## 15. 與其他 spec 接合

| 接合 | spec / 來源 |
|---|---|
| role/scope/team 隔離模型（沿用，不另造） | `auth-and-rbac` § 6、§ 7；ADR-0001 |
| `CheckMembership` 撤權落地（deactivated_at → 404） | `auth-and-rbac` § 6.2 |
| device flow / OAuth2 authcode + PKCE 複用 | `auth-login-flow` § 3、§ 4.3 |
| token cache invalidate（撤權時） | `auth-and-rbac` § 10 |
| audit 寫入點 + 新增 action | `audit-log` § 5.1、§ 5.3 |
| redactor（secret/token 不落地） | `audit-log` § 8；`error-model` § 9 |
| IdP client secret 加密儲存 | `secrets-management` |
| 解的威脅 AU3（集中撤權） | `threat-model` § 5.2、§ 6 |
| 統籌計畫拆解 row（P2） | `trust-and-compliance/plan.md` § 5.1 |
| 新 scope `sso:manage` 列舉 + auth path 決策 | ADR-0016 |
| Team tier SSO 商業承諾 | `0ops-business-plan.md` line 205 |

## 16. 對 `docs/0ops-plan-schema.md` 的修改清單

> 待本 spec 對應 milestone 上線時回填 schema doc（沿用該 doc § 開頭「下游 spec schema 補丁清單」格式）。

1. 新增 `idp_config`、`idp_domain`、`idp_identity` 三表（本 spec § 11.1）。
2. `team_membership`：補 `auth_source`、`deactivated_at`（§ 11.2）。
3. `cli_token`：補 `auth_source`、`idp_config_id`（§ 11.2）。
4. ADR-0016：登記新 scope `sso:manage`（owner-only）與 OIDC auth path 決策；`internal/shared/rbac/scope.go` 同步。

## 17. Open issues

- 多 IdP / 一 team：v1 一 team 一 IdP；多 IdP 與 IdP federation 列 v2。
- SCIM 2.0 自動 provisioning/deprovisioning + group 即時同步：v1.1；v1 以 back-channel logout + 短 TTL + 手動 deprovision 達成撤權。
- SAML 2.0：觸發條件為「僅支援 SAML 之 design partner」；schema 已預留 nullable 欄位，`protocol` CHECK 待擴充。
- group → role 自動降級政策：v1 只記 audit 不自動降級；v1.1 拍板是否自動同步。
- enforce 開啟時既有非 owner PAT 之批次處理：v1 不即時 revoke（避免中斷 CI），靠下次登入校驗 + deprovision 連帶撤；是否提供 owner 一鍵「撤所有非 SSO token」列 v1.1。
- back-channel logout 之 IdP 覆蓋率（並非所有 IdP 支援）：未支援者僅靠短 TTL 重驗；TTL 預設 8h 是否足夠依 design partner 風險偏好調。
- 首批 IdP 之 group claim 形狀差異（Okta 字串 vs Azure object id）：`group_role_map` 以原始 claim 值為 key，文件需給各 IdP 設定範例（runbook）。
- 自架 host 之 SSO callback URL 與多 host：每 deployment callback base URL 由 `OPS_HOST` 推導；onboarding runbook 待補。

## 18. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. SSO **不得另造平行權限模型**：JIT 建立之 membership 必為既有 `owner/admin/member/viewer` 之一；不得新增角色或旁路 RBAC。
2. SSO **不得繞過 team scope**：SSO-issued token 走既有五段 middleware 鏈；跨 team 一律 `404 team_not_found`，與 `auth-and-rbac` § 7.1 一致。
3. JIT **不得直接賦予 `owner`**：`jit_default_role` 與 `group_role_map` 解析結果封頂 `admin`；owner 僅能由既有 owner 手動指派。
4. SSO 設定（建/改/刪 IdP、domain 驗證、enforce 開關、deprovision）為 **owner-only** + scope `sso:manage`；不得降低為 admin 或更低。
5. 集中撤權必同時 (a) 停用/移除 `team_membership` 且 (b) `revoked_at` 該 user 該 team 全部 `cli_token`；不得只做其一。撤權後該 user token 必經既有鏈失效。
6. SSO-issued token **不得 rolling refresh**、**不發** `X-0ops-Token-Refreshed`；TTL ≤ `min(IdP session, session_max_ttl_s, 24h)`（防繞過 IdP 再驗）。
7. IdP client secret、id_token、logout token、DNS 驗證 token **不得**入 DB 明文、log、metric label、audit args/result（沿用 redactor；DB 僅存 `client_secret_ref` 參照）。
8. SSO login/logout/provision/deprovision/config 變更**必入 `audit_log`**（§ 9）；IdP-initiated 來源 `source='system'` 且 `actor_user_id` 為 NULL；不得弱化或略過 audit 寫入。
9. 一個 `domain` **全域唯一**綁定，不可同時屬兩 team；未驗證 domain 不可開 enforce。
10. v1 **不實作 SAML**；`idp_config.protocol` CHECK v1 僅允許 `'oidc'`；SAML 欄位保持 nullable 未使用，不得宣稱已支援（避免合規造假，承 `trust-and-compliance/plan.md` § 6 規則 1）。
11. MCP server **不得寫** `auth.json`、**不得**提供 SSO 設定/deprovision write tool（沿用 `auth-and-rbac` 硬規則 10；防 agent 誤改權限）。

## 19. 實作狀態（M9.5，2026-06-29）

> 誠實切分已交付（可被 `./manage.sh test` 證明）與 deferred-validation（需真 IdP /
> 既有功能尚未具備），承 `trust-and-compliance/plan.md` § 6 規則 1 與 lessons L013/L015。

### 19.1 已落地且可測

- **§11 schema**：migration `00017_idp_config_and_sso.sql`（三表 + `team_membership`/`cli_token` 補欄位；
  `auth_source` 走 nullable→backfill→SET NOT NULL→SET DEFAULT 三步，符 migrationlint R2；index 走
  `CONCURRENTLY` 符 R1）。DB 整合測覆蓋 CRUD、唯一性、JIT、deprovision、token 簽發。
- **§7.2 集中撤權**：`DeprovisionSSOUser` 同 tx 停用 `idp_identity`+`team_membership` 並 `revoked_at`
  該 user 該 team 全部 `cli_token`（device+pat，sso+native）；`CheckTeamMembership` 加 `deactivated_at
  IS NULL`（手寫，因 sqlc schema snapshot 未含新欄），使單次 deprovision 全 team 生效（撤權後 token
  經既有鏈：`AuthBearer` 撞 `revoked_at`→401、`CheckMembership` 撞 deactivated→404，分層各有測試覆蓋）。
- **§6 JIT**：`JITProvision` upsert user+identity+membership（冪等）；`ResolveJITRole` group→role 封頂
  `admin`（hard rule #3）。callback httptest 驗 JIT + audit。
- **§3/§12 OIDC 驗證 + 端點**：自實作 OIDC 驗證器（discovery + JWKS RS256，可注入 httpClient，免新依賴）；
  config CRUD（owner-only）/domain add+verify（resolver 可注入）/callback/backchannel/deprovision handler；
  scope `sso:manage`（owner 寫、admin 讀）入 RBAC；httptest 驗 RBAC、跨 team 404、enforce 前置、domain 衝突、
  domain mismatch 403、audit（含 source=system/actor NULL）。
- **§7.3 TTL**：`SSOTokenExpiry = min(IdP, session_max_ttl_s, 24h)` 純函式；SSO token 不 rolling refresh
  （專案本無 rolling refresh 機制，自然成立）。
- **§7.4 PAT policy**：`createTokenHandler` 以 optional-capability 型別斷言檢查 enforce+`disallow`→
  `403 sso_pat_disabled`；httptest 覆蓋。
- **§13 CLI**：`0ops sso status` / `0ops sso deprovision`（含 backendclient + DTO contract + CLI 測）。

### 19.2 Deferred-validation（需真 IdP / 既有功能缺口；非本 task `manage.sh test` 可驗）

- **device-flow 瀏覽器 IdP 重導端到端**：未改寫既有 GitHub device flow（屬 `auth-login-flow` 範疇且
  `DeviceStartRequest` 不帶 team_slug，全面接線過大）。building block（`BuildAuthorizeURL` 純函式 + 獨立
  callback 端點）已交付且可測；enforce→device 授權頁改走 IdP 的端到端串接 deferred（需真 IdP + 瀏覽器）。
- **真 OIDC code 交換（token endpoint）**：`HTTPCodeExchanger` 為預設實作但需 live IdP + client secret；
  callback 測試以注入式 stub exchanger 驗證 JIT/簽發/audit 全鏈，真交換 deferred。
- **client secret at-rest 加密 + state/secret 共享儲存（HA 前置）**：`SecretStore`/`StateStore` 介面 +
  process-local `MemorySecretStore`/`MemoryStateStore` 預設（DB 僅存 `client_secret_ref`，符 hard rule #7）。
  此 in-memory 預設為 **single-process**，在多 replica（ADR-0008 HA：2 replica + leader）下 secret/state
  不跨 replica 可見 → 真 login/code-exchange 路徑須先以 durable 共享 store（DB / shared cache，依賴
  `secrets-management`，repo 尚無本體）替換。因 §19.2 之真 code-exchange 與 device-flow 重導本即 deferred，
  in-memory 預設只在測試與單機 dev 被完整行使；**啟用真 IdP login 前必先換 durable store**，此前置與
  durable 加密一併 deferred。
- **JWKS 快取**：v1 每次驗證即抓 JWKS（登入頻率低，≤ 每 session_max_ttl 一次）；快取 deferred。

### 19.3 與 spec 之有意微調

- **back-channel logout 路徑**：spec § 12 列於 `/v1/teams/{slug}/sso/backchannel-logout`，但該前綴由
  Bearer middleware 保護而 back-channel 為 IdP 無 token 呼叫；實作改置於 `/v1/auth/sso/{slug}/backchannel-logout`
  （與同屬無認證的 callback 並列，避開 chi mount 衝突）。語意不變（驗 IdP 簽章 + 撤該 sub），非 hard rule
  約束之路徑。
