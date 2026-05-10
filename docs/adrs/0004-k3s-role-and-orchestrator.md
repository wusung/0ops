---
adr: "0004"
title: K3s 角色與 v1 容器編排器選擇
status: Accepted
date: 2026-05-09
tags:
  - kubernetes
  - k3s
  - orchestrator
  - infrastructure
  - foundation
supersedes: []
superseded-by: []
---

# ADR-0004：K3s 角色與 v1 容器編排器選擇

* Status：Accepted
* Date：2026-05-09
* 適用範圍：M0–M5；backend 自身與 managed apps 共用之 runtime 平台選擇
* 來源：`docs/0ops-plan.md`「Runtime topology & operability」「Risks & open #7」段；plan 將實際決策延後至本 ADR
* 上游依賴：[ADR-0001 多租戶模型與 RBAC](0001-multi-tenancy-and-rbac.md)（per-team namespace 與 quota 邊界）；[ADR-0006 Observability baseline](0006-observability-baseline.md)（SLO 99.9% 可達性受 cluster 可靠度上限約束）
* 下游影響：ADR-0008 Backend HA（受本 ADR 之 backend 同 cluster co-location 約束）

## 0. TL;DR（先讀本段）

採用以下六項組合決策：

1. **v1 編排器**：K3s 單 cluster；不採 EKS / GKE / kubeadm / Nomad / standalone Docker。
2. **K3s datastore**：**PostgreSQL via kine**；不採預設 SQLite（plan 已知 > 100 namespace 退化）、不採 embedded etcd（v1 single-node 不需 HA）。
3. **K3s datastore Postgres 實例**：**獨立於 application DB**；不共用 instance（隔離 SLO 與 blast radius）。
4. **Backend 與 managed apps co-location**：同 K3s cluster；以 namespace 區隔（`system-0ops` vs `team-<slug>`）；受同 PSA + NetworkPolicy 規範。
5. **v1 必補 baseline**（不可缺）：datastore snapshot 6h（PostgreSQL `pg_dump` + WAL archive）、per-team `ResourceQuota` + `LimitRange`、PSA `enforce=baseline / warn=restricted`、`ghcr-pull` ImagePullSecret 30 min refresh。
6. **長期定位**：K3s 為 **stopgap-acceptable**；非「永久承諾」亦非「強制 M5 必遷」；v2 重審由 7 條 Revisit Triggers 觸發，候選為 managed K8s（EKS / GKE）—— 遷移時應用層（chart / GitOps / Cloudflare）不需重做。

行為與 namespace / quota 數值細節以 `docs/0ops-plan.md`「Runtime topology & operability」段為準，本 ADR 不重述。

## 1. Context and Problem Statement

0ops backend 與 managed apps 都需要 container orchestration。v1 為單 region（winshare.tw 台灣）、小團隊運維；M5 之前不需 multi-region 或 multi-AZ。需在第一行 chart 寫下之前釘住四件事：

1. v1 編排器選哪個——managed K8s 控本控低但成本高；K3s 自管低成本但運維風險全攬；其他選項（Nomad、Docker swarm）生態與 ArgoCD / Helm 對接落差。
2. K3s 預設 datastore 為 SQLite，plan 已標示「namespace + workload 100 量級退化」；M2 進入 production 前必須選定 backing store。
3. backend 自身與 managed apps 是否共用 cluster——共用節省成本、增加耦合；分離反之。
4. 若選 K3s 為 v1，是「stopgap」（M5 後遷移 managed K8s）還是「長期決策」（v2 仍以 K3s 跨 region 擴展）？此差異決定是否在 chart / IaC 層先做 abstract，避免 vendor lock-in。

ADR-0001 已將 team 設為 namespace 邊界，ADR-0006 已將 SLO 目標訂為 99.9%；本 ADR 在這兩個約束下選擇能達成它們、又符合 v1 規模成本的編排層。

