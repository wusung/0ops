# node-demo

0ops 之 example repo，用於 `local-file-repo` dev mode（ADR-0012）。
Paketo NodeJS buildpack 直接偵測；無 production 用途。

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
