# Runbook：GHA callback 驗章失敗排查

> 對應 spec：`docs/features/build-pipeline-and-callback/spec.md`（callback 簽章）
> 對應 ADR：ADR-0005 § build pipeline 與 callback
> 適用範圍：所有經由 GHA workflow 回呼 `/api/v1/deploys/{run_id}/callback` 的 deploy

## 1. 觸發條件

任一條件成立進本 runbook：

1. server log 連續出現 `callback rejected: invalid signature`（`internal/server/apps.go` `deployRunCallbackHandler`）
2. server log 出現 `stale callback timestamp`
3. GHA workflow run 顯示 callback step 收到 `401 invalid_signature` 或 `400 stale_timestamp`
4. `deploy_runs` 表上某 run 卡在 `building` / `pushing` 且 GHA workflow 已 success，反向佐證 callback 沒被接受

> 若只是偶發單次 reject（< 3 events / hour）先看 `audit_log` 是否同時記錄；可能是 retry 重送。連續 fail 才進 Step 2。

## 2. 簽章協定速查（spec / `validateCallbackSignature`）

- Header：`X-0ops-Timestamp`（Unix epoch seconds）、`X-0ops-Signature`（`sha256=<hex>`）、`X-0ops-Delivery-ID`
- HMAC payload：`timestamp + "." + body`
- Hash：HMAC-SHA256，server 端 `subtle.ConstantTimeCompare`
- Clock skew 容忍：±5 min
- Secret 雙軌：
  1. payload 中的 `ops_token`（per-deploy short-lived JWT，secret 為 token 本身字串）
  2. fallback `OPS_CALLBACK_SECRET` 環境變數
- Reject 路徑：`stale_timestamp` → `validateCallbackTimestamp` 失敗（HTTP 400）；`invalid_signature` → `validateDeployCallbackSignature` 失敗（HTTP 401）

## 3. 排查流程

整體預估時間：10 min。

### Step 1 — 確認失敗類別（≤ 2 min）

取出最近一筆 reject 的 `run_id`：

```bash
kubectl -n system-0ops logs -l app=0ops-server --tail=200 \
  | grep -E "callback rejected: invalid signature|stale callback timestamp" \
  | tail -5
```

對應 GHA log 找出該 run 送了什麼 header：

```bash
gh run view <workflow-run-id> --log | grep -E "X-0ops-(Timestamp|Signature|Delivery-ID)"
```

分支：

- 看到 `stale callback timestamp` → 進 Step 2A（時間問題）
- 看到 `callback rejected: invalid signature` → 進 Step 2B（secret 問題）
- 看到 `run_id mismatch` → 進 Step 2C（payload 篡改 / workflow YAML 帶錯 run_id）

### Step 2A — Clock skew（≤ 3 min）

```bash
kubectl -n system-0ops exec deploy/0ops-server -- date -u
gh api /meta --jq '.git_protocol_supported'  # 順便確認 GitHub API reachable
```

> GHA runner 時鐘由 Actions VM 同步，不可手調。若 server pod 時鐘飄移 > 5 min，重啟 pod 後核對：

```bash
kubectl -n system-0ops rollout restart deploy/0ops-server
```

若 server node 本身飄移，問 ops 修 chrony / NTP；ADR-0008 § migration 安全閘已要求 node 級時間同步。

### Step 2B — Secret 不一致（≤ 5 min）

確認 GHA `OPS_CALLBACK_SECRET` 與 server 端 env 同值：

```bash
# Server 端（注意：不要 echo 出來進 log）
kubectl -n system-0ops exec deploy/0ops-server -- printenv OPS_CALLBACK_SECRET | sha256sum

# GHA 端（在 workflow YAML 中明文比對；secret 本身只能由 repo owner 取出）
gh secret list --repo <org>/<repo>
```

兩端 sha256sum 應一致。若不一致：

1. 由 ops 從 secret manager 取出 canonical 值
2. `kubectl -n system-0ops create secret generic ops-callback --from-literal=secret=<val> --dry-run=client -o yaml | kubectl apply -f -`
3. `gh secret set OPS_CALLBACK_SECRET --repo <org>/<repo>`
4. rolling restart server：`kubectl -n system-0ops rollout restart deploy/0ops-server`
5. 觸發一次測試 deploy 驗證

若使用 per-deploy `ops_token` 路徑（payload 中帶 `ops_token` 欄位），檢查 token 本身是否過期（spec 上 short-lived）；可由 server log `slog.Info("callback received", "run_id", ..., "trace_id", ...)` 比對發放與接收的 token 是否同一支。

### Step 2C — run_id mismatch / payload tamper（≤ 5 min）

```bash
gh run view <workflow-run-id> --log | grep -B2 'callback' | grep -E 'run_id|RUN_ID'
```

對照 `deploy_runs.id`：

```bash
psql "$DATABASE_URL" -c "SELECT id, app_id, status, github_workflow_run_id FROM deploy_runs WHERE github_workflow_run_id = <workflow-run-id>;"
```

修法：通常是 workflow YAML template 取 run_id 取錯欄位（例如取了 `github.run_id` 而非 `deploy_run.id`）；修 `.github/workflows/*.yml` 對應 step 後重觸 deploy。

## 4. 失敗回退

- secret rotate 後仍 fail → 確認 server pod 真的接到新 env（`kubectl get pod -l app=0ops-server -o jsonpath='{.items[*].metadata.annotations.kubectl\.kubernetes\.io/restartedAt}'`）
- clock 修不好（雲端 host 時間異常）→ 暫時把該 deploy 標 `canceled` 走 `0ops redeploy <app-slug>` 重試；同時開 incident
- 持續 > 1h 無法解 → 走 incident escalation：`audit_log` 撈所有 reject events 留證，notify oncall

## 5. 演練要求

無強制演練。建議每次 secret rotate（quarterly）後手動觸發一次 deploy，確認 callback 仍綠。