## 2. Decision Drivers

* **DD1 v1 規模成本約束**：小團隊運維，雲端 managed K8s 月固定成本（control plane fee + minimum node group）對 v1 階段不合理。
* **DD2 backend 與 managed apps 同棧運維**：兩者皆為 0ops 自管；同 cluster 可共享 Cloudflare Tunnel、ImagePullSecret refresh、observability stack。
* **DD3 多租戶安全邊界**：per-team namespace + ResourceQuota + PSA baseline 為硬要求（接續 ADR-0001）；任何編排器必須能落實這三件事。
* **DD4 v2 遷移 optionality**：應用層（Helm chart、ArgoCD ApplicationSet、Cloudflare Tunnel、GitHub Actions deploy workflow）必須與 control plane 解耦；v2 換 cluster 不應重做應用層。
* **DD5 SLO 99.9% 達成**：ADR-0006 已承諾 v1 GA 後 99.9% / 28d API availability；cluster 控制面與 datastore 可靠度為硬上限。
* **DD6 Datastore 可靠度上限**：K3s SQLite 在 plan 標示的「100 namespace 量級退化」是已知事實；不可作 v1 production datastore。
* **DD7 Cluster 失效域**：v1 單 cluster 為已接受的失效域（plan「Risks #7」），但需有明確 backup / DR 與 v2 遷移退路；不可成為「無法被替換」的依賴。
* **DD8 GitOps / Helm / ArgoCD 生態相容**：選用的編排器需與 plan 既定的 GitOps + Argo + Helm + Cloudflare Tunnel 棧相容；非 K8s 系統（Nomad）需重做應用層棧。

## 3. Considered Options

針對 (a) 編排器主體、(b) K3s datastore、(c) 長期定位 做完整 alternative 比較；(d)(e) 為局部技術選擇，列表帶過。

### 3.1 (a) 編排器主體

| Option | 描述 |
|---|---|
| **A1. K3s 單 cluster**（採用） | Rancher / SUSE 維護；標準 K8s API；單 binary；資源占用低；rootless / rootful 皆可 |
| A2. Managed K8s（EKS / GKE / AKS） | 雲端託管 control plane；HA / 升級 / etcd backup 由雲商承擔；月固定成本 |
| A3. kubeadm self-managed | 純 upstream K8s；自行管理 control plane / etcd |
| A4. HashiCorp Nomad | 非 K8s；需重做 ArgoCD / Helm / Ingress 整合 |
| A5. Standalone Docker / Podman + systemd | 無編排層；多租戶隔離靠 Linux primitives |
| A6. K0s（Mirantis） | 與 K3s 對標；單 binary；社群規模較小 |

### 3.2 (b) K3s datastore

| Option | 描述 |
|---|---|
| **B1. PostgreSQL via kine**（採用） | K3s 透過 kine adapter 將 K8s API server 的 etcd 流量轉為 PostgreSQL DML；獨立 instance |
| B2. SQLite（K3s 預設） | 單一檔案；plan 已標示 > 100 namespace 量級退化 |
| B3. Embedded etcd（`--cluster-init`） | K3s 節點自組 etcd cluster；需 ≥ 3 節點（HA） |
| B4. External etcd | 獨立 etcd cluster；自管 |
| B5. MySQL via kine | kine 亦支援 MySQL；組織內無 MySQL 經驗 |

### 3.3 (c) 長期定位

| Option | 描述 |
|---|---|
| **C1. Stopgap-acceptable + 明確 Revisit Triggers**（採用） | v1 用 K3s；不承諾長期；應用層解耦；v2 由 triggers 觸發遷移評估 |
| C2. 長期決策（v2 仍 K3s 跨 region 擴展） | v1 + v2 一致；節省遷移成本；單 cluster 失效域永久存在 |
| C3. 嚴格 stopgap（M5 必遷 managed K8s） | 預先設定 deadline；強制 v2 改 EKS / GKE |
| C4. 雙路徑（dev 用 K3s，prod 用 managed K8s） | 開發 / production 編排器不同 |

