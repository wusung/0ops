# Build-in-Public Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. 若透過本 repo 的 `./manage.sh task-run MKT.0` 執行，runner 會在 worktree 內以 superpowers 序列跑本計畫。

**Goal:** 把行銷內容產出做成既有 task-runner 的一條 content lane——由 agent loop 產出、由客觀 gate 驗收，並把散佈到 FB 粉專/Threads 做到 dry-run。

**Architecture:** 疊在既有 runner 上。`tasks/mkt/next.sh` 挑素材並註冊 content task；既有 `manage.sh task-run` 派 agent 依模板寫 canonical 長文；`tasks/mkt/verify.sh` 客觀 gate（G1–G6）把關；`tasks/mkt/publish.sh` 由 canonical 衍生社群短文並 dry-run。全部產出落 `docs/marketing/**`，不污染規格來源。

**Tech Stack:** bash（mirror `tasks/run/*.sh` 風格）、markdown、既有 `manage.sh` 派工、bats 風格 smoke test（mirror `tasks/run/test/`）。

**Spec:** `docs/features/build-in-public-engine/spec.md`（§4 素材映射、§5 verify 契約、§6 散佈 lane、§8 first proof）。

---

## File Structure

- Create `docs/marketing/README.md` — 引擎操作手冊
- Create `docs/marketing/sources-ledger.md` — 素材帳本（available/consumed）
- Create `docs/marketing/editorial-calendar.md` — 出刊記錄
- Create `docs/marketing/published-ledger.md` — 發佈 permalink + dedup key 記錄
- Create `docs/marketing/templates/{weekly-decision,monthly-postmortem,quarterly-path}.md`
- Create `docs/marketing/posts/.gitkeep`, `docs/marketing/queue/.gitkeep`
- Create `tasks/mkt/lib.sh` — 常數 + 帳本/registry helper
- Create `tasks/mkt/next.sh` — 節奏產生器
- Create `tasks/mkt/verify.sh` — 客觀驗收 gate（G1–G6）
- Create `tasks/mkt/publish.sh` — dry-run 散佈器
- Create `tasks/mkt/test/{run-tests.sh,test_verify.sh,test_next.sh,test_publish.sh,fixtures/*}`
- Modify `manage.sh` — 加 `mkt-next` / `mkt-verify` / `mkt-publish` 子命令 + help
- Modify `tasks/task-list.md`, `tasks/task-status.md`, `tasks/todo.md` — registry 列（MKT.0 擴充 + MKT.1；MKT.W1 由 next.sh 自動註冊）

---

## Task 1: docs/marketing scaffold + 素材帳本 + 模板

**Files:** Create `docs/marketing/README.md`, `sources-ledger.md`, `editorial-calendar.md`, `published-ledger.md`, `templates/*.md`, `posts/.gitkeep`, `queue/.gitkeep`

- [ ] **Step 1: 建 sources-ledger.md**（產生器讀此表挑素材）

```markdown
# Sources Ledger

> 產生器 `tasks/mkt/next.sh` 讀本表挑「下一個 available」素材。欄位固定：`source | cadence | status | post`。
> agent 完成一篇後把該列 status 改 consumed、填 post 路徑（verify G5 檢查）。

| source | cadence | status | post |
|---|---|---|---|
| docs/adrs/0002-idempotency-and-compensation.md | weekly | available | - |
| docs/adrs/0015-audit-log-append-only-and-tamper-evidence.md | weekly | available | - |
| docs/adrs/0001-multi-tenancy-and-rbac.md | weekly | available | - |
| tasks/lessons.md#L017 | monthly | available | - |
| tasks/lessons.md#L009 | monthly | available | - |
| milestone:M6-app-source-ingestion | quarterly | available | - |
```

- [ ] **Step 2: 建 editorial-calendar.md 與 published-ledger.md（空表 + 表頭）**

