# 0ops 商業計畫書

**版本**：v1.0（草案）
**日期**：2026-05-09
**對應技術規劃**：`docs/0ops-plan.md`
**準備對象**：創辦人團隊、種子輪投資人、早期合作客戶

---

## 一、執行摘要（Executive Summary）

### 一句話定位
**0ops 是 AI coding agent 原生用來「出貨」的工具** —— Claude Code、Codex 等 agent 把程式寫完後，原生呼叫 0ops，把「給定 GitHub repo 與域名 → 5 分鐘部署上 production」這一步，以同一組安全 API 同時開放給人類 CLI 與 AI agent 操作。agent 工具帶裡有 read / edit / run，0ops 補上缺的那一格：`ship`。

> MCP 是 agent 接上 0ops 的**機制（how）**，不是 0ops 的**身份（who）**。我們的識別綁在「agent 出貨時呼叫的那隻手」這個角色上，而非任一協定 —— 即使 MCP 被在位者商品化，這個角色不變。「原生」指 agent 把 0ops 當一級工具呼叫（與 Read / Bash 同等地位、走 preview→confirm），不指 0ops 被 Anthropic / OpenAI 內建。

### 為何此刻啟動
1. AI Coding CLI（claude code、codex、Copilot CLI）正取代 Web Console 成為新的主要操作介面，但市面上幾乎無 PaaS 為其原生設計
2. 既有 PaaS（Vercel / Railway / Render / Heroku / Zeabur）皆以 Web UI 為核心，AI agent 操作仰賴 web scraping 或非官方 API，安全與可審計性差
3. AI agent 缺一個原生、可稽核、能碰 production 的「出貨」工具——現有 PaaS 只有 Web UI，agent 得靠 web scraping 或裸 API key；先成為 agent 部署時預設呼叫的工具，即建立工作流習慣黏性（MCP 是當前跨家接入的機制，是先發鑰匙，非護城河本身）
4. Cloud Native Buildpacks 與 GitOps（ArgoCD）成熟，無 Dockerfile 自動化部署的技術門檻顯著降低

### 核心差異化
| 維度 | 0ops | Vercel / Railway / Render | 自建 K8s + GitOps |
|---|---|---|---|
| agent 原生出貨工具 | **是**（agent 工具帶裡的一級 `ship`，原生呼叫） | 否（僅 Web UI / REST） | 否 |
| 寫入安全模型 | **兩階段 preview → confirm** | 單階段、即時生效 | 看實作 |
| agent 操作可稽核性 | **寫入路徑內建稽核**（每次 production 變更落 audit_log + 回溯 Git commit） | 裸 API key / web scraping，稽核薄弱 | 自行建構 |
| 客戶自有域名 | CNAME + Cloudflare Tunnel 即時驗證 | 支援、流程繁瑣 | 自行建構 |
| 台灣本土運維 | **是**（繁中、台灣 zone、本地 SLA） | 海外為主 | N/A |
| 部署模式 | Self-host + Managed 雙軌 | 純 Managed | 純 Self-host |

### 商業模式速覽
- **Phase 1（2026 H1）**：完成技術驗證、內部試點準備、design partner discovery
- **Phase 2（2026 H2）**：限定範圍 dogfooding + 付費 Beta 準備
- **Phase 3（2027 起）**：付費 Beta、self-host 商業化、managed 雲驗證

### 募資需求（Initial）
- **種子輪目標**：USD 500K – 1M（NT$ 16M – 32M）
- **用途**：12–18 個月跑道，達成 Managed 服務 GA、首批 50 付費團隊、ARR USD 200K
- **里程碑對應**：M0–M5（v1 GA）+ M6 Web UI

---

## 二、問題（Problem）

### 目標客戶痛點

#### 痛點一：AI CLI 與 PaaS 之間的「最後一哩」斷層
工程師在 claude code / codex 內完成 90% 的開發，但「部署上線」仍需切到瀏覽器、登入 Vercel/Railway、貼設定、按 deploy。AI agent 無法以受信任的方式直接觸發 production-grade 部署：
- 直接讓 LLM 操作瀏覽器：脆弱、無稽核、不可重現
- 暴露 PaaS API key 給 agent：權限粒度過粗、副作用不可預測
- 自寫 wrapper：每個團隊重造輪子，缺乏跨工具一致性

