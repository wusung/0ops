#!/usr/bin/env bash
# tasks/e2e-sso.sh — SSO/OIDC (M9.5) end-to-end acceptance against the live
# compose stack + in-repo mock IdP.
#
# Drives the full OIDC login dance and the central-revocation guarantee:
#   configure IdP (HTTP) → add domain (HTTP) + mark verified (psql fixture)
#   → GET /authorize → mock IdP → GET /callback → JIT + SSO bearer
#   → bearer hits GET /apps (200)
#   → owner POST /sso/deprovision (200, tokens_revoked>=1)
#   → same bearer hits GET /apps (401, revoked)
#   → psql asserts deactivated_at / revoked_at + sso_deprovision_user audit
#
# HTTP is driven INSIDE the server container (`podman exec <server> curl`) so the
# cross-service OIDC redirects (server→mock-idp→server/callback) resolve via
# compose DNS — no host port publishing, no URL rewriting. jq/processing run on
# host. 對齊 docs/features/sso-saml/release/2026-06-30-oidc-login-and-e2e.md § 5
# 與 docs/features/e2e-testing/spec.md。
#
# Usage:
#   ./manage.sh e2e-sso                  # up overlay → run → (keeps stack up)
#   E2E_SSO_DOWN=1 ./manage.sh e2e-sso   # tear the stack down (-v) at the end
#
# Exit codes (e2e-testing spec § 2.3):
#   0 pass    2 missing tool    3 assertion failed    4 subprocess error
set -euo pipefail

TEAM_SLUG="${TEAM_SLUG:-personal}"
TEAM_NAME="${TEAM_NAME:-Personal}"
GITHUB_LOGIN="${GITHUB_LOGIN:-dev}"
HOST="${OPS_HOST:-http://localhost:${OPS_HOST_PORT:-8080}}"

# Mock IdP identity — email domain must match the verified SSO domain below.
SSO_EMAIL="${MOCK_IDP_EMAIL:-alice@acme.example}"
SSO_DOMAIN="${SSO_EMAIL#*@}"
CLIENT_ID="${SSO_CLIENT_ID:-0ops-e2e-client}"
CLIENT_SECRET="${SSO_CLIENT_SECRET:-0ops-e2e-secret}"
# issuer the server reaches via compose DNS; must equal mock-idp's MOCK_IDP_ISSUER.
ISSUER_INTERNAL="${MOCK_IDP_ISSUER:-http://mock-idp:9000}"
# server's own base URL as seen inside the compose network.
SRV="http://server:8080"

SERVER_CONTAINER="${SERVER_CONTAINER:-0ops-server-1}"
DB_CONTAINER="${DB_CONTAINER:-0ops-db-1}"
DB_DSN_INTERNAL="postgres://ops:ops_dev_pw@localhost:5432/ops?sslmode=disable"
COMPOSE=(podman compose -f compose.yaml -f compose.e2e.yaml)

log()  { echo "[sso-e2e] $*" >&2; }
fail() { echo "[sso-e2e] FAIL: $*" >&2; exit 3; }

# curl from inside the server container (compose DNS resolves server:8080 + mock-idp:9000).
scurl() { podman exec -i "$SERVER_CONTAINER" curl "$@"; }
# psql against the db container (has psql). SQL is fed on STDIN (the proven
# pattern; psql `:'var'` interpolation does not apply to -c here). -q -tA =
# quiet, tuples-only, unaligned → a single scalar for assertions.
psql_q()    { podman exec -i "$DB_CONTAINER" psql "$DB_DSN_INTERNAL" -q -tA | tr -d '[:space:]'; }
psql_exec() { podman exec -i "$DB_CONTAINER" psql "$DB_DSN_INTERNAL" -q -tA >/dev/null; }
# extract the Location header value from a raw `curl -i` response.
location_of() { tr -d '\r' | awk 'tolower($1)=="location:"{print $2}'; }

# ---- preflight -------------------------------------------------------------
for tool in podman jq awk sed; do
  command -v "$tool" >/dev/null 2>&1 || { echo "[sso-e2e] missing tool: $tool" >&2; exit 2; }
done

# ---- 1. stack up (compose.yaml + overlay) ----------------------------------
log "1. compose up (base + e2e overlay) + wait healthy"
"${COMPOSE[@]}" up -d --build
if [ "${E2E_SSO_DOWN:-0}" = "1" ]; then
  trap 'log "tearing down stack"; "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true' EXIT
