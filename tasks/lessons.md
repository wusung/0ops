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

## L004｜`gh pr merge` 在 worktree 內呼叫 → server merge 後 gh local cleanup 失敗 → runner 誤報 Failed

- **情境**：M3.1 / M4.1 兩次踩到。runner step 8d `( cd "$worktree_path" && gh pr merge $num --merge --delete-branch )`。gh 先 call GitHub API 完成 server-side merge，再嘗試 `git checkout main` 做 local cleanup（清掉 task branch）；但 main 已被主 worktree 佔住 → `'main' is already used by worktree` → gh exit 非 0。runner 視為 merge 失敗 → mark_task_failed → 又一個 stale chore commit 到本機 main（同 L003 模式）。
- **錯誤**：cd 到 worktree 對 `gh pr merge $num` 沒必要（PR 編號定位），反而引入 local branch 衝突；且 gh 非 0 退出不等於 merge 失敗。
- **規則**：
  1. step 8d 必須從主 repo root 呼叫（`cd "$TASK_REPO_ROOT"`），不要 cd 到 worktree。
  2. gh pr merge 退出非 0 時，先 `gh pr view $num --json mergedAt` 校驗 server 端，`mergedAt` 非空就視同 merge 成功（純 log 警告）；只有 mergedAt 為空才走 mark_task_failed。
  3. push / pr create / checks 可保留在 worktree 內（不會觸發 local branch 操作）；只 merge 那一步要避開 worktree cwd。

## L003｜runner 從本機 main 開 worktree → stale chore 進 PR → CI 不派發

- **情境**：M2.8 / M3.1 兩次失敗各自留下 `chore(task-runner): mark <ID> failed` commit 在本機 main，未 push。M2.8 之後另一次 task-run 成功並 merge 至 origin/main，但本機 main 仍帶兩筆舊 chore。對 M3.1 跑 `task-rerun` 時 worktree 從本機 main HEAD 開分支，PR 帶上這兩筆 stale chore → 與 origin/main `tasks/task-status.md` 同列三方衝突 → CI 不派發 → runner appear-timeout 後死。
- **錯誤**：worktree base 是漂移過的本機 main，harness 不對 origin 校驗；PR conflicting 時 runner 把 `no checks reported` 當「CI 沒準備好」處理，沒回報衝突。
- **規則**：
  1. `run-one.sh` 開 worktree 前 `git fetch origin && git merge-base --is-ancestor HEAD origin/main`；若 HEAD 不在 origin/main ancestor 集合內，先停下，由人決定要同步還是從 `origin/main` 直接開分支。
  2. `mark_task_failed` 的 chore 紀錄改成 `.task-sessions/<ID>/status.md` sidecar，不在 main 落 commit，避免本機與 origin 漂移。
  3. CI appear-timeout 觸發前先 `gh pr view --json mergeable` 並把 `CONFLICTING` 顯式 log + 走獨立 fail path（不要與 `CI did not pass` 共用訊息）。

## L005｜RoutingDispatcher vs 擴 ClientPayload — 介面契約優於 schema 擴張

- **情境**：M5.6 引入 LocalBuildDispatcher 後，create_app confirm 階段需在 GHA 與本地 build 兩條路徑間分派。第一直覺是擴 `workflowdispatch.ClientPayload` 加 `RepoURL` 欄位讓 dispatcher 自行判斷。
- **問題**：擴 payload 等於 production GHA 與 dev 共享 schema，未來只要任一 dispatcher 想看更多 context 就會持續擴。介面變寬，contract 變脆。
- **解法**：新增 RoutingDispatcher 包兩個 sub-dispatcher，依 `payload.TeamSlug + payload.AppSlug` 反查 `db.GetAppRepoURLByTeamAndAppSlug` 來路由。
- **規則**：dispatcher 介面契約穩定優先於 schema 擴張；分派邏輯放在 wrapping dispatcher 自己負責，避免 GHA 路徑被 dev 需求拉動。ADR-0012 § 3.2。

## L006｜podman socket mount 屬 high-trust，server boot 必加 production panic

