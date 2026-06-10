---
adr: "0007"
title: 客戶自有域名 TLS 與 Custom Hostname 機制
status: Accepted
date: 2026-05-09
tags:
  - tls
  - cloudflare
  - custom-hostname
  - dns
  - security
supersedes: []
superseded-by: []
---

# ADR-0007：客戶自有域名 TLS 與 Custom Hostname 機制

* Status：Accepted
* Date：2026-05-09
* 適用範圍：M3+；客戶自有域名（非 `*.jesontech.com`）的 TLS 終止與 cert lifecycle
* 來源：`docs/0ops-plan.md`「Runtime topology / Ingress / TLS」「Domain verify」「Risks #2」段；plan 將實際決策延後至本 ADR
* 上游依賴：[ADR-0004](0004-k3s-role-and-orchestrator.md)（單 Cloudflare Tunnel 為唯一 origin 入口）；[ADR-0001](0001-multi-tenancy-and-rbac.md)（domain_binding team 隔離）；[ADR-0002](0002-idempotency-and-compensation.md)（domain verify 為 reversible 副作用，補償順序在前）

## 0. TL;DR（先讀本段）

採用以下七項組合決策：

1. **TLS 終止位置**：Cloudflare edge；不採 origin TLS、不採客戶自帶 cert（v1）。
2. **Cert provisioning**：**Cloudflare for SaaS Custom Hostname API**；不採 DIY ACME（cert-manager + LetsEncrypt）。
3. **客戶 zone 控制權**：客戶**保留** zone；不要求 NS delegation。Onboarding 主路徑為「設 CNAME + TXT」，分兩種情境：
   * **非 apex（如 `app.example.com`）**：標準 CNAME。
   * **Apex（如 `example.com`）**：因 RFC 1034 不允許 CNAME 與其他 record 共存，需客戶 DNS 供應商支援以下三者之一：CNAME flattening（Cloudflare DNS）/ ALIAS（Cloudflare、Vercel、easyDNS）/ ANAME（DNSimple）。Backend 在 `add_domain_preview` 用 `publicsuffix` 偵測 hostname 是否為 apex，於 PlanPreview `side_effects` 顯示對應設定指示與已知不相容供應商清單；不相容客戶會在 24h verification window 內持續失敗導致 `domain_binding` 過期（backend 無法在 confirm 前驗外部 DNS 供應商能力，故由 verification 階段自然擋下）。
4. **驗證方法**：DNS-based（CNAME + TXT 雙條件）；30s 輪詢；TTL 24h 可 extend × 2（接續 plan）。
5. **Cert renewal**：完全交給 Cloudflare 自動；backend 不持有任何 customer cert material。
6. **撤銷流程**：客戶移除 CNAME 即觸發 reconciler 偵測 → 7 天 grace（hostname 進 `unverified` 狀態，不立即 503）→ 仍未恢復則 unbind Custom Hostname、release cert。
7. **計費邊界**：Cloudflare for SaaS 為 **0ops 成本**（不轉嫁客戶）；plan tier `pro` 才開放客戶域名功能（free / starter 限 `*.jesontech.com`）。

行為與 domain verify 流程細節以 `docs/0ops-plan.md`「Domain verify」段為準，本 ADR 不重述。

## 1. Context and Problem Statement

`*.jesontech.com` 子網域使用 0ops 自管 Cloudflare zone，wildcard cert 由 Cloudflare 自動簽發，TLS 在 Cloudflare edge 終止後經 Cloudflare Tunnel 進 K3s——這條路徑無 ADR 待決議。

但**客戶自有域名**（如 `app.example.com`）面對三個結構性問題：

1. **客戶 zone 不在 0ops Cloudflare account 內**：Cloudflare 預設的 Universal SSL / Advanced Certificates 只能簽發其管理的 zone；無法直接為「不在自己手上的 hostname」簽 cert。
2. **客戶不應交出 zone NS 控制權**：要求客戶把整個 zone 的 NS 指向 0ops，會破壞客戶既有 DNS 設定（其他子網域、MX、TXT 記錄），是商業上難以接受的條件。
3. **Cert renewal 不能成為人工負擔**：客戶域名數量會隨業務成長到上千個；renewal 必須 100% 自動化。

