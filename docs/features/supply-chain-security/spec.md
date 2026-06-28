# Feature Spec：supply-chain-security

> **狀態**：draft
> **來源**：`docs/trust-and-compliance/plan.md` § 3.3 / § 5.1（P1）；`docs/features/threat-model/spec.md` § 5.4（SC1 / SC2(H) / SC3）+ § 6 缺口彙整指向本 spec
> **適用範圍**：對 build 出的 app image 與 0ops 自身 artifact（server / migrations image、CLI / MCP binary）建立 SBOM、依賴掃描、SLSA provenance、image 簽章與部署端驗簽；gitops repo 寫入收斂與非 backend commit 偵測；既有 callback / webhook 簽章金鑰輪替之文件化。不含個別緩解的營運排程細節（屬 runbook）與滲透測試（屬 P3 runbook）
> **對應 Milestone**：P1（接既有 CI release pipeline + `build-pipeline-and-callback`）
> **依賴**：`build-pipeline-and-callback`（既有 app build：GHA `pack build` → image push GHCR → HMAC callback）、CI release pipeline（`.github/workflows/release.yml` 之 `images` job 發 `0ops-server` / `0ops-migrations` image；`goreleaser` 發 CLI / MCP / server binary）、`gitops-render-and-argocd`（gitops repo 為唯一真相、backend 為唯一 writer）、`secrets-management`、`error-model`
> **引用 ADR**：ADR-0017（image 簽章 / provenance / SLSA 等級策略）；本 spec § 4 / § 5 釘死決策點，ADR-0017 釘住決策邊界
> **讀法**：§ 1 結論 → § 4 SLSA / 簽章決策 → § 11 驗證準則 → § 13 硬性規則

## 1. 結論（先讀本段）

- 本 spec 解 `threat-model` 三條供應鏈威脅：
  - **SC2（H）**：依賴 / build 環境污染植入後門 image——既有僅 self-hosted runner 隔離 + app image 的 Trivy 觀察期掃描，**無 SBOM、無 provenance、無 0ops 自身 image 掃描、無 Go 依賴掃描**。本 spec 補齊。
  - **SC3（M）**：image digest 在 GHCR↔ArgoCD 間被替換——既有僅 callback 帶 digest，**但部署端無簽章驗證，且 gitops 實際以 `:<commit_sha>` tag pin 而非不可變 digest**（見 § 12 跨 spec 不一致）。本 spec 引入 cosign 簽章 + 部署端 admission 驗簽 + gitops digest pin。
  - **SC1（M）**：gitops repo 被惡意 commit 繞過 backend——既有 backend 為唯一 writer + branch protection + Ed25519 SSH commit signing，**但 repo write 權限收斂與非 backend commit 偵測未文件化**。本 spec 收口。
- **明確區分既有與本 spec 引入**（不得把規劃講成已具備）：
  - **既有（已 ship）**：app image 經 `pack build`（CNB）+ Trivy `HIGH,CRITICAL` 掃描（`exit-code=0` 觀察期，`build-pipeline-and-callback` § 7）；0ops server / migrations image 由 `release.yml` 之 `images` job `docker build` 後 push GHCR；CLI / MCP / server binary 由 `goreleaser` 發布；gitops 為唯一真相、backend 為唯一 writer、branch protection（required signature / no force-push / reviews=0 / CODEOWNERS）、commit 用 Ed25519 SSH 簽章（`gitops-render-and-argocd` § 5.3）；callback / webhook 用 HMAC 簽章，`OPS_TOKEN_SIGNING_SECRET` / `OPS_CALLBACK_SECRET` 已定 90 天輪替（`build-pipeline-and-callback` § 5.4）。
  - **本 spec 引入（尚未實作）**：所有 image + CLI binary 的 SBOM 生成與發布、Go `govulncheck` 進 CI、0ops 自身 image 的 Trivy 掃描、SLSA build provenance attestation、cosign keyless 簽章、K3s admission 端驗簽 policy、gitops 由 tag pin 轉 digest pin、非 backend commit 偵測、GitHub webhook secret 輪替 runbook。
