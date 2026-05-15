#!/usr/bin/env bash
# pg_basebackup init container — runs once on first boot of postgres-replica.
# Spec § 5.1.
#
# Idempotency: bail out if PGDATA already initialised (presence of PG_VERSION).

set -euo pipefail

: "${PGDATA:?PGDATA required}"
: "${PRIMARY_HOST:?PRIMARY_HOST required}"
: "${PRIMARY_PORT:=5432}"
: "${REPLICATION_USER:?REPLICATION_USER required}"
: "${REPLICATION_PASSWORD:?REPLICATION_PASSWORD required}"

if [ -f "${PGDATA}/PG_VERSION" ]; then
  echo "replica-init: PGDATA already initialised at ${PGDATA}; skipping pg_basebackup"
  exit 0
fi

echo "replica-init: running pg_basebackup from ${PRIMARY_HOST}:${PRIMARY_PORT}"

export PGPASSWORD="${REPLICATION_PASSWORD}"
pg_basebackup \
  --host="${PRIMARY_HOST}" \
  --port="${PRIMARY_PORT}" \
  --username="${REPLICATION_USER}" \
  --pgdata="${PGDATA}" \
  --wal-method=stream \
  --write-recovery-conf \
  --checkpoint=fast \
  --progress \
  --verbose

# write-recovery-conf already creates standby.signal + primary_conninfo;
# ensure standby.signal exists (idempotent guard for old PG versions).
touch "${PGDATA}/standby.signal"
chmod 700 "${PGDATA}"

echo "replica-init: done"