Plan 提到兩條候選路徑：(1) Cloudflare for SaaS Custom Hostname API；(2) 自管 ACME + cert-manager。本 ADR 在這兩條路徑之間做選擇並釘住 lifecycle 細節。

ADR-0001 已將 `domain_binding` 設為 team-scoped，ADR-0002 已將 domain verify 設為 reversible 副作用（在 image push 等 irreversible 步驟之前）；本 ADR 在這兩個約束下完成 TLS 機制決策。

## 2. Decision Drivers

* **DD1 客戶自助上線**：客戶從「填網域」到「網域上線」應為純自助流程；不需 0ops 客服介入。
* **DD2 Cert renewal 自動化**：客戶域名規模可能 > 1000；renewal 不可人工。
* **DD3 客戶 zone 控制權保留**：客戶不應為了用 0ops 而把 NS delegation 交出。
* **DD4 單一 origin 入口**：所有客戶域名與 `*.jesontech.com` 流量都經 Cloudflare Tunnel 進 K3s（接續 ADR-0004）；不分流。
* **DD5 v1 規模成本控制**：Cloudflare for SaaS 有訂閱費；需與商業 plan tier 對齊。
* **DD6 多租戶 blast radius 隔離**：A team 的客戶域名 cert / TLS 配置不應影響 B team。
* **DD7 撤銷可追溯**：客戶停用後 cert 與 hostname 必須在合理時間內被釋放，避免殘留 hostname 招致 typosquatting / 計費膨脹。

## 3. Considered Options

針對 (a) TLS 終止策略 與 (b) Cert provisioning 機制 做完整 alternative 比較；(c)(d) 為局部決策，列表帶過。

### 3.1 (a) TLS 終止策略

| Option | 描述 |
|---|---|
| **A1. Cloudflare edge（Custom Hostname）**（採用） | TLS 在 Cloudflare 邊緣終止；origin 走 Cloudflare Tunnel；cert 由 Cloudflare 持有 |
| A2. Origin TLS（K3s ingress 自簽 / cert-manager） | TLS 在 K3s ingress controller 終止；Cloudflare 不終止（passthrough） |
| A3. 客戶自帶 cert | 客戶上傳 PEM；backend 持有 private key；renewal 客戶責任 |
| A4. Mutual termination（edge + origin 都 TLS） | edge 終止後 backend 內部再 TLS |

### 3.2 (b) Cert Provisioning 機制

| Option | 描述 |
|---|---|
| **B1. Cloudflare for SaaS Custom Hostname API**（採用） | API 註冊 hostname；Cloudflare 簽 cert（背後為 LetsEncrypt 或 Google Trust Services） |
| B2. DIY cert-manager + LetsEncrypt（DNS-01） | 自管 cert-manager；DNS-01 challenge 需訪問客戶 zone DNS API |
| B3. DIY cert-manager + LetsEncrypt（HTTP-01） | HTTP-01 challenge 需 80 port 暴露於 hostname |
| B4. Cloudflare Origin CA + 客戶 zone Universal SSL | 客戶 zone 啟 Cloudflare proxy（橘色雲）；origin 用 Origin CA cert |

### 3.3 (c)(d) 局部選擇

| 子決策 | 採用 | 否決 | 一句結論 |
|---|---|---|---|
| (c) 客戶 zone 控制權 | 客戶保留 zone；非 apex 走 CNAME + TXT；apex 需 CNAME flattening / ALIAS / ANAME | NS delegation / Cloudflare partner / 不支援 apex | NS delegation 破壞客戶既有 DNS；partner 模式對 v1 規模 over-engineering；忽略 apex 會讓 `example.com` 這類常見輸入直接失效 |
| (d) 撤銷 grace 期 | 客戶移除 CNAME 後 7 天 grace 才 unbind cert | 立即撤銷 / 永不撤銷 | 立即撤銷對誤刪不友善；永不撤銷導致 hostname 累積與計費膨脹 |

