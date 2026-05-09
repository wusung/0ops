# 0ops 商業計畫書

**版本**：v1.0（草案）
**日期**：2026-05-09
**對應技術規劃**：`docs/0ops-plan.md`
**準備對象**：創辦人團隊、種子輪投資人、早期合作客戶

---

## 一、執行摘要（Executive Summary）

### 一句話定位
**0ops 是 AI CLI 原生（MCP-first）的內部 PaaS 控制台**，把「給定 GitHub repo 與域名 → 5 分鐘部署完成」的工作流，同時開放給人類 CLI 與 AI agent（claude code / codex / Copilot CLI）以同一組安全 API 操作。

### 為何此刻啟動
1. AI Coding CLI（claude code、codex、Copilot CLI）正取代 Web Console 成為新的主要操作介面，但市面上幾乎無 PaaS 為其原生設計
2. 既有 PaaS（Vercel / Railway / Render / Heroku / Zeabur）皆以 Web UI 為核心，AI agent 操作仰賴 web scraping 或非官方 API，安全與可審計性差
3. MCP（Model Context Protocol）已成為跨家 AI CLI 的事實標準，先行者可建立工具與 SKILL 約定的網路效應
4. Cloud Native Buildpacks 與 GitOps（ArgoCD）成熟，無 Dockerfile 自動化部署的技術門檻顯著降低

### 核心差異化
| 維度 | 0ops | Vercel / Railway / Render | 自建 K8s + GitOps |
|---|---|---|---|
| AI CLI 原生支援 | **是**（MCP 一級公民） | 否（僅 Web UI / REST） | 否 |
| 寫入安全模型 | **兩階段 preview → confirm** | 單階段、即時生效 | 看實作 |
| 客戶自有域名 | CNAME + Cloudflare Tunnel 即時驗證 | 支援、流程繁瑣 | 自行建構 |
| 台灣本土運維 | **是**（繁中、台灣 zone、本地 SLA） | 海外為主 | N/A |
| 部署模式 | Self-host + Managed 雙軌 | 純 Managed | 純 Self-host |

### 商業模式速覽
- **Phase 1（2026 H1–Q3）**：自用為主，Winshare 內部專案先行（dogfooding）
- **Phase 2（2026 Q4–2027 H1）**：台灣中小團隊與 AI-native 工作室付費 Beta
- **Phase 3（2027 H2 起）**：開源 self-host 版 + 商業 managed 雲（雙軌營利）

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
  nextdemo.winshare.tw 上線（5 分鐘內）
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
| **Vercel** | Next.js 生態、Edge network、品牌 | Edge / 全球 CDN 規模 | MCP-first、Self-host、台灣在地 |
| **Railway** | 開發者體驗極佳、模板豐富 | UX 投入規模 | AI CLI 原生、兩階段安全模型 |
| **Render** | 多語言通吃、價格友善 | 知名度、模板 | MCP、GitOps 透明 |
| **Heroku** | 老牌、企業客戶 | 企業基底 | 現代架構、AI 原生 |
| **Fly.io** | 全球 anycast、Postgres 整合 | 基礎建設規模 | AI CLI、繁中 |
| **Zeabur**（台灣） | 同樣繁中、本地團隊 | 既有客戶基礎 | MCP-first、Self-host 路徑、Buildpack |
| **Sealos**（中國開源 PaaS） | 開源、k8s 原生 | 開源社群成熟度 | AI CLI、台灣合規、產品化 |

### 間接競品
- **自建 K8s + ArgoCD + Helm**：技術強團隊偏好；我們是 paved-road 替代
- **Coolify / Dokku（OSS PaaS）**：self-host 取向；我們加上 AI CLI、商業 managed 與台灣支援
- **Cursor / Devin 等 AI agent**：他們做開發、我們做部署；屬互補而非競爭

### 護城河（Moat）建構順序
1. **0–6 月**：MCP SKILL 設計成為事實標準（先發優勢）
2. **6–12 月**：累積 buildpack adapter、客戶模板、failure mode 知識庫
3. **12–24 月**：Self-host license 與 managed cloud 雙軌客戶綁定
4. **24 月+**：開源社群與生態系、可替代的 backend implementation 由我們主導 reference

---

## 六、產品（Product）

### v1 範圍（M0–M5）
參見技術規劃 `docs/0ops-plan.md` 的 Milestones 表，重點如下：

| 里程碑 | 目標 | 商業意義 |
|---|---|---|
| **M0** | 三 binary scaffold + dev env | 開發起點 |
| **M1** | Read-only API + CLI + MCP | 內部 demo 可用 |
| **M2** | `create_app` 兩階段 + winshare 子網域 | **Winshare 內部正式 dogfooding** |
| **M3** | 客戶自有域名 + DNS verify | 可給外部 alpha 客戶 |
| **M4** | Webhook 自動 redeploy + 手動 redeploy | 完整 CI/CD 體驗 |
| **M5** | `delete_app` + audit log + observability | 對外 Beta（首批付費） |
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
- **Starter**：USD 19 / 月 / team（最多 3 apps、winshare.tw 子網域、社群支援）
- **Pro**：USD 99 / 月 / team（最多 20 apps、自有域名、email 支援）
- **Team**：USD 299 / 月 / team（unlimited apps、SSO、SLA、優先支援）