- **情境**：M5.6 將 host podman socket 掛入 server 容器以跑 `pack build --publish`。若 production compose 模板誤抄此 mount，server 進程即取得 host root-equivalent 容器管控權。
- **問題**：靠 CI lint 阻止「production chart 不含 LOCAL_*」是 best-effort；若 lint 漏網則直接失守。
- **解法**：`internal/shared/runtime.AssertProductionSafe()` 在 `cmd/server/main.go` 啟動最前段檢查 `OPS_ENV=production + 任一 LOCAL_*_ENABLED=true 或 LOCAL_REGISTRY 非空` 立即 panic，配套單元測試與 boot integration 自我保護。
- **規則**：dev 引入 high-trust 依賴時，production 防呆必須在 runtime（panic）+ test（panic 覆蓋）兩層；只在 lint 或 compose comment 註明不夠。ADR-0012 § 3.1 / hard rule #1。

## L007｜examples/<dir> 內 `git init` 會被外層 git 偵測成 submodule

- **情境**：M5.6 加 examples/node-demo 並由 bootstrap.sh 跑 `git init` 產出 inner `.git/`。第一次 `git add examples/` 後外層把整個目錄當 gitlink，commit 留下空 submodule reference。
- **解法**：bootstrap 是 dev runtime 步驟而非 outer commit 步驟；外層 `.gitignore` 排除 `examples/*/.git/` + `examples/*/node_modules/`，且 commit 前若 inner `.git/` 已存在需先移除。
- **規則**：examples/ 樹下任何「在 dev 環境啟動時動態生成」的 git 物件，必須 .gitignore；outer commit 階段不可包含。對 e2e 腳本顯式呼叫 bootstrap 以維持 idempotent。

## L008｜rootless podman + pack lifecycle 之 uid mapping 不對稱

- **情境**：M5.6.1 完成 dispatcher split pack/push 後 e2e 揭露：pack `pack build` 在 server 容器內 exec OK，但 pack 起的 lifecycle ephemeral container（host podman spawn，不在 compose network 內）對 bind-mount 之 host podman socket 報 `permission denied`。
- **根因**：rootless podman 對 lifecycle container 之 cnb user（container uid 1000）採 subuid 映射至 host uid `100999`，與 socket owner host uid 1000 不同；socket perms 預設 `0660` → lifecycle 內 process 既非 owner 也非 group，無 r/w。
- **解法（M5.6.2 採行）**：文件化「dev 機器首次跑 `./manage.sh podman-socket-loosen` 把 socket 改 `0666`」；`tasks/local-build-e2e.sh` preflight 偵測 perms，0660 直接 fail-fast 並印指引。chmod 在 OPS_ENV=production 時 refuse 執行。
- **不採行的替代**：rootful socket（增 host 維護面 + 需 sudo）；server container `userns=keep-id`（牽動其他 mount 的 uid 假設）。
- **規則**：mount host high-trust resource 至 container 後若進一步被 pack/buildah/skopeo 等工具 re-bind 至 lifecycle ephemeral container，必須評估三層 uid mapping（host / 外層 container / lifecycle container）是否一致；不一致就要在 setup 文件中明示步驟，不要等 e2e 階段才發現。ADR-0012 § 6.2 / sub-spec § 15。

## L009｜M6 app-source-ingestion (T1-T23) 收尾複盤

- **情境**：M6 feature 之 23-task 序列實作，CLI → server multipart upload → ingest tree → GHA workflow fetch via JWT；spec docs/features/app-source-ingestion/spec.md。
- **撈到的 Critical issue（review-time）**：
  1. T6 `writeSymlink` 對 absolute `Linkname` 未擋 → 可在 disk 上植入指向 `/etc/passwd` 之 symlink
  2. T8 multipart bomb 無 part count cap → DoS 向量
  3. T8 雙 archive part overwrite → 第二 archive 蓋 disk
  4. T16 packer 無 `io.LimitReader` 之 cap → 單大檔已全寫 out 才報錯
  5. T16 packer Lstat→Open→Copy 之 TOCTOU 窗口
