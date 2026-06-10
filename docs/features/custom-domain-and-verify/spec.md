# Feature Spec：custom-domain-and-verify

> **狀態**：draft（plan tier 部分待 ADR-0011 拍板）
> **來源**：`docs/0ops-plan.md`「Domain verify」段；ADR-0007（客戶域名 TLS）；本 spec 依賴 `winshare-subdomain-and-tunnel`、`preview-confirm-gate`、`gitops-render-and-argocd`、`reconciler-and-incident`、`error-model`、`shared-dto-and-contract`
> **適用範圍**：客戶自有域名之 add / verify / remove 流程；CNAME + TXT 雙條件、apex 偵測、24h TTL + extend、7 天 grace；`pro` plan tier 才開放
> **對應 Milestone**：M3（與 `github-app-install-flow` 並行）

## 1. 結論（先讀本段）

- 客戶域名 add 流程：`add_domain_preview { app_slug, hostname }` → 偵測 apex / 非 apex → 在 PlanPreview side_effects 中顯示對應 DNS 設定指示 → confirm 後寫 `domain_binding(verified=false, expires_at=now()+24h)`、註冊 Cloudflare Custom Hostname
- 驗證採 DNS-based 雙條件：CNAME（apex 走 ALIAS/ANAME/CNAME flattening）+ `_0ops-verify.<host>` TXT；30s polling
- TTL 24h，`--extend` 最多 2 次（共 72h）；過期後 hostname 進 `pending → expired`，保留 7 天供使用者重啟（重發 token）
- 已 verified hostname 撤銷流程：客戶移除 CNAME → reconciler 偵測不再 resolve → 7 天 grace（`unhealthy` 狀態）→ 仍未恢復則 unbind Cloudflare hostname
- Plan tier 限制：`pro`+ 才開；`free` / `starter` 加 `extra` 域名直接 403 `plan_required`（plan tier 細節待 ADR-0011）
- Apex 偵測用 `golang.org/x/net/publicsuffix`；偵測為 apex 時 PlanPreview side_effects 多顯示「需 ALIAS / ANAME / CNAME flattening」與已知不相容 DNS 供應商列表
- Verification token：32-byte hex；產於 preview 階段；TXT 名為 `_0ops-verify.<host>`
- Reconciler 30s polling 偵測；多 backend pod 場景僅 leader 跑 polling（M5 對齊）
- Cert 完全 Cloudflare 自管；backend 不持 customer cert material

## 2. 範圍

### 2.1 包含
- `add_domain` / `remove_domain` action 之 preview-confirm
- `verify_domain` 主動觸發 endpoint（`POST .../domains/{host}:verify`）
- Apex 偵測與 PlanPreview side_effects 提示
- `domain_binding` 表之欄位語意與狀態機（pending → verified → unhealthy → released）
- 驗證 polling 流程（30s tick；雙條件查詢）
- Cloudflare Custom Hostname API 呼叫流程
- TTL 與 extend 操作
- 7 天 grace 撤銷流程
- Plan tier 邊界檢查
- 不相容 DNS 供應商清單與 onboarding 提示

### 2.2 不包含
- Cloudflare zone / Tunnel 本身（屬 `winshare-subdomain-and-tunnel` spec）
- TLS cert 細節（屬 Cloudflare 內部；本 spec 只觀察 `ssl.status`）
- Ingress manifest 之 hostname 列表 render（屬 `gitops-render-and-argocd` § 4.5）
- Cloudflare API 之 retry / backoff（屬 `winshare-subdomain-and-tunnel` § 8）
- BYO cert（v2）
- Plan tier 完整 capability 矩陣（屬 ADR-0011）
- Apex 不相容 DNS 供應商清單之動態維護（屬 `docs/runbooks/apex-dns-providers.md`，非 spec）

## 3. 檔案結構

