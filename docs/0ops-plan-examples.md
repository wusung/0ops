## 使用者腳本範例

### Pattern A：CLI 直接操作

```bash
$ 0ops auth login
→ 開啟 https://github.com/login/device，輸入 ABCD-1234
✓ 已登入為 mingxianliu
✓ 已建立預設 team：personal-mingxianliu（role=owner）

$ 0ops teams list
* personal-mingxianliu        (owner)        ← current
  acme-prod                   (admin)
  winshare-internal           (member)

$ 0ops teams use acme-prod
✓ current team 切換為 acme-prod

$ 0ops apps create nextdemo \
    --repo=https://github.com/vercel/next.js-helloworld \
    --ref=main

正在偵測 stack...
偵測結果：paketo Node.js builder, port 3000

即將執行：在 team `acme-prod` 建立 app `nextdemo`
副作用：
  - 在 0ops-gitops 建 apps/acme-prod/nextdemo/
  - 在 Cloudflare 註冊 hostname nextdemo.winshare.tw
  - 觸發初次 build (GitHub Actions)
preview 將於 10 分鐘後過期。

確認執行? [y/N] y

✓ deploy-run #abc123 已觸發（預計 4–6 分鐘）
  trace_id: 01J2K3M4N5P6Q7R8S9T
觀察進度：0ops deploys logs nextdemo --follow
```

也可在單條命令一次切 team：`0ops --team=acme-prod apps create ...`，CLI flag 優先於 current_team。

### Pattern B：claude code（透過 MCP）

> **使用者**：把 `vercel/next.js-helloworld` 接進來叫 nextdemo
>
> *Claude 呼叫 `list_teams` 取得當前 user 的 team；只有一個 `personal-mingxianliu` → 直接用*
> *Claude 呼叫 `inspect_repo({ team_slug: "personal-mingxianliu", url: "..." })`*
> *Claude 呼叫 `create_app_preview({ team_slug: "personal-mingxianliu", slug: "nextdemo", ... })`*
>
> **Claude**：偵測到 paketo Node.js, port 3000。我準備在 team `personal-mingxianliu` 建立 app `nextdemo`：
> - 在 0ops-gitops 建 apps/personal-mingxianliu/nextdemo/
> - 在 Cloudflare 註冊 hostname nextdemo.winshare.tw
> - 觸發初次 build
>
> preview_id `7f3e2a...` 將於 10 分鐘後過期。確認執行嗎？
>
> **使用者**：yes
>
> *Claude 呼叫 `create_app({ team_slug: "personal-mingxianliu", preview_id: "7f3e2a..." })`*
>
> **Claude**：deploy-run #abc123 已觸發（trace_id `01J...`）。我可以 follow logs，要看進度嗎？

### Pattern C：加自有域名（CLI）

```bash
$ 0ops domains add nextdemo example.com

即將執行：為 nextdemo 加入 extra hostname `example.com`
副作用：
  - 建立 DomainBinding（kind=extra）
  - 產生 verification token

確認執行? [y/N] y

✓ 已建立。請在 example.com 設定：
  CNAME: example.com → tunnel-abc123.cfargotunnel.com
  TXT:   _0ops-verify.example.com → 7f3e2a...

watch 模式：
$ 0ops domains verify nextdemo example.com --watch
...每 30s 查 DNS...
✓ 驗證通過，hostname 已上線
```