- **格式選定**：SBOM 採 **CycloneDX**（理由見 § 4.1）；provenance 採 **SLSA Build L2 為 v1 目標、L3 為 gated 目標**（self-hosted runner 對 L3 的限制見 § 4.3）；簽章採 **cosign keyless（GitHub OIDC + Fulcio + Rekor）**（理由見 § 4.4）。三者均以 OCI referrer / attestation 附到 image **digest**，故 digest pin 為本 spec 的硬前提。
- **部署端為信任閘門的終點**：K3s admission policy（sigstore policy-controller 或 Kyverno `verifyImages`）對所有 `team-*` namespace 與 `system-0ops` namespace 的 image 強制驗簽，拒絕未簽 / 未通過 provenance 述詞的 image。GitOps digest pin 確保「被驗證的 digest == 被部署的 digest」。

## 2. 範圍

### 2.1 包含
- SBOM 生成、儲存、與 image digest / `deploy_run` 的關聯（§ 4.1、§ 6）。
- 依賴掃描：Go module（`govulncheck`）+ 容器 image（Trivy）之 CI 階段與 fail 門檻（§ 4.2）。
- SLSA build provenance 的等級取捨、attestation 產生與儲存（§ 4.3）。
- Image 簽章（cosign）與 K3s admission 端驗簽 policy（§ 4.4、§ 5）。
- GitOps repo 寫入權限收斂、branch protection 強化、非 backend commit 偵測（§ 7）。
- Callback / webhook 簽章金鑰輪替流程文件化（接 AU4 / AU5；§ 8）。
- CI workflow 變更點（`ci.yml`、`release.yml`、`deploy/workflows/deploy-app.yml`）之設計層說明（§ 3）。

### 2.2 不包含
- Trivy 由觀察期升 `exit-code=1` 的條件與量測（屬 `build-pipeline-and-callback` § 7；本 spec 只接其 app image 掃描既有路徑並擴及 0ops 自身 image）。
- secret at-rest 加密與金鑰管理本體（屬 `secrets-management`；本 spec § 8 只文件化輪替「流程」）。
- 自架 Sigstore（Fulcio / Rekor）部署（屬 runbook；v1 用公開 Sigstore，§ 4.4 標 future）。
- 滲透測試與 CVE patching 排程（屬 P3 runbook）。
- 漏洞揭露政策 / `security.txt`（屬 `plan.md` § 3.3 另一條 P2 產出，非本 spec）。
- ArgoCD git commit 簽章驗證設定（GPG/SSH allowed signers 屬 `gitops-render-and-argocd` § 5.3；本 spec § 7 只補偵測與收斂）。

## 3. 檔案結構（CI workflow 變更點；只寫設計不寫實作）

```
0ops/
├── .github/
│   └── workflows/
│       ├── ci.yml                      # 變更：新增 govulncheck step（PR + main，fail-on=HIGH 以上）
│       └── release.yml                 # 變更：images job 後接 syft(SBOM)、trivy(scan)、
│       │                               #        slsa provenance、cosign sign+attest（server/migrations）
│       │                               #        goreleaser：啟用內建 sbom（syft）+ cosign 簽 checksums/binary
│       └── (無新檔；變更集中於既有兩支 workflow)
├── deploy/
│   ├── workflows/
│   │   └── deploy-app.yml              # 變更：Trivy step 後接 syft(SBOM)、cosign sign+attest（app image）
│   │   │                               #        provenance attestation 隨 image digest 附上
│   │   └── scripts/
│   │       └── render-and-push-gitops.sh   # 變更：image_ref 由 tag 改為 <repo>@<sha256-digest>（§ 12 修正 SC3）
│   └── gitops/
│       └── argocd/
│           └── apps/
│               └── policy-controller.yaml   # 新增（設計層）：admission 驗簽 policy 之 ArgoCD app 入口
│                                            # 實際 policy CR 屬 deploy/ 範圍，本 spec 釘介面
└── docs/
    └── runbooks/
        └── signing-key-rotation.md     # 新增：callback / webhook / cosign（若 key-based 退路）金鑰輪替 runbook（§ 8）
```

