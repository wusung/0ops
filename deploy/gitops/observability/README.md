# Observability GitOps Assets

本目錄提供 M2.6 觀測 GA 所需的 GitOps 資產：

- Prometheus recording rules
- Prometheus alert rules（含 burn-rate）
- Grafana dashboard ConfigMaps

注意事項：

1. 指標前綴使用 `zeroops_`，因 Prometheus metric 名稱不可用數字開頭。
2. alert rule 必含 `severity`、`service`、`runbook_url`。

## 驗證

兩道把關：

- Go 層結構驗證：`go test ./deploy/gitops/observability/...`（隨 `make test` 跑）
  - 必備 alert / recording rule 是否存在
  - `severity / service / runbook_url` 標籤必填
  - 必備 source metric 是否被引用
- promtool 完整 PromQL 驗證：`make lint-prom-rules`
  - 透過 `podman run --entrypoint promtool prom/prometheus` 直接跑 `promtool check rules`
  - 對應 spec § 11「Alert rule 表達式合法」驗收項與 M2.6「burn-rate rule 可被 promtool 驗證」