```
0ops/
└── internal/
    └── server/
        ├── services/
        │   └── domainverify/
        │       ├── action_add.go           # add_domain Action 實作
        │       ├── action_remove.go        # remove_domain Action 實作
        │       ├── apex.go                 # publicsuffix-based apex 偵測
        │       ├── verify.go               # 雙條件 DNS 查詢
        │       ├── poller.go               # 30s 背景 polling
        │       ├── grace.go                # 7 天 grace 監控
        │       ├── extend.go               # TTL extend 邏輯
        │       ├── apex_providers.go       # 內嵌不相容 DNS 供應商清單
        │       ├── metrics.go
        │       └── doc.go
        ├── routers/
        │   └── domains.go                  # POST .../domains:preview, POST .../domains, POST .../domains/{host}:verify, DELETE .../domains/{host}:preview, DELETE .../domains/{host}
└── migrations/
    └── 000X_domain_binding_indexes.sql     # (team_id, hostname) 唯一、(verified, expires_at) reconciler 用
```

## 4. `domain_binding` 狀態機

```
                +----------+
                | pending  |   ← preview/confirm 後寫入；expires_at = now()+24h
                +----+-----+
                     |
       (DNS verify pass within 24h)
                     v
                +----------+
                | verified |   ← Cloudflare hostname active；cert 簽發
                +----+-----+
                     |
        (CNAME 移除偵測或 ssl.status 異常)
                     v
                +-----------+
                | unhealthy |   ← 7 天 grace；CLI/MCP 顯示警告；continue serving
                +----+------+
                     |                    \
        (恢復 CNAME)  |                     \  (7 天仍未恢復)
                     v                      v
                +----------+              +----------+
                | verified |              | released |
                +----------+              +----------+
                                              |
                                     (audit + 7 天 hard delete)
                                              v
                                         (DB row 移除)

任何 pending 過 24h（含 extend）→ pending → expired (DB column status='expired')
                                  └→ 7 天保留 → hard delete
                                  └→ user 跑 add_domain 重啟新 verification token
```

### 4.1 欄位（plan.md 已定，本 spec 補語意）

| 欄位 | 語意 |
|---|---|
| `id` | UUID |
| `app_id` | 對應 app |
| `team_id` | 租戶 |
| `hostname` | citext unique 全域；同 team 下兩 app 不可共用 hostname |
| `kind` | `primary` / `extra`；primary 為 `<app>.jesontech.com`，本 spec 只處理 extra |
| `verified` | bool |
| `verification_token` | 32-byte hex；produced at preview |
| `cf_hostname_id` | Cloudflare 端 Custom Hostname ID |
| `cf_dns_record_id` | （客戶域名不需，留 nil；wildcard 子網域亦不用）|
| `expires_at` | pending 階段之 24h 過期；verified 後不適用 |
| `is_apex` | 本 spec 新增欄位（待補 migration）|
| `extends_used` | 本 spec 新增欄位；0..2 |
| `health_check_failed_at` | 本 spec 新增欄位；首次偵測 unhealthy 時間，作 grace 起點 |
| `created_at` / `verified_at` | 同 plan |

> 本 spec 要求 plan.md DB schema 補三欄位：`is_apex bool`、`extends_used int default 0`、`health_check_failed_at timestamptz`

## 5. Add domain flow

### 5.1 Args

```go
// internal/shared/dto/domains.go
type DomainAddArgs struct {
    AppSlug  string `json:"app_slug"`
    Hostname string `json:"hostname"`
}
```

### 5.2 Preview 階段（SideEffects）

```
1. validate hostname：
   - lowercase ASCII
   - 長度 < 254；label < 64；符合 RFC 1035
   - 不得結尾 .jesontech.com（保留給 primary）
2. 檢查 plan tier：
   - team.plan ∈ {free, starter} → 403 plan_required
3. 偵測 apex：
   - publicsuffix.Domain(hostname) == hostname → is_apex=true
   - 否則 false
4. 檢查 hostname 是否已被佔用：
   - SELECT domain_binding WHERE hostname = ?
   - 命中（含其他 team） → 422 domain_taken
5. 計算 side_effects（§ 5.3）
6. 計算 verification_token（32 hex）
7. action_summary：「為 app `<app>` 加入域名 `<hostname>`」
```

### 5.3 Side_effects（依 apex 與否分支）

