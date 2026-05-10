---
adr: "0008"
title: Backend HA、Leader Election 與 Postgres 複製
status: Accepted
date: 2026-05-09
tags:
  - ha
  - leader-election
  - sse
  - postgres
  - replication
  - reliability
supersedes: []
superseded-by: []
---

# ADR-0008：Backend HA、Leader Election 與 Postgres 複製

* Status：Accepted
* Date：2026-05-09
* 適用範圍：M0 single-replica → M5 multi-replica HA；Postgres 主從複製
* 來源：`docs/0ops-plan.md`「Backend 自身部署 topology」「Postgres backup / DR」段；plan 將實際決策延後至本 ADR
* 上游依賴：[ADR-0004](0004-k3s-role-and-orchestrator.md)（backend 與 managed apps 同 K3s cluster + namespace 區隔）；[ADR-0006](0006-observability-baseline.md)（SLO 99.9% 達成路徑）；[ADR-0002](0002-idempotency-and-compensation.md)（reconciler 為 leader-only 任務）

## 0. TL;DR（先讀本段）

採用以下八項組合決策：

1. **HA 演進時程**：v1 GA 至 M4 = single replica；**M5** 升 2 replica + leader election；不採「v1 即多 replica」、不採「永遠 single」、不採 active/active no-leader。
2. **角色分工**：leader 跑 reconciler / preview cleanup / domain verify polling / token refresh；**follower 同時服務 read/write API**（不切讀寫 service）。
3. **Leader election 機制**：`k8s.io/client-go/tools/leaderelection` + Lease object（K8s coordination API）；不採 Postgres advisory lock、不採 Redis distributed mutex、不採 etcd raw lease。
4. **SSE 多實例策略**：**stateless cursor-based reconnection**——client 在 reconnect 時帶 `?cursor=<rfc3339nano>` query，任一 backend pod 都能從 cursor 用 K8s log API `SinceTime` 接續拉流；**不採 ingress sticky cookie**（CLI / MCP 為 Go binary，預設 `http.Client` 不持 `CookieJar`，cookie 不會在 reconnect 自動回送，sticky 假設失效）；不採 Redis pub/sub（v1 規模不引入新組件）；不採長輪詢（UX 落差大）。
5. **Postgres 拓樸**：main + 1 streaming replica（跨 K3s node）；WAL archive 至 R2 / S3 每 5 min；daily `pg_dump` 30 天保留；PITR RPO 5 min / RTO 30 min。
6. **Failover 行為**：v1 single replica 即 K8s rolling update（`maxSurge=1, maxUnavailable=0`）；M5 進入 leader handover——leader pod 收到 SIGTERM 立即 release lease，另一 pod 取得 lease 後 < 5s 接手 background workloads。
7. **Postgres failover**：v1 + M5 = 手動 promote replica（runbook）；**v1.1 評估 Patroni**；不採 v1 即引入 Patroni、不採 cloud managed Postgres（v1）。
8. **Datastore Postgres（K3s control plane）vs application Postgres 為兩個獨立 instance**（接續 ADR-0004 第 4.2 節）；本 ADR 之 Postgres 拓樸與複製策略**僅對 application Postgres 適用**；datastore Postgres 之 backup / DR 屬 ADR-0004 範圍。

行為與部署 YAML 細節以 `docs/0ops-plan.md` 為準，本 ADR 不重述。

## 1. Context and Problem Statement

ADR-0006 已將 API availability SLO 訂為 99.9% / 28d（月度 budget ≈ 40 分鐘）。v1 single-replica backend + 計畫內 K8s rolling update 即會啃 budget；GA 後若每月 budget 用罄 > 80% 持續 2 個週期將觸發 ADR-0006 Revisit。

Backend 同時負擔三類負載：

