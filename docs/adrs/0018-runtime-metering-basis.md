---
adr: "0018"
title: Runtime 計量以 Allocation 為基準
status: Accepted
date: 2026-09-22
tags:
  - metering
  - usage
  - billing
  - observability
supersedes: []
superseded-by: []
---

# ADR-0018：Runtime 計量以 Allocation 為基準

* Status：Accepted
* Date：2026-09-22
* 適用範圍：managed app 之 runtime 資源計量（cpu / memory / gpu）之量測基準、時間來源與誤差方向；不含計價、發票、扣款
* 來源：使用者需求「記錄用戶開的 cpu/gpu/ram 的使用量」；`docs/features/resource-usage-metering/spec.md`
* 上游依賴：[ADR-0001](0001-multi-tenancy-and-rbac.md)（team 為計費邊界）；[ADR-0004](0004-k3s-role-and-orchestrator.md)（K3s 為 orchestrator，pod 為資源單位）；[ADR-0011](0011-plan-tier-capability-matrix.md)（v1 為 flat tier、不引入 metered billing）

## 0. TL;DR（先讀本段）

三項不可違反的決策：

1. **計量基準為 allocation，不是 utilization**：`費用基底 = pod 宣告之資源 × pod 存活秒數`。cpu / memory / gpu 三者一律如此。
2. **時間一律取自 K8s 物件**（`status.startTime`、`containerStatuses[].state.terminated.finishedAt`、`metadata.deletionTimestamp`），**不得**使用 backend 觀察到事件的牆鐘時間。
3. **不確定時必 under-bill**：終止時間無法取得者，以「最後一次確知該 pod 存活的時刻」關閉並標記為推估；不得向後推估、不得補滿至發現時刻。

違反任一項將使帳本從「可對外辯護的紀錄」退化為「估算」，且錯誤方向不可控。

## 1. Context and Problem Statement

`usage_sample` 表自 `migrations/00001_init.sql` 即存在，`docs/0ops-plan-schema.md` 亦已寫定「每 5 min 由 reconciler 從 K8s metrics-server 拉」。但該表自建立起從未有任何寫入路徑，等於計量方案只有意圖、沒有實作，因此基準仍可重新選擇。

要落地時出現實質分歧：計量對象應是**使用者實際消耗了多少**（utilization），還是**使用者佔用了多少**（allocation）？兩者的資料來源、精度上限與失敗模式完全不同，且一旦對外計費就無法無痛更換。

同時必須解釋一個既存事實：其他 PaaS（Fly、Render、Heroku、Lambda）皆能做到秒級計費，而 metrics-server 的 5 分鐘採樣顯然做不到。差異不在於他們採樣得更頻繁，而在於他們根本不採樣。

## 2. Decision Drivers

* **DD1 精度可辯護**：一旦對外收費，數字必須能逐筆回推來源，不能是插值結果。
* **DD2 短命 workload 不可遺漏**：存活 < 採樣間隔的 pod 在 gauge 採樣下完全不存在，這不是精度損失而是資料缺漏。
* **DD3 失敗方向可控**：任何不確定必須朝「少收」而非「多收」偏移。多收一分錢就是爭議。
* **DD4 依賴最小**：metrics-server 可被 `--disable metrics-server` 關閉，且官方明示不供計費用途、不保留歷史。計量不應綁死在一個可被關閉且自認不適任的元件上。
* **DD5 記憶體無累積量**：cgroup 的 `memory.current` 是瞬時值且含 page cache，沒有可積分的累積計數器。按實際用量計 RAM 在語意上不成立。
* **DD6 GPU 無利用率標準**：業界無人按 SM 利用率收費；取得利用率需額外部署 DCGM exporter，且 v1 無 GPU node。
* **DD7 與既有決策一致**：ADR-0011 DD5 已定 v1 為 flat package、不引入 metered billing。本 ADR 不改變該立場，只確保日後啟用計價時底稿正確。

## 3. Decision Outcome

### 3.1 採用：allocation ledger

每個 pod 一筆區間紀錄（`usage_allocation_interval`），記錄其宣告的 cpu / memory / gpu 與存活區間。積分為閉式解：

```
cpu_millicore_seconds = cpu_millicores × (ended_at − started_at)
```

兩個乘數皆為已知定值，故**精度與 backend 的輪詢頻率無關**。輪詢頻率只決定「多久發現一次」，不決定「記成幾點」。

### 3.2 分配值之權威來源為 pod spec

分配值取自 `pod.spec.containers[].resources`，不取自 `app` 表、不取自 tier 預設、不取自 LimitRange 設定檔。

理由：K8s LimitRange 的 `defaultRequest` 於 admission 階段即寫入 pod spec，故讀 pod spec 一次涵蓋「使用者明確指定」與「平台預設補上」兩種情形。這也使 `app-resource-spec` 日後落地時本機制無須任何改動。

### 3.3 時間來源與誤差方向

| 邊界 | 取值順位 | 性質 |
|---|---|---|
| 起始 | `status.startTime` | 權威。backend 停機期間啟動之 pod，其起始時間仍在物件上，可完整補回 |
| 終止 | ① 全部 container 之 `finishedAt` 最大值 ② `metadata.deletionTimestamp` ③ `last_seen_at` | ①② 權威；③ 為保守 fallback，必標 `estimated` |