#### 非 apex
- 1 項 reversible：`Provision pending domain binding (24h TTL); register Cloudflare Custom Hostname`
- 提示文字附上：
  - `CNAME <hostname> → <tunnel_id>.cfargotunnel.com`
  - `TXT _0ops-verify.<hostname> → <verification_token>`

#### Apex
- 1 項 reversible：同上
- 提示文字附上：
  - `ALIAS / ANAME / CNAME-flattening <hostname> → <tunnel_id>.cfargotunnel.com`
  - `TXT _0ops-verify.<hostname> → <verification_token>`
- 警示句：「此域名為 apex；需 DNS 供應商支援 CNAME flattening / ALIAS / ANAME。已知不相容供應商：<list>」
- list 來源：`internal/server/services/domainverify/apex_providers.go` 內嵌；條目 `{name, reason, alternative}`

> 不相容清單為**靜態**內嵌；變更走 PR + plan.md 補運維筆記。v1 起步條目（待 ADR 補入準確度）：
> - GoDaddy classic（不支援 ALIAS）
> - Microsoft Azure DNS classic（不支援 ALIAS / ANAME；建議改 sub-domain or DNS migration）
> - 部分舊版自架 BIND

### 5.4 Confirm 階段（Execute）

```
1. INSERT domain_binding (
     hostname, app_id, team_id, kind='extra',
     verified=false, verification_token,
     is_apex, extends_used=0,
     expires_at=now()+24h
   )
2. Cloudflare API: POST /zones/{0ops_zone_id}/custom_hostnames
     body: { hostname, ssl: { method: 'http', type: 'dv' } }
   → 取得 cf_hostname_id；UPDATE domain_binding SET cf_hostname_id = ?
3. last_result：
     {
       "domain_binding_id": "...",
       "hostname": "...",
       "is_apex": true|false,
       "verification_token": "...",
       "expires_at": "...",
       "dns_setup": {
         "cname_target": "<tunnel_id>.cfargotunnel.com",
         "txt_name": "_0ops-verify.<hostname>",
         "txt_value": "<verification_token>",
         "apex_compatibility": [list]            // 僅 is_apex=true 時填
       }
     }
```

### 5.5 Side_effect 順序

- Reversible：order matters
  1. INSERT domain_binding（先；DB row reversible by DELETE）
  2. Cloudflare register Custom Hostname（reversible by DELETE /custom_hostnames/{id}）
- 無 irreversible step；client 在 verification window 內可主動 remove_domain（preview/confirm）即清除

## 6. Verify polling

### 6.1 Reconciler tick

`internal/server/services/domainverify/poller.go`：

```go
func RunVerifyLoop(ctx context.Context, db *pgxpool.Pool, log *slog.Logger) {
    t := time.NewTicker(30 * time.Second)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C:
            // M5 leader-only：if !isLeader { continue }
            verifyPending(ctx, db, log)
            checkUnhealthy(ctx, db, log)
            cleanupExpired(ctx, db, log)
        }
    }
}
```

### 6.2 `verifyPending`

```sql
SELECT * FROM domain_binding
 WHERE verified = false
   AND expires_at > now()
   AND kind = 'extra'
```

對每筆 row：
1. 雙條件查 DNS（§ 6.3）
2. 雙條件通過：
   - UPDATE domain_binding SET verified=true, verified_at=now()
   - Cloudflare API `PATCH /custom_hostnames/{cf_id}`：activate
   - Push 0ops-gitops 之 ingress.yaml 加入 hostname（接續 `gitops-render-and-argocd` § 4.5）
   - audit_log + metric
3. 否則 continue（下個 tick 再試）

### 6.3 雙條件查詢