> 本 spec 不引入新的 backend Go package；SBOM / 簽章 / provenance 均在 CI 端產生並附到 OCI registry。backend 端唯一新增邏輯為 § 6 的「SBOM 引用 ↔ deploy_run 關聯」與 § 7 的「非 backend commit 偵測」，兩者掛在既有 callback handler 與 reconciler，不新增 package。

## 4. 核心機制與決策

### 4.1 SBOM 生成（解 SC2 之可視性缺口）

| 項目 | 決策 |
|---|---|
| 格式 | **CycloneDX**（JSON）。理由：Trivy 與 syft 均原生輸出、cosign `attest` 原生支援、且 CycloneDX 對 VEX（漏洞可利用性交換）支援優於 SPDX，利於後續 `security-hardening` 與 SOC2 交付以「已知但不可利用」抑制噪音。SPDX 為退路（授權合規場景），v1 不雙發。 |
| 生成工具 | **syft**（產 SBOM）+ Trivy（掃描，§ 4.2）。同一工具鏈避免 SBOM 與掃描結果漂移。 |
| 涵蓋對象 | (a) app image（`deploy-app.yml`，`pack build` 後）；(b) 0ops `0ops-server` / `0ops-migrations` image（`release.yml` images job）；(c) CLI / MCP / server binary（`goreleaser` 內建 sbom，syft）。**注意：v1 無 `0ops-cli` image——CLI 以 goreleaser binary 發布，其 SBOM 隨 binary artifact 附 `.sbom.json`，非附 image。** |
| 儲存 | image SBOM 以 **cosign attestation（in-toto，predicateType=CycloneDX）** 附到 image **digest**，存於 GHCR 同 repo 的 OCI referrer；binary SBOM 隨 release artifact 上傳 GitHub Release。不另建 SBOM 物件儲存。 |
| 關聯 | image SBOM 透過 digest 與 image 永久綁定；backend 於 callback 收到 image digest 後，把 `image_digest` 寫入 `deploy_run`（§ 6），審計查詢可由 `deploy_run → image_digest → cosign attestation` 取回 SBOM。 |

### 4.2 依賴掃描

| 掃描面 | 工具 | CI 階段 | fail 條件 |
|---|---|---|---|
| Go module 漏洞 | `govulncheck` | **`ci.yml`，PR + push main**，於 `go test` 後 | 偵測到「**可達**（call-graph reachable）」的 known vulnerability → `exit 1` fail。govulncheck 預設只報可達者，故門檻即「有任一可達漏洞」。不可用 severity 放寬（govulncheck 不分級；可達即阻擋）。 |
| 容器 image（app）| Trivy | `deploy-app.yml`（既有） | 既有觀察期 `exit-code=0`；升級條件屬 `build-pipeline-and-callback` § 7，本 spec 不改其節奏。 |
| 容器 image（0ops server / migrations）| Trivy | **`release.yml` images job 內，push 前** | v1 `HIGH,CRITICAL` 且 `ignore-unfixed=true`，**`exit-code=1`（直接強制）**。理由：0ops 自身 image 數量少、由內部控制、無 app 那種「客戶 repo 既存 CVE」噪音，可一開始即強制；與 app image 觀察期策略不同為刻意。 |

> govulncheck 置於 `ci.yml` 而非 release：依賴漏洞應在 PR 階段攔截，不等到 tag release。release.yml 的 Trivy 補的是「最終 image OS / 系統套件層」CVE，與 govulncheck 的「Go 原始碼可達漏洞」互補不重疊。

### 4.3 Image provenance（SLSA）

**取捨：L2 vs L3**

| 等級 | 要求 | 0ops 可達性 |
|---|---|---|
| **L2** | 簽章過的 provenance、由託管 build service 生成、來源版本受控 | **v1 目標、可立即達成**。GitHub Actions 為 build service；provenance 由 GitHub OIDC 身份簽署（綁 workflow，非 runner）。 |
| **L3** | 不可偽造的 provenance、build 環境隔離且 ephemeral、build 無法存取簽章材料 | **gated；v1 不宣稱達成**。`slsa-github-generator` 官方僅支援 **GitHub-hosted runner**；0ops app build 走 **self-hosted runner**（`self-hosted-runner` feature、`threat-model` SC2 既有緩解），其環境非由可信控制平面保證 ephemeral 隔離，且 runner 若被入侵可能觸及 OIDC token 簽署流程，無法滿足 L3 的不可偽造性。 |