`finishedAt` 優先於 `deletionTimestamp`：後者記錄「何時被要求刪除」，前者記錄「何時停止消耗」。計量跟隨消耗。

### 3.4 實際用量降為輔軌

metrics-server 之瞬時 cpu / memory 可作為**觀測輔軌**（判斷 app 開太大或逼近 OOM），但：

* 不得進入 allocation 積分
* 不得作為帳務底稿
* 主軌不得依賴其存在

## 4. Pros and Cons of the Options

### 4.1 Allocation ledger（採用）

* Good：精度到秒，且與輪詢頻率解耦（DD1）。
* Good：短命 pod 完整記錄（DD2）。
* Good：cpu / memory / gpu 單一模型涵蓋（DD5、DD6）。
* Good：不需 metrics-server（DD4）。
* Good：滾動更新、resize、replicas 變動天然正確——舊 pod 關區間、新 pod 開區間。
* Good：資料量小，每 pod 一列而非每採樣週期一列。
* Bad：不反映「宣告 2 核但只用 0.1 核」之浪費；需輔軌補足。
* Bad：backend 停機期間結束之 pod，終止時間須推估（受 DD3 約束，方向恆為少收）。

### 4.2 metrics-server 採樣（否決）

* Good：反映實際消耗。
* Bad：gauge 採樣，精度上限等於採樣間隔（違反 DD1）。
* Bad：短命 pod 完全遺漏（違反 DD2）。
* Bad：漏採該段永久遺失，只能插值（違反 DD1、DD3）。
* Bad：RAM 語意不成立（DD5）；GPU 無資料（DD6）。
* Bad：綁定一個可被關閉、且官方聲明不供計費用途的元件（違反 DD4）。

### 4.3 cAdvisor 累積計數器（部分採納為 v2 輔軌）

* Good：`container_cpu_usage_seconds_total` 為單調遞增 counter，差值積分不受採樣頻率影響——這是「按實際用量」唯一可行的作法。
* Bad：僅 CPU 適用；RAM 與 GPU 仍需 allocation（DD5、DD6）。
* Bad：需部署 Prometheus + kube-state-metrics，repo 內目前只有 rules 與 dashboard 的 ConfigMap，無 server 安裝清單。
* 結論：若 v2 需要「實際 CPU 消耗」維度，走此路而非 metrics-server；但計費基準仍為 allocation。

## 5. Consequences

* `usage_sample` 之角色由「主計量表」降為「觀測輔軌」；`docs/0ops-plan-schema.md` 之「每 5 min 從 metrics-server 拉」措辭由本 ADR supersede。
* 新增 `usage_allocation_interval`（保留 13 個月）與 `usage_daily_rollup`（永存）。
* backend 需 cluster-scoped 之 pod `list` / `watch` 讀權；此為 backend 目前最寬的權限，必須維持唯讀。
* 對外輸出（API / CLI / MCP）於 v1 必須標明非計費資料（承 ADR-0011 DD5）。
* `estimated` 比例成為帳本品質的主要指標：長期偏高代表事件路徑失效，須告警。
* 日後若要改為按實際用量計費，屬基準變更，須新 ADR supersede 本 ADR，不得於 feature spec 層默默調整。

## 6. Revisit Triggers

* 商業模式改為 usage-based pricing（flat tier 不再是唯一形態）。
* 出現「宣告遠大於實際」的系統性浪費，且客戶要求按實際消耗計價。
* GPU node pool 落地，需決定 GPU 以佔用時間或利用率計。
* Prometheus + kube-state-metrics 正式部署，使 counter 型資料成為低成本選項。
* K8s in-place pod resize（v1.33+）普及，使單一 pod 的分配值可中途變動。

## 7. More Information

* `docs/features/resource-usage-metering/spec.md`：本 ADR 之落地規格（採樣、schema、積分、API）
* [ADR-0011](0011-plan-tier-capability-matrix.md) § 5.4：v1 不提供 self-service billing；本 ADR 不改變該立場
* [ADR-0005](0005-build-pipeline-and-callback.md) OQ#7：build 用量留在 `deploy_run`，與 runtime 用量分軌
* [ADR-0004](0004-k3s-role-and-orchestrator.md)：K3s 為 orchestrator，pod 為資源分配單位
* `docs/features/k3s-namespace-isolation/spec.md` § 5：ResourceQuota 為配額上限；本 ADR 管的是實際佔用紀錄，兩者不同

## 8. Open Questions

1. 計費啟動後，已刪除 app 的歷史帳本是否須保留（目前為 cascade 刪除）？v2 billing spec 前必須解決。
2. initContainer 之資源目前忽略；是否改為 K8s effective request（`max(init, sum(containers))`）？目前偏低，符合 under-bill 原則。
3. GPU 計量單位（佔用時間 vs 利用率）待 GPU node pool 與定價一併拍板。
4. in-place resize 後單一 pod 分配值中途變動，區間需可分段；schema 已可容納（關舊開新），但事件路徑需偵測 `resources` 變更。