fi
# The dev server image runs `air`, which compiles the Go server on first
# container start — that can take a couple of minutes on a cold build cache,
# so the readiness budget is generous (server depends_on mock-idp:healthy, so a
# healthy server implies the mock IdP is already up).
HEALTH_TRIES="${HEALTH_TRIES:-150}" # × 2s = up to 300s
# Probe health INSIDE the server container (same path the dance uses) so
# readiness does not depend on the published host port (OPS_HOST_PORT may be
# remapped in .env). `podman exec` fails until the container exists, which the
# loop treats as not-ready and retries.
for i in $(seq 1 "$HEALTH_TRIES"); do
  if scurl -fsS http://localhost:8080/health >/dev/null 2>&1; then
    log "  server healthy after ~$((i * 2))s"; break
  fi
  sleep 2
  [ "$i" = "$HEALTH_TRIES" ] && { echo "[sso-e2e] stack not healthy after $((HEALTH_TRIES * 2))s" >&2; exit 4; }
done

# ---- 2. bootstrap owner + mint owner bearer (sso:manage) -------------------
log "2. bootstrap owner + mint owner bearer with sso:manage"
go -C src run ./cmd/cli admin bootstrap-owner \
  --host "$HOST" --team-slug "$TEAM_SLUG" --team-name "$TEAM_NAME" \
  --github-login "$GITHUB_LOGIN" --output json >/dev/null 2>&1 || log "  (already bootstrapped)"

OWNER_TOKEN="$(podman exec -e DATABASE_URL='postgres://ops:ops_dev_pw@db:5432/ops?sslmode=disable' \
  "$SERVER_CONTAINER" go run ./cmd/devtools/seed-cli-token \
  --team-slug "$TEAM_SLUG" --github-login "$GITHUB_LOGIN" --name "sso-e2e-owner" --ttl 1h \
  --scopes "sso:manage,apps:read,teams:read")"
[ -n "$OWNER_TOKEN" ] || { echo "[sso-e2e] failed to mint owner token" >&2; exit 4; }

# ---- 3. configure IdP over HTTP (secret lands in server's MemorySecretStore) -
log "3. POST /sso configure IdP (issuer=$ISSUER_INTERNAL)"
CFG_CODE="$(scurl -s -o /dev/null -w '%{http_code}' -X POST "$SRV/v1/teams/$TEAM_SLUG/sso" \
  -H "Authorization: Bearer $OWNER_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"issuer\":\"$ISSUER_INTERNAL\",\"client_id\":\"$CLIENT_ID\",\"client_secret\":\"$CLIENT_SECRET\",\"jit_default_role\":\"member\"}")"
case "$CFG_CODE" in
  200|201) log "  config created ($CFG_CODE)" ;;
  409)     log "  config already exists (409) — reusing" ;;
  *)       fail "configure IdP HTTP $CFG_CODE" ;;
esac

# ---- 4. add domain (HTTP) + mark verified (psql fixture) --------------------
log "4. POST /sso/domains $SSO_DOMAIN + mark verified (DNS verify is env-dependent)"
scurl -s -o /dev/null -X POST "$SRV/v1/teams/$TEAM_SLUG/sso/domains" \
  -H "Authorization: Bearer $OWNER_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"domain\":\"$SSO_DOMAIN\"}" || true
# IsDomainVerifiedForConfig checks the `verified` bool (MarkIdPDomainVerified
# sets verified=true + verified_at), so the fixture must set both.
echo "UPDATE idp_domain SET verified = true, verified_at = now() WHERE domain = '$SSO_DOMAIN' AND verified = false;" | psql_exec
VERIFIED="$(echo "SELECT count(*) FROM idp_domain WHERE domain = '$SSO_DOMAIN' AND verified = true;" | psql_q)"
[ "$VERIFIED" -ge 1 ] 2>/dev/null || fail "domain $SSO_DOMAIN not marked verified (got '$VERIFIED')"
log "  domain verified"

# ---- 5. OIDC dance: authorize → mock IdP → callback → bearer ---------------
log "5. GET /authorize → follow to mock IdP → callback"
AUTH_LOC="$(scurl -s -i "$SRV/v1/auth/sso/$TEAM_SLUG/authorize" | location_of)"
[ -n "$AUTH_LOC" ] || fail "authorize did not redirect (no Location)"
case "$AUTH_LOC" in *"/authorize?"*) : ;; *) fail "authorize Location not an IdP authorize URL: $AUTH_LOC" ;; esac