#### 痛點二：自建 K8s + GitOps 學習曲線過高
有經驗的 SRE 團隊偏好 ArgoCD + Helm + GitOps，但對中小團隊或產品工程師而言：
- 需熟悉 K8s manifest、Helm template、ArgoCD CRD、Cloudflare API
- 客戶自有域名 + TLS + Tunnel 整合無現成 paved road
- 一個簡單 Next.js 部署可能要寫 5 個 YAML 檔

#### 痛點三：商業 PaaS 在台灣市場的不對等
- Vercel / Railway 計費以美金、無台幣發票、企業採購困難
- 主節點海外，跨境延遲與資料落地法規挑戰
- 客服與 SLA 為英文 / 海外時區
- 對「想用又怕鎖定」的台灣團隊缺乏 self-host 退路

#### 痛點四：寫入操作無安全網
多數 PaaS 一按按鈕就執行，沒有 dry-run 或副作用預覽。AI agent 操作放大此風險：一句模糊指令可能誤刪 production app。

### 痛點規模量化
- 全球 AI Coding 工具 DAU 估計 > 5M（Cursor + Copilot + Claude Code 合計），其中需要部署環節者 > 60%
- 台灣具備一定規模的軟體開發團隊（5 人以上）約 8,000–12,000 個
- 台灣對 self-host 友善 PaaS 的潛在需求量：估 1,500–3,000 團隊

---

## 三、解決方案（Solution）

### 產品全貌
0ops 是一個**單一 Go module、三 binary** 的系統：

1. **`0ops-server`**：純 IaaS REST/SSE backend，不跑 LLM
2. **`0ops`**：人類 CLI（cobra），互動式 preview/confirm
3. **`0ops-mcp`**：給 AI CLI 用的 stdio MCP server，工具一對一映射 backend API

### 核心工作流（v1）
```
任一介面：
  $ 0ops apps create nextdemo --repo=https://github.com/.../next.js-helloworld
  或
  claude code 內：「把 vercel/next.js-helloworld 接進來叫 nextdemo」
            ↓
  系統偵測 stack（Cloud Native Buildpacks）
            ↓
  preview：印出 action_summary + side_effects + 過期時間
            ↓
  使用者確認（CLI y/N；AI CLI 由 LLM 呈現給使用者）
            ↓
  GitHub Actions: pack build → image push GHCR
            ↓
  Render manifest → commit 到 0ops-gitops repo
            ↓
  ArgoCD 同步到 K3s
            ↓
  Cloudflare Tunnel 註冊 hostname
            ↓
  nextdemo.jesontech.com 上線（5 分鐘內）
```

### 為何此設計能贏
1. **安全模型先行**：兩階段 preview/confirm 是 backend 強制（write tool 沒有 preview_id 直接 4xx），AI agent 無法繞過
2. **Backend 不跑 LLM**：成本可預期、無 prompt injection 風險、agent 邏輯下放到使用者端 AI CLI（使用者付的 token，我們不墊）
3. **MCP 一份、三家通**：透過 SKILL 包（claude-code / codex / copilot）覆蓋三大 AI CLI 生態
4. **GitOps 為儲存唯一真相**：所有部署狀態可追溯到 Git commit，無黑盒
5. **Self-host 友善**：客戶可選擇自建（買 license）或我們託管，不鎖定

詳細技術設計參見 `docs/0ops-plan.md`。

---

## 四、市場（Market）

### TAM / SAM / SOM
| 層級 | 範圍 | 規模估計 | 推算依據 |
|---|---|---|---|
| TAM | 全球 PaaS / Application Platform | USD 164B（2024）→ 548B（2034） | Precedence Research |
| SAM | AI-native dev team + self-host friendly PaaS | USD 8B–12B | TAM × 5–7%（AI-native 滲透率假設） |
| SOM（3 年） | 台灣 + 部分東南亞華語團隊 | USD 5M–15M ARR | 1,500–3,000 團隊 × USD 100–500/月 |

### 市場時機（Why Now）
1. **MCP 標準化**（2025 H2 起）：claude code、codex、Copilot 三家逐步統一 tool 協定，先進入者建立 SKILL 約定的飛輪
2. **AI CLI 取代 GUI**：根據 GitHub 與 Anthropic 公開數據，AI CLI 使用者過去 12 個月成長 4–6 倍
3. **CNB（Cloud Native Buildpacks）成熟**：paketo 已穩定支援主流語言，無 Dockerfile 自動化部署技術風險低
4. **Cloudflare Tunnel 普及**：客戶自有域名 + 零信任 TLS 邊緣終止幾乎免費
5. **台灣 AI 政策**：本地 LLM、本地部署需求受政策驅動（資料落地、合規）

