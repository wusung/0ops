#!/usr/bin/env bash
# Daily logical backup (spec § 7).
# Writes a compressed pg_dump archive to R2 / S3 then deletes the local copy.

set -euo pipefail

: "${PGHOST:?PGHOST required}"
: "${PGUSER:?PGUSER required}"
: "${PGPASSWORD:?PGPASSWORD required}"
: "${PGDATABASE:?PGDATABASE required}"
: "${DUMP_S3_BUCKET:?DUMP_S3_BUCKET required}"
: "${DUMP_S3_PREFIX:?DUMP_S3_PREFIX required}"
: "${DUMP_S3_ENDPOINT:?DUMP_S3_ENDPOINT required}"
: "${AWS_ACCESS_KEY_ID:?AWS_ACCESS_KEY_ID required}"
: "${AWS_SECRET_ACCESS_KEY:?AWS_SECRET_ACCESS_KEY required}"

ts="$(date -u +%Y%m%d-%H%M%S)"
out="/tmp/0ops-${ts}.sql.gz"

trap 'rm -f "${out}"' EXIT

# -Fc = custom format (suitable for pg_restore -j); -Z9 = max gzip
pg_dump -Fc -Z9 -f "${out}" "${PGDATABASE}"

aws s3 cp --no-progress \
  --endpoint-url "${DUMP_S3_ENDPOINT}" \
  "${out}" "s3://${DUMP_S3_BUCKET}/${DUMP_S3_PREFIX}/${ts}.sql.gz"