```go
func verifyDNS(ctx context.Context, hostname, expectedToken, tunnelTarget string, isApex bool) error {
    // 條件 1：CNAME / A 解析至 tunnel
    if isApex {
        // ALIAS / ANAME 在 DNS 線上是 A/AAAA；用 LookupHost 反向比對
        ips, err := net.DefaultResolver.LookupHost(ctx, hostname)
        if err != nil { return err }
        // 比對是否含 tunnel target 之 IP（提前 LookupHost(tunnelTarget) 取參考 set）
        tunnelIPs, _ := net.DefaultResolver.LookupHost(ctx, tunnelTarget)
        if !intersects(ips, tunnelIPs) { return errCNAMENotMatched }
    } else {
        cname, err := net.DefaultResolver.LookupCNAME(ctx, hostname)
        if err != nil { return err }
        if !strings.HasSuffix(cname, "."+tunnelTarget+".") { return errCNAMENotMatched }
    }

    // 條件 2：TXT 含 token
    txtRecords, err := net.DefaultResolver.LookupTXT(ctx, "_0ops-verify."+hostname)
    if err != nil { return err }
    if !slices.Contains(txtRecords, expectedToken) { return errTXTNotMatched }

    return nil
}
```

### 6.4 主動觸發 `verify_domain`

`POST /v1/teams/{slug}/apps/{app}/domains/{host}:verify`

- read endpoint；不需 preview/confirm
- 立即跑一次 § 6.3 雙條件查詢；回 `{verified: bool, errors: []}` 給 client
- 用於 user 設好 DNS 後不想等 30s 自動 polling 之即時驗證

### 6.5 Polling fallback / 收斂

接續 `reconciler-and-incident` spec：
- `reconciliation_job(kind='domain_verify', subject_id=domain_binding_id)`：每次 polling 失敗計入 `attempts`
- 不採退避（30s 固定 tick）；attempts > 24h * 3600 / 30 = 2880 次後仍未通過 → 自然由 `expires_at < now()` 進 `cleanupExpired`

## 7. Extend TTL

### 7.1 Endpoint

`POST /v1/teams/{slug}/apps/{app}/domains/{host}:extend`（無 preview/confirm；視為 read-style 操作但會改 DB）

> 設計權衡：extend 為 reversible（縮回 expires_at），不引入 user-confirm preview 即可；但仍走 audit log。

### 7.2 行為

```
1. SELECT domain_binding WHERE hostname = ?
2. 檢查：verified=false, extends_used < 2, expires_at > now()
   - 否則 422 cannot_extend
3. UPDATE expires_at = expires_at + 24h, extends_used = extends_used + 1
4. audit_log
5. 回 200 + 新 expires_at
```

### 7.3 CLI

`0ops domains verify <app> <host> --extend`：呼上述 endpoint；用於 user 確認 DNS 設定還在處理但 24h 即將過期

## 8. Remove domain flow

### 8.1 主動 remove

```
1. remove_domain_preview { app_slug, hostname }
   side_effects:
     - DELETE Cloudflare Custom Hostname (irreversible：cert 釋放)
     - 從 0ops-gitops ingress.yaml 移除 hostname
     - DELETE domain_binding row
2. remove_domain { preview_id }
   - Cloudflare API DELETE /custom_hostnames/{cf_id}
   - Render & push gitops（移除 hostname）
   - DELETE domain_binding
```

不適用 7 天 grace；user 主動移除即立即生效。

### 8.2 7 天 grace（自動 reconcile-driven 撤銷）

```
1. checkUnhealthy (in poller § 6.1):
   SELECT * FROM domain_binding
    WHERE verified = true AND kind = 'extra'

   For each:
     - 跑 § 6.3 雙條件查詢
     - 通過 → UPDATE health_check_failed_at = NULL（從 unhealthy 恢復）
     - 失敗 + health_check_failed_at IS NULL → SET health_check_failed_at = now()，audit
     - 失敗 + health_check_failed_at IS NOT NULL → 檢查 grace
       - now() - health_check_failed_at > 7 days → unbind:
         - Cloudflare API DELETE
         - Render & push gitops（移除 hostname）
         - UPDATE domain_binding status='released'
         - audit + alert owner
```

### 8.3 Status 欄位

`domain_binding.status` 為 enum（v1 只列舉本 spec 用到）：

