---
adr: "0017"
title: Supply-Chain Signing and Provenance
status: Proposed
date: 2026-06-28
tags:
  - supply-chain
  - security
  - ci
  - provenance
  - signing
supersedes: []
superseded-by: []
---

# ADR-0017：Supply-Chain Signing and Provenance

* Status：Proposed（對應 spec 為 draft；尚未實作。承 `docs/trust-and-compliance/plan.md` § 6 規則 1，狀態誠實標示）
* Date：2026-06-28
* 適用範圍：image SBOM / 依賴掃描 / SLSA provenance / cosign 簽章 / 部署端驗簽 / gitops digest pin 之決策邊界；不含營運排程（屬 runbook）
* 來源：`docs/trust-and-compliance/plan.md` § 5.1（P1）；`docs/features/threat-model/spec.md` § 5.4（SC1/SC2(H)/SC3）；對應 spec [`docs/features/supply-chain-security/spec.md`](../features/supply-chain-security/spec.md)
* 上游依賴：[ADR-0005](0005-build-pipeline-and-callback.md)（GHA + CNB build、HMAC callback）；[ADR-0004](0004-k3s-role-and-orchestrator.md)（K3s admission 載體）；[ADR-0010](0010-cli-distribution.md)（goreleaser）；既有 [`docs/features/gitops-render-and-argocd/spec.md`](../features/gitops-render-and-argocd/spec.md)（gitops 唯一真相）、[`docs/features/build-pipeline-and-callback/spec.md`](../features/build-pipeline-and-callback/spec.md)

> **編號說明**：ADR-0014 已由 `tasks/todo.md` Q3 預留給「OCI artifact registry」（v1 不採、條件觸發）；本 ADR 接 0015（audit）、0016（SSO）之後取 0017，不佔用 0014 預留位。

## 0. TL;DR（先讀本段）

採用以下六項組合決策：

1. **SBOM 格式 CycloneDX**：對 app image、`0ops-server`/`0ops-migrations` image、CLI/MCP binary 以 syft 生成 CycloneDX SBOM，以 cosign attestation 附到 image **digest**（binary SBOM 隨 release artifact）。SPDX 為退路，v1 不雙發。
2. **依賴掃描雙面**：Go 原始碼 `govulncheck` 進 `ci.yml`（PR + main，偵測**可達**漏洞即 fail）；image OS/套件層 Trivy——app image 沿用既有觀察期，0ops 自身 image 一開始即 `exit-code=1` 強制。
3. **SLSA provenance 分級**：app image v1 採 **L2**（self-hosted runner 非 ephemeral，明示殘餘風險，L3 列 gated future）；`0ops-server`/`migrations` image 在 GitHub-hosted runner 上跑、採 **L3**（`actions/attest-build-provenance`）。自身 image 標準高於 app image。
4. **簽章 cosign keyless（GitHub OIDC + Fulcio + Rekor）**：無長期私鑰需保管/輪替；身份綁 workflow identity，self-hosted runner 不影響可驗證性。key-based 為離線退路、其私鑰納入輪替範圍。
5. **部署端為信任閘門終點**：K3s admission（sigstore policy-controller 或 Kyverno `verifyImages`）對 `team-*` 與 `system-0ops` namespace 強制驗簽 + SBOM + provenance；首輪 `warn` 觀察、一次性 PR 切 `enforce`。
6. **gitops 由 tag pin 轉 digest pin**：gitops manifest 必 pin `<repo>@sha256:<digest>`，使「被驗證 digest == 被部署 digest == `deploy_run.image_digest`」三者一致——此為 SC3 緩解硬前提（修正既有 mutable `:<commit_sha>` tag pin）。

行為與 CI/工具細節以 spec [`docs/features/supply-chain-security/spec.md`](../features/supply-chain-security/spec.md) 為準；本 ADR 釘住決策邊界。

## 1. Context and Problem Statement

威脅模型 § 5.4 列三條供應鏈威脅：SC1（gitops 惡意 commit 繞過 backend）、**SC2（H，依賴/build 污染植入後門 image）**、SC3（image 替換）。現況：

