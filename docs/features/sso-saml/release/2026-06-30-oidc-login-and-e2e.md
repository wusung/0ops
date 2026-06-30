# M9.5 OIDC 登入入口 + compose e2e（2026-06-30）

> 來源：sso-saml spec § 12（端點）、§ 8.1（authorize→callback hop）、§ 19.2（deferred 盤點）。
> 本檔記錄「為跑通 OIDC 端到端 e2e 而補上 server 端 `/authorize` 登入入口」之設計與邊界，
> 並定義 `tasks/e2e-sso.sh` + compose mock IdP 棧。對齊 `docs/features/e2e-testing/spec.md`。

## 1. 結論（先讀）

- 既有缺口：server **沒有 `/authorize` 登入入口**。`MemoryStateStore.Save` 無任何 HTTP handler
  呼叫，`BuildAuthorizeURL` 只在單元測試被叫；故 `callback` 的 `State.Consume` 在 live server
  永遠失敗 —— OIDC 登入無法在真棧上發起（spec § 19.2 列為 deferred）。
- 本次補上 `GET /v1/auth/sso/{team_slug}/authorize`：產 state + PKCE、`State.Save`、302 重導
  IdP authorization endpoint。這讓 OIDC dance 能在 compose 真棧上端到端跑通。
- state 仍用 process-local `MemoryStateStore`：對 **single-replica**（dev / e2e / 單機 prod）
  正確且充分。**multi-replica HA**（ADR-0008，2 replica + leader）下 state 不跨 replica，需
  durable 共享 StateStore —— 此為**窄化後的 deferred**（見 § 6），不阻擋本次 e2e。
- e2e（`tasks/e2e-sso.sh`）對 compose 棧 + in-repo mock IdP 行使完整 dance，坐實 M9.5 招牌保證
  **集中撤權端到端**（spec § 19.1 自承僅「分層各有測試覆蓋」、無端到端）。

## 2. `/authorize` 登入入口

### 2.1 路由與認證

- `GET /v1/auth/sso/{team_slug}/authorize`，掛在 `RegisterAuthRoutes`（與 callback 同屬無
  0ops token 的 IdP-facing 子樹；登入起點本就無 token）。
- 流程：
  1. `ResolveTeamBySlug` → `GetIdPConfigByTeam`（無 config → `sso_not_configured` 404）。
  2. 產 `state`（≥32B 隨機）、PKCE `code_verifier` + `code_challenge`（S256）。
  3. `State.Save(state, StateData{TeamSlug, RedirectURI: callbackRedirectURI(slug), CodeVerifier})`。
  4. `Verifier.Discover(issuer)` 取 authorization_endpoint → `BuildAuthorizeURL(disc, cfg,
     redirectURI, state, codeChallenge)`。
  5. `302` `Location: <IdP authorize URL>`。
- 失敗 fail-closed：discovery 不可達 → `sso_idp_unreachable` 422，不洩漏內部。

### 2.2 安全不變量（必測）

- state 一次性：callback `Consume` 後再用同 state 必失敗（既有 `MemoryStateStore` 已保證）。
- PKCE：authorize 存 `code_verifier`，callback 交換時帶回；challenge=S256(verifier)。
- 不繞既有 callback 強制：authorize 只負責「發起」，所有 id_token/email/domain/JIT 驗證仍由
  callback 既有路徑執行（hard rule #1/#2 不變）。

## 3. in-repo mock IdP（`src/cmd/devtools/mock-idp`）

最小 OIDC provider，僅供 dev/e2e；production compose 永不含。以 RS256 簽 id_token（重用
`oidc_test.go` 既有 keygen/JWKS 形狀）。端點：

| 端點 | 行為 |
|---|---|
| `GET /.well-known/openid-configuration` | 回 issuer/authorization_endpoint/token_endpoint/jwks_uri（指回自身） |
| `GET /jwks` | 回 RS256 公鑰（單把 key，kid 固定） |
| `GET /authorize` | auto-approve：直接 `302` 回 `redirect_uri?code=<code>&state=<state>`（免互動） |
| `POST /token` | 驗 `grant_type=authorization_code` + code，回 `{id_token, expires_in}`；id_token claims：`sub`/`email`/`email_verified=true`/`aud=client_id`/`iss`/`exp` + 可選 `groups` |

- 設定經 env：`MOCK_IDP_ISSUER`（對外 issuer URL，須等於 server 連得到的位址）、
  `MOCK_IDP_CLIENT_ID`、`MOCK_IDP_EMAIL`、`MOCK_IDP_GROUPS`。
- issuer 一致性：server 走 compose DNS `http://mock-idp:9000`；該位址同時是 discovery 文件裡的
  issuer，且 id_token `iss` 相同（VerifyIDToken 比對 iss）。