| status | 語意 |
|---|---|
| `pending` | verified=false；未過期 |
| `verified` | verified=true；正常 |
| `unhealthy` | verified=true 但 health_check_failed_at != NULL；7 天 grace 中 |
| `expired` | pending 過 24h+extend |
| `released` | unhealthy 過 7 天或主動 remove |

> 本 spec 要求 plan.md schema 補 `status text not null default 'pending'`；目前 plan.md 之 `domain_binding` 沒有 status 欄位

## 9. Plan tier 邊界

### 9.1 v1 規則（source: ADR-0011 § 3.1）

| Plan | extra hostname 配額 |
|---|---|
| `free` | 0（403 plan_required） |
| `starter` | 0（同上） |
| `pro` | 5 per team |
| `team` | 20 per team |

### 9.2 檢查時機

- preview 階段：team.plan + extra hostname count < quota？
- 不過 → 403 plan_required + details `{required_plan: pro, current_plan: free}`
- 升 tier 後新 hostname 立即可加；既有不影響

### 9.3 降 tier 處理

- user 從 `pro` 降至 `free`：既有 extra hostname **保留 7 天 grace**（與 unhealthy 撤銷對齊）；7 天後若仍未升回 `pro` → 自動 release
- 此規則需與 ADR-0011 一致；本 spec 列為 open issue 待 user 拍板

## 10. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| Cloudflare Custom Hostname API client | `winshare-subdomain-and-tunnel` § 8 |
| `<tunnel_id>.cfargotunnel.com` 為 CNAME target | `winshare-subdomain-and-tunnel` § 5.3 |
| Ingress manifest hostname 列表 render | `gitops-render-and-argocd` § 4.5 |
| Reconciler `domain_verify` job kind | `reconciler-and-incident` spec |
| Plan tier 啟用判斷 | `auth-and-rbac` spec + ADR-0011 |
| `cloudflare_api_error` / `plan_required` 失敗碼 | `error-model` § 5.5 |
| Verification token 不入 log（屬 secret 範疇） | `secrets-management` 概念對齊 |
| `0ops_domain_verify_attempts_total` metric | `observability-skeleton` § 4.4 |
| Audit log 寫入 | `audit-log` spec |
| TTL / extend 行為 | `preview-confirm-gate` 之外的 read-style 操作 |

## 11. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| Apex 偵測 | `publicsuffix.Domain("example.com")=="example.com"` | is_apex=true |
| 非 apex 偵測 | `publicsuffix.Domain("app.example.com")=="example.com"` | is_apex=false |
| Plan tier `free` add custom domain | preview | 403 plan_required |
| Plan tier `pro` add custom domain | preview / confirm | 通過 |
| 重複 hostname | 同 hostname 跨 team | 422 domain_taken |
| Reserved suffix | hostname 結尾 .jesontech.com | 422 reserved_hostname |
| 雙條件驗證通過 | mock DNS 兩條 record | UPDATE verified=true；ingress 加入；audit 記錄 |
| 雙條件僅 CNAME 通過 | mock 缺 TXT | continue pending；不改 verified |
| 24h TTL 過 | 25h 後 polling | UPDATE status='expired' |
| Extend 1 次 | 已用 0 次 | UPDATE extends_used=1, expires_at +24h |
| Extend 第 3 次 | extends_used=2 | 422 cannot_extend |
| Active hostname CNAME 移除偵測 | mock CNAME 不再 resolve | UPDATE health_check_failed_at；status=unhealthy |
| 7 天 grace 結束 | 8 天後 polling | Cloudflare DELETE；status=released；audit |
| Grace 中恢復 CNAME | 第 5 天 mock 恢復 | UPDATE health_check_failed_at=NULL；status=verified |
| Apex 提示含不相容供應商列表 | preview apex domain | side_effects 文字含 GoDaddy / Azure 列表 |
| `verify_domain` 主動觸發 | mock DNS 已就緒 | 200 + verified=true |
| Cloudflare API 5 次 retry 後失敗 | mock 持續 503 | error envelope `cloudflare_api_error`；preview saga compensating |
| Leader-only polling（M5） | 兩 backend pod | 僅 leader pod 跑 verify tick |

## 12. SLI 對應