### 3.4 (d)(e) 局部選擇

| 子決策 | 採用 | 否決 | 一句結論 |
|---|---|---|---|
| (d) Backend 與 managed apps co-location | 同 K3s cluster + namespace 區隔（`system-0ops` vs `team-<slug>`） | 獨立 cluster / 跑在 host 外 | 獨立 cluster 雙倍 ops 成本；host 外執行失去 K8s rolling / probe 機制 |
| (e) v1 必補 baseline | 6h datastore snapshot + per-team Quota + PSA baseline+warn restricted + 30min ImagePullSecret refresh | 缺其一 / 都不補 | 任一缺項即無法達成 ADR-0001 隔離承諾或 ADR-0006 SLO |

## 4. Decision Outcome

採用 **A1 + B1 + C1**，搭配 (d) co-location、(e) 完整 baseline。

具體展開：

1. **v1 cluster topology**：
   * 單 K3s cluster，部署於 winshare 台灣 region。
   * 控制面節點與 worker 節點現階段允許 co-located；M3 後評估分離（與 ADR-0008 backend HA 一同決議）。
   * cluster 版本鎖 K3s upstream LTS（具體版本由 runbook 落地，非 ADR 範圍）。
2. **Datastore（B1）**：
   * K3s 啟動參數帶 `--datastore-endpoint=postgres://...`；走 kine adapter。
   * Datastore Postgres 為**獨立 instance**，不與 application DB 共用——隔離 SLO（control plane 故障不應拖垮 app data，反之亦然）、不同 backup 頻率、不同 resource profile。
   * Datastore Postgres 的 HA / DR 屬本 ADR 之 (e) baseline 範圍；application DB 的 HA 屬 ADR-0008 範圍。
3. **Namespace 與多租戶邊界**：以 ADR-0001 為準，per-team `team-<team_slug>` + `system-0ops`；命名 / quota / NetworkPolicy / PSA 細節以 plan「Runtime topology & operability」段為準，本 ADR 不重述。
4. **v1 必補 baseline（hard requirement，缺項即不得進 GA）**：
   * **Datastore snapshot**：每 6 小時 `pg_dump` 到 R2 / S3；WAL archive 每 5 分鐘推 segment；保留 30 天。
   * **Per-team ResourceQuota + LimitRange**：依 plan tier（free / starter / pro）配置；新 team 建立時自動 apply。
   * **PSA baseline**：`pod-security.kubernetes.io/enforce=baseline`、`warn=restricted`；v2 升 `enforce=restricted`。
   * **ImagePullSecret**：team namespace 預埋 `ghcr-pull`；backend 用 GitHub App installation token 簽發 1h GHCR token；背景 goroutine 每 30 min refresh。
   * **NetworkPolicy 預設**：ingress 僅允許 `kube-system/traefik` + 同 namespace；egress 允許 `0.0.0.0/0` 但封 RFC1918 內網（保留 K8s service CIDR + Cloudflare Tunnel pod 例外）。
5. **應用層解耦（為支撐 C1）**：
   * Helm chart（`deploy/chart/launchpad/`、`deploy/chart/managed-app/`）不含 K3s 特定資源（`HelmChart` CRD、`traefik` 特定設定）；走 upstream K8s API。
   * ArgoCD ApplicationSet 模板不依賴 K3s embedded resource。
   * Cloudflare Tunnel connector 部署為標準 Deployment / DaemonSet；非 K3s 專屬。
6. **長期定位（C1）**：
   * 不在 plan 或本 ADR 強制「M5 後必遷」；不在 plan 或本 ADR 承諾「v2 仍用 K3s」。
   * 由本 ADR 第 7 節 Revisit Triggers 決定何時重審；trigger 觸發時開新 ADR 評估遷移。
   * v2 候選優先順序：GKE > EKS > AKS > kubeadm self-managed（依「對既有 GitOps + Argo + Cloudflare 棧的整合摩擦最小」排序）。

