# Lessons

## L001｜dev 驗證走 compose / Makefile，不直接跑 host binary

- **情境**：M0 scaffold 完成後想用 `./bin/0ops-server` + `curl localhost:8080/health` 做 smoke test。
- **錯誤**：spec § 1 已硬性規定 dev 入口為 root `compose.yaml`、workflow 經 `Makefile` 收口；繞過會掩蓋 compose / Dockerfile 真正的問題，且與既已使用 8080 的 podman 容器發生 port 衝突。
- **規則**：
  1. dev 任何驗證都從 `make lint-compose` / `make dev` / `make migrate` 起；不在 host 直接 run binary 取代 dev stack。
  2. host binary build 用於：unit test / `golangci-lint` / `go test`；非取代 compose smoke。
  3. 若 host port 衝突，循 spec § 12 規劃中的 `compose.override.yaml` 機制處理，不擅改 root compose.yaml。

## L002｜task-runner 失敗 task 二次失敗時 silent exit 0

- **情境**：M3.1 verify 失敗 → `mark_task_failed` 改 task-status.md → 該行已是 Failed、寫回同值 → 後續 `git commit` 報 "nothing to commit" → `set -e` 殺 runner，但因為呼叫端是 `bash run-one.sh ... | tee log` 缺 `pipefail`，外殼看到 exit 0，誤判收尾完成。後續 verify-only 與 CI appear-timeout 路徑都會踩到同一坑。
- **錯誤**：`flip_task_status` / `mark_task_failed` 不 idempotent；tee pipeline 沒 pipefail，掩蓋真實退出碼。
- **規則**：
  1. `flip_task_status` 偵測新舊狀態相同立刻 no-op return，不寫檔不 commit。
  2. `mark_task_failed` 若 staged diff 為空就跳過 commit、正常返回。
  3. 所有 `bash <runner> | tee log` 形式呼叫前 `set -o pipefail`（或棄 pipe 直接 `>(tee log)` 結構）。

## L003｜runner 從本機 main 開 worktree → stale chore 進 PR → CI 不派發

- **情境**：M2.8 / M3.1 兩次失敗各自留下 `chore(task-runner): mark <ID> failed` commit 在本機 main，未 push。M2.8 之後另一次 task-run 成功並 merge 至 origin/main，但本機 main 仍帶兩筆舊 chore。對 M3.1 跑 `task-rerun` 時 worktree 從本機 main HEAD 開分支，PR 帶上這兩筆 stale chore → 與 origin/main `tasks/task-status.md` 同列三方衝突 → CI 不派發 → runner appear-timeout 後死。
- **錯誤**：worktree base 是漂移過的本機 main，harness 不對 origin 校驗；PR conflicting 時 runner 把 `no checks reported` 當「CI 沒準備好」處理，沒回報衝突。
- **規則**：
  1. `run-one.sh` 開 worktree 前 `git fetch origin && git merge-base --is-ancestor HEAD origin/main`；若 HEAD 不在 origin/main ancestor 集合內，先停下，由人決定要同步還是從 `origin/main` 直接開分支。
  2. `mark_task_failed` 的 chore 紀錄改成 `.task-sessions/<ID>/status.md` sidecar，不在 main 落 commit，避免本機與 origin 漂移。
  3. CI appear-timeout 觸發前先 `gh pr view --json mergeable` 並把 `CONFLICTING` 顯式 log + 走獨立 fail path（不要與 `CI did not pass` 共用訊息）。
