# Task Status

> 由 task runner 與 agent 共寫；人類維護僅限 reset (Failed → Pending) 場景。
> 狀態列舉：`Pending` / `Done` / `Failed`。執行中 / 可續跑狀態以 `.worktrees/<ID>` 是否存在隱含表示。
> `Completed Date` 為 `YYYY-MM-DD`；尚未完成填 `-`。

| ID    | Title                                       | Status   | Completed Date |
|-------|---------------------------------------------|----------|----------------|
| M1    | Read-only API + CLI + MCP + RBAC            | Done     | 2026-05-12     |
| M1.5  | Identity bootstrap + team member provisioning | Done   | 2026-05-11     |
| M2.1  | create_app orchestration 落地              | Done     | 2026-05-13     |
| M2.2  | GitHub Actions dispatch + callback 全鏈路   | Done     | 2026-05-13     |
| M2.3  | GitOps render/push + ArgoCD sync 鏈路       | Done     | 2026-05-13     |
| M2.4  | K3s namespace isolation 最小可用版          | Done     | 2026-05-15     |
| M2.5  | winshare 子網域真實路由                     | Done     | 2026-05-15     |
| M2.6  | Observability GA                            | Done     | 2026-05-15     |
| M2.7  | MCP preview/confirm description lint 契約   | Done     | 2026-05-15     |
| M2.8  | 端到端驗收腳本                              | Done     | 2026-05-15     |
| M3.1  | 客戶自有域名 DNS verify (24h TTL + extend)  | Done     | 2026-05-16     |
| M3.2  | GitHub App install/uninstall 流程           | Done     | 2026-05-15     |
| M4.1  | Webhook auto/manual redeploy + replay protection | Done     | 2026-05-16     |
| M4.2  | Rate limit (per-token / per-team) + 429     | Done     | 2026-05-16     |
| M5.1  | delete_app 安全刪除 + 資源清理              | Done     | 2026-05-16     |
| M5.2  | audit_log + audit CLI/MCP                   | Done     | 2026-05-16     |
| M5.3  | reconciler GA + incident classification     | Done     | 2026-05-16     |
| M5.4  | Postgres HA + WAL archive + PITR 演練       | Done     | 2026-05-16     |
| M5.5  | Backend 2 replica + Leader election         | Done     | 2026-05-16     |
| M5.6  | Local file repo + local build pipeline (dev mode) | Done   | 2026-05-19     |
| M5.6.1 | Split pack/push + rewrite imageRef to LOCAL_REGISTRY | Done   | 2026-05-19     |
| M5.6.2 | Document rootless podman socket perms + e2e preflight | Done   | 2026-05-19     |
| M5.6.3 | Pack --docker-host=inherit + libpod push + e2e truth source | Done   | 2026-05-19     |
| M6    | App source ingestion（production file source） | Done | 2026-05-21     |
| M8    | Remove deprecated github repo_url alias (Q6) | Done | 2026-06-09     |
| M9.0  | Threat model (STRIDE 系統威脅模型)          | Done     | 2026-06-28     |
| M9.1  | Audit append-only + tamper-evidence + export/verify | Done     | 2026-06-29     |
| M9.2  | Compliance framework mapping (PDPA/SOC2 控制對應) | Done     | 2026-06-29     |
| M9.3  | Security hardening                          | Done     | 2026-06-29     |
| M9.4  | Supply-chain security                       | Done     | 2026-06-29     |
| M9.5  | SSO/OIDC + 集中撤權 [^m95e2e]               | Done     | 2026-06-29     |
| M9.6  | Audit event notification (outbox webhook)   | Done     | 2026-06-29     |
| M7    | Web UI (post-v1)                            | Pending  | -              |
| MKT.0 | Build-in-public engine bootstrap            | Done     | 2026-07-05     |
| MKT.1 | Social distribution lane (dry-run)          | Done     | 2026-07-05     |

[^m95e2e]: e2e 補完 2026-07-01：補 `GET .../sso/{slug}/authorize` OIDC 登入入口 + in-repo mock IdP
（`cmd/devtools/mock-idp`）+ `compose.e2e.yaml` overlay + `tasks/e2e-sso.sh`（`./manage.sh e2e-sso`）。
對真 compose 棧跑通完整 OIDC dance + 集中撤權端到端（PASS）。設計：
`docs/features/sso-saml/release/2026-06-30-oidc-login-and-e2e.md` + 跨切面標準 `docs/features/e2e-testing/spec.md`。
窄化 deferred：multi-replica HA 之 durable StateStore（spec § 19.2）。
| MKT.W1 | Build-in-public weekly post from 0002-idempotency-and-compensation.md | Done | 2026-07-05 |
| MKT.W2 | Build-in-public weekly post from 0015-audit-log-append-only-and-tamper-evidence.md | Done | 2026-07-05 |