**建議**：
- **app image**：v1 採 **L2**——provenance 述詞記錄 `builder.id`、`buildType`、`invocation`（repository_dispatch payload 之 `run_id` / `commit_sha` / `image_ref`）、materials（source repo digest + builder image digest）。self-hosted runner 場景下，provenance 的可信度取決於 runner 隔離強度，**明示為殘餘風險**（§ 10）。
- **0ops server / migrations image**：`release.yml` 在 GitHub-hosted runner 上跑，**可達 L3**；建議直接以 `actions/attest-build-provenance`（GitHub-hosted OIDC，non-falsifiable）生成 L3 provenance。即「自身 image 標準高於 app image」。
- **L3 for app image** 列 future：待 self-hosted runner 改為 ephemeral 隔離（每 build 全新 VM + 控制平面注入 OIDC）後重評。

**儲存**：provenance 以 cosign attestation（`predicateType=https://slsa.dev/provenance/v1`）附到 image digest，與 SBOM 並存於 OCI referrer。

> **決策點 → ADR-0017**：app image 採 L2 且接受 self-hosted runner 殘餘風險、自身 image 採 L3；provenance generator 選型（`slsa-github-generator` vs `actions/attest-build-provenance`）。此為跨 release 流程與信任聲明的架構決策，須先寫 ADR 再實作（`plan.md` § 5.1 列「視是否改 release 流程」；本 spec 判定**需改 release 流程故需 ADR**）。

### 4.4 Image 簽章與驗證（解 SC3）

| 項目 | 決策 |
|---|---|
| 簽章方式 | **cosign keyless（OIDC）**。理由：keyless 用 GitHub OIDC token 向 Fulcio 換短期憑證、簽章記入 Rekor 透明日誌，**無長期私鑰需保管 / 輪替**；身份綁 workflow identity（`repo` + workflow path + ref），即使 self-hosted runner 也由 GitHub 簽發 OIDC token，故 runner 位置不影響簽章身份的可驗證性。key-based 為退路（自架 Sigstore 未就緒前的離線場景），其私鑰納入 § 8 輪替範圍。 |
| 簽章對象 | image digest（sign）+ SBOM attestation + provenance attestation（attest）。 |
| 透明日誌 | v1 用**公開 Sigstore（Fulcio + Rekor）**；自架 Sigstore 列 future（§ 2.2）。 |
| 驗證點 | **K3s admission**：sigstore **policy-controller**（或 Kyverno `verifyImages`）對 `team-*` 與 `system-0ops` namespace 強制：image 必須 (a) 有 cosign 簽章且 identity 匹配預期 workflow、(b) 有 CycloneDX SBOM attestation、(c) 有 SLSA provenance attestation 且 `builder.id` 匹配。任一不符 → admission 拒絕（deny），workload 不 schedule。 |
| 與既有 digest pin 關係 | cosign 驗的是 **digest** 上的簽章；故 gitops manifest 必須 pin `@sha256:<digest>` 而非 mutable tag，否則「驗證的 digest」與「ArgoCD 拉取的 tag→digest 解析結果」可能不一致，留下 SC3 的替換窗口。**本 spec 把 gitops 由 commit_sha tag pin 改為 digest pin（§ 12）為 SC3 緩解的硬前提。** |

### 4.5 信任鏈（端到端）

```
source repo (commit_sha) ─pack build──▶ image digest
                                          ├─ syft ──▶ CycloneDX SBOM ─cosign attest─┐
                                          ├─ trivy ─▶ scan（門檻見 § 4.2）           │ 附到 digest
                                          └─ slsa ──▶ provenance ───cosign attest───┘
image digest ─cosign sign（keyless OIDC）─▶ Rekor 透明日誌
callback(image digest) ─▶ backend 寫 deploy_run.image_digest
gitops render：image: <repo>@sha256:<digest>（digest pin，非 tag）─push─▶ ArgoCD sync
K3s admission（policy-controller）：驗簽 + SBOM + provenance ─不符→deny─▶ schedule
```