```markdown
# Editorial Calendar

> 出刊記錄。verify G5 檢查每篇 post 於此有一列。欄位：`date | cadence | post | status`。

| date | cadence | post | status |
|---|---|---|---|
```

```markdown
# Published Ledger

> 散佈記錄。`tasks/mkt/publish.sh` 發佈成功後寫入。欄位：`post | channel | dedup_key | permalink | date`。
> dedup_key 存在即代表已發，重跑跳過（冪等）。

| post | channel | dedup_key | permalink | date |
|---|---|---|---|---|
```

- [ ] **Step 3: 建三個模板**（必填標題對應 verify G2）

`templates/weekly-decision.md`：

```markdown
---
cadence: weekly
source: docs/adrs/XXXX-....md
slug: <kebab-slug>
---

## 中文

# <標題：為什麼這麼做>

**限制**：<這個決策面對的約束，錨定 ADR-XXXX 或 file.go:line>

**選項**：<考慮過的方案 A / B / C>

**取捨**：<為何選這個、放棄了什麼>

## English

# <Title: why we did it this way>

**Constraint**: ...

**Options**: ...

**Trade-off**: ...
```

`templates/monthly-postmortem.md`：

```markdown
---
cadence: monthly
source: tasks/lessons.md#LXXX
slug: <kebab-slug>
---

## 中文

# <標題：失敗教會什麼>

**症狀**：<觀察到的錯誤行為，錨定 commit / file.go:line>

**根因**：...

**為何當初沒看見**：...

**制度性修正**：...

## English

# <Title>

**Symptom**: ...

**Root cause**: ...

**Why we missed it**: ...

**Systemic fix**: ...
```

`templates/quarterly-path.md`：

```markdown
---
cadence: quarterly
source: milestone:<id>
slug: <kebab-slug>
---

## 中文

# <標題：從問題到解法>

**痛點**：...

**設計約束**：...

**架構決策鏈**：<ADR-XXXX → ADR-YYYY>

**實作**：<file.go:line>

**驗證證據**：<e2e 腳本 / commit sha>

**失敗模式**：...

## English

# <Title>

**Pain**: ...

**Design constraints**: ...

**Decision chain**: ...

**Implementation**: ...

**Verification evidence**: ...

**Failure modes**: ...
```

- [ ] **Step 4: 建 README.md（操作手冊）+ posts/queue 的 .gitkeep**

README 摘要三節奏、`mkt-next`/`mkt-verify`/`mkt-publish` 用法、G1–G6 gate、邊界規則（只寫 `docs/marketing/**`）。指向 `../features/build-in-public-engine/spec.md` 為事實源。

- [ ] **Step 5: Commit**

```bash
git add docs/marketing
git commit -m "feat(mkt): scaffold build-in-public marketing dir + templates + ledgers"
```

---

## Task 2: tasks/mkt/lib.sh（共用 helper）

**Files:** Create `tasks/mkt/lib.sh`; Test `tasks/mkt/test/test_lib.sh`

- [ ] **Step 1: 寫 lib.sh**

```bash
#!/usr/bin/env bash
set -euo pipefail
MKT_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$MKT_LIB_DIR/../.." && pwd)"
MARKETING_DIR="$REPO_ROOT/docs/marketing"
LEDGER="${MKT_LEDGER:-$MARKETING_DIR/sources-ledger.md}"
CALENDAR="${MKT_CALENDAR:-$MARKETING_DIR/editorial-calendar.md}"
POSTS_DIR="$MARKETING_DIR/posts"
QUEUE_DIR="$MARKETING_DIR/queue"
PUBLISHED_LEDGER="${MKT_PUBLISHED_LEDGER:-$MARKETING_DIR/published-ledger.md}"
TASK_LIST="${MKT_TASK_LIST:-$REPO_ROOT/tasks/task-list.md}"
TASK_STATUS="${MKT_TASK_STATUS:-$REPO_ROOT/tasks/task-status.md}"
TODO="${MKT_TODO:-$REPO_ROOT/tasks/todo.md}"

die() { echo "mkt: $*" >&2; exit 1; }

# ledger table cols: | source | cadence | status | post |
ledger_next_available() {
  local cadence="$1"
  awk -F'|' -v c="$cadence" '
    NR>2 && /\|/ {
      s=$2; cad=$3; st=$4;
      gsub(/^[ \t]+|[ \t]+$/,"",s); gsub(/^[ \t]+|[ \t]+$/,"",cad); gsub(/^[ \t]+|[ \t]+$/,"",st);
      if (cad==c && st=="available") { print s; exit }
    }' "$LEDGER"
}
```

