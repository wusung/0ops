# Task List

> 本檔為 task runner 的描述事實源。欄位順序固定：`ID | Title | Dependencies | Spec / Plan Refs | Expected Paths`。
> 維護規則：手動編輯；新增 task 時 status 預設 `Pending`（在 `task-status.md` 同步加列）。

| ID    | Title                                       | Dependencies            | Spec / Plan Refs                                                                       | Expected Paths                                                                                |
|-------|---------------------------------------------|-------------------------|----------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------|
| M2.1  | create_app orchestration 落地              | -                       | docs/features/create-app-flow/spec.md                                                  | `internal/server/services/createapp/**`                                                       |
| M2.2  | GitHub Actions dispatch + callback 全鏈路   | M2.1                    | docs/features/build-pipeline-and-callback/spec.md                                      | `internal/server/services/workflowdispatch/**`, `deploy/workflows/deploy-app.yml`             |
| M2.3  | GitOps render/push + ArgoCD sync 鏈路       | M2.2                    | docs/features/gitops-render-and-argocd/spec.md                                         | `internal/server/services/gitops/**`                                                          |
| M2.4  | K3s namespace isolation 最小可用版          | M2.1, M2.2              | docs/features/k3s-namespace-isolation/spec.md                                          | `internal/server/services/k3s/**`                                                             |
| M2.5  | winshare 子網域真實路由                     | M2.4                    | docs/features/winshare-subdomain-and-tunnel/spec.md                                    | `internal/server/services/winshare/**`                                                        |
| M2.6  | Observability GA                            | M2.2                    | docs/features/observability-skeleton/spec.md, docs/features/slo-and-alerting/spec.md   | `internal/server/observability/**`, `deploy/**`                                               |
| M2.7  | MCP preview/confirm description lint 契約   | M2.1                    | docs/features/mcp-tool-description-lint/spec.md                                        | `internal/mcp/**`                                                                             |
| M2.8  | 端到端驗收腳本                              | M2.4, M2.5, M2.6, M2.7  | docs/features/create-app-flow/spec.md                                                  | `tasks/m2-*-e2e-*.sh`, `Makefile`                                                             |