## 5. Pros and Cons of the Options

### 5.1 (a) 編排器主體

#### A1. K3s 單 cluster（採用）

* Good：標準 K8s API；GitOps / Helm / ArgoCD 生態完全相容；遷移 managed K8s 應用層不需重做。
* Good：單 binary、資源占用低；v1 規模 ops 成本最低。
* Good：Rancher / SUSE 商業支援可選；社群規模較 K0s 大。
* Good：rootless 模式可選；與 ADR-0001 多租戶安全模型對齊。
* Bad：control plane HA 自管；M5 前 single-node control plane 為失效域。
* Bad：預設 datastore SQLite 不可用於 production；必須切 PostgreSQL（B1）。
* Bad：upgrade 自管；K3s 版本升級需建立 runbook 與測試流程。

#### A2. Managed K8s（EKS / GKE / AKS）

* Good：control plane HA / etcd backup / 升級皆由雲商承擔；99.95% control plane SLA。
* Good：與 cloud-native ecosystem（IAM、Load Balancer、Storage）整合最深。
* Bad：月固定成本（EKS $73/cluster、GKE $73/cluster + node group 最低費用）；v1 規模不合理。
* Bad：vendor lock-in；切換 cloud 成本高。
* Bad：cloud-specific 整合（IAM、ELB）若被引入應用層，未來重做成本大。

#### A3. kubeadm self-managed

* Good：純 upstream K8s；無 distribution-specific 行為。
* Bad：control plane / etcd / 升級全自管；ops 工作量遠超 K3s。
* Bad：對 v1 規模 over-investment；無 K3s 的 single-binary 優勢。

#### A4. HashiCorp Nomad

* Good：scheduler 簡單；單 binary；多 workload 支援（包括非 container）。
* Bad：非 K8s API；需重做 GitOps + Helm + Ingress 整合（plan 既定棧）。
* Bad：與 ArgoCD / Cloudflare Tunnel / Cert-Manager 整合摩擦大。
* Bad：違反 DD8。

#### A5. Standalone Docker / Podman + systemd

* Good：最簡單；無編排層學習成本。
* Bad：多租戶隔離靠 Linux primitives；ResourceQuota / NetworkPolicy / PSA 需自實作。
* Bad：rolling update / health probe / autoscaling 需自寫；ops 成本失控。
* Bad：違反 DD3。

#### A6. K0s

* Good：與 K3s 同類；對標 minimal K8s。
* Bad：社群規模較小；issue / 文件 / chart 資源較 K3s 弱。
* Bad：與 K3s 比無顯著優勢；換用即失去 K3s 較強的 distribution support。

### 5.2 (b) K3s datastore

#### B1. PostgreSQL via kine（採用）

* Good：plan 已建議；社群多實例驗證；可使用既有 PostgreSQL 運維能力（與 application DB 同類，雖獨立 instance）。
* Good：backup / DR 工具鏈成熟（`pg_dump`、WAL archive、PITR）；6h snapshot 可達。
* Good：可水平擴展讀取（M5 後若需要）；datastore 不是 cluster 成長瓶頸。
* Good：kine 為 K3s 預設 abstraction；切換 datastore 不需改 K8s API server。
* Bad：多一個 PostgreSQL instance；M0 需建立 datastore Postgres provisioning runbook。
* Bad：kine 對 PostgreSQL 行為的 corner case（如長交易、空閒連線）需 spike 驗證。
* Bad：write latency 通常高於 etcd；對 K8s API 高頻 list / watch 場景理論上較慢（v1 規模可忽略）。

#### B2. SQLite（K3s 預設）

* Good：零依賴；單檔 backup。
* Bad：plan 已標示 > 100 namespace 量級退化；v1 GA 即可能撞牆。
* Bad：違反 DD6。