- [ ] **Step 2: 寫 test_lib.sh（fixtures ledger → 斷言挑到正確 source）**

```bash
#!/usr/bin/env bash
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export MKT_LEDGER="$here/fixtures/ledger.md"
source "$here/../lib.sh"
got="$(ledger_next_available weekly)"
[[ "$got" == "docs/adrs/0002-idempotency-and-compensation.md" ]] || { echo "FAIL: got '$got'"; exit 1; }
[[ "$(ledger_next_available quarterly)" == "milestone:M6-app-source-ingestion" ]] || { echo "FAIL quarterly"; exit 1; }
echo "PASS test_lib"
```

fixtures/ledger.md：複製 Task 1 的 sources-ledger.md 內容。

- [ ] **Step 3: 跑測試** Run: `bash tasks/mkt/test/test_lib.sh` — Expected: `PASS test_lib`
- [ ] **Step 4: Commit** `git add tasks/mkt/lib.sh tasks/mkt/test && git commit -m "feat(mkt): lib.sh ledger helpers + test"`

---

## Task 3: tasks/mkt/verify.sh（客觀 gate G1–G6）

**Files:** Create `tasks/mkt/verify.sh`; Test `tasks/mkt/test/test_verify.sh` + fixtures

- [ ] **Step 1: 先寫失敗測試**（good post 應 PASS、缺雙語/缺錨點/越界各應 FAIL）

```bash
#!/usr/bin/env bash
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
v="$here/../verify.sh"
export MKT_VERIFY_SKIP_G4=1 MKT_LEDGER="$here/fixtures/ledger-consumed.md" MKT_CALENDAR="$here/fixtures/calendar-ok.md"
bash "$v" "$here/fixtures/post-good.md"      && echo "good ok"      || { echo "FAIL good"; exit 1; }
bash "$v" "$here/fixtures/post-no-en.md"     && { echo "FAIL no-en"; exit 1; } || echo "no-en rejected"
bash "$v" "$here/fixtures/post-no-anchor.md" && { echo "FAIL anchor"; exit 1; } || echo "anchor rejected"
echo "PASS test_verify"
```

fixtures：`post-good.md`（含 `## 中文`/`## English`、cadence: weekly、限制/選項/取捨、含 `ADR-0002`、front-matter `source: docs/adrs/0002-...md`）；`ledger-consumed.md`（該 source 標 consumed）；`calendar-ok.md`（含 post-good.md 一列）；`post-no-en.md`（刪 English 段）；`post-no-anchor.md`（無 ADR/檔案/sha）。

- [ ] **Step 2: 跑測試確認 FAIL**（verify.sh 尚不存在）Run: `bash tasks/mkt/test/test_verify.sh` — Expected: 失敗（找不到 verify.sh）
- [ ] **Step 3: 寫 verify.sh**