## 4. Decision Outcome

採用 **A1 + B1**，搭配 (c) 客戶保留 zone、(d) 7 天 grace。

具體展開：

1. **TLS 路徑**：
   * 客戶端 ↔ Cloudflare edge（TLS 終止，Cloudflare 持 cert）↔ Cloudflare Tunnel ↔ K3s ingress（HTTP，無 TLS）↔ managed app pod。
   * Origin 端（K3s）不持任何 customer cert；只接 HTTP（plan 已決）。
2. **客戶 onboarding 流程**：
   * CLI / MCP 呼 `add_domain_preview { hostname }` → backend 計算 PlanPreview。
   * Backend 在 preview 階段先**判斷 hostname 是否為 apex**（hostname 與其註冊 zone 的 effective TLD+1 相同；用 `golang.org/x/net/publicsuffix` 計算）：
     * **非 apex**（如 `app.example.com`）：preview 通過；CLI / MCP 提示客戶設：
       * `CNAME <hostname> → tunnel-<id>.cfargotunnel.com`
       * `TXT _0ops-verify.<hostname> → <verification_token>`
     * **Apex**（如 `example.com`）：preview 通過但 PlanPreview `side_effects` 多一行警示「此域名為 apex，需 DNS 供應商支援 CNAME flattening / ALIAS / ANAME」；`requires_apex_compatible_dns=true` flag 帶入 confirm 階段。CLI / MCP 提示客戶設：
       * `ALIAS / ANAME <hostname> → tunnel-<id>.cfargotunnel.com`（依 DNS 供應商）；或於 Cloudflare DNS 啟 CNAME flattening
       * `TXT _0ops-verify.<hostname> → <verification_token>`
       * 客戶若無相容 DNS：preview 階段不阻擋 confirm（因 backend 無法在簽發 token 前驗 DNS 供應商能力），但 24h TTL 內 verification 會持續失敗，最終 `domain_binding` 過期。recommended path：CLI / MCP 在 preview 階段印出常見不相容供應商清單（見 Open Question Q5），引導客戶改子網域或遷移 DNS。
   * Confirm 後 backend：
     * 寫 `domain_binding` row（`kind=extra`、`verified=false`、`expires_at=now()+24h`、`is_apex={bool}`）。
     * 透過 Cloudflare API `POST /zones/{0ops_zone_id}/custom_hostnames` 建立 pending hostname；存 `cf_hostname_id`。
     * 不立即啟用：等 DNS 驗證通過才 activate（接續 plan「Domain verify」段）。
3. **驗證**：
   * Backend 背景 goroutine 每 30s 對 pending domain_binding 跑 `net.DefaultResolver` 雙條件查詢：
     * 條件 1：hostname 解析最終目標符合 `tunnel-<id>.cfargotunnel.com`（透過 `LookupCNAME` 對非 apex；對 apex 則用 `LookupHost` 後反向比對 tunnel target IP，因 ALIAS / ANAME / CNAME flattening 在 DNS 線上是 A/AAAA record，CNAME 不可見）。
     * 條件 2：`_0ops-verify.<hostname>` TXT token 正確。
   * 雙條件通過 → `verified=true`、`verified_at=now()`；呼 Cloudflare API activate hostname。
   * Cloudflare 端在 hostname active 後自動簽 cert（背後為 LetsEncrypt / Google Trust Services，由 Cloudflare 選）；通常 < 5 min。
4. **Cert renewal**：
   * 完全交給 Cloudflare 自動。
   * Backend 對 cert lifecycle **無責任**；不持 private key、不存 cert material。
   * Backend 只觀察 `GET /custom_hostnames/{id}` 的 `ssl.status`；如非 `active` 持續 > 24h，寫 audit_log + owner 通知。