- **SC2**：app image 僅有 self-hosted runner 隔離 + Trivy 觀察期掃描；**無 SBOM、無 provenance、無 Go 依賴掃描、無 0ops 自身 image 掃描**——後門植入無可視性、無來源證明。
- **SC3**：callback 帶 digest，但 `gitops-render-and-argocd` § 4.3 實際以 **mutable `:<commit_sha>` tag** pin（非 `@sha256` digest）；tag 可被重指向，替換窗口仍開。威脅模型原宣稱「GitOps pin digest」為**未實作的緩解**（已回填修正）。
- **SC1**：backend 為唯一 writer + branch protection + Ed25519 commit signing 已具備，但 write 權限收斂與非 backend commit 偵測未文件化。

需要一組決策，把 build→registry→gitops→K3s 的信任鏈端到端閉合，且明確區分「已具備」與「規劃中」，不得宣稱未實作的緩解。

## 2. Decision Drivers

* **DD1 可視性與來源證明**：必須能回答「這個 running image 由哪個 commit、哪條 workflow、用哪些依賴 build 出來」——SBOM + provenance。
* **DD2 部署端強制驗證**：信任閘門須在 K3s admission 終點強制，而非僅靠 CI 自律；未簽/未通過述詞的 image 不得 schedule。
* **DD3 digest 為信任錨**：簽章/attestation 綁 digest；故部署端 pin 必為不可變 digest，否則驗證與部署脫鉤（SC3）。
* **DD4 誠實分級**：self-hosted runner 無法保證 L3 不可偽造性；不得宣稱 app image 達 L3。自身 image 在 hosted runner 可達 L3 則採之。
* **DD5 最小私鑰負擔**：優先 keyless（無長期私鑰），降低金鑰保管/輪替攻擊面。
* **DD6 漸進上線不致 cluster 不可用**：admission enforce 須先 warn 觀察，避免一次擋光既有未簽 image。

## 3. Decision Outcome

### 3.1 SBOM 與掃描
* SBOM：CycloneDX（syft），cosign attestation 附 digest。涵蓋 app image、自身 image、CLI/MCP binary。
* `govulncheck`：`ci.yml` 必過 gate，可達 known vulnerability 即 fail，不得以 severity / allowlist 靜默放寬（個案豁免走 ADR + 註記）。
* Trivy：app image 沿用 `build-pipeline-and-callback` § 7 觀察期節奏（本 ADR 不改）；自身 image `HIGH,CRITICAL` + `ignore-unfixed` + `exit-code=1`。

### 3.2 Provenance 等級
* app image：SLSA **L2**；self-hosted runner 隔離強度決定可信度，**明示殘餘風險**；L3 列 future（待 runner 改 ephemeral）。
* 自身 image：SLSA **L3**（`actions/attest-build-provenance`，GitHub-hosted OIDC，non-falsifiable）。
* provenance 以 cosign attestation（`predicateType=slsa.dev/provenance/v1`）附 digest。

### 3.3 簽章與驗證
* cosign **keyless**（GitHub OIDC → Fulcio 短期憑證 → Rekor 透明日誌）為主；key-based 退路私鑰納入 § 簽章金鑰輪替。
* K3s admission（policy-controller / Kyverno）對 `team-*` + `system-0ops` 強制：cosign 簽章 identity 匹配預期 workflow + CycloneDX SBOM + SLSA provenance 存在。`mode: warn` 觀察一輪 → 一次性 PR 切 `enforce`。第三方 upstream image 走獨立 policy / namespace 排除。

### 3.4 gitops digest pin（SC3 緩解硬前提）
* callback 補帶 `image_digest`，backend 寫 `deploy_run.image_digest`（新欄位）。
* gitops render 改 `<repo>@sha256:<digest>` pin（取代既有 `:<commit_sha>` tag）。
* admission 驗的 digest == gitops pin 的 digest == `deploy_run.image_digest`。

### 3.5 gitops 防護（SC1）
* write 收斂為單一 repo-scoped deploy key；branch protection required signature + no force-push + no deletion + bypass list 空。
* 非 backend commit（signer ≠ ops-bot / author ≠ ops-bot / 無對應 deploy_run_id）由 reconciler 偵測 → `audit_log`（`gitops_unauthorized_commit`）+ 告警（detective control，與 branch protection preventive 並存）。

## 4. Pros and Cons of the Options

