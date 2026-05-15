#!/usr/bin/env bash
# Local PITR drill — boots two throwaway PostgreSQL containers (source + target)
# via podman, exercises WAL archive + recovery_target_time, and asserts the
# canary row inserted before a destructive DELETE is restored.
#
# This is the "Local PITR drill" tier from
# `docs/runbooks/postgres-restore-test.md` § 3 — meant to be runnable every
# time the chart or scripts change. It does NOT replace the staging-cluster
# full drill required by spec § 8.3 + § 16 hard rule #5.

set -euo pipefail

PGVER="${PGVER:-17-alpine}"
NETWORK="0ops-pitr-drill"
SRC="0ops-pitr-src"
TGT="0ops-pitr-tgt"
ARCHIVE_VOL="0ops-pitr-archive"
LOG_FILE="${LOG_FILE:-/tmp/0ops-pitr-drill-$(date -u +%Y%m%d-%H%M%S).log}"

log() { printf '%s [drill] %s\n' "$(date -u +%H:%M:%S)" "$*" | tee -a "${LOG_FILE}" ; }
fail() { log "FAILED — $*"; cleanup; exit 1; }

cleanup() {
  podman rm -f "${SRC}" "${TGT}" >/dev/null 2>&1 || true
  podman volume rm -f "${ARCHIVE_VOL}" >/dev/null 2>&1 || true
  podman network rm -f "${NETWORK}" >/dev/null 2>&1 || true
}

trap cleanup EXIT INT TERM

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

require_cmd podman

log "starting PITR drill — log: ${LOG_FILE}"

podman network create "${NETWORK}" >/dev/null
podman volume create "${ARCHIVE_VOL}" >/dev/null

log "boot source postgres (${PGVER})"
podman run -d --name "${SRC}" --network "${NETWORK}" \
  -e POSTGRES_USER=ops -e POSTGRES_PASSWORD=drill -e POSTGRES_DB=ops \
  -v "${ARCHIVE_VOL}:/wal-archive" \
  "docker.io/library/postgres:${PGVER}" \
  -c wal_level=replica \
  -c archive_mode=on \
  -c "archive_command=cp %p /wal-archive/%f" \
  -c archive_timeout=5 \
  -c max_wal_senders=5 \
  -c hot_standby=on >/dev/null

log "wait for source readiness"
for _ in $(seq 1 30); do
  if podman exec "${SRC}" pg_isready -U ops -d ops >/dev/null 2>&1; then break; fi
  sleep 1
done
podman exec "${SRC}" pg_isready -U ops -d ops >/dev/null 2>&1 \
  || fail "source postgres never became ready"

log "seed canary table"
podman exec "${SRC}" psql -U ops -d ops -v ON_ERROR_STOP=1 -c "
  CREATE TABLE canary(id int primary key, note text, ts timestamptz default now());
  INSERT INTO canary VALUES (1, 'before-target');
" >>"${LOG_FILE}" 2>&1

log "force WAL switch (archive segment 1)"
podman exec "${SRC}" psql -U ops -d ops -c "SELECT pg_switch_wal();" >>"${LOG_FILE}" 2>&1
sleep 2

TARGET_TS="$(podman exec "${SRC}" psql -U ops -d ops -At -c "SELECT now();")"
log "recovery target time = ${TARGET_TS}"

log "simulate destructive write (post-target)"
podman exec "${SRC}" psql -U ops -d ops -c "INSERT INTO canary VALUES (2, 'after-target');" >>"${LOG_FILE}" 2>&1
podman exec "${SRC}" psql -U ops -d ops -c "DELETE FROM canary WHERE id=1;" >>"${LOG_FILE}" 2>&1
podman exec "${SRC}" psql -U ops -d ops -c "SELECT pg_switch_wal();" >>"${LOG_FILE}" 2>&1
sleep 6  # let archive_timeout/archive_command persist

log "take base backup"
BACKUP_DIR="/tmp/0ops-pitr-base-$$"
mkdir -p "${BACKUP_DIR}"
PGPASSWORD=drill podman exec -e PGPASSWORD=drill "${SRC}" \
  pg_basebackup -U ops -h 127.0.0.1 -D /tmp/basebackup -Fp --wal-method=fetch >>"${LOG_FILE}" 2>&1
podman cp "${SRC}:/tmp/basebackup" "${BACKUP_DIR}/data"

log "boot target postgres in recovery mode"
podman run -d --name "${TGT}" --network "${NETWORK}" \
  -e POSTGRES_USER=ops -e POSTGRES_PASSWORD=drill \
  -v "${ARCHIVE_VOL}:/wal-archive" \
  -v "${BACKUP_DIR}/data:/var/lib/postgresql/restore" \
  "docker.io/library/postgres:${PGVER}" \
  bash -c '
    set -euo pipefail
    rm -rf /var/lib/postgresql/data/pgdata
    cp -a /var/lib/postgresql/restore /var/lib/postgresql/data/pgdata
    chown -R postgres:postgres /var/lib/postgresql/data/pgdata
    chmod 700 /var/lib/postgresql/data/pgdata
    cat >> /var/lib/postgresql/data/pgdata/postgresql.auto.conf <<EOF
restore_command = '"'"'cp /wal-archive/%f %p'"'"'
recovery_target_time = '"'"'${TARGET_TS}'"'"'
recovery_target_action = '"'"'promote'"'"'
EOF
    touch /var/lib/postgresql/data/pgdata/recovery.signal
    exec gosu postgres postgres -D /var/lib/postgresql/data/pgdata
  ' >>"${LOG_FILE}" 2>&1 \
  || fail "target boot failed (see ${LOG_FILE})"

log "wait for target readiness (recovery + promote)"
ready=false
for _ in $(seq 1 60); do
  if podman exec "${TGT}" pg_isready -U ops -d ops >/dev/null 2>&1; then
    ready=true; break
  fi
  sleep 1
done
${ready} || fail "target postgres never became ready"

log "verify canary row state at recovery_target_time"
CANARY_BEFORE=$(podman exec "${TGT}" psql -U ops -d ops -At -c "SELECT count(*) FROM canary WHERE id=1;")
CANARY_AFTER=$(podman exec "${TGT}" psql -U ops -d ops -At -c "SELECT count(*) FROM canary WHERE id=2;")

log "canary id=1 (before target): count=${CANARY_BEFORE}"
log "canary id=2 (after target):  count=${CANARY_AFTER}"

if [[ "${CANARY_BEFORE}" != "1" ]]; then
  fail "expected canary id=1 to exist at recovery_target_time, got count=${CANARY_BEFORE}"
fi
if [[ "${CANARY_AFTER}" != "0" ]]; then
  fail "expected canary id=2 to NOT exist at recovery_target_time, got count=${CANARY_AFTER}"
fi

log "PASSED — PITR drill confirms RPO < 5 min path is wired correctly"
log "drill log: ${LOG_FILE}"
