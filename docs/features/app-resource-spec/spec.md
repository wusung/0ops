# Feature Spec：app-resource-spec

> **狀態**：draft
> **來源**：使用者需求「建立 app 時可指定 region/cpu/ram/gpu 等資訊」；ADR-0011（plan tier 配額）；ADR-0004（K3s 單 cluster baseline）；`k3s-namespace-isolation` spec § 5（ResourceQuota / LimitRange）
> **適用範圍**：app 層級資源規格（region / zone / cpu / memory / gpu / replicas）之 DTO、DB schema、配額驗證、manifest render
> **對應 Milestone**：待排（schema 與驗證為 v1 可落地；gpu / 多 region 排程為 v2）

## 1. 結論（先讀本段）

- App 新增可選 `spec` 區塊：`region`、`zone`、`cpu`、`memory`、`gpu`、`replicas`；全部可省略，省略即沿用現行行為（LimitRange 預設 + replicas 1）
- **裸值，不採 preset**：cpu / memory 用 K8s resource quantity 字串（如 `250m`、`512Mi`）；不提供 small/large 等 instance size 代號
- **region / zone 命名採 AWS 格式**：region `{geo}-{direction}-{n}`、zone 為 region + 字母後綴；v1 唯一 region 為 `ap-east-2`（台北），唯一 zone 為 `ap-east-2a`，對應自管 K3s cluster——借用 AWS 命名規約與地理代號，**不代表底層是 AWS**；v2 遷 managed K8s 時代號可無縫對應
- 使用者指定值作為 **request**；limit 固定為 request × 2（與 ADR-0011 / LimitRange 之 `limits = 2× requests` 慣例一致；v1 不開放 limit 自訂）
- 配額驗證在 **preview 階段**先行：`Σ(app.cpu_request × replicas)`（含本次變更）不得超過 team tier 之 `requests.cpu` / `requests.memory`；K8s ResourceQuota 仍為最終防線
- `gpu` 欄位 schema 先收、v1 一律 preview fail（cluster 無 GPU node）；zone 指定非 `ap-east-2a` 同樣 preview fail
- 不動 ADR-0011 的 tier 框架：per-app spec 只在 team quota **內**分配，不得突破 tier 上限
- spec 變更（resize）走既有 preview / confirm gate，confirm 後 re-render manifest → gitops push → ArgoCD sync

## 2. 範圍

### 2.1 包含

- `AppCreateRequest` / `AppUpdateRequest` 之 `spec` 欄位定義與驗證規則
- `app` 表 migration（spec 欄位）
- preview 階段之 team quota 驗算
- `deployment.yaml.tmpl` 之 `resources` block 與 `replicas` 參數化
- MCP tool schema（`create_app_preview` / `create_app`，及 update 對應 tool）與 CLI flag
- region registry（v1 為 config 內靜態表）

### 2.2 不包含

- GPU node pool 建置與 GPU 排程（v2；本 spec 只預留 schema）
- 多 region / 多 cluster 排程與 region 定價（v2；ADR-0011 § 10 OQ#6）
- autoscaling / HPA、scale-to-zero（v2；plan.md non-goals）
- env vars / secrets / process types（屬 `secrets-management` 與後續 feature）
- persistent volume 規格（待 ADR-0004 OQ#4 之 CSI 拍板）
- team tier 配額數值本身（ADR-0011 釘定；本 spec 不改）

## 3. Spec 欄位定義

### 3.1 結構

```json
{
  "spec": {
    "region":   "ap-east-2",
    "zone":     "ap-east-2a",
    "cpu":      "250m",
    "memory":   "512Mi",
    "gpu":      { "type": "nvidia-t4", "count": 1 },
    "replicas": 2
  }
}
```

所有欄位可選；`spec` 整塊省略 = 全預設。

### 3.2 欄位規則

| 欄位 | 型別 | 預設 | 驗證規則 |
|---|---|---|---|
| `region` | string | `ap-east-2` | 必在 region registry 內且 `available=true`；v1 僅 `ap-east-2` |
| `zone` | string | 省略（由排程決定） | 必為所選 region 之合法 zone；v1 僅 `ap-east-2a`；指定不存在之 zone → preview fail |
| `cpu` | quantity string | 省略（LimitRange `100m`） | K8s quantity 格式；下限 `50m`、粒度 `1m`；上限受 § 4 quota 驗算 |
| `memory` | quantity string | 省略（LimitRange `256Mi`） | K8s quantity 格式（`Mi` / `Gi`）；下限 `64Mi`、粒度 `1Mi`；上限受 § 4 quota 驗算 |
| `gpu.type` | string | 無 | 必在 gpu registry 內；**v1 registry 為空 → 任何 gpu 指定皆 preview fail**，錯誤訊息明示「GPU 於 v2 開放」 |
| `gpu.count` | int | 0 | ≥ 1（指定 `gpu` 時）；上限隨 gpu registry 定義 |
| `replicas` | int | 1 | 1 ≤ n ≤ 20；`replicas × request` 與 `pods` 計入 § 4 quota 驗算 |