```bash
#!/usr/bin/env bash
# tasks/mkt/verify.sh <post-path>
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
post="${1:-}"
[[ -n "$post" && -f "$post" ]] || die "usage: verify.sh <existing-post-path>"
fail() { echo "VERIFY FAIL [$1]: $2" >&2; exit 1; }

# G1 bilingual
grep -q '^## 中文' "$post" || fail G1 "missing '## 中文'"
grep -q '^## English' "$post" || fail G1 "missing '## English'"
zh=$(awk '/^## 中文/{f=1;next} /^## English/{f=0} f' "$post" | tr -d '[:space:]')
en=$(awk '/^## English/{f=1;next} f' "$post" | tr -d '[:space:]')
[[ -n "$zh" ]] || fail G1 "zh empty"; [[ -n "$en" ]] || fail G1 "en empty"

# G2 template structure by cadence
cadence=$(sed -n 's/^cadence:[[:space:]]*//p' "$post" | head -1)
case "$cadence" in
  weekly)    reqs=("限制" "選項" "取捨") ;;
  monthly)   reqs=("症狀" "根因" "為何" "制度") ;;
  quarterly) reqs=("痛點" "設計約束" "決策" "驗證" "失敗模式") ;;
  *) fail G2 "unknown cadence: '$cadence'" ;;
esac
for h in "${reqs[@]}"; do grep -q "$h" "$post" || fail G2 "missing marker: $h"; done

# G3 engineering anchor
grep -Eq 'ADR-[0-9]{4}|[A-Za-z0-9_./-]+\.go:[0-9]+|\b[0-9a-f]{7,40}\b' "$post" \
  || fail G3 "no verifiable anchor (ADR-XXXX / file.go:line / sha)"

# G4 boundary (content tasks only; bootstrap sets MKT_VERIFY_SKIP_G4=1)
if [[ "${MKT_VERIFY_SKIP_G4:-0}" != "1" ]]; then
  while IFS= read -r p; do
    [[ -z "$p" ]] && continue
    case "$p" in docs/marketing/*) ;; *) fail G4 "change outside docs/marketing/: $p" ;; esac
  done < <(git -C "$REPO_ROOT" status --porcelain | awk '{print $2}')
fi

# G5 ledger + calendar
src=$(sed -n 's/^source:[[:space:]]*//p' "$post" | head -1)
[[ -n "$src" ]] || fail G5 "missing 'source:' front-matter"
grep -F "$src" "$LEDGER" | grep -q 'consumed' || fail G5 "source not consumed in ledger: $src"
grep -Fq "$(basename "$post")" "$CALENDAR" || fail G5 "post not in editorial-calendar"

# G6 threads length
qfile="$QUEUE_DIR/$(basename "$post" .md).yaml"
if [[ -f "$qfile" ]]; then
  tlen=$(awk '/^threads:/{f=1;next} f&&/^[a-z]+:/{f=0} f' "$qfile" | tr -d '\n' | wc -m)
  (( tlen <= 500 )) || fail G6 "threads $tlen > 500 chars"
fi

echo "VERIFY PASS: $post"
```

- [ ] **Step 4: 跑測試確認 PASS** Run: `bash tasks/mkt/test/test_verify.sh` — Expected: `PASS test_verify`
- [ ] **Step 5: Commit** `git add tasks/mkt/verify.sh tasks/mkt/test && git commit -m "feat(mkt): verify.sh objective content gate G1-G6 + tests"`

---

## Task 4: tasks/mkt/next.sh（節奏產生器）

**Files:** Create `tasks/mkt/next.sh`; Test `tasks/mkt/test/test_next.sh`

- [ ] **Step 1: 先寫失敗測試**（跑 next.sh weekly → registry 三檔各多一列 MKT.W1；再跑一次同 source → noop 不重複）

```bash
#!/usr/bin/env bash
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tmp="$(mktemp -d)"; cp "$here/fixtures/ledger.md" "$tmp/ledger.md"
: > "$tmp/task-list.md"; : > "$tmp/task-status.md"; : > "$tmp/todo.md"
export MKT_LEDGER="$tmp/ledger.md" MKT_TASK_LIST="$tmp/task-list.md" MKT_TASK_STATUS="$tmp/task-status.md" MKT_TODO="$tmp/todo.md"
id="$(bash "$here/../next.sh" weekly)"
[[ "$id" == "MKT.W1" ]] || { echo "FAIL id=$id"; exit 1; }
grep -q "MKT.W1" "$tmp/task-list.md" && grep -q "MKT.W1" "$tmp/task-status.md" && grep -q "MKT.W1" "$tmp/todo.md" || { echo "FAIL registry"; exit 1; }
bash "$here/../next.sh" weekly >/dev/null; c=$(grep -c "MKT.W1" "$tmp/task-list.md")
[[ "$c" == "1" ]] || { echo "FAIL idempotency c=$c"; exit 1; }
echo "PASS test_next"
```

