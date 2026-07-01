#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

resume="false"
if [[ "${1:-}" == "--resume" ]]; then
  resume="true"
  shift
fi

[[ $# -eq 1 ]] || die "usage: $0 [--resume] <TASK_ID>"
task_id="$1"
task_exists "$task_id" || die "task not found: $task_id"
task_title_value="$(task_title "$task_id")"

if [[ "$resume" == "true" ]]; then
  printf '（這是 RESUME；worktree 內可能已有先前 partial 工作，請先檢視 git status 與既有檔案再決定下一步，不要從零重做）\n\n'
fi

cat <<EOF
你被 task runner 派來執行 Task $task_id：$task_title_value

工作環境：
- 你目前在 git worktree：$(task_worktree_path "$task_id")
- 你的分支：$(task_branch_name "$task_id")
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
- tasks/todo.md（找 $task_id 對應 acceptance bullets）
- tasks/lessons.md
- tasks/task-list.md
- tasks/task-status.md
EOF

while IFS= read -r ref; do
  [[ -n "$ref" ]] || continue
  printf -- '- %s\n' "$ref"
done < <(task_spec_refs "$task_id")

mapfile -t expected_paths_list < <(task_expected_paths "$task_id")
if (( ${#expected_paths_list[@]} > 0 )); then
  printf '\n【Expected Paths（task runner verify 契約）】\n'
  printf 'runner 在 verify 階段會檢查改動清單（含 untracked）至少有一條 path 符合以下 glob：\n'
  for glob in "${expected_paths_list[@]}"; do
    printf -- '- %s\n' "$glob"
  done
  printf '若你判定實作應用更貼語意的命名（package / 檔名），動工前停下來回報，由 user 決定要更新 tasks/task-list.md 還是讓你對齊現有 glob。不要在 worktree 裡靜默偏離 — verify 會失敗，runner 會把 task 標為 Failed。\n'
fi

cat <<EOF

【完成定義】
- todo.md 內 $task_id 對應 acceptance bullets 全部符合
- 對應測試補齊；高風險區（preview/confirm、idempotent、隔離、權限、簽章、reconciler）強制覆蓋
- dev 驗證走 compose + ./manage.sh（不可在 host 直跑 binary）
- worktree 內 \`./manage.sh test\` 必須通過
- 完成後將 tasks/task-status.md 中 $task_id 該列 Status 改為 Done，並把 Completed Date 欄填為當天日期（格式 YYYY-MM-DD，UTC 或本地皆可，只要與 todo.md 一致）
- 不要動其他 task 的 status 或 Completed Date
- 不要 commit；commit 由 runner 完成

【範圍硬限制】
- 只動 $task_id 範圍；任何順手修正一律不做（AGENTS.md「Commits」段）
- 若中途發現需新增 ADR，立即停止實作並回報（AGENTS.md「Document Reading Order」§ADR 讀取策略）
- 若發現 task 邊界錯（例如依賴未滿足、spec 不一致），停止實作並回報，不要強行繞過
EOF