5. **撤銷流程**：
   * 客戶在自己 zone 移除 CNAME → 30s polling 偵測到不再 resolve 至 tunnel → `domain_binding.health_check_failed_at=now()`。
   * **7 天 grace**：hostname 在 Cloudflare 端仍 active，但 `domain_binding.status='unhealthy'`；CLI / MCP 顯示警告。
   * 7 天後若 CNAME 仍未恢復 → backend 呼 Cloudflare API `DELETE /custom_hostnames/{id}`、`UPDATE domain_binding SET status='released'`、cert 釋放。
   * 客戶主動跑 `0ops domains remove <slug> <host>` → preview/confirm 後立即 unbind（無 grace）。
6. **Plan tier 邊界**：
   * `free` / `starter`：限 `*.jesontech.com` 子網域；客戶域名功能不開放。
   * `pro`：開放客戶域名；計費含 Cloudflare for SaaS 訂閱攤提。
   * Backend handler 在 `add_domain_preview` 端點檢查 `team.plan` + `kind=extra` 組合；不符回 `403 plan_required`。
7. **多租戶 blast radius 隔離**：
   * 所有客戶域名共用 0ops 同一個 Cloudflare account / zone（用於 Custom Hostname 註冊）。
   * Cloudflare API call 失敗時，使用 `0ops_cloudflare_api_calls_total{outcome=throttled}` 監控（接續 ADR-0006）；單 team 高頻呼叫不影響其他 team 的 ADD/REMOVE，但 read 路徑共享 rate limit 上限。
   * Custom Hostname 數量上限為 Cloudflare for SaaS plan 的硬性配額；接近上限觸發 dashboard 告警。

## 5. Pros and Cons of the Options

### 5.1 (a) TLS 終止策略

#### A1. Cloudflare edge（採用）

* Good：cert 完全 Cloudflare 自管；renewal 為 Cloudflare 責任；0ops 不持 customer private key。
* Good：DDoS 防護、WAF、CDN 為 Cloudflare edge 自帶能力。
* Good：與 plan 既定 `*.jesontech.com` 走同一條路徑；客戶域名與系統域名 TLS 模型一致。
* Good：cert 洩漏 blast radius 在 Cloudflare 內，0ops 不需做 cert revocation。
* Bad：完全依賴 Cloudflare；Cloudflare 服務中斷直接影響所有客戶域名。
* Bad：cert lifecycle 完全黑盒；無法控制簽發 CA（Cloudflare 自選 LetsEncrypt 或 Google）。
* Bad：Cloudflare for SaaS 為付費 feature；v1 成本入口存在。

#### A2. Origin TLS（K3s 自管）

* Good：完全控制 cert lifecycle；無第三方依賴。
* Good：可選任意 CA / 自簽。
* Bad：cert-manager + LetsEncrypt 在大規模 hostname 場景需 DNS-01 challenge；客戶 zone 不在 0ops Cloudflare account 內，DNS-01 需客戶授權。
* Bad：cert material 駐留 K3s；備援 / 跨 cluster 同步複雜。
* Bad：違反 DD2（renewal 自動化壓力轉到 0ops）。

#### A3. 客戶自帶 cert

* Good：客戶完全控制 cert。
* Bad：renewal 客戶責任；過期即停用，客戶體驗差。
* Bad：0ops 持 customer private key；責任邊界與合規風險。
* Bad：違反 DD2。

#### A4. Mutual termination

* Good：origin 端通信也加密。
* Bad：edge → origin 已在 Cloudflare Tunnel 內加密，無增益。
* Bad：origin cert 仍需自管；A2 的問題重現。

### 5.2 (b) Cert Provisioning 機制

#### B1. Cloudflare for SaaS Custom Hostname API（採用）

