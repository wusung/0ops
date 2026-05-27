#!/usr/bin/env bash
# tasks/m6-source-upload-e2e.sh — M6 (T22) acceptance script.
#
# Exercises the upload-source CLI path end-to-end against the live compose
# stack:
#   bootstrap → CLI upload + create_app via Source{upload}
#   → preview + confirm → upload pinned in DB
#   → server-signed JWT → GET /v1/uploads/{id}/archive returns the tarball
#
# Scope boundary: in dev mode the actual GHA workflow
# (deploy-app-from-upload.yml, T15) is NOT triggered because no runner is
# watching the 0ops repo. The deploy_run row is created and dispatch is
# attempted; full workflow execution is validated in production CI, not in
# this script. The deploy_run will remain in a non-live state — this is
# expected and the script does not wait for it.
#
# Per ADR-0013 / docs/features/app-source-ingestion/spec.md § 22.
#
# Usage:
#   ./manage.sh m6-source-upload-e2e
#   # or directly:
#   bash tasks/m6-source-upload-e2e.sh

set -euo pipefail

TEAM_SLUG="${TEAM_SLUG:-personal}"
TEAM_NAME="${TEAM_NAME:-Personal}"
# Append a timestamp to make the slug unique across re-runs.
# Override via APP_SLUG env to use a fixed name (will fail if app already exists).
APP_SLUG="${APP_SLUG:-t22-upload-$(date +%s)}"
GITHUB_LOGIN="${GITHUB_LOGIN:-dev}"
SOURCE_PATH="${SOURCE_PATH:-./examples/node-demo}"
HOST="${OPS_HOST:-http://localhost:${OPS_HOST_PORT:-8080}}"
DATABASE_URL_DEFAULT="postgres://ops:ops_dev_pw@127.0.0.1:5432/ops?sslmode=disable"
DATABASE_URL="${DATABASE_URL:-$DATABASE_URL_DEFAULT}"
SERVER_CONTAINER="${SERVER_CONTAINER:-0ops-server-1}"
DB_CONTAINER="${DB_CONTAINER:-0ops-db-1}"
DB_DSN_INTERNAL="postgres://ops:ops_dev_pw@localhost:5432/ops?sslmode=disable"

log() { echo "[t22-e2e] $*" >&2; }

log "1. compose up + wait healthy"
podman compose up -d
for i in $(seq 1 30); do
  if curl -fsS "$HOST/health" >/dev/null 2>&1; then
    log "  server healthy after $((i * 2))s"
    break
  fi
  sleep 2
  if [ "$i" = "30" ]; then
    echo "[t22-e2e] server not healthy after 60s" >&2
    exit 1
  fi
done

log "2. bootstrap example repo (idempotent)"
bash examples/node-demo/bootstrap.sh

log "3. bootstrap owner (idempotent — ignore already-exists)"
go -C src run ./cmd/cli admin bootstrap-owner \
  --host "$HOST" \
  --team-slug "$TEAM_SLUG" \
  --team-name "$TEAM_NAME" \
  --github-login "$GITHUB_LOGIN" \
  --output json >/dev/null 2>&1 || log "  (already bootstrapped, continuing)"

log "4. mint dev bearer via seed-cli-token (inside server container to reach db DNS)"
TOKEN="$(podman exec -e DATABASE_URL='postgres://ops:ops_dev_pw@db:5432/ops?sslmode=disable' \
  "$SERVER_CONTAINER" \
  go run ./cmd/devtools/seed-cli-token \
  --team-slug "$TEAM_SLUG" \
  --github-login "$GITHUB_LOGIN" \
  --name "t22-e2e" \
  --ttl 1h)"

if [ -z "$TOKEN" ]; then
  echo "[t22-e2e] failed to mint token" >&2
  exit 1
fi

log "5. CLI: apps create --source $SOURCE_PATH (upload pipeline)"
# --output json puts AppCreateResponse on stdout; progress lines go to stderr.
CREATE_OUT_FILE=$(mktemp)
CREATE_ERR_FILE=$(mktemp)
trap 'rm -f "$CREATE_OUT_FILE" "$CREATE_ERR_FILE" "${ARCHIVE_TMP:-}"' EXIT

if ! go -C src run ./cmd/cli apps create \
  --host "$HOST" --team "$TEAM_SLUG" --token "$TOKEN" \
  --slug "$APP_SLUG" --source "$SOURCE_PATH" --ref main --yes \
  --output json >"$CREATE_OUT_FILE" 2>"$CREATE_ERR_FILE"; then
  echo "[t22-e2e] apps create failed; stderr:" >&2
  cat "$CREATE_ERR_FILE" >&2
  exit 1
fi

log "  CLI stderr:"
sed 's/^/    /' "$CREATE_ERR_FILE" >&2

APP_ID="$(jq -r '.app_id' <"$CREATE_OUT_FILE")"
DEPLOY_RUN_ID="$(jq -r '.deploy_run_id' <"$CREATE_OUT_FILE")"
if [ -z "$APP_ID" ] || [ "$APP_ID" = "null" ]; then
  echo "[t22-e2e] could not parse app_id from CLI output" >&2
  cat "$CREATE_OUT_FILE" >&2
  exit 1
fi
if [ -z "$DEPLOY_RUN_ID" ] || [ "$DEPLOY_RUN_ID" = "null" ]; then
  echo "[t22-e2e] expected deploy_run_id in CLI response, got '$DEPLOY_RUN_ID'" >&2
  cat "$CREATE_OUT_FILE" >&2
  exit 1
fi
log "  app_id=$APP_ID deploy_run_id=$DEPLOY_RUN_ID"