- **撈到的 Important（review-time）**：
  1. T9 archive download 之 `Subject ↔ UploadID` 未 cross-check
  2. T7 `Verify` 無 empty-secret guard（fail-open）
  3. T11 Source path 欄位驗 trim 但不 write-back（T12 吃 padded 字串）
  4. T11 createAppHandler 之 `req.RepoURL` 為空導致 summary string regression
  5. T14 `archiveSigner.(*ingestion.TokenSigner)` brittle type assertion
  6. T14 `workflowdispatch/testing.go` 為 production-compiled 之 test helper
  7. T17 server 早期 4xx + mid-stream packErr 之 priority chain（broken pipe 掩蓋 401/413）
  8. T18 empty directory upload silent 成功害下游
  9. T19 audit Result 用未 SELECT 之欄位 → production audit 永遠記 `0` / `""`
  10. T21 metric 無 `zeroops_` namespace prefix（Prometheus rename 是 destructive）
  11. T22 e2e SQL injection 介面（`$TEAM_SLUG` 直接內插）
- **規則**：
  1. **CLI library 等級之 cap 必須在 stream 寫 out 之前 enforced**，不能 post-check（T16 critical bug 模式）。`capWriter` 包在 zstd 之前，wire 失敗立即中止。
  2. **DB query / SQL 寫入腳本必須走 prepared parameter**（`-v var=value` + `:'var'`）—— 即使 dev script 也適用
  3. **Audit Result map 只放 SELECT 之欄位**：T19 教訓 —— 若 Result 引用 `ListExpiredUploads` 沒 SELECT 之欄位，production 永遠記 zero/empty。要嘛 SELECT 多欄位，要嘛 drop 那些 Result key。
  4. **Prometheus metric 之 namespace 在第一個 release 前必須對齊**：counter rename 是 storage-level destructive；T21 重訂 namespace 是 last-mile fix
  5. **Type-safe sentinel 取代字串內容耦合**：T20 `quotaError.Reason` 之 string-match 模式被 T21 review 抓出來；改用 typed `QuotaDimension` 是 compile-time-safe 升級
  6. **Defense-in-depth 在 review 中常被 surface**：`tar --no-same-owner`（T15）、`trap EXIT` cleanup、Open-then-Stat 取代 Lstat-Open-Copy 都是 reviewer 加的層
- **過程觀察**：
  - 兩階段 review（spec → code quality）連續 23 task 撈出 5 critical + 數十個 important；平均 fix 1 task < 5 min（reviewer 提供精準 diff suggestion）
  - 每個 PR 之 fix commit 平均 1.5 個（每 task 平均 2 commit：feat + fix）
  - dev 環境本身（compose stack）為 T1-T21 之舊 binary 無法跑 T22 e2e；要驗端到端需 rebuild，超出 task 範圍

## verify 任務在 audit 結束後常變 fix 任務（2026-05-29）

`trace_id 全鏈路驗證` task 原始描述為 verify-only，但 audit 結果發現 3 個 critical
gap（middleware ctx 注入、preview.trace_id 欄位、callback 缺 audit.Log）已違 ADR-0005
/ ADR-0006。實作期間又陸續發現：

- callback handler 不是「呼叫但 trace_id 錯」而是「完全沒寫 audit_log」
- `confirmRedeployHandler` 讀 `middleware.GetReqID` 而非 `audit.TraceIDFromContext`，
  同一 bug class 散落 `confirmCreateAppHandler` / `confirmDeleteAppHandler` 也有
- 專案 `R2_not_null_three_step` lint 禁 `NOT NULL DEFAULT` 一步寫法，需 nullable +
  app-layer sentinel 模式（與 sibling trace_id 欄位一致）

**Pattern：** 標 verify 的 task 進場前要先區分

- 真 verify（only grep + 寫 test 鎖住現狀）
- 隱含 fix（audit 結束發現規格未實作 → 應建獨立 task 並改 PR 範圍）

