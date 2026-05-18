# node-demo

0ops 之 example repo，用於 `local-file-repo` dev mode（ADR-0012）。
Paketo NodeJS buildpack 直接偵測；無 production 用途。

## 首次 setup（dev 機器只跑一次）

rootless podman 下 pack 之 lifecycle ephemeral container 因 uid mapping
不對稱無法讀掛載之 socket，必須先鬆綁 socket perms：

    make m5-6-podman-socket-loosen

效果維持至下次 `systemctl --user restart podman.socket`；host 重開機後
需重跑。詳見 `docs/features/dev-environment/local-file-repo.md` § 15。

## 使用

從 0ops repo 根目錄：

    make dev-create-example

或手動：

    bash examples/node-demo/bootstrap.sh     # git init + initial commit
    0ops apps create \
      --host http://localhost:8080 \
      --team personal \
      --slug node-demo \
      --repo-url file:///workspace/examples/node-demo \
      --ref main \
      --yes

驗證：

    0ops deploys status node-demo            # 期望 live
    curl http://localhost:5000/v2/0ops-apps/personal/node-demo/tags/list