### 趨勢風險
- AI CLI 市場仍可能洗牌：claude code / codex 之外可能出現新王者（短期透過 SKILL 抽象層即可吸納）
- Vercel 等大廠可能推出官方 MCP server（時間窗口估 12–18 個月，需快速建立先發優勢）

---

## 五、競品分析（Competition）

### 直接競品
| 公司 | 核心優勢 | 我們的相對劣勢 | 我們的相對優勢 |
|---|---|---|---|
| **Vercel** | Next.js 生態、Edge network、品牌 | Edge / 全球 CDN 規模 | agent 原生出貨工具、Self-host、台灣在地 |
| **Railway** | 開發者體驗極佳、模板豐富 | UX 投入規模 | agent 原生出貨、兩階段安全模型 |
| **Render** | 多語言通吃、價格友善 | 知名度、模板 | agent 原生出貨、GitOps 透明 |
| **Heroku** | 老牌、企業客戶 | 企業基底 | 現代架構、AI 原生 |
| **Fly.io** | 全球 anycast、Postgres 整合 | 基礎建設規模 | AI CLI、繁中 |
| **Zeabur**（台灣） | 同樣繁中、本地團隊 | 既有客戶基礎 | agent 原生出貨工具、Self-host 路徑、Buildpack |
| **Sealos**（中國開源 PaaS） | 開源、k8s 原生 | 開源社群成熟度 | AI CLI、台灣合規、產品化 |

### 間接競品
- **自建 K8s + ArgoCD + Helm**：技術強團隊偏好；我們是 paved-road 替代
- **Coolify / Dokku（OSS PaaS）**：self-host 取向；我們加上 AI CLI、商業 managed 與台灣支援
- **Cursor / Devin 等 AI agent**：他們做開發、我們做部署；屬互補而非競爭

### 護城河（Moat）建構順序
1. **0–6 月**：成為 agent 出貨時預設呼叫的工具，搶下工作流習慣（MCP 接入是先發鑰匙，非護城河本身；身份綁角色不綁協定）
2. **6–12 月**：累積 buildpack adapter、客戶模板、failure mode 知識庫
3. **12–24 月**：Self-host license 與 managed cloud 雙軌客戶綁定
4. **24 月+**：開源社群與生態系、可替代的 backend implementation 由我們主導 reference

> **架構級信任為難以事後補上的 moat**：preview/confirm 後端強制、GitOps 唯一真相、backend 不跑 LLM、audit_log 業務帳本——這些在 agent 時代成為 enterprise 採購核心顧慮（「AI 對我的 production 做了什麼、能不能擋、能不能查」）的答案，皆內建於 0ops 的寫入路徑。Web-UI-first 競品要補上，需重構其安全模型而非加一個 feature。詳見 `docs/trust-and-compliance/plan.md`。

---

## 六、產品（Product）

### v1 範圍（M0–M5）
參見技術規劃 `docs/0ops-plan.md` 的 Milestones 表。下表是目前規劃狀態，不代表已完成或已驗證：

| 里程碑 | 目標 | 商業意義 |
|---|---|---|
| **M0** | 三 binary scaffold + dev env | 進入可執行實作，而非停留在文件設計 |
| **M1** | Read-only API + CLI + MCP | 建立第一條可驗證 vertical slice |
| **M2** | `create_app` 兩階段 + winshare 子網域 | 達成 dogfooding entry criteria |
| **M3** | 客戶自有域名 + DNS verify | 可啟動限定範圍 design partner 試點 |
| **M4** | Webhook 自動 redeploy + 手動 redeploy | 建立可重複的 CI/CD 體驗 |
| **M5** | `delete_app` + audit log + observability | 達成對外 Beta 前置條件 |
| **M6** | Web UI（Vue 3 + Vite + Tailwind + shadcn-vue） | 拓寬非 CLI 使用者 |

### v2 規劃（2027 起）
- 多服務 stack（compose / multi-service）
- 多分支 preview deployment
- 客戶自帶 TLS 憑證
- 配額 / 計量 / 帳單
- Cloudflare for SaaS 整合（自動為客戶域名簽發憑證）
- 開源 self-host community edition