**動作：** audit 階段結束後立即更新 spec（accurate ground truth），避免 plan 階段用
舊假設展開。本次 C3 spec 重寫一次、Task 1 migration 因 lint 重做一次、Task 5
surface 隔壁 handler 同 bug 再補 fix 一次 — 都是延遲修正導致 plan 重寫的成本。
往後 audit subagent 報告必須區分「已存在但 broken」與「完全缺失」兩種 gap。

## L010｜dev compose 在 systemd-resolved host 上需要 podman 全域 DNS override

- **情境**：2026-05-29 跑 `./manage.sh dev-create-example`，server 容器卡 `building...`
  永不 healthy；server log 顯示 `lookup proxy.golang.org on 10.89.13.1:53: i/o timeout`。
  改 `compose.override.yaml` 給 server 加 `dns: [1.1.1.1, 9.9.9.9]` 後 server 通了，
  但 pack lifecycle ephemeral container 在 ANALYZING/BUILDING phase 又踩同樣坑：
  `lookup nodejs.org on 10.89.14.1:53: i/o timeout`（注意 14.x 是另一個 podman 網段，
  由 host podman 經 `DOCKER_HOST` 另起，**不走 compose**）。
- **錯誤**：
  1. 只在 compose 層補 DNS 不夠，pack 的 ephemeral container 吃的是 podman 全域 DNS。
  2. 第一次寫 `~/.config/containers/containers.conf` 把 `dns_servers` 放在 `[network]`
     section，podman 不報錯但完全不生效。正確是 `[containers].dns_servers`。
  3. 症狀根因：host `/etc/resolv.conf` 只指 `127.0.0.53`（systemd-resolved stub），
     aardvark-dns 無法把 query 轉發到任何 container 可達的上游。
- **規則**：
  1. 這台 host 跑任何 rootless podman buildpack 流程前，先確認
     `~/.config/containers/containers.conf` 有 `[containers] dns_servers = ["1.1.1.1", "9.9.9.9"]`。
  2. 若同時要動 compose container 的 DNS，用 `compose.override.yaml` 加 `dns:` block，
     不改 root `compose.yaml`（同 L001 § 規則 3）。
  3. log pattern 看到 `lookup <host> on 10.89.x.1:53: i/o timeout` 直接定位這條規則，
     不要往 GOPROXY、proxy.golang.org outage、podman socket perms 等方向歪。
  4. 改 `containers.conf` 後不需要 restart podman；新 container 立即生效，既有 long-running
     container 要 recreate（或走 compose `up -d <svc>`）。

## L011｜移除 normalize/compat shim 前，先確認「誰共用該欄位」與「消費端讀哪個欄位」

- **情境**：2026-06-09 做 M8 移除 deprecated github-via-`repo_url` alias。看似刪 3 個檔的小事，
  實際踩到兩個 spec 內部張力 + 一個差點上線的 prod bug。
- **兩個刪除前必查的點（都靠提問 user / 讀 code 才浮現，不是憑印象）**：
  1. **欄位共用**：`repo_url` 同時承載「github URL（deprecated）」與「dev `file://`（ADR-0012,
     spec § 2.2 明文保留）」。照字面全刪會打爛 dev `file://` 與 `dev-create-example`/`e2e-create-app`。
     → 規則：刪一個「語意」前，grep 該欄位/flag 的**所有** scheme 分支，逐一確認哪些是要保留的。
  2. **介面缺口**：MCP `create_app_preview` 當時只收 `repo_url`、無 `source` 欄位，移除 server
     normalize shim 會讓 MCP github 建立無替代路徑。→ 規則：移除「對外只剩 X」的 shim 前，確認
     **所有** client 介面（API/CLI/MCP）都已支援 X。