#### B3. Embedded etcd

* Good：原生 K8s datastore；性能最佳。
* Good：HA 模式 K3s `--cluster-init` 自動形成 etcd cluster。
* Bad：HA 需 ≥ 3 節點；v1 single-node 規模不需 HA、不該為此付額外節點成本。
* Bad：etcd backup / 還原比 PostgreSQL 工具鏈陌生（社群成熟度仍可，但組織既有運維能力為 PostgreSQL）。

#### B4. External etcd

* Good：與 upstream K8s 對齊；datastore 可獨立擴展。
* Bad：自管 etcd cluster ops 成本高；v1 規模不需要。
* Bad：與 B3 比無增益。

#### B5. MySQL via kine

* Good：kine 支援。
* Bad：組織無 MySQL 運維經驗；不必為 datastore 引入新 RDBMS。

### 5.3 (c) 長期定位

#### C1. Stopgap-acceptable + Revisit Triggers（採用）

* Good：保留 optionality；v2 不被預先綁定。
* Good：應用層解耦的設計約束（Helm chart 不依賴 K3s-specific）變成可被驗證的規則，而非「希望」。
* Good：Revisit Triggers 為客觀指標；遷移決策不靠人為情緒。
* Bad：心智負擔——團隊需保持「這可能會改」的張力，不能讓 K3s 特定假設潛入應用層。
* Bad：未承諾長期，組織內擴展（hire、培訓）對「我們用 K3s」的承諾感較弱。

#### C2. 長期決策

* Good：團隊心智單一；可深度投資 K3s ecosystem。
* Bad：單 cluster 失效域永久存在；v2 遷移選項被自我限縮。
* Bad：對企業客戶「multi-AZ 要求」無回答路徑。

#### C3. 嚴格 stopgap（M5 必遷）

* Good：deadline 強制應用層解耦徹底；ops 學習雲端 K8s。
* Bad：M5 規模未必觸發遷移必要性；強制遷移可能浪費資源。
* Bad：「為了遷移而遷移」違反 YAGNI。

#### C4. 雙路徑（dev K3s + prod managed K8s）

* Good：dev 成本低、prod 可靠。
* Bad：兩套 chart / GitOps / observability 配置；dev / prod parity 失準。
* Bad：違反 DD2（同棧運維）。

## 6. Consequences

### 6.1 Positive

* v1 規模 ops 成本顯著低於 managed K8s；月固定成本主要為節點本身與 PostgreSQL instance。
* Backend 與 managed apps 同 cluster 共享 Cloudflare Tunnel / ImagePullSecret refresh / observability stack；架構簡單。
* PostgreSQL kine + WAL archive 給 K3s control plane 提供 PITR；RPO 5min 可達。
* 應用層解耦（Helm / ArgoCD / Cloudflare Tunnel 不依賴 K3s）讓 v2 遷移成本可控。
* PSA baseline + ResourceQuota 強制讓多租戶安全模型在 cluster 層落實，與 ADR-0001 SQL 防線形成多層防禦。

### 6.2 Negative

* 單 K3s cluster 為失效域；節點 / control plane / datastore Postgres 任一全失效即整服務不可用。
* Datastore PostgreSQL 為新增運維面；M0 需建立 provisioning + backup + 升級 runbook。
* K3s upgrade 自管；upstream K8s 升版節奏需追隨。
* 「stopgap-acceptable」需團隊紀律維持應用層解耦；任何 K3s-specific 假設潛入即破壞 v2 optionality。
* `system-0ops` 與 `team-*` 同 cluster 意味著 managed app 異常理論上可能影響 backend；NetworkPolicy + PSA + ResourceQuota 是必要而非充分條件——M5 backend HA 升級時應重審是否分離 cluster。

### 6.3 Neutral