### v3+ 願景
- Marketplace：第三方提供 buildpack / template / addon
- 多雲基底（GCP / AWS / Azure / 在地 IaaS 切換）
- AI-driven cost optimization（agent 監控成本並建議規格調整）

---

## 七、商業模式（Business Model）

### 收入結構
**Phase 1（內部 + Alpha）：無外部收入**
**Phase 2（Beta 付費）**：
- **Starter**：USD 19 / 月 / team（最多 3 apps、jesontech.com 子網域、社群支援）
- **Pro**：USD 99 / 月 / team（最多 20 apps、自有域名、email 支援）
- **Team**：USD 299 / 月 / team（unlimited apps、SSO、SLA、優先支援）

**Phase 3（Self-host License）**：
- **Community**（OSS）：免費、社群支援
- **Business**：USD 5,000–15,000 / 年 / 安裝（含商業支援、私人 buildpack registry）
- **Enterprise**：USD 30,000+ / 年（客製化、on-prem、合規與稽核）

> **Enterprise tier 信任承諾**（已具備 vs 規劃中，承 `docs/trust-and-compliance/plan.md`，不混淆已實作與路線圖）：
> - **已具備**：兩階段 preview/confirm、team RBAC + scope、audit_log 業務帳本（13 個月保留）、token argon2 雜湊 + 加密儲存、GitOps 全變更可回溯。
> - **規劃中**：SSO / OIDC 集中身分與撤權（ADR-0016）、audit export + tamper-evidence 取證交付（ADR-0015）、供應鏈 SBOM + 簽章驗證（ADR-0017）、SOC2 Type II 與台灣個資法控制對應、資料落地分區、DPA。
> 對外溝通時須以此狀態標示，不得把規劃中能力宣稱為已具備。

### 單位經濟（Unit Economics 假設）

| 指標 | 數值（保守） | 備註 |
|---|---|---|
| 月平均 ARPU | USD 80 | Pro 為主流方案 |
| Gross Margin | 70–80% | 主要成本：K3s 節點、Cloudflare bandwidth、GHCR storage |
| CAC（Beta 期） | USD 100–200 | 內容行銷、社群、AI CLI 文件 SEO |
| LTV（estimated 24 月） | USD 1,500–2,000 | 假設 30% 年留存衰減 |
| LTV / CAC | 8x–15x | Beta 期；GA 後預期降至 3–5x |
| Payback period | 2–4 月 | Beta 期 |

### 成本結構
- **基礎設施**（25–30%）：K3s 節點、Postgres、Cloudflare、GHCR、監控
- **人力**（55–65%）：工程 + 客服 + 行銷
- **業務開發 + Marketing**（5–10%）
- **法務 / 合規 / 帳務**（3–5%）

---

## 八、進入市場策略（Go-to-Market）

### 三階段 GTM

#### 階段一：技術驗證與試點準備（2026 H1）
- 完成 M0–M1，建立第一條可運行 vertical slice
- 定義 dogfooding entry criteria 並逐項驗證
- 選定 1 個內部低風險服務作為首個試點目標

#### 階段二：限定範圍 dogfooding 與 design partner 驗證（2026 H2）
- Winshare 內部首個服務進入限定範圍 dogfooding
- 與 3–5 家 design partner 驗證部署流程、權限模型、可審計性
- 在取得可重現案例後，再釋出技術內容與 SKILL packs
- **目標**：3 個 LOI、2 個實際 design partner、1 個內部服務穩定運行 30 天

#### 階段三：對外 Beta 與垂直擴張（2027 H1 起）
- **Vibe coder / 接案工作室**：強打「AI 寫一句、自動上線」工作流
- **教育市場**：與台灣大學資工系合作，作為實驗課程基礎設施
- **企業內部 PaaS**：以 self-host license 切入有資料落地需求的中型企業
- **目標**：在 design partner 成功轉換後，再追求付費擴張與 ARR

### 關鍵渠道優先序
1. **Winshare 內部試點**：先證明團隊自己願意把低風險服務交給 0ops
2. **Design partners**：3–5 家有 AI CLI 與自動部署需求的團隊
3. **Case study**：輸出可重現的部署案例、失敗模式與治理機制
4. **內容與社群**：技術部落格、demo、中英雙語 docs、社群互動
5. **平台合作**：與 AI CLI 廠商建立生態合作，列為加速器，不列為前提

---

## 九、團隊（Team）