- **差點上線的 bug（code review 才抓到）**：normalize shim 原本把 github `repo_url` 正規化成
  `Source`，但 confirm/gitops/dispatch pipeline 一直只讀 **top-level `payload.RepoURL/Ref`**，
  不讀 `Source.GitHub`。刪 shim 後，Source-only github 請求會以**空 repo URL/ref 部署**。
  （其實 `--source github` 在 M8 前就已壞，只是當時 github 真正能動的是 repo_url 路徑而被掩蓋。）
  → 規則：移除一個「正規化/橋接」步驟時，必查**下游消費端讀的是哪個欄位**；若消費端讀的是被
  正規化「出來」的欄位，刪掉橋接 = 消費端拿到空值。測試要斷言值有流到消費端（dispatch payload /
  stored row），不能只斷言 `SourceKind` 之類的分類欄位——本案舊測試正是只測 kind 才漏掉。
- **流程價值**：本案 brainstorming 階段兩次 STOP 提問釐清範圍（避免 1、2），requesting-code-review
  抓到第 3 點。三道關卡缺一都會出事。驗證測試務必覆蓋「值的端到端流動」，非僅型別/分類。

## L012｜hash-chain verify 必須抓「完整鏈」而非使用者時間視窗（M9.1 slice d）

- **情境**：M9.1 `0ops audit verify --since/--until` 第一版直接用使用者視窗呼 export，再對
  回傳的 row 重算鏈。code-review subagent 抓到 CRITICAL：鏈是 per-(team, month)，`VerifyChain`
  自 genesis 重算並核對 `audit_chain_head.row_count`（整月總數）與 linkage（首筆 prev_hash==genesis）。
  使用者給次月視窗（如 `--since 24h`）會切掉月初的 row → 首筆 prev_hash≠genesis + count 不符 →
  **false BREAK**（明明沒被竄改卻報失敗）。
- **解法**：verify 與 export 分流。export 照使用者視窗逐字輸出；verify 走 `fetchChainsForVerify`：
  把視窗展開到「觸及的每個月完整邊界」（`monthsInRange`），逐月完整抓取再重算，保住 completeness 語意。
- **規則**：
  1. 任何「自 anchor/genesis 重算並核對總數」的完整性驗證，輸入必須是**該鏈的完整集合**，
     不能是呼叫端任意子區間；驗證層要自行補齊到鏈邊界。
  2. partition/anchor 內含「全量計數」（row_count）時，凡拿子集去比對 count 必假陽性——
     先確認資料粒度（整鏈 vs 視窗）再寫比對。
  3. 角色拆分 migration 的 Down 要用 `REASSIGN OWNED ... TO CURRENT_USER` 再 `DROP OWNED`/`DROP ROLE`；
     直接 `DROP OWNED BY` 會連該角色「擁有的表」一起刪（production 以 `0ops_migrate` 跑 migration 時
     等於刪光 schema）。

## L013｜task-runner 的 `claude -p` agent 無法中途收人類決策（M9.2）

- **情境**：`manage.sh task-run M9.2`（compliance-framework-mapping）。spawned `claude -p` agent
  正確依 prompt「發現 spec 不一致就停下回報」揪出真問題：spec 把已 ship 的控制（M9.1 audit
  append-only/export、M5.4/M5.5/M2.6 HA/PITR/SLO）標成 `規劃中`，且 §3 拿「M5 HA/DR」當 `規劃中`
  範例已失準。升 `已具備` 涉及對審計斷言可用性，agent 要 sign-off。但 `-p` 是一次性、無法收答案 →
  agent 結束、未翻 status → runner `VERIFY_FAILED=status` → 標 Failed、保留 worktree。
- **這是 harness 設計要的行為**（停下回報優於 bulldoze），非 bug。
- **規則**：
  1. 純文件/需人類判斷（狀態準則、對外合規斷言、範圍取捨）的 task **不適合無人值守 task-runner**。
     要嘛把**決策預先寫進 `tasks/todo.md` 對應 acceptance bullets** 再 `task-run`，要嘛人工完成後
     用 `task-run --verify-only` 走 runner 的 verify+merge。
  2. 自足 code task（明確 spec + 可測，如 M9.1）才是 task-runner 的甜蜜點——它能一氣呵成
     implement→test→PR→CI→merge。
  3. runner verify 較弱（只查 status 翻 Done + 一條 expected-path + `manage.sh test`），不保證
     完整性；高風險 task（安全/合規）合入後仍值得人工複審一眼。
