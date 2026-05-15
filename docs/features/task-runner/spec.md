# Feature Spec：task-runner

> **狀態**：draft
> **來源**：使用者請求（仿 `tehmag-foods/user-cases/scripts/task/` 之 run-all / run-one 模式）
> **適用範圍**：本機開發者觸發、worktree 內隔離執行、auto commit、auto merge to main
> **對應 Milestone**：跨 milestone（M2/M3 後續多 task 串跑）

## 1. 結論（先讀本段）

- 本 runner 為「拍下 task → 送一發 agent → verify → commit → merge」的最小 task automation；不重新發明 workflow engine。
- Task 事實源固定為兩份 markdown table：`tasks/task-list.md`（描述）+ `tasks/task-status.md`（狀態），不引入 SQL，不解析 `tasks/todo.md` narrative。
- Task ID 沿用 `tasks/todo.md` 既有編號（`M2.4` / `M2.5` / `M2.7` …），不重新發 `T01..Tnn`。
- 每個 task 一個 `git worktree`（`.worktrees/<TASK_ID>`）+ 一個分支（`task/<TASK_ID>`）。
- 預設 agent CLI 為本機 `claude -p`（headless），可透過 `TASK_AGENT_BIN` 切換到 `copilot` / `codex`。
- Runner 不 enforce Mandatory Agent Loop 內部步驟；流程責任以 prompt 文字交給 agent 自走，runner 只看「task 邊界」是否完成。
- Status 簡化為 `Pending / Done / Failed` 三態；「正在執行 / 可續跑」狀態以 `.worktrees/<ID>` 是否存在隱含表示，不寫入 status file。
- Verify 三段式必須全過：① `tasks/task-status.md` 出現在 `git diff main` 且 agent 已將該 task 寫成 `Done`、② `Expected Paths` glob 至少 1 條命中 changed paths（排除 status file）、③ worktree 內 `make test` 通過。
- Verify 通過後自動 `git commit -m "task(<ID>): <title>"`（包含 status `Done`），自動 `gh pr create` 與 `gh pr merge --merge --delete-branch`，等 CI 綠後才 merge。
- Run-all 任一 task 失敗即 die；不跳過、不續跑下一個。中斷或失敗後重新呼叫 `make task-run-all` 即從 `next.sh` 找下一個可執行 task 接續；既有 worktree 視為可續跑，runner 直接重啟 agent（prompt 內提醒這是 resume）。
- 強制重跑單一 task 用 `--force`：同時繞過 deps / status 檢查 + 若 worktree 已存在則先刪後重建。Runner 在無 `--force` 時永不主動破壞既有 worktree / 分支。

## 2. 範圍

### 2.1 包含
- `tasks/task-list.md`、`tasks/task-status.md` 兩份 markdown table 之格式定義。
- `tasks/run/` 下之 shell script 集合（lib / next / show / prompt / run-one / run-all / verify）。
- `Makefile` 對應 target：`task-list` / `task-next` / `task-run-all` / `task-run` / `task-rerun`。
- 預設 agent 為 `claude -p`，提供 `TASK_AGENT_BIN` 切換點。
- Worktree 隔離、auto commit、auto PR、auto merge to main 全鏈路。
- Resume 規則（中斷後再啟動如何接續）。
- Force re-run 規則（單 task 強制重跑與既有 worktree 處理）。

### 2.2 不包含
- 自動拆解 phase 為 task 列項（人工先填 `task-list.md`）。
- 並行執行多個 task（一律單線序列；多 worktree 並行屬未來工作）。
- Task 內部子步驟的 state machine（brainstorm / plan / TDD / review 等都由 agent 自管）。
- 自動更新 `tasks/todo.md`（todo.md 仍是人類維護的 narrative；runner 只動 `task-status.md`）。
- 跨 repo / 遠端 task 派發（純本機）。
- 任何 SQL `todos` / `todo_deps` 表（AGENTS.md 「Phase 功能實作流程」§2 提到的 SQL 表不在本 runner 範圍；若未來導入，本 spec 需重訂）。

## 3. 檔案結構