IDP_LOC="$(scurl -s -i "$AUTH_LOC" | location_of)"
[ -n "$IDP_LOC" ] || fail "mock IdP did not redirect back (no Location)"
case "$IDP_LOC" in *"/callback?"*) : ;; *) fail "IdP redirect not a callback URL: $IDP_LOC" ;; esac

CB_JSON="$(scurl -s "$IDP_LOC")"
SSO_BEARER="$(echo "$CB_JSON" | jq -r '.bearer_token // empty')"
CB_EMAIL="$(echo "$CB_JSON" | jq -r '.email // empty')"
[ -n "$SSO_BEARER" ] || fail "callback returned no bearer_token: $CB_JSON"
[ "$CB_EMAIL" = "$SSO_EMAIL" ] || fail "callback email = '$CB_EMAIL', want '$SSO_EMAIL'"
log "  login OK — SSO bearer issued for $CB_EMAIL"

# ---- 6. SSO bearer can read apps (live Bearer→CheckMembership→scope) -------
APPS_CODE="$(scurl -s -o /dev/null -w '%{http_code}' "$SRV/v1/teams/$TEAM_SLUG/apps" \
  -H "Authorization: Bearer $SSO_BEARER")"
[ "$APPS_CODE" = "200" ] || fail "GET /apps with SSO bearer = $APPS_CODE, want 200"
log "6. SSO bearer GET /apps = 200"

# ---- 7. owner deprovisions the SSO user ------------------------------------
DEP_JSON="$(scurl -s -X POST "$SRV/v1/teams/$TEAM_SLUG/sso/deprovision" \
  -H "Authorization: Bearer $OWNER_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"user\":\"$SSO_EMAIL\"}")"
DEACT="$(echo "$DEP_JSON" | jq -r '.membership_deactivated // false')"
REVOKED="$(echo "$DEP_JSON" | jq -r '.tokens_revoked // 0')"
[ "$DEACT" = "true" ] || fail "deprovision membership_deactivated=$DEACT (resp: $DEP_JSON)"
[ "$REVOKED" -ge 1 ] 2>/dev/null || fail "deprovision tokens_revoked=$REVOKED, want >=1"
log "7. deprovision OK — membership_deactivated=$DEACT tokens_revoked=$REVOKED"

# ---- 8. revoked SSO bearer is now rejected ---------------------------------
APPS_AFTER="$(scurl -s -o /dev/null -w '%{http_code}' "$SRV/v1/teams/$TEAM_SLUG/apps" \
  -H "Authorization: Bearer $SSO_BEARER")"
[ "$APPS_AFTER" = "401" ] || fail "GET /apps after deprovision = $APPS_AFTER, want 401"
log "8. revoked SSO bearer GET /apps = 401 (central revocation end-to-end)"

# ---- 9. DB + audit assertions ----------------------------------------------
log "9. DB assertions (deactivated_at / revoked_at / audit)"
MEMB_DEACT="$(echo "SELECT count(*) FROM team_membership m JOIN idp_identity i ON i.user_id=m.user_id WHERE i.email='$SSO_EMAIL' AND m.deactivated_at IS NOT NULL;" | psql_q)"
[ "$MEMB_DEACT" -ge 1 ] 2>/dev/null || fail "membership.deactivated_at not set for $SSO_EMAIL (got '$MEMB_DEACT')"

LIVE_TOK="$(echo "SELECT count(*) FROM cli_token t JOIN idp_identity i ON i.user_id=t.owner_user_id WHERE i.email='$SSO_EMAIL' AND t.auth_source='sso' AND t.revoked_at IS NULL;" | psql_q)"
[ "$LIVE_TOK" = "0" ] || fail "still $LIVE_TOK live SSO tokens for $SSO_EMAIL, want 0"

AUDIT_CT="$(echo "SELECT count(*) FROM audit_log WHERE action='sso_deprovision_user' AND outcome='success';" | psql_q)"
[ "$AUDIT_CT" -ge 1 ] 2>/dev/null || fail "no sso_deprovision_user success audit row"
log "  membership deactivated, 0 live SSO tokens, audit present"

log "PASS — SSO OIDC login + central revocation end-to-end on compose stack"
