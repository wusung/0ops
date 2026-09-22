---
name: 0ops
description: 用 0ops CLI 把 repo 部署成線上 app、查部署狀態與 log、管理 app / 網域 / 團隊成員 / GitHub App / incident / audit log。當使用者要求「把這個 repo 部署上去」「deploy 到 0ops」「查部署狀態」「看部署 log」「重新部署」「列出我的 app」「刪掉某個 app」「邀請成員到 team」「裝 GitHub App」「查 incident」「查稽核紀錄」，或對應英文請求（deploy this repo, ship it, check deploy status, tail deploy logs, redeploy, list apps, delete app, invite member, install github app）時務必啟用；即使只說「幫我上線」也應觸發。不適用：0ops 後端本身的開發（改 backend/CLI 程式碼）→ 依 AGENTS.md。
---

# 0ops CLI

0ops 是 internal PaaS control plane。agent 對它的唯一入口是 `0ops` CLI；所有寫入走 preview → confirm 兩階段，確認權在使用者。

## 前置檢查

先確認登入與 team，再執行任何指令：

```bash
0ops auth status              # 無 token → 請使用者跑 0ops auth login --host=<backend>
0ops teams list --output json # 確認目標 team；必要時 0ops teams use <slug>
```

解析輸出時一律加 `--output json`。憑證來自 `auth.json` 或 `OPS_HOST` / `OPS_TEAM` / `OPS_BEARER_TOKEN`，**不得**把 token 寫進指令列。

## 唯讀查詢

```bash
0ops apps list --output json
0ops apps get <slug> --output json
0ops deploys status <slug> --output json
0ops deploys logs <slug> --limit 200        # --follow 只在使用者要求即時追蹤時用
0ops repo inspect <slug> --output json
0ops domains list <slug> --output json
0ops incidents list --status open --output json
0ops incidents get <id> --output json
0ops audit list --since 24h --output json   # 亦可 --actor me / --action create_app / --trace <id>
0ops audit get <id> --output json
0ops audit export --since 24h --format json # 匯出含完整性 manifest
0ops members list --output json
0ops teams github status
```

查部署失敗原因的標準順序：`deploys status` → `deploys logs` → 必要時 `repo inspect`、`incidents list`。

## 寫入：preview → confirm

**先 preview，呈報副作用，取得使用者明確同意，才執行 confirm。** 不得在同一回合內自行完成兩階段，不得用 `--yes` 略過確認。

建立並部署 app：

```bash
0ops apps create --slug <slug> --source <local-path|github-url> --ref main --dry-run
# 呈報 preview 的副作用清單 → 使用者同意後：
0ops apps create --slug <slug> --source <same> --ref main
```

重新部署：

```bash
0ops deploys redeploy <slug> --ref <branch> --dry-run
0ops deploys redeploy <slug> --ref <branch>          # 同意後
```

邀請成員：

```bash
0ops members preview-invite --github-login <login> --role member --output json
0ops members invite --preview-id <id-from-preview>   # 同意後
```

GitHub App：`0ops teams github install`（preview → confirm → 開瀏覽器 → 輪詢）；移除為 `0ops teams github uninstall`，屬破壞性操作。

登入新後端：`0ops onboard <host>`（等同 device-flow login），由使用者執行。

## 破壞性操作

`apps delete`、`members remove`、`teams github uninstall` 一律由使用者親自確認；agent 只負責先呈報影響範圍，不得代為輸入確認字串或加 `--yes`。

```bash
0ops apps delete <slug>              # 互動式要求輸入 app slug 才執行，交給使用者
0ops members preview-remove --user-id <id> --output json
0ops members remove --preview-id <id>
```

跨 team 寫入前必須複述目標 team，避免打到錯的 team。

## 不得由 agent 執行

- `0ops audit verify` — 稽核鏈驗證刻意只開放人類操作；可說明指令，不得代跑
- `0ops admin bootstrap-owner` — 一次性初始化，不可逆
- 任何在指令列帶明文 `--token` 的用法

## 關閉 incident

需使用者指示才執行，並帶上根因說明：

```bash
0ops incidents close <id> --note "<root cause>"
```

## 錯誤處置

- `auth` 相關錯誤 → 請使用者跑 `0ops auth login --host=<backend>`
- DNS / 連線失敗 → 後端位址可能已變更，確認 `OPS_HOST` 或 `auth.json` 內的 host
- preview 過期（TTL 10 分鐘）→ 重跑 preview，不得沿用舊 `preview_id`
- 權限不足 → 回報使用者，不嘗試改用其他 team 或旁路