1. **同步 API**：CLI / MCP 客戶端 + GitHub webhook + GHA callback；read 為主、write 為 preview/confirm 兩階段。
2. **長連線 SSE**：`tail_logs` 從 K8s 拉取 pod log 後推送至 client；連線維持時間可達數分鐘至數小時。
3. **背景任務**：reconciler、preview 過期清理、domain verify polling、`ghcr-pull` token refresh（每 30 min）；任一任務跑兩次或都不跑都會出問題。

升 multi-replica 必須同時解三件事：

* 背景任務的「跑一次」語意——不可雙 leader 同時 reconcile 同一個 deploy_run。
* SSE 連線的 stickiness——client 重新建立連線可能落到另一 pod，從哪裡接 log offset？
* Read/write API 的負載均衡——若仍是 single-active，等於沒升 HA。

Plan 把實際時程設為「v1 GA 後 M5 升 HA」，但具體機制（leader election 用 K8s Lease 還是 Postgres advisory lock；SSE 用 sticky 還是 Redis pub/sub）尚未敲定。本 ADR 將其釘成不可變協定，並把 v1.1 之 Postgres failover 自動化候選正式化。

ADR-0004 已將 backend 與 managed apps 同 cluster co-location 設為前提；本 ADR 在此前提下解 HA。

## 2. Decision Drivers

* **DD1 SLO 99.9% 達成**：v1 single replica 計畫內 rollout 即啃 budget；M5 必須升 HA。
* **DD2 「跑一次」語意**：reconciler / preview cleanup / token refresh 等背景任務若雙 leader 同時跑，會造成 race（同一 preview 重複 cleanup、同 token 重複 refresh）；leader election 為硬要求。
* **DD3 v1 規模成本**：v1 不應為 v3 規模引入過多 HA 組件（Redis、Patroni、外部 ZK 等）；v1 應只做「為了升 M5 不必重做應用層」的最小投資。
* **DD4 Read 流量主導**：CLI / MCP 多為 read（list、get、status）；多 replica 對 read scale 收益最大。
* **DD5 SSE 為長連線**：log follow 連線可達數小時；不能用「每次 request 重新 hash」的負載均衡。
* **DD6 Postgres 為單一資料源**：`preview`、`deploy_run`、`audit_log` 全在 application Postgres；Postgres 不可恢復即整服務失能。
* **DD7 K3s coordination API 即用即得**：K8s Lease object 為 cluster 內天然分散式鎖；不需引入新組件。
* **DD8 Failover 自動化非 v1 必須**：手動 promote runbook 對 v1 規模可接受；v1.1 評估自動化（Patroni）以降 RTO。

## 3. Considered Options

針對 (a) HA 演進時程、(b) SSE 多實例策略、(c) leader election 機制 做完整 alternative 比較；(d)(e)(f) 為局部決策，列表帶過。

### 3.1 (a) HA 演進時程

| Option | 描述 |
|---|---|
| **A1. v1 single → M5 升 leader election + 2 replica**（採用） | 與 plan 既定 milestone 對齊；M0–M4 不投入 HA 工程 |
| A2. v1 GA 即 multi-replica | M0 即引入 leader election；HA 全期生效 |
| A3. 永遠 single replica，垂直擴展 | 不引入 HA；靠 vertical scale + fast restart |
| A4. Active/active 無 leader（idempotent 設計） | 所有背景任務都設計為冪等可並發 |

### 3.2 (b) SSE 多實例策略

| Option | 描述 |
|---|---|
| **B0. Stateless cursor-based reconnection**（採用） | client 重連帶 `?cursor=<ts>`；任一 pod 用 K8s log API `SinceTime` 接續拉流；無 stickiness |
| B1. Ingress sticky session by `set-cookie` | client 第一次連線 ingress 發 cookie；後續連線同 pod |
| B2. Redis pub/sub | SSE event 透過 Redis 廣播；任一 pod 接 client 都收得到 event |
| B3. SSE 改長輪詢 + cursor | client 反覆 poll，無長連線 |
| B4. SSE 由獨立 deployment 服務（單實例） | API replicas + 1 SSE pod；SSE 不 HA |