* Cloudflare Tunnel connector 部署形態（DaemonSet / Deployment）為運維細節，不在本 ADR。
* CSI / 持久存儲選擇（local-path / longhorn / 雲端 CSI）為未來 ADR 範圍。
* Service mesh（Linkerd / Cilium）非 v1 範圍；plan 未列入。
* M3+ 若引入 multi-region，本 ADR 即被觸發 Revisit。

## 7. Revisit Triggers

下列任一條件觸發時，應重新評估本 ADR 對應段並開新 ADR 決議遷移路徑：

1. **Cluster scale 上限**：namespace 總數 > 500 或 pod 總數 > 5000，或 PostgreSQL kine datastore 在 production observation 中出現 latency p95 > 100ms 持續 24h。
2. **SLO 持續不達**：API availability 99.9% 連 2 個 28d 週期 budget 用罄 > 80%，且 root-cause 涉及 cluster control plane 或 datastore（非應用層 bug）。
3. **客戶 multi-AZ / multi-region 需求**：商業承諾出現「跨可用區 / 跨地域容錯」要求 → 立即觸發 v2 遷移評估。
4. **Ops effort 超出**：cluster 維運實際工時 > 0.5 FTE 持續 1 個 quarter（含 K3s upgrade、datastore 維護、incident response）。
5. **PSA restricted 升級**：v2 升 `enforce=restricted` 時，managed app workload 廣泛違反 → 評估是否同時換 cluster 解決。
6. **Datastore Postgres 異常**：kine + Postgres 在 corner case（長交易、連線數爆炸）出現 production incident，且 etcd 模型可解 → 重審 (b)。
7. **Cloudflare Tunnel HA 限制**：tunnel connector 與 K3s 整合摩擦阻擋 multi-AZ 部署 → 重審 (a)(d)。

## 8. More Information

* Runtime topology 細節（namespace、ResourceQuota、LimitRange、NetworkPolicy、PSA、ImagePullSecret）：`docs/0ops-plan.md`「Runtime topology & operability」段。
* Postgres backup / DR for application DB（與本 ADR datastore Postgres 為兩個 instance）：規劃為 ADR-0008（待寫）。
* Build pipeline 與 image push 對 ImagePullSecret 的依賴：規劃為 ADR-0005（待寫）。
* Backend HA（v1 single replica → M5 leader election）：規劃為 ADR-0008，受本 ADR co-location 約束。
* SLO 99.9% 達成的 cluster 可靠度上限：[ADR-0006 Observability baseline](0006-observability-baseline.md) 第 5 節。
* 客戶自有域名 TLS 對 cluster 入口的影響：規劃為 ADR-0007（待寫）。

## 9. Open Questions

下列問題不阻擋本 ADR 通過，但需在 M2（GA 前）敲定：

1. **K3s 版本鎖定**：採 K3s LTS 還是追隨 stable？K3s upstream upgrade window 與 upstream K8s n−2 政策的對齊。
2. **Datastore Postgres instance 規格**：CPU / memory / 連線數上限；連線池策略（pgbouncer 是否引入）。
3. **節點規格與數量**：M2 上線時的 node count / size，與成本模型對齊；M5 是否引入專用 control plane node。
4. **CSI 選擇**：managed app 若需 PVC，採 local-path（單節點）還是 longhorn / openebs？對 v1 single-node 的影響。
5. **K3s upgrade 流程**：採 system-upgrade-controller 還是手動？multi-node 時的 upgrade order。
6. **Etcd snapshot vs Postgres backup 對齊**：plan「v1 必補：etcd backup」用詞為通用；本 ADR 已釘為 PostgreSQL backup（kine 模式下無 etcd），需在 plan 同步修訂用詞。
7. **Backend HA 是否需要分離 cluster**：M5 backend 升 2 replica 時，是繼續 co-location 還是分離至獨立 cluster？由 ADR-0008 決議，但需與本 ADR Revisit Triggers 對齊。