## 4. compose e2e 棧（`compose.e2e.yaml` overlay）

疊在 root `compose.yaml` 上：

- 新增服務 `mock-idp`（`build: src/cmd/devtools/mock-idp`，healthcheck 打 discovery）。
- 覆寫 `server` env：`OPS_PUBLIC_BASE_URL=http://server:8080`（callback redirect_uri 用）、
  確保 server 連得到 `mock-idp:9000`。
- `server.depends_on.mock-idp: service_healthy`。
- 不掛 host podman socket、不需 registry（SSO 不碰 build path），e2e 可關掉非必要服務。

啟動：`podman compose -f compose.yaml -f compose.e2e.yaml up -d --wait`。

## 5. e2e dance（`tasks/e2e-sso.sh`）

全程經 live HTTP（`OPS_HOST`），僅 1 筆 psql fixture（domain 標 verified，理由見下）：

1. **fixture**：以 owner token + team 為前置（沿用既有 bootstrap 取 owner bearer）。
2. owner→`POST /v1/teams/{slug}/sso`（HTTP）：建 idp_config，issuer=`http://mock-idp:9000`，
   client_id/secret=mock 對應值。secret 落 server 進程內 `MemorySecretStore`（故**必經 HTTP**，
   不可用 SQL 植 config，否則 secret 不在 store、code 交換失敗）。
3. owner→`POST /sso/domains`（HTTP）建 domain，取回 verification token。
4. **psql fixture**：`UPDATE idp_domain SET verified_at=now()`。理由：domain verify 走真 DNS TXT
   `_0ops-sso.<domain>`，離線環境不可得；verify handler 已於 handler tier 單測覆蓋，e2e 視
   verified domain 為前置 fixture（e2e-testing spec § 2.3 允許非招牌保證之 fixture 植入）。
5. `GET /v1/auth/sso/{slug}/authorize`（HTTP，`-i` 不自動跟隨）→ 斷言 `302`、`Location` 指
   mock-idp、且帶 state。
6. 跟隨 redirect 打 mock-idp `/authorize` → 斷言 `302` 回 `/callback?code&state`。
7. `GET /v1/auth/sso/{slug}/callback?...`（HTTP）→ server 對 mock-idp `/token` 交換 → 驗 JWKS →
   JIT → 簽 bearer。斷言 `200` + body `bearer_token` 非空。**【招牌前置：登入端到端成立】**
8. 用該 bearer→`GET /v1/teams/{slug}/apps` → 斷言 `200`（真 Bearer→CheckMembership→scope 鏈）。
9. owner→`POST /sso/deprovision {user|subject}` → 斷言 `200`、`membership_deactivated=true`、
   `tokens_revoked>=1`。
10. 同一 bearer 再打 `/apps` → 斷言 `401`（撞 `revoked_at`）。**【招牌保證：集中撤權端到端】**
11. psql 斷言：`team_membership.deactivated_at` 非空、該 user 全 `cli_token.revoked_at` 非空、
    `idp_identity.deactivated_at` 非空；`audit_log` 有 `sso_deprovision_user`（outcome=success）。

`manage.sh e2e-sso` 封裝 up→run→down；CI 以 `E2E_REQUIRE_PASS=1` 跑。

## 6. 邊界與 deferred（誠實盤點）

- **multi-replica HA 之 durable StateStore**：本次仍用 `MemoryStateStore`。窄化後的 deferred：
  「2-replica HA 下啟用 SSO 互動登入前，須換 DB/shared-cache 之 durable StateStore」。建議後續
  task：`sso_login_state` 表（state PK、team_id、code_verifier、redirect_uri、expires_at、
  consumed_at one-time）+ StateStore 介面加 ctx/error。不在本次範圍。
- **真 IdP（Google/Okta/Azure）互通**：e2e 用 in-repo mock IdP；真 IdP 互通屬人工/staging 驗收，
  deferred。
- **client secret at-rest 加密**：續用 `MemorySecretStore`（依賴 secrets-management，repo 尚無
  本體），deferred 不變。
- **device-flow 瀏覽器整合**：本次 `/authorize` 回 302 給 IdP，適用「瀏覽器/curl 跟隨」之 web
  登入起點；CLI device-flow 改走 IdP 重導的整合仍 deferred（spec § 19.2，不在本次）。

## 7. 對 spec 的回填

本次落地後，sso-saml spec § 19.2 之「真 OIDC code 交換」「authorize 端點」由 deferred 改為
**single-replica 已具備**，並新增「HA durable StateStore」為窄化 deferred。回填 spec 於本 task
合入時一併處理（AGENTS.md：系統行為改變必同步文件）。
