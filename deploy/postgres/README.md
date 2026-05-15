# Postgres Chart

Application Postgres (main + 1 streaming replica) with WAL archive to R2 +
daily `pg_dump`, per `docs/features/postgres-ha-and-dr/spec.md`.

Scope rules (spec § 16, enforced by templates and `chart_test.go`):

1. main + replica；不得 single
2. anti-affinity `requiredDuringSchedulingIgnoredDuringExecution`
3. `archive_timeout` ≤ 300s（RPO ≤ 5 min）
4. Daily `pg_dump` CronJob
5. PITR / failover runbook — see `docs/runbooks/`
6. Migration staging 24h — see CI (out of chart scope)
7. ALTER 大表 必 CONCURRENTLY — enforced by `internal/server/db/migrationlint/`
8. ADD COLUMN NOT NULL 必拆 3 步 — enforced by `internal/server/db/migrationlint/`
9. R2 bucket lifecycle 30d — `values.yaml` `walArchive.retentionDays=30` (R2
   bucket lifecycle rule must be configured separately at the bucket level)
10. `DATABASE_URL` 變更後必 rolling restart backend — runbook §

## 不可違反的硬性規則

This chart is for **application** Postgres only — it does **not** cover the
K3s control-plane datastore Postgres (that is ADR-0004 + ops runbook scope).

## Files

```
deploy/postgres/
├── Chart.yaml
├── README.md
├── chart_test.go
├── scripts/
│   ├── wal-push.sh        # archive_command runs this against R2
│   ├── pg-dump.sh         # daily logical backup script
│   ├── replica-init.sh    # pg_basebackup init container
│   └── pitr-drill.sh      # local compose-based PITR drill
├── templates/
│   ├── configmap-pg-hba.yaml
│   ├── configmap-postgresql-conf.yaml
│   ├── configmap-scripts.yaml
│   ├── cronjob-pg-dump.yaml
│   ├── networkpolicy.yaml
│   ├── secret-placeholder.yaml
│   ├── service-main.yaml
│   ├── service-replica.yaml
│   ├── statefulset-main.yaml
│   └── statefulset-replica.yaml
└── values.yaml
```

## Local PITR drill

`scripts/pitr-drill.sh` boots a throwaway main + staging Postgres pair via
podman, exercises a `recovery_target_time` restore, and asserts that the
canary row pre-dating the bad `DELETE` is recovered. Drill output is captured
in `docs/runbooks/postgres-restore-test.md` per spec § 8.3.

```
./deploy/postgres/scripts/pitr-drill.sh
```
