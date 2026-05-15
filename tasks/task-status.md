# Task Status

> 由 task runner 與 agent 共寫；人類維護僅限 reset (Failed → Pending) 場景。
> 狀態列舉：`Pending` / `Done` / `Failed`。執行中 / 可續跑狀態以 `.worktrees/<ID>` 是否存在隱含表示。

| ID    | Title                                       | Status   |
|-------|---------------------------------------------|----------|
| M2.1  | create_app orchestration 落地              | Done     |
| M2.2  | GitHub Actions dispatch + callback 全鏈路   | Done     |
| M2.3  | GitOps render/push + ArgoCD sync 鏈路       | Done     |
| M2.4  | K3s namespace isolation 最小可用版          | Pending  |
| M2.5  | winshare 子網域真實路由                     | Pending  |
| M2.6  | Observability GA                            | Pending  |
| M2.7  | MCP preview/confirm description lint 契約   | Pending  |
| M2.8  | 端到端驗收腳本                              | Pending  |