### 創辦團隊（必補）
本節目前未完成，屬於對外溝通阻斷項。

對投資人、design partner、早期客戶，至少需要明確回答以下問題：

1. 創辦團隊是否實際管理過 production infra、deployment platform、或開發者工具。
2. 團隊是否具備 Go、Kubernetes、GitOps、GitHub Actions、Cloudflare 整合經驗。
3. 團隊為何比既有 PaaS 或平台團隊更早看見 `AI CLI -> deployment` 的斷層。
4. 團隊是否有足夠 credibility 讓早期客戶願意把 repo、token、domain 與 deployment workflow 交給 0ops。

若以上內容無法具體回答，應優先補齊共同創辦人、核心顧問、或可驗證的實戰經驗，而不是先擴大募資敘事。

### v1 必要編制（12 個月內擴編）
| 角色 | 人數 | 優先序 | 工作重心 |
|---|---|---|---|
| Founding Engineer（Go backend） | 1 | P0 | server + CLI + MCP 主幹 |
| Founding Engineer（Infra / SRE） | 1 | P0 | K3s、ArgoCD、Cloudflare 整合 |
| Product / DevRel | 1 | P1 | SKILL packs、文件、社群 |
| Designer（兼 UI v2） | 0.5 | P2 | v1 暫不需；M6 前到位 |
| 客戶成功 | 0.5 | P2 | Beta 客戶 onboarding |
| **合計** | 4 FTE | | |

### 顧問與董事
- **技術顧問**：MCP / Anthropic 生態熟悉者；K8s / GitOps 資深 SRE
- **業務顧問**：曾於 Vercel / Heroku / 台灣 SaaS 公司任職者
- **獨立董事**（種子輪後）：依領投投資人協商

---

## 十、財務預估（Financials）

### 三年計畫（保守估）

| 期間 | 註冊團隊 | 付費團隊 | MRR (USD) | 累積投入 (USD) | 備註 |
|---|---|---|---|---|---|
| 2026 H1 | 0 | 0 | 0 | 100K | 技術驗證與試點準備 |
| 2026 H2 | 5 | 0 | 0 | 250K | 限定範圍 dogfooding + design partner 驗證；2–5 家 design partner 進場但尚未付費 |
| 2027 H1 | 200 | 30 | 3,000 | 450K | Beta GA |
| 2027 H2 | 500 | 100 | 12,000 | 700K | 商業化啟動 |
| 2028 H1 | 1,200 | 250 | 30,000 | 950K | 損益兩平接近 |
| 2028 H2 | 2,500 | 500 | 65,000 | 1.1M | 接近現金流正向 |

### 樂觀情境（觸發條件：拿到 1–2 個大型 self-host license）
- 2028 ARR：USD 1.5M–2.5M（managed + self-host 並進）

### 悲觀情境（觸發條件：MCP 標準分裂、競品快速抄襲）
- 2028 ARR：USD 200K–400K，需準備二輪募資或調整為純 self-host 軟體授權商業模式

### 關鍵假設
1. 若 M2 於 2026 Q3 前完成，且內部試點連續穩定 30 天，則 v1 GA 目標維持 2026 Q4；否則整體商業時程順延至少一季。
2. 早期付費轉換率：LOI -> design partner -> beta 需逐段驗證，不預設直接成立。
3. 月流失率：Beta 期 5–8%；GA 後降至 2–4%，此處僅為情境假設，不是已驗證數據。
4. AI CLI 市場成長存在平台依賴風險，不作為單一成立前提。

---

## 十一、風險（Risks）與緩解

### 技術風險
| 風險 | 影響 | 緩解 |
|---|---|---|
| Buildpack 偵測失敗於冷僻語言 | M | v1 提示後續支援；v1.1 加 Dockerfile mode |
| MCP SDK 與多家 AI CLI 相容性不一致 | M | 以官方 `go-sdk` 為主，對三家 CLI 逐一做相容性矩陣驗證 |
| Cloudflare for SaaS 整合複雜（客戶自有域名 TLS） | M | v1 先用 Cloudflare Tunnel + DNS 驗證；v2 升級 |
| GitOps repo 高並發衝突 | L | retry + rebase；後期切多 repo |