## 5. K3s admission 驗簽 policy（設計層）

- **載體**：sigstore policy-controller 之 `ClusterImagePolicy`（或 Kyverno `ClusterPolicy` `verifyImages`），由 ArgoCD 以 `deploy/gitops/argocd/apps/policy-controller.yaml` 管理（與既有 root-app 一致，policy 本身亦走 GitOps 唯一真相）。
- **匹配範圍**：`ghcr.io/<owner>/0ops-server`、`0ops-migrations`、`ghcr.io/.../<team>-<app>`（app image glob）。
- **驗證述詞**：
  - keyless identity：`issuer=https://token.actions.githubusercontent.com`、`subject` 匹配 0ops repo 的 release.yml / deploy-app.yml workflow。
  - attestation：要求 CycloneDX SBOM + SLSA provenance 存在且簽章有效。
- **失效模式**：policy `mode: enforce`（拒絕）；**啟用前先 `mode: warn` 觀察一輪**（避免首次上線把既有未簽 image 全擋導致 cluster 不可用）。warn→enforce 切換為一次性 PR，類比 Trivy 觀察期升級。
- **例外**：基礎設施 image（ArgoCD / Traefik / postgres 等第三方 upstream）走 namespace selector 排除或獨立 policy（驗 upstream 簽章），不在 0ops 自簽範圍。

## 6. SBOM ↔ deploy_run 關聯（backend 端唯一新增邏輯）

- callback payload（`build-pipeline-and-callback` § 6.3）之 `image` 欄位 **v1 為 tag**；本 spec 要求 callback 額外帶 **`image_digest`**（`sha256:...`），由 GHA `docker buildx` / `pack` output 取（push 後 registry 回 digest）。
- backend callback handler 把 `image_digest` 寫入 `deploy_run.image_digest`（新欄位，migration）。
- 審計 / 取證查詢：`deploy_run.image_digest` → `cosign download attestation <repo>@<digest>` 取回 SBOM 與 provenance。SBOM 本體不入 DB（避免主表膨脹），只存 digest 指標。
- gitops render（§ 12）以同一 `image_digest` 寫 manifest，確保 deploy_run 記錄、被驗證 digest、被部署 digest 三者一致。

## 7. GitOps repo 防護（解 SC1）

### 7.1 既有（重申，非本 spec 引入）
- backend 為唯一 writer；hand-edit 為違反操作（`gitops-render-and-argocd` § 14 規則 2）。
- branch protection：required signature、no force-push、reviews=0、CODEOWNERS（`@ops-bot`）。
- commit 用 Ed25519 SSH key 簽章（GitHub allowed signers）。

### 7.2 本 spec 引入的強化
| 強化 | 設計 |
|---|---|
| Write 權限收斂 | `0ops-gitops` 只掛**單一 repo-scoped deploy key**（write），對應 private key 存單一 K8s Secret（`gitops-deploy-key`，`secrets-management` 管）；禁止個人 PAT / org-wide token 對該 repo 有 write。deploy key 為 repo-scoped，天然無法跨 repo（`gitops-render-and-argocd` § 10 驗證項已涵蓋）。 |
| Branch protection 強化 | 明文要求：`main` 啟用 required signature + **disallow force-push** + **disallow deletion** + linear history；bypass list 為空（無人可繞過，含 admin）。 |
| Commit 來源限定 | 合法 commit 必須 (a) author = `ops-bot <ops-bot@jesontech.com>`、(b) 由 ops-bot 的 Ed25519 allowed signer 簽署、(c) 第一行符合 `<action>: <team>/<app> @ <deploy_run_id>` contract（`gitops-render-and-argocd` § 5.2）。 |
| 非 backend commit 偵測 | reconciler 週期（或 GitHub push webhook 觸發）掃 `main` 新 commit：凡簽章 signer ≠ ops-bot key、或 author ≠ ops-bot、或無對應 `deploy_run_id` 的 commit → 標記為 `gitops_unauthorized_commit`，寫 `audit_log`（`source=system`、`action=gitops_unauthorized_commit`、`outcome=failure`），並觸發告警（`slo-and-alerting`）。偵測為 detective control（branch protection 為 preventive，兩層）。 |