# Extract upload_id from the stderr progress line:
#   Uploaded N files (B bytes, sha256=Z) → upl_<id>
UPLOAD_ID="$(grep -oE 'upl_[A-Za-z0-9_-]+' "$CREATE_ERR_FILE" | head -1)"
if [ -z "$UPLOAD_ID" ]; then
  echo "[t22-e2e] could not parse upload_id from CLI stderr" >&2
  exit 1
fi
log "  upload_id=$UPLOAD_ID"

log "6. verify DB: upload row exists and is status='pinned'"
# Use the postgres container which has psql available.
UPLOAD_STATUS="$(echo "SELECT status FROM app_source_uploads WHERE id = :'upload_id' LIMIT 1;" \
  | podman exec -i "$DB_CONTAINER" \
    psql "$DB_DSN_INTERNAL" -tA \
    -v upload_id="$UPLOAD_ID" \
  | tr -d '[:space:]')"

case "$UPLOAD_STATUS" in
  pinned) log "  status=pinned OK" ;;
  "")
    echo "[t22-e2e] upload row not found for id=$UPLOAD_ID" >&2
    exit 1
    ;;
  *)
    echo "[t22-e2e] expected status=pinned, got '$UPLOAD_STATUS'" >&2
    exit 1
    ;;
esac

log "7. verify DB: deploy_run row exists for deploy_run_id"
DEPLOY_COUNT="$(echo "SELECT COUNT(*) FROM deploy_run WHERE id = :'deploy_run_id';" \
  | podman exec -i "$DB_CONTAINER" \
    psql "$DB_DSN_INTERNAL" -tA \
    -v deploy_run_id="$DEPLOY_RUN_ID" \
  | tr -d '[:space:]')"
if [ "$DEPLOY_COUNT" != "1" ]; then
  echo "[t22-e2e] expected 1 deploy_run row for id=$DEPLOY_RUN_ID, got $DEPLOY_COUNT" >&2
  exit 1
fi
log "  deploy_run found OK"

log "8. verify archive download: server signs JWT + GET /v1/uploads/{id}/archive"
# Look up team UUID from DB (needed to sign the fetch JWT).
TEAM_ID="$(echo "SELECT id FROM team WHERE slug = :'team_slug' LIMIT 1;" \
  | podman exec -i "$DB_CONTAINER" \
    psql "$DB_DSN_INTERNAL" -tA \
    -v team_slug="$TEAM_SLUG" \
  | tr -d '[:space:]')"

if [ -z "$TEAM_ID" ]; then
  echo "[t22-e2e] could not look up team_id for slug=$TEAM_SLUG" >&2
  exit 1
fi
log "  team_id=$TEAM_ID"

FETCH_TOKEN="$(podman exec \
  -e OPS_BUILD_TOKEN_SECRET="${OPS_BUILD_TOKEN_SECRET:-dev-build-token-secret-change-me}" \
  "$SERVER_CONTAINER" \
  go run ./cmd/devtools/seed-fetch-token \
  --team-id "$TEAM_ID" \
  --upload-id "$UPLOAD_ID" \
  --deploy-run-id "$DEPLOY_RUN_ID" \
  --ttl 5m)"

if [ -z "$FETCH_TOKEN" ]; then
  echo "[t22-e2e] seed-fetch-token returned empty token" >&2
  exit 1
fi

ARCHIVE_TMP="$(mktemp)"
ARCHIVE_HTTP_CODE="$(curl -sS -o "$ARCHIVE_TMP" -w "%{http_code}" \
  -H "Authorization: Bearer $FETCH_TOKEN" \
  "$HOST/v1/uploads/$UPLOAD_ID/archive")"

if [ "$ARCHIVE_HTTP_CODE" = "200" ]; then
  ARCHIVE_SIZE="$(stat -c %s "$ARCHIVE_TMP")"
  rm -f "$ARCHIVE_TMP"
  log "  archive download OK (HTTP 200, ${ARCHIVE_SIZE} bytes)"
else
  rm -f "$ARCHIVE_TMP"
  echo "[t22-e2e] archive download failed: HTTP $ARCHIVE_HTTP_CODE" >&2
  exit 1
fi

log "9. verify metrics: zeroops_app_source_upload_total{result=\"success\"} >= 1"
METRICS_BODY="$(curl -fsS "$HOST/metrics")"
if [ -z "$METRICS_BODY" ]; then
  echo "[t22-e2e] /metrics endpoint returned empty body" >&2
  exit 1
fi
# Match: zeroops_app_source_upload_total{...result="success"...} <number>
METRIC_VAL="$(echo "$METRICS_BODY" \
  | awk '/^zeroops_app_source_upload_total\{[^}]*result="success"[^}]*\}/ {print $2}' \
  | head -1)"
if [ -z "$METRIC_VAL" ]; then
  echo "[t22-e2e] zeroops_app_source_upload_total{result=\"success\"} not found in /metrics" >&2
  echo "[t22-e2e] T21 metrics may be broken or the metric name has drifted" >&2
  exit 1
fi
# strip trailing fractional/scientific notation if any
if awk "BEGIN{exit !($METRIC_VAL > 0)}"; then
  log "  metric value=$METRIC_VAL (>= 1) OK"
else
  echo "[t22-e2e] expected zeroops_app_source_upload_total{result=\"success\"} >= 1, got $METRIC_VAL" >&2
  exit 1
fi

log ""
log "OK — T22 acceptance complete."
log "  Validated: compose up, bootstrap, token mint, CLI upload+create,"
log "             DB upload pinned, deploy_run present, archive download."
log ""
log "Note: full GHA workflow firing (deploy-app-from-upload.yml, T15) is NOT"
log "validated in dev. The deploy_run was created and dispatch was attempted;"
log "the run will stay queued because no GHA runner is watching the 0ops repo"
log "for deploy-app-from-upload events. Production CI is the truth source for"
log "end-to-end workflow execution."