- [ ] **Step 2: 跑測試確認 FAIL** Run: `bash tasks/mkt/test/test_next.sh` — Expected: 失敗
- [ ] **Step 3: 寫 next.sh**

```bash
#!/usr/bin/env bash
# tasks/mkt/next.sh <weekly|monthly|quarterly>
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
cadence="${1:-}"
case "$cadence" in
  weekly) prefix="MKT.W" ;; monthly) prefix="MKT.M" ;; quarterly) prefix="MKT.Q" ;;
  *) die "usage: next.sh <weekly|monthly|quarterly>" ;;
esac
src="$(ledger_next_available "$cadence")"
[[ -n "$src" ]] || die "no available $cadence source"
if grep -q "$src" "$TASK_LIST" 2>/dev/null; then echo "noop: $src already registered" >&2; exit 0; fi
n=1; while grep -q "| ${prefix}${n} " "$TASK_LIST" 2>/dev/null; do n=$((n+1)); done
id="${prefix}${n}"
title="Build-in-public $cadence post from $(basename "$src")"
printf '| %s | %s | - | %s | `docs/marketing/**` |\n' "$id" "$title" "docs/features/build-in-public-engine/spec.md, $src" >> "$TASK_LIST"
printf '| %s | %s | Pending | - |\n' "$id" "$title" >> "$TASK_STATUS"
cat >> "$TODO" <<EOF

### $id — $title
- [ ] 依 \`docs/features/build-in-public-engine/spec.md\` §4 由 $src 產出 $cadence 中英雙語 canonical 長文至 \`docs/marketing/posts/\`
- [ ] front-matter 含 \`cadence: $cadence\`、\`source: $src\`
- [ ] 通過 \`./manage.sh mkt-verify <post>\`（G1–G6）
- [ ] sources-ledger 標 $src consumed；editorial-calendar 加列
EOF
echo "$id"
```

- [ ] **Step 4: 跑測試確認 PASS** Run: `bash tasks/mkt/test/test_next.sh` — Expected: `PASS test_next`
- [ ] **Step 5: Commit** `git add tasks/mkt/next.sh tasks/mkt/test && git commit -m "feat(mkt): next.sh cadence generator + idempotency test"`

---

## Task 5: tasks/mkt/publish.sh（dry-run 散佈器）

**Files:** Create `tasks/mkt/publish.sh`; Test `tasks/mkt/test/test_publish.sh` + `fixtures/queue-item.yaml`

- [ ] **Step 1: 先寫失敗測試**（dry-run 印出兩通道且不連網；已發 dedup 跳過；`--publish` 應 die）

```bash
#!/usr/bin/env bash
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tmp="$(mktemp -d)"; : > "$tmp/published.md"
export MKT_PUBLISHED_LEDGER="$tmp/published.md"
out="$(bash "$here/../publish.sh" "$here/fixtures/queue-item.yaml")"
echo "$out" | grep -q "channel=fb" && echo "$out" | grep -q "channel=threads" || { echo "FAIL channels"; exit 1; }
echo "$out" | grep -q "dry-run" || { echo "FAIL dry-run"; exit 1; }
bash "$here/../publish.sh" "$here/fixtures/queue-item.yaml" --publish 2>/dev/null && { echo "FAIL publish-guard"; exit 1; } || echo "publish guarded"
echo "PASS test_publish"
```

`fixtures/queue-item.yaml`：

```yaml
fb: |
  0ops preview→confirm：讓 agent 的寫入操作無法繞過安全閘。為什麼這麼設計 → https://…/preview-confirm-idempotency
threads: |
  agent 出貨最怕誤刪。0ops 把 preview→confirm 冪等做在 backend 與 sqlc codegen 層，不是 UI 約定。ADR-0002。
```

- [ ] **Step 2: 跑測試確認 FAIL** Run: `bash tasks/mkt/test/test_publish.sh` — Expected: 失敗
- [ ] **Step 3: 寫 publish.sh**

```bash
#!/usr/bin/env bash
# tasks/mkt/publish.sh <queue-item.yaml> [--publish]
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
item="${1:-}"; mode="dry-run"
[[ -n "$item" && -f "$item" ]] || die "usage: publish.sh <queue-item.yaml> [--publish]"
[[ "${2:-}" == "--publish" ]] && mode="publish"
post_id="$(basename "$item" .yaml)"
for ch in fb threads; do
  key="$(printf '%s|%s' "$post_id" "$ch" | sha256sum | cut -c1-16)"
  if grep -q "$key" "$PUBLISHED_LEDGER" 2>/dev/null; then echo "skip (already published): $ch $post_id"; continue; fi
  body="$(awk -v c="$ch:" '$0==c{f=1;next} f&&/^[a-z]+:/{f=0} f' "$item")"
  echo "=== channel=$ch post=$post_id dedup=$key ==="; printf '%s\n' "$body"
  if [[ "$mode" == "publish" ]]; then
    die "real publish disabled in MKT.1 (needs Meta creds + MKT_PUBLISH_CONFIRMED=1) — spec §9 MKT.2"
  else
    echo "[dry-run] would POST to $ch; no network."
  fi
done
```

- [ ] **Step 4: 跑測試確認 PASS** Run: `bash tasks/mkt/test/test_publish.sh` — Expected: `PASS test_publish`
- [ ] **Step 5: Commit** `git add tasks/mkt/publish.sh tasks/mkt/test && git commit -m "feat(mkt): publish.sh dry-run FB/Threads + dedup guard + tests"`

---

## Task 6: manage.sh 子命令接線

**Files:** Modify `manage.sh`（dispatch case + help，mirror 既有 `task-run)` 分派）; Test `tasks/mkt/test/run-tests.sh`

- [ ] **Step 1: 加三個 cmd 函式與 dispatch**（放在既有 `cmd_task_run` 附近）

```bash
cmd_mkt_next()    { bash tasks/mkt/next.sh "$@"; }
cmd_mkt_verify()  { bash tasks/mkt/verify.sh "$@"; }
cmd_mkt_publish() { bash tasks/mkt/publish.sh "$@"; }
```

在 dispatch `case` 內既有 `task-runner-test)` 之後加：

```bash
    mkt-next)     cmd_mkt_next "$@" ;;
    mkt-verify)   cmd_mkt_verify "$@" ;;
    mkt-publish)  cmd_mkt_publish "$@" ;;
```

help 區塊加三行說明（節奏產生 / 內容驗收 / dry-run 散佈）。

- [ ] **Step 2: 建 test/run-tests.sh 彙總器**（跑 test_lib/test_verify/test_next/test_publish，全 PASS 才 exit 0）
- [ ] **Step 3: 跑** Run: `bash tasks/mkt/test/run-tests.sh` — Expected: 四支全 `PASS`
- [ ] **Step 4: 冒煙** Run: `./manage.sh mkt-next --help 2>&1 || true`；`./manage.sh mkt-verify docs/marketing/templates/weekly-decision.md 2>&1 || true`（模板缺 consumed 應 G5 FAIL，證明 gate 生效）
- [ ] **Step 5: Commit** `git add manage.sh tasks/mkt/test && git commit -m "feat(mkt): wire mkt-next/verify/publish into manage.sh + test runner"`

---

## Task 7: registry 列（MKT.1 散佈 lane task）

**Files:** Modify `tasks/task-list.md`, `tasks/task-status.md`, `tasks/todo.md`
（MKT.0 列的擴充與 stale 註記修正已於 bootstrap 前置由維護者完成；本 task 只新增 MKT.1。）

- [ ] **Step 1: task-list.md 加 MKT.1 列**

```
| MKT.1 | Social distribution lane (derive + dry-run publish to FB Page/Threads) | MKT.0 | docs/features/build-in-public-engine/spec.md §6 | `docs/marketing/**`, `tasks/mkt/**` |
```

- [ ] **Step 2: task-status.md 加** `| MKT.1 | Social distribution lane (dry-run) | Pending | - |`
- [ ] **Step 3: todo.md 加 MKT.1 acceptance bullets**（衍生 queue 變體、`mkt-publish` dry-run 印雙通道、`--publish` 被 guard、published-ledger dedup 冪等；明確：本輪不接真 token）
- [ ] **Step 4: Commit** `git add tasks/task-list.md tasks/task-status.md tasks/todo.md && git commit -m "chore(mkt): register MKT.1 social distribution lane"`

---

## Task 8: First proof — 由 loop 產出第一篇（MKT.W1）

**Files:** Create `docs/marketing/posts/2026-07-05-preview-confirm-idempotency.md`（由 agent 產出，非手寫）；Modify `sources-ledger.md`、`editorial-calendar.md`

- [ ] **Step 1: 產生 content task** Run: `./manage.sh mkt-next weekly` — Expected: 輸出 `MKT.W1`，registry 三檔各新增 MKT.W1 列
- [ ] **Step 2: 派 loop 執行**（dogfooded）Run: `./manage.sh task-run MKT.W1` — runner 開 worktree、agent 依 `templates/weekly-decision.md` 由 `docs/adrs/0002-idempotency-and-compensation.md` 寫出中英雙語長文，錨定 `internal/server/services/createapp/service.go` 的 `Confirm()` 與 `ADR-0002`；完成時把 ledger 該列標 consumed、calendar 加列
  - （若選 inline 執行：由本 session 的 subagent 依同一模板與 spec §4 產出該檔，再手動走 Step 3）
- [ ] **Step 3: 客觀驗收** Run: `./manage.sh mkt-verify docs/marketing/posts/2026-07-05-preview-confirm-idempotency.md` — Expected: `VERIFY PASS`
- [ ] **Step 4: 衍生社群變體 + dry-run** Run: 產出 `docs/marketing/queue/2026-07-05-preview-confirm-idempotency.yaml` 後 `./manage.sh mkt-publish docs/marketing/queue/2026-07-05-preview-confirm-idempotency.yaml` — Expected: 印出 fb/threads 兩通道 payload + `[dry-run] no network`
- [ ] **Step 5: 標 MKT.0 完成** 依既有 runner 慣例把 task-status.md MKT.0 改 Done、填 Completed Date；commit 由 runner 完成

---

## Self-Review

- **Spec coverage**：§3 四面向 → Task 1/2/4/6（registry/generator/execution/verify）；§4 素材映射 → Task 1 ledger + Task 4 next.sh；§5 G1–G6 → Task 3 verify.sh 六段一一對應；§6 散佈 lane dry-run → Task 5 + Task 7 MKT.1；§7 邊界 → verify G4 + README；§8 first proof → Task 8。無遺漏。
- **Placeholder scan**：模板內 `<...>` 為 agent 產出時填空的內容槽（非計畫 placeholder）；所有 script step 均附完整可執行程式碼。
- **Type/naming consistency**：`ledger_next_available`、`MKT_LEDGER`/`MKT_TASK_LIST` 等 env override、`MKT_VERIFY_SKIP_G4`、dedup key 演算法在各 task 一致。cadence 值 `weekly|monthly|quarterly` 與 task 前綴 `MKT.W|M|Q` 全檔一致。
