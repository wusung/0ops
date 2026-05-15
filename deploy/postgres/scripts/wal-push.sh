#!/usr/bin/env bash
# WAL archive_command — push a single WAL segment to R2 / S3.
# Called by PostgreSQL on every segment switch (spec § 6.2).
#
# Usage: wal-push.sh <src_path> <wal_filename>
# Exits non-zero on failure so PostgreSQL retries (archive_timeout window).

set -euo pipefail

src="${1:?source path required}"
name="${2:?wal filename required}"

: "${WAL_S3_BUCKET:?WAL_S3_BUCKET required}"
: "${WAL_S3_PREFIX:?WAL_S3_PREFIX required}"
: "${WAL_S3_ENDPOINT:?WAL_S3_ENDPOINT required}"
: "${AWS_ACCESS_KEY_ID:?AWS_ACCESS_KEY_ID required}"
: "${AWS_SECRET_ACCESS_KEY:?AWS_SECRET_ACCESS_KEY required}"

# WAL filename layout: TTTTTTTTLLLLLLLLLLLLLLLL — first 8 chars are the timeline.
timeline="$(printf '%s' "$name" | head -c 8)"
dst="s3://${WAL_S3_BUCKET}/${WAL_S3_PREFIX}/${timeline}/${name}"

aws s3 cp --no-progress \
  --endpoint-url "${WAL_S3_ENDPOINT}" \
  "${src}" "${dst}"