### 3.3 (c) Leader Election 機制

| Option | 描述 |
|---|---|
| **C1. K8s Lease（`client-go/tools/leaderelection`）**（採用） | K8s coordination API；無外部依賴 |
| C2. Postgres advisory lock | `pg_try_advisory_lock` |
| C3. Redis distributed mutex（Redlock） | Redis SETNX + TTL |
| C4. etcd raw lease | 直接打 K3s 嵌入式 etcd（v1 為 PostgreSQL via kine，etcd 不存在） |
| C5. Zookeeper / Consul | 外部協調服務 |

### 3.4 (d)(e)(f) 局部選擇

| 子決策 | 採用 | 否決 | 一句結論 |
|---|---|---|---|
| (d) Failover 行為 | M5 leader 收 SIGTERM 立即 release lease；新 leader < 5s 接手 | 主動 ZK heartbeat / 等 lease TTL 自然超時 | 立即 release 對 SLO 友善；等 TTL 超時 = 服務空窗 |
| (e) Postgres 拓樸 | main + 1 streaming replica + WAL archive + daily pg_dump | single Postgres / cloud managed DB / multi-master | single 違反 DD6；cloud managed v1 成本與 ADR-0004 K3s self-host 不對等；multi-master 為 v3 議題 |
| (f) Postgres failover | v1 + M5 手動；v1.1 評估 Patroni | v1 即 Patroni / 永遠手動 / managed DB auto failover | v1 即 Patroni 為 over-investment；永遠手動違反 SLO 99.9% 對 RTO 的暗示 |

## 4. Decision Outcome

採用 **A1 + B0 + C1**，搭配 (d) 立即 release lease、(e) main + 1 replica + WAL archive、(f) v1.1 評估 Patroni。

具體展開：

1. **v1 拓樸（M0–M4）**：
   * Backend `Deployment` replicas=1；無 leader election 程式碼。
   * Rolling update 策略：`maxSurge=1, maxUnavailable=0`；新 pod 通過 `/readyz` 才接流量；preStop hook `sleep 5 + drain SSE`。
   * SSE 連線在 rollout 期間 client 端會 reconnect；CLI 自動重試（接續 plan「使用者腳本範例」中 `--follow`）。
2. **M5 拓樸**：
   * Backend `Deployment` replicas=2；HPA 暫不開（手動調），M5 後再評估。
   * 兩 pod 都跑同樣 binary；啟動時都呼 `leaderelection.RunOrDie()`（C1）。
   * Leader 才註冊 reconciler / preview cleanup / domain verify / token refresh 之 ticker；follower 不啟動這些 goroutine。
   * Read/write API handler 在 leader / follower 都啟動；不切讀寫 service。
   * Service `0ops-backend` 走 ClusterIP + ingress；SSE endpoint（`*/logs?follow=true`）走無狀態 cursor 模型（見第 5 點），ingress 不需 sticky 配置。
3. **Leader Election（C1）**：
   * Lease 物件位於 `system-0ops` namespace，名稱 `0ops-backend-leader`。
   * Lease duration 15s、renew deadline 10s、retry period 2s（K8s 慣例）。
   * Identity = pod name + UUID；pod 啟動時 generate。
   * Leader 失去 lease（renew 失敗）：立即停止背景 goroutine、unregister metrics、進 follower 模式；不 panic。
4. **Failover 路徑**：
   * 計畫內：leader 收 `SIGTERM`（preStop hook）→ 立即呼 `cancel()` 釋放 lease → 新 leader 在 retry period（2s）內取得 lease → 啟動背景 goroutine。空窗 < 5s。
   * 非計畫內（leader pod crash / network partition）：新 leader 在 lease duration（15s）超時後取得；空窗 ≤ 15s。
