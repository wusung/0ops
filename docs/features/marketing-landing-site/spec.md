# Feature Spec：marketing-landing-site

> **狀態**：draft
> **來源**：`docs/features/build-in-public-engine/spec.md`（內容來源與寫作契約）；六階段判讀 § Stage 5（landing／公開 demo 致命缺口）；social 變體 `{{canonical_url}}` 回鏈與 `0ops.sh` CTA 需落點。
> **適用範圍**：一個靜態站，兩職責——產品 landing ＋ build-in-public blog（渲染 `docs/marketing/posts/*.md`）。本輪（MKT.3）做到 build ＋ 本地預覽 ＋ 部署腳本 dry-run；真實 Cloudflare Pages 部署需 CF 專案／token → MKT.4（gated）。
> **對應 Milestone**：MKT.3（渲染器＋landing＋blog＋canonical 接線，零外部憑證）；MKT.4（真部署，gated）

## 1. 結論（先讀本段）

- landing page 是行銷引擎缺的**另一半漏斗**：social 變體的 `{{canonical_url}}` 與文案 CTA 的 `0ops.sh` 目前都無落點，一真發即死鏈。
- 一個**靜態站**兩職責：landing（價值＋安裝 CTA）＋ blog（渲染既有 posts）。內容源就是 `docs/marketing/posts/`，不另立內容流程。
- 渲染器為 **Go 小工具**（`src/cmd/devtools/mkt-site/`，goldmark ＋ html/template），產靜態 HTML 到 `docs/marketing/site/dist/`；可由 `./manage.sh test` 的 Go 測試覆蓋。
- `{{canonical_url}}` 解析為 `<base>/blog/<slug>`；base 由 `MKT_SITE_BASE_URL`（預設 `https://0ops.sh`）設定；`publish.sh` 於 dry-run 時據 post slug 填入（不再輸出死佔位符）。
- 主機為 **Cloudflare Pages**（與產品既有 CF 棧一致）。本輪只到 build ＋ 本地預覽 ＋ `deploy-site.sh` dry-run；真部署 gated（需 CF Pages 專案＋token）→ MKT.4。
- 文案沿用 `docs/marketing/WRITING-PRINCIPLES.md`（零內部代號、必有 CTA），擴一節涵蓋 landing 文案，同一契約管所有對外文字。

## 2. 範圍

### 2.1 包含
- Go 渲染器：解析 posts front-matter（`slug`/`cadence`/`source`）＋ `## 中文`／`## English` 雙語段 → 靜態 HTML。
- 版面模板 ＋ 最小自含 CSS（無 JS framework、RWD、無外部字型/CDN）。
- 產出：`index.html`（landing）、`blog/index.html`（文章列表）、`blog/<slug>.html`（每篇，雙語）。
- `{{canonical_url}}` 接線：`publish.sh` 依 slug ＋ base 填入真實 blog URL。
- 指令：`./manage.sh mkt-site-build`（渲染）、`mkt-site-serve`（本地預覽）、`tasks/mkt/deploy-site.sh`（dry-run，印 `wrangler pages deploy` 指令，不連網）。
- Go 測試 ＋ build 驗收 gate。
- `WRITING-PRINCIPLES.md` landing 文案節。

### 2.2 不包含
- 真實 Cloudflare Pages 部署（需 CF 專案 ＋ token）→ MKT.4。
- CMS／後端／A-B／analytics／表單。
- 精緻設計系統（本輪最小可上、乾淨即可）。

## 3. 架構

```
docs/marketing/posts/*.md  ──►  mkt-site (Go: goldmark + html/template)  ──►  docs/marketing/site/dist/
   (引擎既有產出)                  ▲ 版面模板 + CSS：docs/marketing/site/templates/、assets/         index.html
                                                                                                   blog/index.html
                                                                                                   blog/<slug>.html
                                          ./manage.sh mkt-site-build → dist/
                                          ./manage.sh mkt-site-serve → 本地預覽 dist/
                                          tasks/mkt/deploy-site.sh  → dry-run（wrangler pages deploy dist/）
```

- 渲染器讀每個 post 的 front-matter 取 `slug`；產 `blog/<slug>.html`。無 `slug` → 由檔名推導並警告。
- 雙語：blog 頁同時呈現 `## 中文` 與 `## English` 兩段（含語言錨點/標題），landing 亦雙語。
- landing 內容：hero（一句價值）＋ 安裝 CTA（`curl … | 0ops apps create`）＋ 幾個能力點 ＋ 最新 posts 連結。文案遵守 WRITING-PRINCIPLES.md。

## 4. canonical_url 接線

- 新增 `MKT_SITE_BASE_URL`（預設 `https://0ops.sh`）。
- `publish.sh` 產 payload 時，若 queue/post 帶 `slug`，把 `{{canonical_url}}` 換成 `${MKT_SITE_BASE_URL}/blog/<slug>`；仍為 dry-run，不連網。
- blog 頁的 canonical URL 與此一致，確保 social→blog 回鏈可解析。

## 5. 驗收 gate（客觀）

`./manage.sh mkt-site-build` 後：

- **S1 build 成功**，`docs/marketing/site/dist/` 存在。
- **S2 覆蓋**：每個 `docs/marketing/posts/*.md` 都有對應 `blog/<slug>.html`；另有 `index.html` 與 `blog/index.html`。
- **S3 雙語**：每篇 blog 頁同時含中英內容（非空）。
- **S4 無死鏈**：dist 內不得殘留 `{{canonical_url}}` 或其他 `{{…}}` 佔位符。
- **S5 對外安全**：landing 與 blog 輸出不得含內部代號（`ADR-XXXX` / `file.go:line`）——沿用 WRITING-PRINCIPLES.md 原則 2（因內容源已過 build-in-public G3，此為二次保險）。
- **S6 Go 單元測試**：front-matter 解析、slug→url、雙語段抽取、佔位符替換皆有測試。

## 6. 邊界

- 站原始碼與產出落 `docs/marketing/site/**`；Go 工具落 `src/cmd/devtools/mkt-site/**`；不碰其他 `src/`。
- 真部署為對外動作，本輪只到 dry-run；真 token／CF 專案屬 MKT.4，gated。
- `dist/` 為 build 產物，建議 `.gitignore` 排除（只版控 templates／assets／landing 原稿），避免把可重生輸出灌進 repo。

## 7. Future（MKT.4，gated）

- 真實 `wrangler pages deploy`：需 CF Pages 專案 ＋ API token（env／sealed-secrets，不進 repo）。
- 網域 `0ops.sh` 綁定（CF DNS，與產品同帳號）。
- production 路徑轉綠後，把本站自架到 0ops 上——旗艦 dogfood ＋ 季更「從問題到解法」素材。