* Good：API 化 onboarding；客戶端只需設 CNAME + TXT。
* Good：cert renewal 完全 Cloudflare 自動；不需訪問客戶 DNS。
* Good：與 plan 既定 Cloudflare Tunnel 同帳號；運維面收歸於一。
* Good：支援 Cloudflare for SaaS 配套功能（Custom Hostname Fallback Origin、SSL/TLS settings per hostname）。
* Bad：訂閱費（依 Cloudflare 定價，每 hostname 月費或固定 plan）；需與 plan tier 對齊。
* Bad：Cloudflare API rate limit 在大規模批次 onboarding 時需處理。
* Bad：vendor lock-in 加深；遷移其他 CDN 需重做整套機制。

#### B2. DIY cert-manager + LetsEncrypt（DNS-01）

* Good：開源；無訂閱費。
* Good：cert lifecycle 完全自管；可審計。
* Bad：DNS-01 challenge 需客戶 zone DNS API 寫入權限；客戶不會給。
* Bad：可繞過——要求客戶設 `_acme-challenge.{hostname}` CNAME 至 0ops 控制的 acme-challenge zone（如 `acme-delegation.0ops.io`）；複雜度高、客戶教育成本大。
* Bad：違反 DD3（客戶 zone 控制權）；雖然只是 _acme-challenge subdomain，仍是額外設定負擔。
* Bad：rate limit 嚴格（LetsEncrypt 每 zone 每週 50 cert）；批次 onboarding 撞牆。

#### B3. DIY cert-manager + LetsEncrypt（HTTP-01）

* Good：實作最簡單。
* Bad：HTTP-01 challenge 必須客戶 hostname `:80` 路由至 LetsEncrypt validation server；要求客戶在 DNS 層先 proxy 到 0ops。
* Bad：與 Cloudflare Tunnel 模型衝突（tunnel 預設不開 80 port）。
* Bad：renewal 期間若 80 port 路由斷，cert 過期；運維脆弱。

#### B4. Cloudflare Origin CA + 客戶 Universal SSL

* Good：客戶 zone 只需開 Cloudflare proxy；簡單。
* Bad：需要客戶把 zone 加入 Cloudflare 並啟 proxy；違反 DD3。
* Bad：客戶零售商需 Cloudflare 帳號；商業上門檻高。

## 6. Consequences

### 6.1 Positive

* Cert lifecycle 完全 Cloudflare 自動；0ops 不持 customer cert material。
* 客戶 onboarding 為純自助；CNAME + TXT 雙條件即可上線。
* 客戶 zone 完全保留控制權；NS delegation 不需要。
* 與 `*.jesontech.com` 共用 Cloudflare Tunnel；運維面與成本面合一。
* DDoS / WAF / CDN 由 Cloudflare edge 自帶；無需自管。
* Plan tier 邊界（`pro` 才開）讓客戶域名功能成為商業槓桿，不是免費送出。

### 6.2 Negative

* 完全綁 Cloudflare；Cloudflare 服務中斷影響面大（plan「Risks #8」已提）。
* Cloudflare for SaaS 訂閱費為 0ops 持續成本；plan tier 定價必須涵蓋此攤提。
* Custom Hostname 數量上限為 Cloudflare plan 硬性配額；商業擴展時需升 enterprise plan。
* Cert 簽發 CA 由 Cloudflare 選；某些客戶可能要求特定 CA（v1 不支援）。
* 撤銷 7 天 grace 對誤刪友善但對停用客戶不立即釋放配額；高 churn 場景需重審。
* Cloudflare API rate limit 在批次 add / remove domain 時需 retry + backoff。

### 6.3 Neutral

* Cloudflare for SaaS 具體 SKU（Cloudflare for SaaS / Cloudflare for Platforms）由商業合約決議；不在本 ADR 範圍。
* Hostname 健康檢查（domain_binding.status='unhealthy' 觸發條件）細節屬 reconciler 範圍；本 ADR 僅約束「30s 雙條件偵測」。
* Plan tier `pro` 的具體 hostname 配額（每 team 上限）為 plan spec 範圍；不在本 ADR。
* Origin 端是否需要 hostname-based routing（K3s ingress 區分 jesontech.com vs 客戶域名）為實作細節。

