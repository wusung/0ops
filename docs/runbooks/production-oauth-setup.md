# Runbook：production GitHub OAuth App 註冊

> 對應 spec：`docs/features/auth-login-flow/spec.md` § 4.0
> 對應 spec：`docs/features/production-deployment/spec.md` § 5.1
> 適用範圍：production rollout 前，註冊 0ops backend 用的 GitHub OAuth App，
> 取得 `GITHUB_OAUTH_CLIENT_ID` / `GITHUB_OAUTH_CLIENT_SECRET` 寫入
> `deploy/bootstrap/.env.prod`。

## 1. 結論

兩種走法，效果相同：

| 走法 | 適用 | 指令 |
|---|---|---|
| 助手腳本 | 預設 | `./manage.sh prod-setup-oauth` |
| 純手動 | 想自己點 dashboard | 走 § 3 手動步驟 |

## 2. 助手腳本流程

```
./manage.sh prod-setup-oauth
   ↓
讀 .env.prod 內 PROD_API_HOST → 算出 callback URL
   ↓
印出 pre-filled URL（已帶 application_name / homepage_url / callback_url）；
若 host 有圖形介面，自動 xdg-open / open
   ↓
user 在 GitHub 頁面按 Register application
   ↓
user 在腳本 prompt 貼 Client ID 與 Client Secret
   ↓
腳本驗 secret 非空 + Client ID 形如 `Ov23li...` / `Iv1.xxx` 任一格式
   ↓
腳本將 GITHUB_OAUTH_CLIENT_ID / SECRET / REDIRECT_URI 三行寫進 .env.prod
（若已有值且不同，先 prompt 是否覆蓋）
   ↓
腳本提示下一步：./manage.sh prod-verify-oauth
```

## 3. 純手動流程

1. 開啟 `https://github.com/settings/applications/new`（個人 OAuth App）
   - 或 `https://github.com/organizations/<ORG>/settings/applications/new`（組織用）
2. 填寫：

   | 欄位 | 值 |
   |---|---|
   | Application name | `0ops (<your-domain>)` 例：`0ops (api.jesontech.com)` |
   | Homepage URL | `https://<PROD_API_HOST>` 例：`https://api.jesontech.com` |
   | Authorization callback URL | `https://<PROD_API_HOST>/v1/auth/oauth2/callback` |
   | Enable Device Flow | **勾選**（CLI 預設走 device flow） |

3. Register application
4. 複製 Client ID
5. Generate a new client secret → 立刻複製（離開頁面後不可再讀）
6. 把兩值貼到 `deploy/bootstrap/.env.prod`：
   ```bash
   GITHUB_OAUTH_CLIENT_ID=<貼這>
   GITHUB_OAUTH_CLIENT_SECRET=<貼這>
   GITHUB_OAUTH_REDIRECT_URI=https://api.jesontech.com/v1/auth/oauth2/callback
   ```

## 4. 驗證（強烈建議在 prod-up 之前跑）

```bash
./manage.sh prod-verify-oauth
```

腳本對 GitHub 公開 device-code 端點 (`https://github.com/login/device/code`) 打一次，
驗 Client ID 有效（GitHub 會回 401 / 404 if invalid，回 200 + device_code 表 OK）。
不驗 Client Secret（device flow start 階段 GitHub 不查 secret；secret 在 backend
跑起來、user 第一次 login 才會用到）。

## 5. 常見錯誤

| 錯誤 | 排查 |
|---|---|
| `prod-up` 後 user 跑 `0ops auth login` 卡在 pending | 99% callback URL 不對；GitHub Settings → Applications → Your OAuth App 看註冊值是否符 `https://<host>/v1/auth/oauth2/callback`；不符就 update + 重跑 `prod-up`（envFrom 重灌 backend） |
| backend log 報 `invalid_client_id` | 把 .env.prod 的 ID 與 GitHub OAuth App 對比，常見漏字 |
| backend log 報 `invalid_client_secret` | 不能從 GitHub UI 重看舊 secret；按 Generate a new client secret → 重貼 → `bash deploy/bootstrap/seal-secrets.sh && kubectl apply -f deploy/bootstrap/tmp/sealed/` → `kubectl -n system-0ops rollout restart deploy/ops-server` |
| Device flow user code 進 GitHub 後回 `Could not authorize` | OAuth App 沒勾 Enable Device Flow；回 GitHub UI 勾上即可（無需新 secret） |
| `prod-verify-oauth` 回 `unauthorized_client` | OAuth App 雖建好但 Enable Device Flow 沒勾 |

## 6. 變更 Client ID / Secret 時

- 改 `.env.prod`
- 重跑 `bash deploy/bootstrap/seal-secrets.sh`
- `kubectl apply -f deploy/bootstrap/tmp/sealed/`
- `kubectl -n system-0ops rollout restart deploy/ops-server`
- `./manage.sh prod-verify-oauth`

不要 amend 已建好的 OAuth App 後不重啟 backend；backend 透過 envFrom 取 secret，pod 不重啟就讀不到新值。

## 7. 不在本 runbook 範圍

- GitHub App（不同於 OAuth App）的 install / uninstall：走 `docs/features/github-app-install-flow/`
- Org-level vs user-level OAuth App 的權限差異：留 ADR-0010 或 user 自決
- 已過期 secret 的 audit log 留存：v1 不做，留 v2