5. **SSE 無狀態 reconnection（B0）**：
   * SSE 回應每個 event 帶 `id:` 欄位（W3C SSE spec），值為 K8s log 行的 RFC3339Nano timestamp（取自 K8s log line prefix）。
   * Client（CLI `--follow` / MCP `tail_logs`）維持「最後接收 event id」狀態；連線中斷 → reconnect 帶 `?cursor=<rfc3339nano>` query；W3C SSE 預設行為亦會帶 `Last-Event-ID` header（CLI / MCP 主動實作 SSE 反序列化時兩者皆可，backend 任一接受）。
   * 任一 backend pod 收到 reconnect → 從 K8s log API 取流：`Pods.GetLogs(name, &corev1.PodLogOptions{Follow: true, SinceTime: &metav1.Time{Time: cursor}})`；無需 sticky 至原 pod。
   * Pod 失效（不在 endpoints 內）→ ingress 自然路由至健康 pod；client reconnect 時透過 cursor 接續，無感切換。
   * **為何不用 ingress sticky cookie**：CLI / MCP 為 Go binary，預設 `http.Client` 不持有 `CookieJar`；標準慣例下 ingress 下發的 sticky cookie 不會在 reconnect 被自動回送，導致下次連線落到任意 pod，sticky 假設立刻失效。明確走 cursor 模型避開此 hidden footgun，且 W3C SSE 的 `Last-Event-ID` 機制本就為這條路徑設計。
   * **Trade-off**：每次 reconnect 對 K8s API server 重建一次 watch；v1 規模可忽略，M5 後若 reconnect QPS 異常需評估改 (B2) Redis pub/sub。
   * **Cursor 精度**：K8s log line 同 ts 多行需 client 端 dedupe；backend 對 nanosecond 等精度的 timestamp 不做去重（K8s log API 本身不保證唯一）。
6. **Postgres 拓樸（e）**：
   * Application Postgres：main + 1 streaming replica（async）；replica 跨 K3s node。
   * WAL archive 每 5 min 推 segment 至 R2 / S3；保留 30 天。
   * Daily `pg_dump`（邏輯備份）保留 30 天。
   * PITR：archive + base backup 達成；RPO 5 min、RTO 30 min（演練於 M5）。
   * Read replica 在 v1 不暴露於應用層 read 路徑（避免 replication lag 引入語意 bug）；M5 後評估特定 read endpoint 走 replica。
7. **Postgres Failover（f）**：
   * v1 + M5：手動。runbook：promote replica（`pg_ctl promote`）→ 改 backend `DATABASE_URL` Secret（K8s ConfigMap update）→ rolling restart backend。預估 RTO 30 min。
   * v1.1 評估 Patroni：自動化 leader election + promote；引入 Patroni 為新運維面，需獨立 ADR 補充。
8. **Migration safety**（接續 plan）：
   * CI 跑 `goose status` + `goose validate`。
   * Migration 必先在 staging 跑過 24h 才能上 prod。
   * ALTER 大表強制 `CONCURRENTLY` 變體 + lint 攔。

## 5. Pros and Cons of the Options

### 5.1 (a) HA 演進時程

#### A1. v1 single → M5 升 HA（採用）

* Good：M0–M4 工程聚焦於業務功能；HA 不打斷功能 milestone。
* Good：v1 GA 後即可量測單實例 SLO 表現；M5 升 HA 為「為了 budget 而升」而非「為了未來」。
* Good：應用層程式碼即使 single 也需處理 graceful shutdown / preStop；M5 升 HA 不需重做。
* Good：成本上 v1–M4 為 1 replica，M5 後才升。
* Bad：v1 GA 後 SLO 達成依賴 single replica 計畫內 rollout；不可控失敗會直接啃 budget。
* Bad：M5 才升 HA 對企業客戶承諾偏晚；早期 PoC 客戶可能看到不可用 window。

#### A2. v1 GA 即多 replica

* Good：SLO 達成路徑最穩。
* Good：HA 程式碼從 day 1 跑生產；少 surprise。
* Bad：M0–M2 工程量大幅增加；leader election + SSE sticky + Postgres replica 都需在 M2 GA 前到位。
* Bad：違反 plan milestone；GA 時程後延。
* Bad：v1 規模成本 vs 必要性失衡。