## 8. Callback / webhook 簽章金鑰輪替（接 AU4 / AU5）

> 補既有簽章機制的**輪替流程文件化**；簽章機制本身已 ship，本節不改驗章邏輯。

| Secret | 既有狀態 | 本 spec 文件化的輪替流程 |
|---|---|---|
| `OPS_TOKEN_SIGNING_SECRET`（callback 主簽章 key）| 已定 90 天輪替、雙 window 30 分鐘（`build-pipeline-and-callback` § 5.4） | runbook 落地：產新 key → K8s Secret 加第二 key version → 等 30 分鐘雙驗窗（涵蓋在途 ops_token，TTL ≤ 20 min）→ 移除舊 key → 寫 `audit_log`（`secret_rotate_start/finalize`）。 |
| `OPS_CALLBACK_SECRET`（emergency fallback）| 90 天輪替 | 同上雙 window；v2 移除後本條退役。 |
| **GitHub webhook secret**（AU4，假 push 偽造）| **既有：HMAC 驗章已 ship；輪替流程未文件化（threat-model AU4 缺口）** | **本 spec 補**：GitHub repo webhook 支援多 secret 並存有限，故採「短雙窗」：在 GitHub 端更新 webhook secret 前，backend 先進入「新舊雙 secret 並驗」模式（30 分鐘）→ GitHub 端換 secret → 移除舊 secret。輪替寫 `audit_log`。週期 90 天，與 callback 對齊。 |
| cosign key-based 退路私鑰（若 § 4.4 退路啟用）| 本 spec 引入 | 納入同一 runbook；keyless 為主時無此 key（無輪替負擔）。 |

- 統一 runbook：`docs/runbooks/signing-key-rotation.md`，列每把 key 的雙窗時長、輪替指令、audit 事件、驗證步驟。
- 輪替期間「新舊雙驗」為硬性：任何時點拒收的合法請求數必為 0（驗證項見 § 11）。

## 9. 失敗點與 CI 行為對應

| 階段 | 工具 | 失敗→行為 |
|---|---|---|
| Go 依賴掃描 | govulncheck | CI fail；PR 不可合入 |
| 0ops image 掃描 | Trivy（`exit-code=1`）| release `images` job fail；image 不 push；tag release 中止 |
| app image 掃描 | Trivy（觀察期 `exit-code=0`）| 不阻擋（既有，`build-pipeline-and-callback` § 7）|
| SBOM 生成失敗 | syft | release / deploy-app job fail（SBOM 為硬產出，缺則視同 build 失敗）|
| 簽章 / attest 失敗 | cosign | job fail；未簽 image 不得進 gitops（即使已 push 也因 admission `enforce` 被擋）|
| admission 驗簽失敗 | policy-controller | `enforce`：workload deny → `deploy_run` 標 `failed`（classification `image_policy_denied`，列舉接 `reconciler-and-incident`）；`warn`：放行 + 告警 |
| 非 backend commit | reconciler | `audit_log` + 告警；不自動 revert（v1 偵測為主，revert 列 Open issue）|

## 10. 殘餘風險與明示接受

| 殘餘風險 | 為何接受（v1） | 重新評估觸發 |
|---|---|---|
| app image 僅 SLSA L2（self-hosted runner 非 ephemeral）| L2 簽章 provenance 已遠優於無；L3 受限於 runner 隔離強度，屬基礎設施改造 | runner 改 ephemeral 隔離 / SOC2 要求 L3 |
| v1 用公開 Sigstore（Fulcio / Rekor）| 公開透明日誌可驗、零維運；自架為主權 / 離線需求 | enterprise design partner 要求資料主權 / 離線 |
| admission 初期 `warn` 不阻擋 | 避免首次上線擋住既有未簽 image 致 cluster 不可用 | 觀察一輪後一次性切 `enforce` |
| 非 backend commit 僅偵測不自動 revert | 自動 revert 可能與正常 push 競態；偵測 + 告警 + 人工處置先行 | 出現實際惡意 commit 事件 |
| keyless 依賴 GitHub OIDC 可用性 | OIDC 由 GitHub 維護；簽章為 build-time，registry 端離線可驗 | GitHub OIDC 重大事故 |