### 市場風險
| 風險 | 影響 | 緩解 |
|---|---|---|
| Vercel / Railway 推出官方 MCP server | **H** | 12–18 月先發窗口；建立 SKILL 與生態 |
| AI CLI 市場洗牌（claude code / codex 退場） | M | SKILL 抽象層快速適配新 CLI |
| 台灣中小企業 PaaS 付費意願低 | M | 雙軌商業模式（self-host license 補強） |
| 開源 PaaS（Coolify、Dokku）AI 化 | M | 持續加深商業 managed 與在地服務差異化 |

### 營運風險
| 風險 | 影響 | 緩解 |
|---|---|---|
| 客戶 production 故障導致信任損失 | **H** | SLA 設計、incident response runbook、漸進式 rollout |
| 創辦團隊規模過小，無法覆蓋所有面向 | M | 初期聚焦 P0；DevRel 優先於 sales |
| 募資 timeline 不如預期 | M | 維持 18 月 runway buffer；保留 self-host 一次性收入 fallback |

### 合規 / 法務（風險緩解 + 主動信任資產）
- **資料落地**：客戶自有域名 + Tunnel 下，**執行期資料與 app secret 不入控制平面**（控制平面仍存 PII 與 audit metadata，非「完全不存任何資料」）；managed 版本明確分區。資料盤點與分類見 `compliance-framework-mapping`。
- **GitHub Token / Cloudflare Token 管理**：argon2 雜湊、加密儲存、稽核日誌（已實作）。
- **GDPR / 個資法（PDPA）**：v1 僅蒐集 GitHub login 與 email；privacy policy 與帳號/PII 刪除權流程為**規劃中**（`compliance-framework-mapping` 已標缺口）。
- **主動信任資產（非僅防守）**：威脅模型（STRIDE）、audit 完整性、供應鏈簽章、SSO 集中撤權之框架對應，集中於 `docs/trust-and-compliance/`，作為 design partner / 投資人 due diligence 之可出示材料。

---

## 十二、募資（Ask）

### 種子輪
- **金額**：USD 500K – 1M
- **形式**：SAFE 或 priced round（依領投偏好）
- **估值區間**：post-money USD 4M – 8M（依市場）
- **用途分配**：
  - 工程人力（55%）：2 名 FTE engineer × 12 月
  - 基礎設施（15%）：K3s、Cloudflare、監控、CI
  - DevRel / 行銷（15%）：內容、文件、社群活動
  - 法務 / 帳務 / 行政（10%）
  - 預備金（5%）

### 12 個月關鍵 KPI
- 完成 M0–M2，建立可重複 demo 與 dogfooding entry criteria
- 3 個 LOI、2 個 design partner、1 個內部服務穩定運行 30 天
- 至少 10 次真實 create/redeploy 操作紀錄可供回顧
- 1 個 self-host license 或企業試點機會，用於驗證 enterprise 路徑

### 投資人輪廓
- 熟悉 DevTools / Infra / 開源商業模式
- 對 AI agent 基礎設施有 thesis
- 能引薦台灣 / 東南亞 SaaS 渠道
- 有耐心走 24–36 月 Beta → Growth 旅程

---

## 十三、立即下一步（Immediate Next Steps）

### 商業面（本季）
1. 確認創辦團隊組成與股權結構
2. 公司主體選址（台灣 / 新加坡 / Delaware C-Corp 三選一，影響稅務與募資）
3. 商標 / Domain 申請：`0ops.io`、`0ops.tw`、`0ops.dev`
4. 與 3–5 家潛在 alpha 客戶（Winshare 內部 + 外部友軍）簽 LOI
5. 起草投資人簡報（基於本商業計畫 + 技術 demo）

### 技術面（本季）
依 `docs/0ops-plan.md` 的 M0 計畫執行：
1. `go mod init github.com/winshare/zeroops`
2. 建立 `cmd/{server,cli,mcp}` 三 binary scaffold
3. CI / lint / release pipeline（GitHub Actions + goreleaser）
4. 第一條 read-only chain：`GET /v1/apps` → `0ops apps list` → MCP `list_apps`
5. 同步建 `0ops-gitops` 空 repo 與 ArgoCD ApplicationSet

### Dogfooding Entry Criteria
只有同時滿足以下條件，才應把狀態表述為「進入 dogfooding」：

1. repo 已具備 `cmd/server`、`cmd/cli`、`cmd/mcp` 三個可建置入口。
2. 第一條 vertical slice 已跑通：`GET /v1/apps` → CLI → MCP。
3. `create_app_preview` / `create_app` contract 已定稿並完成最小驗證。
4. 至少 1 個內部 demo repo 可成功部署到 `*.jesontech.com`。
5. 同一部署流程可重跑且結果冪等。
6. 基本 audit log 與 failure trace 可回看。