```
0ops/
├── tasks/
│   ├── task-list.md            # 任務描述事實源（人工維護）
│   ├── task-status.md          # 任務狀態事實源（runner 與 agent 共寫）
│   ├── todo.md                 # 既有 narrative，不變
│   ├── lessons.md              # 既有，不變
│   └── run/
│       ├── lib.sh              # 共用 helper、路徑常數、agent runner、commit / merge cmd builder
│       ├── show.sh             # 印出全部 task + 狀態
│       ├── next.sh             # 找下一個可執行 task（Pending 且 deps 全 Done）
│       ├── prompt.sh           # 組單一 task 的 agent prompt
│       ├── run-one.sh          # 單跑一個 task（worktree + agent + verify + commit + merge）
│       └── run-all.sh          # 迴圈 next + run-one 直到無可執行 task
├── .worktrees/<TASK_ID>/       # 由 runner 建立，每個 task 一份
└── Makefile                    # 新增 task-* target
```

## 4. Task 事實源格式

### 4.1 `tasks/task-list.md`

固定為單一 markdown table，欄位順序不可變，runner 只認此格式：

```markdown
# Task List

| ID    | Title                                  | Dependencies | Spec / Plan Refs                                              | Expected Paths                                              |
|-------|----------------------------------------|--------------|---------------------------------------------------------------|-------------------------------------------------------------|
| M2.4  | K3s namespace isolation 最小可用版     | M2.1, M2.2   | docs/features/k3s-namespace-isolation/spec.md                 | `internal/server/services/k3s/**`, `internal/server/services/k3s/*_test.go` |
| M2.5  | winshare 子網域真實路由                | M2.4         | docs/features/winshare-subdomain-and-tunnel/spec.md           | `internal/server/services/winshare/**`, `internal/server/services/winshare/*_test.go` |
| M2.6  | Observability GA                       | M2.2         | docs/features/observability-skeleton/spec.md, docs/features/slo-and-alerting/spec.md | `internal/server/observability/**`, `deploy/**` |
| M2.7  | MCP preview/confirm description lint   | M2.1         | docs/features/mcp-tool-description-lint/spec.md               | `internal/mcp/**`, `internal/mcp/*_test.go`                |
| M2.8  | 端到端驗收腳本                         | M2.4, M2.5, M2.6, M2.7 | docs/features/create-app-flow/spec.md                  | `tasks/m2-*-e2e-*.sh`, `Makefile`                          |
```

欄位語意：

- **ID**：沿用 `tasks/todo.md` 既有編號；regex `^M[0-9]+\.[0-9]+$` 或 `^P[0-9]$` 等（runner 不限定，僅要求每列首格非空且唯一）。
- **Title**：commit / PR title 來源。
- **Dependencies**：逗號分隔 ID；空值或 `-` 視為無依賴。不支援 tehmag-foods 的 `T01~T05` range 語法（0ops task ID 非連續）。
- **Spec / Plan Refs**：prompt.sh 會把這些路徑列入 agent 必讀清單。
- **Expected Paths**：逗號分隔 glob（用 ` `` ` 包裹）；verify.sh 用 bash `[[ ]]` glob 比對 changed paths。

### 4.2 `tasks/task-status.md`

```markdown
# Task Status

| ID    | Title                                  | Status      |
|-------|----------------------------------------|-------------|
| M2.4  | K3s namespace isolation 最小可用版     | Pending     |
| M2.5  | winshare 子網域真實路由                | Pending     |
| M2.6  | Observability GA                       | Pending     |
| M2.7  | MCP preview/confirm description lint   | Pending     |
| M2.8  | 端到端驗收腳本                         | Pending     |
```

Status 列舉：`Pending` / `Done` / `Failed`（**無 `In Progress`**）。

- `Pending`：尚未開跑或已 reset。
- `Done`：agent 自己於完成後寫入（prompt 內明文要求），隨 PR merge 回 main。
- `Failed`：runner 在 verify / commit / push / PR / merge 任何環節失敗時，於 main 直接寫入 + 提交 `chore(task-runner): mark <ID> failed`。下次 run-all / run-one 不會自動續跑該 task，需 `--force` 才能重跑。

「執行中 / 可續跑」狀態**不寫入 status file**，改以 `.worktrees/<ID>` 是否存在隱含表示：
- worktree 存在且 status==Pending → resume 候選，runner 重啟 agent（prompt 提醒這是 resume）。
- worktree 存在且 status==Failed → 必須 `--force` 才能跑（會先刪掉 worktree）。
- worktree 不存在且 status==Pending → 全新開跑。
- worktree 不存在且 status==Done → 跳過（除非 `--force`）。

> **設計取捨**：tehmag-foods 模型把 `In Progress` 寫進 status file，但因為跨 worktree 與 main 之間 commit 順序會打架（runner 寫 `In Progress` 在 main vs agent 寫 `Done` 在 task 分支會在同一行衝突），改以 worktree 存在性表達。代價是「我目前在跑哪個 task」的 read 操作要 `ls .worktrees/`，不是 grep status file，但 `show.sh` 會把這資訊一起印。

## 5. Script 介面

### 5.1 `lib.sh` 對外常數與 helper

```sh
TASK_REPO_ROOT          # repo 根
TASK_LIST_FILE          # tasks/task-list.md
TASK_STATUS_FILE        # tasks/task-status.md
TASK_WORKTREE_DIR       # .worktrees
TASK_SESSION_DIR        # .task-sessions（暫存 prompt.txt）
TASK_AGENT_BIN          # 環境變數，預設空 → 走 claude -p

die "<msg>"
trim "<str>"
task_exists <ID>
task_title <ID>
task_status <ID>            # 讀 main 的 task-status.md
task_dependencies <ID>      # 展開為一行一 ID
task_expected_paths <ID>    # 展開為一行一 glob
task_spec_refs <ID>         # 展開為一行一路徑
task_is_executable <ID>     # status == Pending 且 deps 全 Done
task_branch_name <ID>       # task/<ID>
task_worktree_path <ID>     # .worktrees/<ID>
task_worktree_exists <ID>   # 是否已有 worktree（resume 判斷）
task_prompt_path <ID>       # .task-sessions/<ID>/prompt.txt

agent_runner                # 印出 argv 序列；TASK_AGENT_BIN 優先 → claude → 退路 die
build_agent_command <prompt_text>  # NUL-separated argv
build_commit_command <ID> <title>  # `git commit -m "task(<ID>): <title>"`
build_pr_command <ID> <title> <body>
build_merge_command <pr_number>    # `gh pr merge <#> --merge --delete-branch`

git_changed_paths_vs_main   # worktree 內 `git diff main --name-only` + untracked
path_matches_glob <path> <glob>...
mark_task_failed <ID>       # 在 main 寫 Failed + 提交 chore commit + push
```

### 5.2 `show.sh`

無參數。輸出表格 `ID | TITLE | STATUS | WORKTREE | DEPENDENCIES`。`WORKTREE` 欄為 `yes` / `no`，供使用者一眼看出哪些 task 處於可 resume 狀態。供 `make task-list`。

### 5.3 `next.sh`

無參數。掃 `task-list.md` 行序，找第一個 `task_is_executable == 0` 的 task，輸出：

```
TASK_ID=M2.4
TITLE=K3s namespace isolation 最小可用版
STATUS=Pending
DEPENDENCIES=M2.1, M2.2
```

無可執行 task 時 stderr 印 `ERROR: no executable task found` 並 exit 非 0；run-all 用此字串判斷收尾。

### 5.4 `prompt.sh <TASK_ID>`

輸出組好的 prompt 字串到 stdout。Prompt 模板：

```
你被 task runner 派來執行 Task <ID>：<Title>

工作環境：
- 你目前在 git worktree：.worktrees/<ID>
- 你的分支：task/<ID>
- 主分支：main
- 不要切到其他分支；不要 push（push 與 PR 由 runner 完成）
- 完成前不要 commit；commit 由 runner 完成

【強制流程】依 AGENTS.md「Mandatory Agent Loop Trigger」走完：
  using-superpowers → brainstorming → writing-plans OR executing-plans
  → test-driven-development → requesting-code-review → receiving-code-review
  → verification-before-completion → finishing-a-development-branch
（using-git-worktrees 步驟由 runner 預先建立，跳過該 skill）
（push / PR / merge 由 runner 完成，跳過 finishing-a-development-branch 內的 push 與 PR）

【先讀文件】依 AGENTS.md「Document Reading Order」順序：
- AGENTS.md
- docs/0ops-business-plan.md
- docs/0ops-plan.md
- docs/agents-guide.md
- docs/adr-reading-strategy.md（依其決策矩陣判斷 ADR 讀取深度）
- tasks/todo.md（找 <ID> 對應 acceptance bullets）
- tasks/lessons.md
- tasks/task-list.md
- tasks/task-status.md
<逐行展開 task_spec_refs <ID>>

【完成定義】
- todo.md 內 <ID> 對應 acceptance bullets 全部符合
- 對應測試補齊；高風險區（preview/confirm、idempotent、隔離、權限、簽章、reconciler）強制覆蓋
- dev 驗證走 compose + Makefile（不可在 host 直跑 binary）
- worktree 內 `make test` 必須通過
- 完成後將 tasks/task-status.md 中 <ID> 該列 Status 改為 Done
- 不要動其他 task 的 status
- 不要 commit；commit 由 runner 完成

【範圍硬限制】
- 只動 <ID> 範圍；任何順手修正一律不做（AGENTS.md「Commits」段）
- 若中途發現需新增 ADR，立即停止實作並回報（AGENTS.md「Document Reading Order」§ADR 讀取策略）
- 若發現 task 邊界錯（例如依賴未滿足、spec 不一致），停止實作並回報，不要強行繞過
```

### 5.5 `run-one.sh [--force] <TASK_ID>`

序列（在 main repo 工作目錄執行；step 6 之後切到 worktree）：

1. Parse flags；`task_exists <ID>` 否則 die。
2. Status / deps 檢查：
   - 無 `--force`：`task_status` 必須是 `Pending`（`Done` / `Failed` 直接 die）；`check_dependencies_done <ID>` 未 Done 即 die。
   - 有 `--force`：兩項全跳過。
3. Worktree 處理：
   - `.worktrees/<ID>` 不存在 → `git worktree add .worktrees/<ID> -b task/<ID>` 從 main 開分支。
   - 已存在且無 `--force` → 視為 resume：印 `RESUMING=<ID>`，沿用既有 worktree 與分支，跳到 step 5（不重新組 prompt 也行，但 prompt.sh 永遠 stateless 重組更安全 → 仍跑 step 5）。
   - 已存在且 `--force` → `git worktree remove --force .worktrees/<ID>` + `git branch -D task/<ID>` + 重建。
4. （無 status file 寫入；In Progress 狀態以 worktree 存在性表示。）
5. `prompt.sh <ID> > .task-sessions/<ID>/prompt.txt`。Resume 時在 prompt 開頭額外塞一行 `（這是 RESUME；worktree 內可能已有先前 partial 工作，請先檢視 git status 與既有檔案再決定下一步，不要從零重做）`。
6. `cd .worktrees/<ID>` 並執行 agent CLI（同步等待退出）；agent 退出非 0 → `mark_task_failed <ID>` + die。
7. Verify 三段式（在 worktree 內）：
   - **Section A — status**：`git diff main -- tasks/task-status.md` 非空，且 diff 內可見 `<ID>` 該列被改成 `Done`（grep `^\+.*<ID>.*Done`）。Agent 沒改即視為沒走完。
   - **Section B — expected paths**：`git diff main --name-only`（含 untracked、排除 `tasks/task-status.md`）至少 1 條命中 task 的 expected glob。
   - **Section C — tests**：`make test` exit 0。
   - 任一不過 → `mark_task_failed <ID>` + die。失敗時保留 worktree（人工進去看）。
8. Commit：`git add -A && git commit -m "task(<ID>): <title>"`。Commit signing enabled 時沿用 tehmag-foods 的 `prepare_gnupg_home` pattern。
9. Push：`git push -u origin task/<ID>`。
10. PR：`gh pr create --base main --head task/<ID> --title "task(<ID>): <title>" --body <auto>`，body 含「completes <ID>」+ task-list.md 該列引用 + spec refs。
11. 等 CI：以 `timeout ${TASK_CI_TIMEOUT:-1800} gh pr checks <#> --watch --interval 30 --required` 包裹（gh 本身無 `--timeout` flag）。exit 0 視為 CI 綠；任何非 0 → `mark_task_failed <ID>` + die；PR 不關，留人工接手。若 repo 尚未配置 CI / required check，`--required` 會立即失敗 → 由 `TASK_SKIP_CI_WAIT=1` 環境變數覆寫成「不等 CI 直接 merge」（僅供 CI 尚未上線的過渡期）。
12. Merge：`gh pr merge <#> --merge --delete-branch`。
13. 收尾：`cd` 回 main repo → `git worktree remove .worktrees/<ID>` → `git fetch origin && git checkout main && git pull --ff-only origin main`。Worktree 移除後 task-status.md 在 main 已是 `Done`。
14. 印 `COMPLETED=<ID>` + 該 PR URL。

### 5.6 `run-all.sh`

```sh
max_iterations=${TASK_RUN_ALL_MAX_ITERATIONS:-50}
count=0
while true:
  next_output = next.sh    # 失敗且訊息為 "no executable task found" → 印 ALL_TASKS_COMPLETED, exit 0
  task_id = parse(next_output)
  run-one.sh <task_id>     # 失敗 → die（不續跑）
  count++
  if count >= max_iterations: die "exceeded max iterations"
```

### 5.7 Makefile target

```make
task-list:
	@bash tasks/run/show.sh

task-next:
	@bash tasks/run/next.sh

task-run-all:
	@bash tasks/run/run-all.sh

task-run:
	@test -n "$(TASK)" || (echo "usage: make task-run TASK=<ID>" >&2; exit 1)
	@bash tasks/run/run-one.sh $(TASK)

task-rerun:
	@test -n "$(TASK)" || (echo "usage: make task-rerun TASK=<ID>" >&2; exit 1)
	@bash tasks/run/run-one.sh --force $(TASK)
```

## 6. Resume / Force / 失敗語意對照表

| 情境 | task-status | worktree | `make task-run-all` 行為 | `make task-run TASK=<ID>` 行為 | `make task-rerun TASK=<ID>` 行為 |
|------|-------------|----------|--------------------------|--------------------------------|----------------------------------|
| 從未開跑 | `Pending` | 不存在 | 跑（檢 deps） | 跑（檢 deps） | 跑（不檢 deps） |
| Ctrl-C 中斷 / agent crash | `Pending` | 存在 | 跑（resume 模式：沿用 worktree） | resume | 砍 worktree 重建跑 |
| Agent 完成 + merge 成功 | `Done` | 不存在 | 跳過 | die（status 非 Pending） | 跑（不檢 status） |
| Verify / CI / merge 失敗 | `Failed` | 留著（供查驗） | die（status==Failed） | die（status==Failed） | 砍 worktree + reset status 為 Pending + 跑 |
| 想 abandon 失敗的 task | `Failed` | 任何 | — | — | 人工：手動把 status 改回 `Pending` 並 `git rm -rf .worktrees/<ID>` |

關鍵不變式：

- **Runner 在無 `--force` 時永不破壞既有 worktree / 分支**。
- **Runner 永不直接修改 main 程式碼**，所有產品變更走 PR + `gh pr merge`。Runner 對 main 的直接 commit 僅限 `chore(task-runner): mark <ID> failed`（小型 status flip）。
- **失敗即 die**，run-all 不自動跳過下一個（避免 deps 受影響時雪崩）。
- **Resume 預設啟用**：worktree 存在 + status `Pending` 視為 resumable，無需任何 flag。Force 是「砍掉重練」的副作用觸發器。

## 7. 對 AGENTS.md 的對齊

- **Mandatory Agent Loop Trigger**：runner 將整個 Loop 委派給 agent；prompt 明文列出步驟。`using-git-worktrees` 與 push / PR 三步由 runner 接手（worktree 預建、commit 由 runner、push/PR/merge 由 runner），其餘步驟由 agent 自走。
- **Document Reading Order**：prompt.sh 將閱讀順序 1–7 全列入；feature 相關 spec 由 `Spec / Plan Refs` 欄補入。
- **Phase 功能實作流程 §2「SQL `todos` 與 `todo_deps`」**：本 runner 不採 SQL；以 markdown table 替代。若未來確需 SQL，重訂本 spec。
- **Testing**：verify 第三段強制 `make test`；prompt 內要求 agent 自己補測試。
- **Commits**：commit 訊息固定 `task(<ID>): <title>`；prompt 限制 agent 不可 commit、不可順手修正。
- **Documentation**：spec / plan / runbook 漂移由 agent 在 task 內處理；runner 不檢查文件變更（過嚴會把單純 bug fix 卡住）。

## 8. 與 tehmag-foods 版本的差異

| 項目 | tehmag-foods | 0ops 本 runner |
|------|--------------|----------------|
| Task ID | `T01..Tnn` 連續 | 沿用 `M2.4` 等既有編號 |
| 事實源位置 | `docs/51_task-list.md` / `docs/52_task-status.md` | `tasks/task-list.md` / `tasks/task-status.md` |
| 預設 agent | `copilot` (gh-copilot CLI) | `claude -p` |
| Prompt 流程 | 直接交代讀文件、改檔、改 status | 加上 Mandatory Agent Loop 全步驟 + ADR 讀取策略 |
| Verify 第三段 | 無 | `make test` 必過 |
| Auto PR | 無（只 commit） | `gh pr create` |
| Auto merge | 無 | `gh pr merge --merge --delete-branch`（等 CI 綠） |
| Worktree 強制重建 | 不支援 | `--force` 同時觸發 |
| Resume 模式 | 無顯式設計 | worktree 存在即自動 resume |
| Status 列舉 | `Pending` / `In Progress` / `Done` | `Pending` / `Done` / `Failed`（無 In Progress） |
| Dependency range 語法 | 支援 `T01~T05` | 不支援（ID 非連續） |

## 9. 驗收準則

- `tasks/task-list.md` 與 `tasks/task-status.md` 兩份範例檔成立，能被 `show.sh` / `next.sh` 正確 parse。
- `make task-list` 印出全表並包含實際 status。
- `make task-next` 在無可執行 task 時清楚回報 `no executable task found`。
- `make task-run TASK=<未存在 ID>` 立即 die。
- `make task-run TASK=<deps 未 Done>` 立即 die。
- `make task-run TASK=<合法>` 走完：建 worktree → agent → verify → commit → push → PR → 等 CI → merge → 清 worktree → status `Done`。
- `make task-run TASK=<已 Done>` 不重跑（status 已非 Pending）。
- `make task-rerun TASK=<已 Done>` 重建 worktree 並重跑。
- `make task-run TASK=<已 Failed>` die；`make task-rerun TASK=<已 Failed>` 砍 worktree + reset Pending + 跑。
- `make task-run-all` 中途 Ctrl-C 後再呼叫：若 worktree 仍在 + status==Pending，runner 直接 resume（同 ID 沿用 worktree，prompt 提醒 resume）；無需任何 flag。
- `make task-run-all` 中某 task verify 失敗，runner 寫 status==Failed + 整個 run-all die；下次 `make task-run-all` 不自動重跑該失敗 task。
- Agent 為 `claude -p`；改 `TASK_AGENT_BIN=copilot` 環境變數後可切換。
- `make test` 在 worktree 內可跑（沿用既有 Makefile target）。
- 自動 merge 採 merge commit（非 squash），分支自動刪除。

## 10. Open Questions

- 目前 0ops repo 尚未在 GitHub 設定 required CI check；`TASK_SKIP_CI_WAIT=1` 為過渡開關，待 CI 上線後預設改為「強制等 CI」。
- `make test` 目前在 0ops repo 含哪些 suite，是否會跑太久（>30 分鐘）影響 run-all UX？由實作期 spike 決定是否需 `TASK_TEST_CMD` 切換點。
- 多人協作時 `task-status.md` 衝突解法？暫定靠 git rebase 解；若頻繁衝突再考慮改用 line-oriented 格式或 SQL。
- `claude -p` 的權限策略（`--dangerously-skip-permissions` vs `--allowedTools`）？暫定走 `--dangerously-skip-permissions` 配合 worktree 隔離（worktree 之外的破壞由 runner 不該允許 → 未來可改 `--add-dir .worktrees/<ID>` + 限制工具集）。
- `.worktrees/` 與 `.task-sessions/` 是否該入 `.gitignore`？應該；實作期一併補。
- Status file 在「runner 寫 Failed」與「agent 在 PR 中寫 Done」可能在同一行衝突（人工把 Failed reset 為 Pending 後重跑時）。Mitigation：runner 在 reset 時就 commit 一次 `chore(task-runner): reset <ID> to pending`，讓 agent 的 PR 從乾淨基底開分支。