#### A3. 永遠 single replica（垂直擴展）

* Good：實作最簡單。
* Bad：違反 DD1；99.9% SLO 達不到。
* Bad：節點重啟即服務不可用；不接受。

#### A4. Active/active 無 leader（idempotent）

* Good：無 leader 切換 latency；任一 pod 都可跑任務。
* Bad：reconciler / cleanup / token refresh 設計為冪等可並發為高難度；preview cleanup 二次跑無傷，但 ghcr token refresh 並發跑會超 GitHub API quota。
* Bad：DDoS 自身效應；多 pod 同時掃同 query 增負載。
* Bad：違反 DD2（「跑一次」語意對部分任務為硬需求）。

### 5.2 (b) SSE 多實例策略

#### B0. Stateless cursor-based reconnection（採用）

* Good：與 Go HTTP client 預設行為相容；CLI / MCP 不需特別 wire `CookieJar`。
* Good：無 stickiness state；任一 pod 接 reconnect 都能正確接續；leader handover、rolling update、pod crash 對 SSE 透明。
* Good：無外部組件（Redis）；與 ADR-0004 v1 規模成本對齊。
* Good：cursor 為 RFC3339Nano timestamp，K8s log API 原生 `SinceTime` 支援；W3C SSE 既有 `Last-Event-ID` 機制天然契合。
* Good：負載均勻——任一 pod 都可接任一 reconnect，HPA 規則無需考慮 SSE stickiness 帶來的 hotspot。
* Bad：每次 reconnect 對 K8s API server 重建一次 watch；高頻 reconnect 場景負載累加（v1 規模可忽略）。
* Bad：Cursor 精度取決於 K8s log timestamp（nanosecond）；同 ts 多行需 client side dedupe；backend 不擔此責任。
* Bad：long-running SSE 連線本身仍可能受 ingress idle timeout 影響；需配合 ingress timeout 調整或定期 keepalive comment line。

#### B1. Sticky session by `set-cookie`

* Good：實作最少；ingress 既有功能。
* Good：無外部組件（Redis）。
* Good：SSE 連線生命週期內穩定；無跨 pod 同步問題。
* Bad：**依賴 client 持有 cookie**；CLI / MCP 為 Go binary，預設 `http.Client` 不帶 `CookieJar`，reconnect 時 `Set-Cookie` 不會被自動回送，sticky 失效。要求 client 端工程師明確 wire `CookieJar` 為 hidden footgun，review 必看項——本 ADR 採 B0 即為避開此風險。
* Bad：Pod 失效時連線斷；client 需 reconnect 且可能落到不同 pod。
* Bad：負載不均勻——某 pod 累積長連線多即 hot；HPA 規則設計需考量。
* Bad：cookie 在嚴格 ingress proxy / 客戶側網路設備可能不持久；需測試。

#### B2. Redis pub/sub

* Good：任一 pod 都可服務 SSE；無 stickiness。
* Good：負載均勻。
* Bad：引入 Redis 為新運維面；違反 DD3（v1 不應為 v3 引入）。
* Bad：SSE event 多重廣播浪費頻寬（事件被所有 pod 收下，但只一個 pod 推給 client）。
* Bad：Redis 失效即 SSE 全失效；新增 SPOF。

#### B3. SSE 改長輪詢

* Good：無長連線；負載均衡簡單。
* Bad：UX 落差大；CLI `--follow` 體感為「按 enter 才有下一段」。
* Bad：違反 plan「Pattern A 範例」描述的即時 follow 行為。

#### B4. SSE 由獨立 deployment

* Good：API HA；SSE 不 HA。
* Bad：SSE 仍 SPOF；不算解 HA。
* Bad：兩個 deployment 增加運維面。

### 5.3 (c) Leader Election 機制

#### C1. K8s Lease（採用）