- `cpu` / `memory` 兩者**只指定其一**時，另一項沿用 LimitRange 預設值參與 quota 驗算與 render（render 時兩項都寫死進 manifest，不留一半給 LimitRange，避免行為分裂）
- limit 一律 = request × 2，由 backend 計算後寫入 manifest；DTO 不收 limit 欄位

### 3.3 Region / zone 命名規約（參考 AWS）

- Region ID 格式：`{geo}-{direction}-{n}`（如 `ap-east-2`、`ap-northeast-1`）；zone 為 region ID + 單一小寫字母（`ap-east-2a`）
- 與 AWS 實際 region 代號**地理對齊**：`ap-east-2` = 台北、`ap-northeast-1` = 東京；0ops 自管 cluster 落在哪個地理位置就用對應代號
- Region registry 為 server config 內靜態表（v1 不入 DB）：

```yaml
regions:
  - id: ap-east-2
    display_name: "Taipei"
    zones: ["ap-east-2a"]
    available: true
    cluster: default          # 對應之 cluster；v1 僅一個
```

- 新增 region = 改 config + 部署；v2 多 cluster 時再評估入 DB 與 per-region 定價（ADR-0011 OQ#6）

## 4. 配額驗證（preview 階段）

### 4.1 驗算公式

對 team 內所有 app（含本次 create / update 之新值）：

```
Σ(cpu_request × replicas)    ≤ tier.requests.cpu
Σ(memory_request × replicas) ≤ tier.requests.memory
Σ(replicas)                  ≤ tier.pods
```

- `cpu_request` / `memory_request`：app 指定值，未指定者以 LimitRange 預設（`100m` / `256Mi`）計
- limits 不需另驗：limit = request × 2 恆成立，tier 之 `limits.*` 亦為 `requests.*` × 2（ADR-0011），request 過驗即 limit 過驗
- 超額 → preview fail，錯誤訊息含：目前用量、本次需求、tier 上限、升級提示（error-model 規約）

### 4.2 與 K8s ResourceQuota 的關係

- preview 驗算為 **fail-fast UX**，非唯一防線；TOCTOU（preview 後他人 confirm 先吃掉配額）由 K8s ResourceQuota 最終擋下
- ResourceQuota 擋下時：deploy 失敗、reconciler 標 `quota_exceeded`（與 `reconciler-and-incident` spec 對齊）、audit_log 記錄

## 5. DB Schema

`app` 表新增欄位（migration）：

| 欄位 | 型別 | 預設 | 說明 |
|---|---|---|---|
| `region` | text NOT NULL | `'ap-east-2'` | region ID |
| `zone` | text NULL | NULL | NULL = 不指定 |
| `cpu_request_millicores` | integer NULL | NULL | NULL = 沿用 LimitRange |
| `memory_request_bytes` | bigint NULL | NULL | NULL = 沿用 LimitRange |
| `gpu_type` | text NULL | NULL | |
| `gpu_count` | integer NOT NULL | 0 | |
| `replicas` | integer NOT NULL | 1 | |

- 數值以 canonical 單位入庫（millicores / bytes），quantity 字串只存在於 DTO 邊界；序列化回 DTO 時轉回人類可讀字串（`1500m` → `1500m`、`536870912` → `512Mi`）
- 既有 app row 由 migration 預設值補齊，行為不變

## 6. Manifest Render

`deployment.yaml.tmpl` 變更：

```yaml
spec:
  replicas: {{ .Replicas }}            # 原硬寫 1
  template:
    spec:
      containers:
        - name: app
          resources:                    # spec 有指定 cpu/memory 任一時才出現
            requests:
              cpu: {{ .CPURequest }}
              memory: {{ .MemoryRequest }}
            limits:
              cpu: {{ .CPULimit }}      # request × 2
              memory: {{ .MemoryLimit }}
```

- `render.go` 之 template data 增補上述欄位
- cpu / memory 皆未指定：**不 render `resources` block**，維持 LimitRange 注入之現行行為
- zone：v1 單 node，**不 render** `nodeSelector` / affinity（指定 `ap-east-2a` 為合法 no-op）；v2 多 node 時補 `topology.kubernetes.io/zone` nodeSelector
- gpu：v1 不可達（preview 已擋）；v2 render `resources.limits["nvidia.com/gpu"]` + nodeSelector

## 7. API / MCP / CLI 變更

