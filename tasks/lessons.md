# Lessons

## L001｜dev 驗證走 compose / Makefile，不直接跑 host binary

- **情境**：M0 scaffold 完成後想用 `./bin/0ops-server` + `curl localhost:8080/health` 做 smoke test。
- **錯誤**：spec § 1 已硬性規定 dev 入口為 root `compose.yaml`、workflow 經 `Makefile` 收口；繞過會掩蓋 compose / Dockerfile 真正的問題，且與既已使用 8080 的 podman 容器發生 port 衝突。
- **規則**：
  1. dev 任何驗證都從 `make lint-compose` / `make dev` / `make migrate` 起；不在 host 直接 run binary 取代 dev stack。
  2. host binary build 用於：unit test / `golangci-lint` / `go test`；非取代 compose smoke。
  3. 若 host port 衝突，循 spec § 12 規劃中的 `compose.override.yaml` 機制處理，不擅改 root compose.yaml。
