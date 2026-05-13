# Observability GitOps Assets

本目錄提供 M2.6 觀測 GA 所需的 GitOps 資產：

- Prometheus recording rules
- Prometheus alert rules（含 burn-rate）
- Grafana dashboard ConfigMaps

注意事項：

1. 指標前綴使用 `zeroops_`，因 Prometheus metric 名稱不可用數字開頭。
2. alert rule 必含 `severity`、`service`、`runbook_url`。

