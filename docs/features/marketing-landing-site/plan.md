# Marketing Landing Site Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development / executing-plans. TDD each task. 若走 runner：`./manage.sh task-run MKT.3`。

**Goal:** 一個靜態站（產品 landing ＋ build-in-public blog），把 social `{{canonical_url}}` 與 `0ops.sh` CTA 接上落點。本輪到 build ＋ 本地預覽 ＋ 部署 dry-run；真部署 MKT.4。

**Architecture:** Go 渲染器（goldmark ＋ html/template）讀 `docs/marketing/posts/*.md` → 靜態 HTML 到 `docs/marketing/site/dist/`。`manage.sh` 包裝 build/serve；`publish.sh` 接 canonical_url。

**Tech Stack:** Go（goldmark、html/template、testing）、原生 HTML/CSS（無 JS framework、無 CDN）、bash、Cloudflare Pages（部署 dry-run）。

**Spec:** `docs/features/marketing-landing-site/spec.md`（§3 架構、§4 canonical、§5 gate S1–S6、§6 邊界）。

---

## File Structure

- Create `src/cmd/devtools/mkt-site/main.go` — 渲染器入口（flags：`-posts`、`-out`、`-base-url`、`-templates`）
- Create `src/cmd/devtools/mkt-site/render.go` — 純函式：parse front-matter、抽雙語段、slug→url、render
- Create `src/cmd/devtools/mkt-site/render_test.go` — 單元測試（S6）
- Create `docs/marketing/site/templates/{layout,landing,blog-index,blog-post}.html.tmpl`
- Create `docs/marketing/site/assets/styles.css` — 自含 CSS，RWD
- Create `docs/marketing/site/landing.md` — landing 文案原稿（雙語，遵守 WRITING-PRINCIPLES.md）
- Modify `manage.sh` — 加 `mkt-site-build` / `mkt-site-serve`
- Create `tasks/mkt/deploy-site.sh` — dry-run（印 `wrangler pages deploy` 指令，不連網）
- Modify `tasks/mkt/publish.sh` — `{{canonical_url}}` → `${MKT_SITE_BASE_URL:-https://0ops.sh}/blog/<slug>`
- Modify `tasks/mkt/test/test_publish.sh` — 斷言 canonical_url 被替換
- Modify `docs/marketing/WRITING-PRINCIPLES.md` — 加 landing 文案節
- Create `docs/marketing/site/.gitignore` — 排除 `dist/`
- Modify `tasks/task-list.md` / `task-status.md` / `todo.md` — MKT.3 / MKT.4 registry

---

## Task 1: Go 渲染器核心（parse + 純函式）+ 測試

**Files:** `src/cmd/devtools/mkt-site/render.go`, `render_test.go`

- [ ] **Step 1: 先寫測試**（TDD）——涵蓋：
  - `parsePost(md)` 取出 front-matter `slug/cadence/source` 與 `zh`、`en` 兩段內容；兩段皆非空。
  - `canonicalURL(base, slug)` == `base + "/blog/" + slug`（base 末尾斜線正規化）。
  - `renderPost` 輸出含中英內容、不含任何 `{{` 佔位符。
  - 缺 `slug` → 由檔名（去日期前綴）推導並回傳 warning。
- [ ] **Step 2: 跑測試確認 FAIL**（函式未實作）Run: `cd src && go test ./cmd/devtools/mkt-site/...`
- [ ] **Step 3: 實作 render.go**：front-matter 以 `---` 分隔解析；雙語段以 `## 中文` / `## English` 切；markdown→HTML 用 `github.com/yuin/goldmark`（`go get`）；slug→url 純函式。
- [ ] **Step 4: 跑測試 PASS**
- [ ] **Step 5: Commit** `feat(mkt-site): markdown post parser + bilingual + canonical url (Go, tested)`

## Task 2: 版面模板 + CSS + landing 文案

**Files:** `docs/marketing/site/templates/*.html.tmpl`, `assets/styles.css`, `landing.md`

- [ ] **Step 1: layout.html.tmpl**（共用外框：`<head>` meta/canonical、header、footer、link styles.css）。
- [ ] **Step 2: landing.html.tmpl**（hero 一句價值 + 安裝 CTA `curl … | 0ops apps create` + 3–4 能力點 + 最新 posts 列表）。
- [ ] **Step 3: blog-index.html.tmpl**（posts 列表：標題 + 日期 + 連結）。
- [ ] **Step 4: blog-post.html.tmpl**（中英兩段，語言錨；canonical link）。
- [ ] **Step 5: styles.css**（乾淨、深色技術風、RWD、無外部字型/CDN）。
- [ ] **Step 6: landing.md**（雙語 landing 文案；遵守 WRITING-PRINCIPLES.md：零內部代號、含 CTA）。
- [ ] **Step 7: Commit** `feat(mkt-site): layout/landing/blog templates + self-contained CSS`