## 11. 驗證準則

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| 未簽 image 被拒 | 部署一個無 cosign 簽章的 image 至 `team-*` namespace（policy `enforce`）| admission deny；workload 不 schedule；`deploy_run` 標 `image_policy_denied` |
| 已簽 image 放行 | 部署正常 build 出的已簽 image | admission 放行；workload 起 |
| SBOM 隨 image 產生 | release / deploy-app build 後 | `cosign download attestation <repo>@<digest>` 取得 CycloneDX SBOM；predicateType 正確 |
| SBOM ↔ deploy_run 關聯 | 一次 create_app / redeploy | `deploy_run.image_digest` 非空；以該 digest 可取回 SBOM |
| govulncheck CI 攔截 | PR 引入一個有已知可達漏洞的依賴 | `ci.yml` fail；PR 不可合入 |
| govulncheck 不誤擋不可達 | 依賴含漏洞但 call-graph 不可達 | govulncheck pass（驗證「可達才擋」語意）|
| 0ops image 掃描強制 | release 時 image 含 HIGH/CRITICAL（fixed）CVE | `images` job fail；image 不 push |
| provenance 產生（自身 image L3）| release.yml | `cosign verify-attestation` provenance 有效；`builder.id` 為 GitHub-hosted |
| provenance 產生（app image L2）| deploy-app.yml | provenance attestation 存在且簽章有效 |
| gitops digest pin | 任一 render | manifest `image:` 為 `<repo>@sha256:...`，非 mutable tag |
| 被驗 digest == 被部署 digest | 端到端一次 deploy | deploy_run.image_digest == gitops manifest digest == admission 驗證 digest |
| 非 backend commit 被偵測 | 用非 ops-bot key 對 gitops push 一筆 commit（測試環境）| reconciler 標 `gitops_unauthorized_commit`；`audit_log` 有 row；告警觸發 |
| branch protection 強化 | 嘗試 force-push / 刪 main / admin bypass | 全部被 GitHub 拒 |
| 簽章金鑰輪替零中斷 | 跑一次 webhook / callback secret 輪替 | 雙窗期間合法請求拒收數 = 0；舊 key 移除後新 key 正常驗 |
| admission warn→enforce | 切換 policy mode | warn 期未簽 image 放行 + 告警；enforce 後同 image 被拒 |
| attestation 防篡改 | 改 SBOM 內容後重附但不重簽 | `cosign verify-attestation` 失敗 |

## 12. 與其他 spec 接合（含跨 spec 不一致修正）

| 接合 | spec | 說明 |
|---|---|---|
| app image build / Trivy 掃描既有路徑 | `build-pipeline-and-callback` § 4 / § 7 | 本 spec 在其 Trivy step 後插入 syft + cosign；callback 補帶 `image_digest` |
| 0ops server / migrations image release | `.github/workflows/release.yml` images job | 本 spec 補 syft + Trivy(enforce) + provenance(L3) + cosign |
| CLI / MCP binary | `goreleaser`（ADR-0010 cli-distribution）| 本 spec 啟用 goreleaser 內建 sbom + binary 簽章；無 cli image |
| gitops 唯一真相 / backend 唯一 writer / branch protection | `gitops-render-and-argocd` § 5.3 / § 14 | 本 spec § 7 強化收斂 + 偵測；**§ 4.4 / § 12 要求 digest pin** |
| trace_id 跨界 | `observability-skeleton` § 6 | image_digest 隨 callback 與既有 trace_id 同行 |
| audit 寫入（unauthorized commit / secret rotate）| `audit-log` § 5.1 | 新增 `gitops_unauthorized_commit`；`secret_rotate_*` 既有 |
| secret 輪替本體 | `secrets-management` § 9 | 本 spec § 8 文件化「流程」，key 管理屬 secrets-management |
| 失敗分類 `image_policy_denied` | `reconciler-and-incident` | 新增列舉 |
| 威脅來源（SC1/SC2/SC3）| `threat-model` § 5.4 / § 6 | 本 spec 為其指定下游緩解 spec |
| 控制對應矩陣消費本 spec | `compliance-framework-mapping`（待寫）| SBOM / provenance / 簽章為 SOC2 供應鏈控制證據 |