## 7. Revisit Triggers

下列任一條件觸發時，應重新評估本 ADR 對應段：

1. **Cloudflare 服務中斷頻率 > 1 次 / quarter 影響 GA**：重審 (a)，可能引入多 CDN（Fastly / CloudFront）作為退路。
2. **Cloudflare for SaaS 配額撞牆**：Custom Hostname 接近 plan 上限 → 升級 plan 或重審 (b)（部分大客戶走 DIY ACME 退路）。
3. **特定客戶要求特定 CA**：商業合約要求自選 CA → 評估 BYOC（Bring Your Own Cert）退路。
4. **客戶域名 churn 率高**：撤銷 grace 7 天造成配額浪費 → 重審 (d) 縮短 grace 或加自動化召回。
5. **Cloudflare for SaaS 定價變動**：訂閱費突升 → 重審 (b)，可能評估 DIY ACME 為 fallback。
6. **企業客戶要求 origin TLS**：合規要求 end-to-end 加密（即使在 Cloudflare Tunnel 內）→ 重審 (a)，可能 A4。
7. **DNS-01 acme-challenge delegation 模式成熟**：若 cert-manager 對「_acme-challenge CNAME 委派」模式變成業界標準 → 評估 B2 為退路。

## 8. More Information

* Domain verify 流程（CNAME + TXT、30s polling、TTL 24h、extend × 2）：`docs/0ops-plan.md`「Domain verify」段。
* `domain_binding` schema：`docs/0ops-plan.md`「DB schema」段。
* Cloudflare Tunnel 為唯一 origin 入口：[ADR-0004 K3s 角色與 v1 容器編排器選擇](0004-k3s-role-and-orchestrator.md) 第 4 節。
* Domain verify 為 reversible 副作用，補償順序在 image push 之前：[ADR-0002 Idempotency 與副作用補償](0002-idempotency-and-compensation.md) 第 4 節。
* Cloudflare API rate limit 監控：[ADR-0006 Observability baseline](0006-observability-baseline.md)「Metrics 暴露」段（`0ops_cloudflare_api_calls_total{outcome=throttled}`）。

## 9. Open Questions

下列問題不阻擋本 ADR 通過，但需在 M3（GA 前）敲定：

1. **Cloudflare for SaaS SKU 選擇**：Cloudflare for SaaS（per-hostname 計費）vs Cloudflare for Platforms（meta-customer 模型）；商業合約敲定。
2. **Plan tier `pro` 客戶域名配額**：每 team 上限多少 custom hostname？超出處理（拒絕 / 升 plan / 加價）。
3. **Cert 簽發 CA 觀察**：Cloudflare 實際使用哪家 CA（LetsEncrypt vs Google Trust Services）會否對部分客戶端（舊 Android / 嵌入式）相容性產生影響？
4. **撤銷 grace 期是否與 plan 對齊**：plan tier 升降級時 hostname 配額調整 grace 是否與本 ADR 7 天一致？
5. **Apex 不相容 DNS 供應商清單**：哪些常見 DNS 供應商不支援 CNAME flattening / ALIAS / ANAME（如部分企業 DNS、Microsoft Azure DNS classic、舊版 GoDaddy）？需於 onboarding 文件落地相容性矩陣，並讓 backend `add_domain_preview` 在 apex case 時主動印出該清單，避免客戶設了「ALIAS-named 但實際不會 flatten 至 IP」的失敗模式。
6. **`tunnel-<id>.cfargotunnel.com` 命名穩定性**：tunnel ID rotation 時所有客戶 CNAME 失效；rotation 政策與通知機制需明確（v1 不 rotate；rotation 為 v2 事件）。
7. **`*.jesontech.com` wildcard 與 custom hostname 衝突**：客戶若把網域 CNAME 至 `nextdemo.jesontech.com` 而非 tunnel hostname，TLS 行為待 spike。