* Good：cluster 內天然存在；無外部依賴。
* Good：`client-go/tools/leaderelection` 為標準函式庫；社群成熟。
* Good：Lease object 可直接被 K8s 工具觀察（`kubectl get lease`）；運維可見性高。
* Good：K3s + kine + Postgres 模式下，Lease 寫入即進 PostgreSQL（接續 ADR-0004），與 backend application Postgres 為不同 instance，故障域隔離。
* Bad：依賴 K3s control plane 可用性；K3s control plane 故障即 leader election 故障（但 backend 自己也跑在同 K3s，已是同一故障域）。
* Bad：Lease duration 為 trade-off：太短 false failover、太長空窗大；需調參。

#### C2. Postgres advisory lock

* Good：無新組件；application Postgres 已存在。
* Bad：故障域與資料庫綁定；Postgres 失效 → 同時影響資料層 + leader election。
* Bad：advisory lock 在 connection 死亡時自動釋放；長連線管理需細心。
* Bad：Postgres failover 時 advisory lock 會丟失；需重新 election。

#### C3. Redis Redlock

* Good：成熟；社群多用。
* Bad：引入 Redis；違反 DD3。
* Bad：Redlock 演算法本身有爭議（時鐘偏移敏感）。

#### C4. etcd raw lease

* 不適用：v1 K3s datastore 為 PostgreSQL via kine，無獨立 etcd。

#### C5. Zookeeper / Consul

* Bad：引入新運維面；Java / Go 雙運行時；違反 DD3、DD7。

## 6. Consequences

### 6.1 Positive

* M0–M4 工程聚焦業務功能；HA 在 plan 既定 M5 milestone 才到位。
* C1（K8s Lease）無外部依賴；leader election 為「cluster 內生」能力。
* B0（stateless cursor）讓 SSE 對 leader handover / rolling update / pod crash 透明；應用層不需 stickiness 配置；CLI / MCP 客戶端用標準 `http.Client` 即可，無 cookie 設定 hidden footgun。
* Application Postgres main + replica + WAL + daily pg_dump 給 PITR 提供完整路徑；RPO 5 min 可達。
* v1.1 Patroni 為 future evaluation，不阻擋 v1 GA。
* M5 leader handover 立即 release（< 5s 空窗）對 99.9% SLO 友善。

### 6.2 Negative

* v1 GA 至 M4 為 single replica；任何不可控故障（K3s node crash、Postgres 故障）即 SLO budget 全噴；M5 升 HA 必須準時。
* Lease duration 15s 對極端網路抖動（K3s control plane 短暫不可達 > 15s）會 false failover；需 production observation 後調參。
* B0 cursor 模型對 K8s API server 多一次 watch 建立；M5 後若 SSE reconnect QPS 大量上升需重審，可能升 B2（Redis pub/sub）以聚合 watch。
* Postgres failover v1 + M5 手動；RTO 30 min 對 99.9% 月度 budget（40 分鐘）為高風險；單次 failover 即可能噴半個月 budget。
* Patroni 評估在 v1.1；v1 GA 至 v1.1 的 window 內 Postgres 故障 RTO 為人為控制。
* v1 部署期間（M0–M4）若 SLO 觀察顯示 budget 持續偏高，可能需提前進 M5（HA）；plan milestone 排程有彈性壓力。

### 6.3 Neutral

* HPA 是否在 M5 開啟為運維決策；本 ADR 默認手動 replicas=2。
* Read replica 在 M5 後是否暴露於應用層 read 路徑為效能優化；不在本 ADR。
* Multi-region / multi-AZ 為 v2+ 議題；本 ADR 範圍限單 cluster 內 HA。
* CSI / 持久存儲對 Postgres 主從跨 node 部署的影響為運維細節；不在本 ADR。

## 7. Revisit Triggers

下列任一條件觸發時，應重新評估本 ADR 對應段：