### 12.1 跨 spec 不一致（本 spec 發現並修正）

> **SC3 既有緩解描述與實作不符**：`threat-model` § 5.4 SC3 既有緩解寫「callback 帶 digest；**GitOps pin digest**」，但 `gitops-render-and-argocd` § 4.3 deployment 模板實際為 `image: ghcr.io/winshare/<team>-<app>:<commit_sha>`（mutable **tag**），`build-pipeline-and-callback` § 6.3 callback `image` 範例亦為 `...:abc123`（tag）。即**目前 pin 的是 commit_sha tag，非不可變 sha256 digest**——tag 可被重新指向不同 digest，SC3 的替換窗口實際仍開。
>
> **本 spec 修正**：(a) callback 補帶 `image_digest`（§ 6）；(b) gitops render 改用 `<repo>@sha256:<digest>` pin（§ 4.4、§ 3 `render-and-push-gitops.sh` 變更）；(c) admission 驗的 digest 即 gitops pin 的 digest。**此修正需同步回填 `threat-model` SC3 既有緩解措辭（改為「規劃中：digest pin + 簽章驗證」）與 `gitops-render-and-argocd` § 4.3 模板**，避免威脅模型宣稱未實作的緩解（違反 `threat-model` § 11 規則 1 / `plan.md` § 6 規則 1）。本 spec 不直接改該兩檔，列為合入前置依賴。

## 13. 不可違反的硬性規則

> 違反以下任一項，PR 不可合入。

1. 本 spec 引入的能力（SBOM / govulncheck / provenance / cosign / admission 驗簽 / digest pin / 非 backend commit 偵測）一律標「規劃中」，不得在任何對外信任聲明中講成已具備（承 `plan.md` § 6 規則 1、`threat-model` § 11 規則 1）。
2. 所有受控 image（app + `0ops-server` + `0ops-migrations`）必生成 CycloneDX SBOM 並以 cosign attestation 附到 image **digest**；缺 SBOM 視同 build 失敗。
3. `govulncheck` 為 `ci.yml` 必過 gate；偵測到可達 known vulnerability 即 fail，不得以 severity 或 allowlist 靜默放寬（個案豁免須走 ADR + 註記）。
4. 0ops 自身 image 的 Trivy 掃描為 `exit-code=1`（強制），不走 app image 的觀察期模式。
5. image 簽章採 cosign keyless（GitHub OIDC）為主；K3s admission policy 對 `team-*` 與 `system-0ops` namespace 最終必為 `enforce`，拒絕未簽 / 缺 attestation 的 image（首輪 `warn` 觀察為唯一例外，切 `enforce` 須一次性 PR）。
6. gitops manifest 必以 `<repo>@sha256:<digest>` 即不可變 digest pin，不得用 mutable tag；「被驗證 digest == 被部署 digest == deploy_run.image_digest」三者必一致（SC3 緩解硬前提）。
7. `0ops-gitops` 之 write 收斂為單一 repo-scoped deploy key；branch protection 必含 required signature + no force-push + no deletion，bypass list 為空（含 admin 不可繞過）。
8. 非 backend commit（signer ≠ ops-bot / author ≠ ops-bot / 無對應 deploy_run_id）必被偵測並寫 `audit_log` + 告警；偵測為 detective control，不得移除（與 branch protection preventive control 並存）。
9. 簽章金鑰輪替（callback / webhook / cosign key-based 退路）必走「新舊雙窗並驗」，輪替期間合法請求拒收數必為 0，且寫 `audit_log`。
10. 涉及 release 流程變更與信任聲明的 provenance / 簽章策略（L2 vs L3、generator 選型、keyless vs key-based）必先寫 **ADR-0017** 再實作（承 `plan.md` § 6 規則 6）。
