# Runbook：GHA self-hosted runner（0ops production）

> 對應 spec：`docs/features/self-hosted-runner/spec.md`
> 適用範圍：在 0ops production K3s host 上跑 / 維護 / 切換 GitHub Actions self-hosted runner。
> 切換成本：刪 `vars.GHA_RUNNER_LABEL` 變數即可立即回 fallback `ubuntu-latest`。

## 1. 安裝

```bash
# 前置：.env.prod 已填好；本機 gh CLI 已 'gh auth login'
./manage.sh prod-install-runner
```

裝完印 `runner online: 0ops-prod-1`。

```bash
# 把 workflow 切過去
gh variable set GHA_RUNNER_LABEL --repo wusung/0ops --body 0ops-builder

# 驗
./manage.sh prod-runner-status
./manage.sh prod-runner-validate
```

## 2. 維護

### 2.1 升級 actions-runner

```bash
GHA_RUNNER_VERSION=2.319.5 ./manage.sh prod-install-runner
```

`install-runner.sh` 偵測 `$RUNNER_HOME/config.sh` 已存在則 skip 下載；要升版本，先在
remote 端：

```bash
ssh $PROD_HOST sudo systemctl stop 0ops-runner.service
ssh $PROD_HOST sudo rm -rf /opt/0ops-runner/{bin,externals,run.sh,config.sh,_diag}
./manage.sh prod-install-runner   # 帶新版本號重跑
```

### 2.2 升級 pack

```bash
PACK_VERSION=0.37.0 ./manage.sh prod-install-runner
```

腳本偵測 `pack version` 包含目標版本字串則 skip。

### 2.3 看 runner log

```bash
ssh $PROD_HOST sudo journalctl -u 0ops-runner.service -f
```

### 2.4 暫停（不撤銷註冊）

```bash
ssh $PROD_HOST sudo systemctl stop 0ops-runner.service
# workflow 會卡在 queued；切回 hosted：
gh variable delete GHA_RUNNER_LABEL --repo wusung/0ops
```

### 2.5 撤銷註冊

```bash
# 在 GitHub UI: Settings → Actions → Runners → 0ops-prod-1 → Remove
# 或：gh api -X DELETE /repos/wusung/0ops/actions/runners/<RUNNER_ID>
ssh $PROD_HOST sudo systemctl disable --now 0ops-runner.service
ssh $PROD_HOST sudo rm -rf /opt/0ops-runner /etc/systemd/system/0ops-runner.service
```

## 3. 切換 hosted ↔ self-hosted

| 動作 | 指令 | 生效時機 |
|---|---|---|
| Hosted → self-hosted | `gh variable set GHA_RUNNER_LABEL --body 0ops-builder` | 下次 workflow run |
| Self-hosted → hosted | `gh variable delete GHA_RUNNER_LABEL` | 下次 workflow run |

無破壞性。已 queued 的 run 不受影響（沿用觸發時的 label）。

## 4. 故障

| 症狀 | 排查 |
|---|---|
| `prod-install-runner` 卡在 verify 階段（runner 一直 offline） | ssh log 看 `0ops-runner.service` 是否 active；常見原因：podman.socket 沒起、權限 |
| workflow 卡 queued | 確認 `vars.GHA_RUNNER_LABEL` 值與 runner 註冊 label 對齊；runner 線上 |
| `pack` 找不到 | 重跑 install-runner.sh，會偵測缺少並安裝 |
| `permission denied (publickey)` | `.env.prod` 的 `PROD_SSH_KEY` 是否能 ssh 到 PROD_HOST；確認 `~/.ssh/authorized_keys` |
| `gh auth status` 失敗 | 本機 `gh auth login`；scope 需含 `repo` + `workflow` + `admin:org`（若 org-level） |
| job 內 `docker login ghcr.io` 失敗 | runner 跑 `podman login`；workflow 用 `docker` 命令時走 podman alias 或改 workflow（v1 用 docker CLI） |
| Pack cache 撑爆 disk | `ssh $PROD_HOST sudo -u ghrunner pack prune --all`；定期排 cron |

## 5. 觀測

### 5.1 runner 狀態

```bash
./manage.sh prod-runner-status
```

印：

- 註冊的 runners + status + label
- 最近 5 runs 與其 status / conclusion
- 當前 `vars.GHA_RUNNER_LABEL`（或 unset → fallback）

### 5.2 GitHub UI

`https://github.com/wusung/0ops/settings/actions/runners`

### 5.3 metrics

self-hosted runner 不自帶 Prometheus exporter；v1 觀察走 systemd `journalctl`
+ workflow run 結果。v1.1 評估 `actions-runner-controller` chart 提供 metrics endpoint。

## 6. 安全

- Runner 跑於 `ghrunner` user，不在 docker group；podman rootless。
- repo 設為 private 時，self-hosted runner 不接受 fork PR 之 build（GitHub 預設）；
  本 workflow 為 `repository_dispatch`，PR 不觸發。
- registration token TTL 1 hr；script run 後立即用，不入 .env.prod、不入 git。
- `/opt/0ops-runner` 權限 0700 ghrunner only。