## Task 3: 渲染器組裝（main.go：讀 posts → dist）

**Files:** `src/cmd/devtools/mkt-site/main.go`

- [ ] **Step 1: main.go**：flags（`-posts docs/marketing/posts`、`-out docs/marketing/site/dist`、`-templates docs/marketing/site/templates`、`-base-url`）；掃 posts → 每篇 `blog/<slug>.html`；產 `index.html`、`blog/index.html`；複製 `assets/` 到 dist。
- [ ] **Step 2: 手動 build** Run: `cd src && go run ./cmd/devtools/mkt-site -base-url https://0ops.sh`；確認 `docs/marketing/site/dist/{index.html,blog/index.html,blog/*.html}` 產出、無 `{{` 殘留。
- [ ] **Step 3: Commit** `feat(mkt-site): renderer entrypoint — posts → static dist`

## Task 4: manage.sh 接線 + 部署 dry-run

**Files:** `manage.sh`, `tasks/mkt/deploy-site.sh`

- [ ] **Step 1: manage.sh** 加 `mkt-site-build)`（`cd src && go run ./cmd/devtools/mkt-site "$@"`）與 `mkt-site-serve)`（`python3 -m http.server` over dist 或等效）+ help。
- [ ] **Step 2: deploy-site.sh** dry-run：印 `wrangler pages deploy docs/marketing/site/dist --project-name 0ops-site`，不執行；`--deploy` 被 guard（需 `CF_API_TOKEN` + `MKT_SITE_DEPLOY_CONFIRMED=1`，本輪不接）。
- [ ] **Step 3: 冒煙** `./manage.sh mkt-site-build`；`bash tasks/mkt/deploy-site.sh` 印指令不連網。
- [ ] **Step 4: Commit** `feat(mkt-site): manage.sh build/serve + deploy dry-run`

## Task 5: publish.sh canonical_url 接線

**Files:** `tasks/mkt/publish.sh`, `tasks/mkt/test/test_publish.sh`

- [ ] **Step 1: 改 test_publish.sh**：queue fixture 加 `slug:`；斷言輸出把 `{{canonical_url}}` 換成 `https://0ops.sh/blog/<slug>`（no `{{` 殘留）。
- [ ] **Step 2: 跑確認 FAIL**
- [ ] **Step 3: 改 publish.sh**：讀 queue/post `slug`，`base=${MKT_SITE_BASE_URL:-https://0ops.sh}`，`sed` 把 `{{canonical_url}}` → `$base/blog/$slug`；仍 dry-run。
- [ ] **Step 4: 跑 PASS**（`bash tasks/mkt/test/run-tests.sh` 全綠）
- [ ] **Step 5: Commit** `feat(mkt): resolve {{canonical_url}} to blog url in publish dry-run`

## Task 6: 文案契約 + gitignore + registry

**Files:** `docs/marketing/WRITING-PRINCIPLES.md`, `docs/marketing/site/.gitignore`, registry 三檔

- [ ] **Step 1: WRITING-PRINCIPLES.md** 加「landing 文案」節（同原則：零內部代號、價值先行、CTA；hero 一句話價值）。
- [ ] **Step 2: `docs/marketing/site/.gitignore`** 排除 `dist/`。
- [ ] **Step 3: registry**：`task-list.md`/`task-status.md` 加 MKT.3（Done 於本輪收尾）與 MKT.4（Pending，gated）；`todo.md` acceptance bullets。
- [ ] **Step 4: Commit** `docs(mkt): landing copy contract + gitignore dist + register MKT.3/MKT.4`

## Task 7: 驗收 gate（S1–S6）

- [ ] `./manage.sh mkt-site-build -base-url https://0ops.sh` 成功（S1）
- [ ] 每 post 有 `blog/<slug>.html` + `index.html` + `blog/index.html`（S2）
- [ ] 每 blog 頁中英皆非空（S3）
- [ ] `! grep -rl '{{' docs/marketing/site/dist`（S4 無死鏈/佔位符）
- [ ] `! grep -rlE 'ADR-[0-9]{4}|[A-Za-z0-9_./-]+\.go:[0-9]+' docs/marketing/site/dist`（S5 對外安全）
- [ ] `cd src && go test ./cmd/devtools/mkt-site/...` 綠 + `bash tasks/mkt/test/run-tests.sh` 綠（S6）

---

## Self-Review
- Spec §3→Task1-3；§4→Task5；§5 S1-S6→Task7；§6 邊界（site 落 docs/marketing/site/**、工具落 src/cmd/devtools/mkt-site/**）→ 各 task Files 皆守。
- 無 placeholder；Go 走 TDD；build 產物 gitignore；真部署明確劃到 MKT.4。