**Phase 3（Self-host License）**：
- **Community**（OSS）：免費、社群支援
- **Business**：USD 5,000–15,000 / 年 / 安裝（含商業支援、私人 buildpack registry）
- **Enterprise**：USD 30,000+ / 年（客製化、on-prem、合規與稽核）

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

#### 階段一：Dogfooding（2026 H1）
- Winshare 內部所有專案統一遷至 0ops
- 累積真實 failure mode 與 buildpack adapter
- 撰寫使用案例（Case Study）作為對外 marketing 素材

#### 階段二：開發者社群滲透（2026 Q3–Q4）
- **內容行銷**：技術部落格、AI CLI 教學系列（中英雙語）
- **社群種子**：MCP / claude code / Hacker News 相關討論串
- **AI CLI 文件 SEO**：成為「claude code 部署」「codex deploy」搜尋第一名
- **Open SKILL packs**：將 SKILL.md 開源至 GitHub，吸引 AI CLI 使用者試用
- **目標**：100 個註冊團隊、20 個活躍 Beta

#### 階段三：垂直擴張（2027 H1 起）
- **Vibe coder / 接案工作室**：強打「AI 寫一句、自動上線」工作流
- **教育市場**：與台灣大學資工系合作，作為實驗課程基礎設施
- **企業內部 PaaS**：以 self-host license 切入有資料落地需求的中型企業
- **目標**：500 註冊、100 付費、ARR USD 200K

### 關鍵渠道優先序
1. **AI CLI 內建發現**：透過 MCP SKILL 註冊，使用者首次安裝 claude code / codex 時即可探索
2. **GitHub README badge**：類似「Deploy to Vercel」按鈕的「Deploy to 0ops」
3. **內容**：技術部落格 + YouTube demo + 中英雙語 docs
4. **社群**：Discord / Slack 頻道、Office Hours
5. **合作**：與 AI CLI 廠商（Anthropic、OpenAI、GitHub）建立官方 SKILL pack 生態

---

## 九、團隊（Team）

### 創辦團隊（待補）
*（此處由創辦人填入：背景、過往成果、為何能做此題）*

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
| 2026 H1 | 0 | 0 | 0 | 100K | 純 dogfooding |
| 2026 H2 | 50 | 5 | 500 | 250K | Alpha → 早期 Beta |
| 2027 H1 | 200 | 30 | 3,000 | 450K | Beta GA |
| 2027 H2 | 500 | 100 | 12,000 | 700K | 商業化啟動 |
| 2028 H1 | 1,200 | 250 | 30,000 | 950K | 損益兩平接近 |
| 2028 H2 | 2,500 | 500 | 65,000 | 1.1M | 接近現金流正向 |

### 樂觀情境（觸發條件：拿到 1–2 個大型 self-host license）
- 2028 ARR：USD 1.5M–2.5M（managed + self-host 並進）

### 悲觀情境（觸發條件：MCP 標準分裂、競品快速抄襲）
- 2028 ARR：USD 200K–400K，需準備二輪募資或調整為純 self-host 軟體授權商業模式

### 關鍵假設
1. v1 GA 於 2026 Q4 達成（M5 完成）
2. 早期付費轉換率：alpha → 10%、beta → 20–30%
3. 月流失率：Beta 期 5–8%；GA 後降至 2–4%
4. AI CLI 市場至少維持目前 30% 季成長率

---

## 十一、風險（Risks）與緩解

### 技術風險
| 風險 | 影響 | 緩解 |
|---|---|---|
| Buildpack 偵測失敗於冷僻語言 | M | v1 提示後續支援；v1.1 加 Dockerfile mode |
| MCP SDK（mark3labs/mcp-go）穩定度不足 | M | 早期 prototype 驗證；必要時 fork 維護 |
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

### 合規 / 法務風險
- **資料落地**：客戶自有域名 + Tunnel 不存客戶資料；managed 版本明確分區
- **GitHub Token / Cloudflare Token 管理**：argon2 雜湊、加密儲存、稽核日誌（已於 schema 設計）
- **GDPR / 個資法**：v1 僅蒐集 GitHub login 與 email；明確 privacy policy

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
- v1 GA（M5 完成）
- 50 個付費團隊、ARR USD 100K–200K
- 1 個 self-host license 簽約（驗證 enterprise 商業模式）
- 至少 2 家 AI CLI 廠商（Anthropic / GitHub / OpenAI）官方推薦或 SKILL pack 收錄

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

### 待決事項（TBD）
- [ ] 公司法律主體
- [ ] 領投人選與時程
- [ ] Open source 範圍（v1 全閉源 → v2 部分開源 vs v1 即 OSS core）
- [ ] 商業 managed cloud 上線時程（建議與 v2 Web UI 同步）
- [ ] 對 AI CLI 廠商的合作 outreach 順序

---

## 附錄

### A. 對應技術文件
- `docs/0ops-plan.md`：完整技術規劃（架構、tool catalog、DB schema、auth、verification）

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

---

**文件結束**