| 決策點 | 採用 | 否決選項 |
|---|---|---|
| SBOM 格式 | CycloneDX（VEX 支援佳、工具鏈原生） | SPDX（授權合規場景退路） |
| provenance generator | app=GHA OIDC L2、自身=`actions/attest-build-provenance` L3 | `slsa-github-generator`（僅支援 GitHub-hosted，app 之 self-hosted 不適用） |
| 簽章 | cosign keyless | key-based（私鑰保管/輪替負擔，列退路） |
| 驗證點 | K3s admission enforce | 僅 CI 自律（無部署端強制，違反 DD2） |
| image pin | digest | mutable tag（SC3 窗口，否決） |

### keyless vs key-based
keyless 無長期私鑰、身份可由 Rekor 公開驗證、self-hosted runner 仍由 GitHub 簽 OIDC token；缺點是依賴公開 Sigstore 可用性與 GitHub OIDC（build-time，registry 端離線仍可驗）。key-based 可離線/自主但引入私鑰生命週期，僅作退路。

### L2 vs L3（app image）
L3 要求 build 環境 ephemeral 隔離且不可觸及簽章材料；0ops app build 走 self-hosted runner，無法保證，故 app 採 L2 並明示殘餘風險。強行宣稱 L3 違反 DD4（誠實分級）。

## 5. Consequences

### 5.1 正面
* SC2 從「無可視性」升為 SBOM + provenance + 雙面掃描 + 部署端驗簽；後門 image 無法 schedule。
* SC3 關閉：digest pin + 簽章驗證使 tag 重指向失效。
* 提供 SOC2 CC7/CC8（變更管理、系統運作）與供應鏈控制證據（接 `compliance-framework-mapping`）。

### 5.2 負面
* CI 複雜度上升：syft / cosign / provenance / Trivy 多 step；release 與 deploy-app workflow 變更面大。
* admission policy 為 cluster 級新元件；warn→enforce 切換需謹慎，初期誤擋風險（故先 warn）。
* app image 僅 L2；self-hosted runner 被入侵仍可能影響 provenance 可信度（殘餘風險，列 Revisit）。
* gitops digest pin 改動 render 流程與既有模板（需同步回填 `gitops-render-and-argocd` § 4.3）。

### 5.3 中性
* 公開 Sigstore（Fulcio/Rekor）v1 採用；自架列 future。
* 非 backend commit v1 偵測為主，不自動 revert（列 Open Question）。

## 6. Revisit Triggers

* **self-hosted runner 改 ephemeral**：app image 重評 L3。
* **資料主權 / 離線需求**：enterprise 要求自架 Sigstore 或離線簽章 → 啟用 key-based + 自架 Fulcio/Rekor。
* **惡意 commit 實例**：非 backend commit 出現實際攻擊 → 評估自動 revert。
* **SOC2 audit 要求 L3 全鏈**：app image L2 不被接受時重新評估 runner 架構。

## 7. More Information

* **Feature spec**：[`docs/features/supply-chain-security/spec.md`](../features/supply-chain-security/spec.md)（CI 變更點、驗證準則、信任鏈圖以本檔為準）
* **威脅模型**：[`docs/features/threat-model/spec.md`](../features/threat-model/spec.md) § 5.4 SC1/SC2/SC3
* **統籌計畫**：[`docs/trust-and-compliance/plan.md`](../trust-and-compliance/plan.md) § 5.1
* **ADR-0005**：[0005-build-pipeline-and-callback.md](0005-build-pipeline-and-callback.md)（build + callback；本 ADR 補 SBOM/簽章/digest）
* **gitops spec**：[`docs/features/gitops-render-and-argocd/spec.md`](../features/gitops-render-and-argocd/spec.md)（§ 4.3 模板須由 tag pin 改 digest pin）

## 8. Open Questions

1. **policy-controller vs Kyverno**：admission 驗簽載體選型；依既有 cluster add-on 與維運熟悉度定（spec 釘介面、ADR 不鎖實作）。
2. **app image L3 路徑**：self-hosted runner ephemeral 隔離（每 build 全新 VM + 控制平面注入 OIDC）之成本與時程。
3. **非 backend commit 自動 revert**：偵測後是否自動回退；v1 偵測 + 告警 + 人工，revert 與正常 push 競態風險待評估。
4. **自架 Sigstore**：資料主權需求浮現時的 Fulcio/Rekor 自架拓樸。
5. **goreleaser binary 簽章驗證面**：CLI binary 簽章後，end-user 安裝端（`install.sh`）是否驗簽、如何分發信任根。