| 介面 | 變更 |
|---|---|
| `POST /v1/apps`（create preview/confirm） | request 增 `spec` 物件；response 之 app DTO 回傳生效 spec（含預設值展開） |
| `PATCH /v1/apps/{slug}`（update，resize 路徑） | 同上；spec 變更屬 side effect，走 preview / confirm gate，confirm 後觸發 re-render + redeploy |
| MCP `create_app_preview` / `create_app` | tool schema 增 `spec`；preview 輸出含 quota 驗算結果（目前用量 / 需求 / 上限） |
| MCP update 對應 tool | 同 create；無既有 update tool 時隨本 feature 一併開（範圍限 spec resize，不含其他欄位） |
| CLI | `0ops apps create --region --zone --cpu --memory --gpu-type --gpu-count --replicas`；update 同 flag |

依 AGENTS.md：DTO、MCP schema、CLI output contract、migration 變更皆**必補 contract test**。

## 8. 與其他 spec 接合點

| 接合 | spec |
|---|---|
| tier 配額數值與 limits=2×requests 慣例 | ADR-0011 § 3.1；`k3s-namespace-isolation` § 5 |
| LimitRange 預設值（未指定時行為） | `k3s-namespace-isolation` § 5（`100m/256Mi`、`500m/1Gi`） |
| preview / confirm gate | `preview-confirm-gate` spec |
| quota 超額錯誤格式 | `error-model` spec |
| re-render 與 ArgoCD sync | `gitops-render-and-argocd` spec |
| `quota_exceeded` 失敗分類 | `reconciler-and-incident` spec |
| spec 變更之 audit | `audit-log` spec（記 before/after spec） |

## 9. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| spec 省略 = 現行行為 | create 不帶 spec → 比對 render 輸出 | manifest 與變更前 byte-identical（replicas 1、無 resources block） |
| quantity 解析 | 單元測試：`250m`/`1`/`512Mi`/`2Gi`/非法值 | 合法值正規化入庫；非法值 400 |
| 下限驗證 | cpu `49m` / memory `63Mi` | preview fail，錯誤指明下限 |
| quota 驗算矩陣 | free tier 下逐步 create 至超額 | 額度內過、超額 preview fail 且訊息含三數值 |
| limit 推導 | cpu `250m` → manifest | limits.cpu = `500m` |
| region 驗證 | region `us-east-1` | preview fail，列出可用 region |
| zone 驗證 | zone `ap-east-2b` | preview fail |
| gpu v1 擋下 | gpu type 任意值 | preview fail，訊息含 v2 說明 |
| replicas render | replicas 3 → confirm → manifest | `replicas: 3`；pods 計入 quota 驗算 |
| resize redeploy | update cpu → confirm | gitops repo 出現新 manifest commit；ArgoCD sync 後 pod resources 更新 |
| migration 回填 | 既有 app row | region=`ap-east-2`、replicas=1、其餘 NULL/0 |
| DTO/MCP contract | contract test | schema 與 server 序列化一致 |
| TOCTOU 防線 | 兩 session 並發 confirm 吃同額度 | 後者 deploy 失敗、分類 `quota_exceeded`、audit_log 有記錄 |

## 10. 對 `docs/0ops-plan.md` 的修改清單

1. 「Runtime topology」段：補「app 可選 resource spec（request 裸值，limit=2×request）；region 命名採 AWS 格式，v1 僅 `ap-east-2`」並交叉引用本 spec
2. Non-goals 段：autoscaling / 多 region 維持 v2，但註明「per-app resource spec 與 region 欄位 schema 已於 v1 預留」
3. create_app 流程描述：補 preview 階段 quota 驗算

## 11. Open issues

- region 命名規約是否需獨立 ADR（跨 v2 多 cluster、定價、data residency 三題）：傾向實作前補 ADR-0012，本 spec § 3.3 為其草案
- `replicas` 上限 20 為暫定值；是否應隨 tier 變動（free 限 1？）待 ADR-0011 補丁拍板
- update tool 的範圍：v1 只開 spec resize，或一併開 builder / ref 變更，待 create-app-flow spec 對齊
- gpu registry 的 type 命名（`nvidia-t4` vs K8s device plugin resource name）：v2 建 GPU node pool 時拍板
- multi-zone 後 zone 指定與 PV 之 zone 親和性互動：待 CSI 拍板（ADR-0004 OQ#4）

## 12. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. 不採 preset / instance size 代號；cpu、memory 一律裸值（K8s quantity）
2. region / zone 命名必符 AWS 格式且與地理對齊；不得自創格式
3. per-app spec 不得突破 team tier 之 ResourceQuota；preview 驗算不得跳過
4. limit 恆為 request × 2，由 backend 推導；DTO 不得收 limit 欄位（開放自訂需 ADR）
5. spec 全省略時 render 輸出必須與導入前完全一致（零行為變更）
6. spec 變更必走 preview / confirm gate，不得直接生效
7. DB 以 canonical 數值（millicores / bytes）入庫；quantity 字串不得入庫
8. v1 gpu 必於 preview 擋下；不得讓 gpu spec 進入 render 路徑