1. **v1 GA 後 SLO budget 持續用罄 > 80% 連 2 個 28d 週期**：提前進 M5（HA），打破原 milestone 排程。
2. **M5 升 HA 後 SLO 仍未達 99.9%**：重審 (a)(b)(c)；可能需 multi-region（屬 v2 ADR 範圍）。
3. **SSE reconnect / K8s watch 壓力過大**：M5 後 reconnect QPS 持續上升或 K8s API server `apiserver_request_total{verb="WATCH"}` 中 log API 占比異常 → 重審 (b)，可能升 B2（Redis pub/sub）以聚合 watch。
4. **Postgres failover 演練 RTO > 30 min**：v1.1 必須引入 Patroni；本 ADR (f) 升級為「v1.1 必補」。
5. **Lease false failover 頻率 > 1 次 / week**：重審 lease duration 與 K3s control plane 穩定性；可能改 advisory lock 退路。
6. **背景任務跑兩次的 incident**：leader election 失敗 → 重審 (c)，可能加冪等性保險（雙 leader 都嘗試也無傷）。
7. **企業客戶要求 SLA 99.95+**：商業承諾觸發；重審整個 ADR，可能引入 multi-region。

## 8. More Information

* Backend 與 managed apps 同 cluster co-location 約束：[ADR-0004 K3s 角色與 v1 容器編排器選擇](0004-k3s-role-and-orchestrator.md) 第 4 節。
* SLO 99.9% 達成路徑與 burn-rate alert：[ADR-0006 Observability baseline](0006-observability-baseline.md) 第 4 節。
* Reconciler 為 leader-only 任務、preview 過期清理 leader-only：[ADR-0002 Idempotency 與副作用補償](0002-idempotency-and-compensation.md) 第 4 節。
* `ghcr-pull` token refresh 為 leader-only 任務：[ADR-0004](0004-k3s-role-and-orchestrator.md) 第 4.4 節 + plan「Runtime topology / ImagePullSecret」段。
* Datastore Postgres（K3s control plane）的 backup / DR：[ADR-0004](0004-k3s-role-and-orchestrator.md) 第 4.4 節（與本 ADR application Postgres 為兩個 instance）。
* Migration 安全閘（goose validate、staging 24h、CONCURRENTLY lint）：`docs/0ops-plan.md`「Postgres backup / DR / Migration 安全閘」段。

## 9. Open Questions

下列問題不阻擋本 ADR 通過，但需在 M5（HA 升級前）敲定：

1. **Lease duration / renew deadline 調參**：K3s 環境下實際的 control plane jitter 分布需 spike；15/10/2s 為起手，可能需調。
2. **HPA 是否在 M5 開**：開啟條件（CPU / RPS / 自訂 metric）；對 leader handover 頻率的影響。
3. **Read replica 暴露路徑**：哪些 read endpoint 可走 replica（接受 replication lag）？list / get for app catalog 適合；audit log query 也適合；preview / state machine 不適合。
4. **Patroni v1.1 評估範圍**：Patroni 與 K3s 整合（Operator vs StatefulSet）；DCS 用 K8s API 還是 etcd（K3s 模式下無獨立 etcd）。
5. **K3s control plane HA 對 backend HA 的依賴**：v1 K3s control plane 為 single node；M5 backend HA 但 K3s control plane single 仍是 SPOF；是否同時升 K3s embedded etcd HA？
6. **SSE 連線總數上限**：M5 兩 pod 各持多少 SSE 連線為設計上限？OOM / file descriptor 限制需 spike。
7. **Cross-cluster failover（v2）**：multi-region / multi-AZ 為 v2 議題，但本 ADR 的 leader election + cursor SSE 模型是否都需重做？需在 v2 ADR 評估遷移成本。
8. **Ingress idle timeout 與 SSE keepalive**：traefik / nginx-ingress 預設 idle timeout 多落在 60–600s；長 SSE 連線需 backend 定期送 SSE comment line（`: keepalive\n\n`）或 ingress 端調 timeout。M5 前實測各 ingress 行為。