| SLI | 目標 | 量測 |
|---|---|---|
| Domain verify 成功率（24h 內） | > 80% / 28d | `domain_binding.verified=true / total pending in 24h` |
| Verify p95 latency（user 設 DNS 後到 verified） | < 5 min | `verified_at - <user 設 DNS 時間>`（無精確訊號；近似為 `verified_at - created_at`） |
| Apex 比例 | 觀察 | `is_apex / total` 之佔比；高比例提示需強化 onboarding 教育 |
| Grace → released 比例 | < 5% / 28d | `status=released / total verified` |
| Cloudflare hostname 失敗率 | < 1% | `0ops_cloudflare_api_calls_total{op=hostname_create, outcome=error}` |

## 13. 對 `docs/0ops-plan.md` 的修改清單

1. 「Domain verify」段：交叉引用本 spec 為流程 source；plan 之 30s polling、24h TTL、extend × 2 細節保留
2. 「DB schema § domain_binding」：補 3 欄位：`is_apex bool`、`extends_used int default 0`、`health_check_failed_at timestamptz`、`status text not null default 'pending'`
3. 「Tool catalog」`add_domain` / `remove_domain` / `verify_domain`：交叉引用本 spec
4. 「Auth & RBAC § Authorization」：補入「`domains:write` scope 在 `add_domain` 時需配 plan tier 檢查」
5. ADR-0011（plan tier 矩陣）建立後，本 spec § 9.1 數值轉為引用 ADR

## 14. Open issues

> 來源：ADR-0007 § 9 之 7 條 OQ + 本 spec 撰寫期間發現

- ADR-0007 OQ#1（Cloudflare for SaaS SKU）：商業層面，不在 spec
- ADR-0007 OQ#2（plan tier custom hostname 配額）：屬 ADR-0011；本 spec § 9.1 為提案值
- ADR-0007 OQ#3（Cert CA 觀察相容性）：v1 不主動處理；高風險客戶反饋時再評估
- ADR-0007 OQ#4（plan tier 升降級配額調整 grace）：本 spec § 9.3 採與 unhealthy 同 7 天；待 ADR 確認
- ADR-0007 OQ#5（Apex 不相容 DNS 供應商清單動態維護）：本 spec § 5.3 採內嵌靜態；運維文件 `docs/runbooks/apex-dns-providers.md` 維護
- ADR-0007 OQ#6（tunnel_id rotation）：v1 不 rotate；rotation 屬 v2 + 客戶通知 runbook
- ADR-0007 OQ#7（客戶 CNAME 至 winshare 子網域）：v1 行為未定；本 spec 採「拒絕」（reserved suffix 規則）
- 主動觸發 `verify_domain` 之 rate limit：v1 採 `domains:write` scope 共用 60/min；可能需獨立 limit 防 abuse
- Cloudflare hostname 配額接近上限時的告警閾值：v1 設 80%
- 客戶 DNS TTL 過長（如 1h）導致驗證延遲：v1 不主動處理；onboarding 文件提示設短 TTL
- 雙條件驗證之 DNS resolver 選擇：v1 用 Go stdlib `net.DefaultResolver`；M5 後若需多 region 視角，評估第三方 resolver

## 15. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. add / remove 必經 preview-confirm-gate
2. extra hostname add 必檢查 plan tier；未達標即 403 plan_required
3. hostname 結尾 `.jesontech.com` 為 reserved；不得允許
4. Apex 偵測必走 `publicsuffix`；不得自寫 regex
5. Verification token 必為 32-byte hex（crypto/rand）；不得含可猜模式
6. 雙條件驗證為硬性（CNAME + TXT）；不得只查一條即放行
7. TTL 24h 與 extend × 2 為硬性；不得個別放寬
8. 7 天 grace 為硬性；不得立即撤銷已 verified 之 hostname（除非 user 主動 remove）
9. Cloudflare cert material 不得進 backend DB / log / metric / audit；backend 對 cert 為純觀察
10. Custom Hostname 必透過本 spec 之 client 操作；不得 Cloudflare Dashboard 手動設（避免 audit 缺失）