### 待決事項（TBD）
- 公司法律主體
- 領投人選與時程
- Open source 範圍（v1 全閉源 → v2 部分開源 vs v1 即 OSS core）
- 商業 managed cloud 上線時程（建議與 v2 Web UI 同步）
- 對 AI CLI 廠商的合作 outreach 順序

---

## 附錄

### A. 對應技術文件
- `docs/0ops-plan.md`：完整技術規劃（架構、tool catalog、DB schema、auth、verification）
- `docs/trust-and-compliance/plan.md`：Compliance / Audit / Security 統籌計畫與拆解（含威脅模型、框架對應、ADR-0015/0016/0017）

### B. 競品定價對照（2026 Q1 公開資訊）
| 產品 | 入門 | 中階 | 企業 |
|---|---|---|---|
| Vercel | Hobby Free / Pro USD 20/seat | Enterprise quote | Quote |
| Railway | USD 5 + usage | USD 20 + usage | Quote |
| Render | Free → USD 7+ | USD 25+ | Quote |
| Heroku | Eco USD 5 | Basic 7+, Standard 25+ | Quote |
| Zeabur | Developer USD 5 | Team USD 18 | Quote |
| **0ops（規劃）** | **USD 19** | **USD 99** | **USD 299+ / 自架 5K+** |

### C. 名詞對照
| 術語 | 說明 |
|---|---|
| MCP | Model Context Protocol，Anthropic 主導的 AI tool 標準協定 |
| CNB | Cloud Native Buildpacks，無 Dockerfile 自動偵測 stack 並打包 image |
| GitOps | 以 Git repo 作為 desired state 唯一真相的部署模式 |
| ArgoCD | K8s GitOps 控制器，持續同步 cluster 狀態到 Git |
| Cloudflare Tunnel | 反向 tunnel，讓 origin server 不需公開 IP 即可對外提供服務 |
| Two-phase write | preview → confirm 兩段式 API，避免沉默副作用 |

### D. 信任與合規一頁表（due diligence 用）

> 狀態誠實標示：**已具備**＝已實作可驗證；**規劃中**＝有 spec/ADR、尚未實作。對外不得把規劃中講成已具備（承 `docs/trust-and-compliance/plan.md` § 6 規則 1）。

| 維度 | 控制 / 能力 | 0ops 機制 | 狀態 | 來源 |
|---|---|---|---|---|
| 安全 | 寫入需人類閘門、agent 無法靜默生效 | preview → confirm 後端強制 | 已具備 | `preview-confirm-gate` |
| 安全 | 無 prompt injection 攻擊面（後端） | backend 不跑 LLM | 已具備 | `0ops-plan.md` |
| 安全 | 租戶隔離與最小權限 | team RBAC + scope；K3s namespace 隔離 | 已具備 | ADR-0001、`k3s-namespace-isolation` |
| 安全 | 系統威脅模型 | STRIDE（含 agent 攻擊面） | 已具備（文件） | `threat-model` |
| 安全 | 集中身分與撤權（SSO） | OIDC + membership 停用連帶 token revoke | 規劃中 | ADR-0016、`sso-saml` |
| 安全 | 供應鏈完整性 | SBOM + cosign 簽章 + SLSA provenance + 部署端驗簽 | 規劃中 | ADR-0017、`supply-chain-security` |
| 稽核 | 業務行為帳本 | audit_log（寫入/刪除/auth/secret 全入帳、13 個月保留） | 已具備 | `audit-log` |
| 稽核 | 帳本不可竄改 + 取證交付 | append-only + hash chain + export/verify | 規劃中 | ADR-0015、`audit-export-and-integrity` |
| 稽核 | 重要事件對外通知 | outbox webhook（簽章、redact） | 規劃中 | `audit-event-notification` |
| 合規 | 框架控制對應 | PDPA → SOC2 Type II → ISO 27001 矩陣 | 規劃中 | `compliance-framework-mapping` |
| 合規 | 資料落地與分類 | self-host 不入控制平面 / managed 分區；四級分類 | 部分已具備 | `compliance-framework-mapping` |
| 合規 | 密鑰保護 | token argon2 雜湊 + 加密儲存 | 已具備 | `0ops-plan-schema.md` |

---

**文件結束**
