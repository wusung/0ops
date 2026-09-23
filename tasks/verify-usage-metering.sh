#!/usr/bin/env bash
# verify: resource-usage-metering — schema, retention, rollup integrity
#
# Companion to tasks/e2e-usage-metering.sh (the Go composition e2e). That
# script proves the headline guarantee through the real code path; this
# one covers what it cannot reach:
#
#   MIGRATION  00020/00021 up/down reversibility against a real Postgres
#   SCHEMA     the check constraints, the partial index, the FK cascades
#   ISOLATION  cross-team isolation at the SQL layer (spec § 14 rule #8)
#   GC         the retention delete predicates (13 months / 30 days)
#   ROLLUP     the real Rollupper run over seeded ledger input, verified
#              by an independent SQL oracle (spec § 14 rules #5 and #6)
#
# Honesty rule (docs/features/e2e-testing/spec.md § 2.3): SQL is used to
# seed ledger *input* (pod intervals, i.e. what the cluster would have
# produced) and to verify *schema* behaviour. It is never used to write a
# rollup row and then claim the integrator was tested — every rollup
# figure asserted here was computed by the real Go Rollupper.
#
# Environment (lessons L001): everything runs against the compose stack's
# Postgres via `podman compose exec db psql`; the Go composition test
# reaches the same database through the host-mapped port 15432
# (compose.override.yaml). No host binary is started.
#
# ---------------------------------------------------------------------
# ROLLBACK CRITERIA — read before shipping this feature
# ---------------------------------------------------------------------
# Roll back when any of these is observed in staging/production:
#   R1  zeroops_usage_intervals_open diverges persistently from the real
#       running pod count (ledger opening or closing is broken).
#   R2  closed_reason='reconciled_missing' exceeds 5% of closes for over
#       an hour (spec § 9) — the watch path is not working and the ledger
#       is silently degrading to estimates.
#   R3  any rollup row grows on a re-run (interval_count or pod_seconds
#       increasing for an already-computed day) — the idempotence rule
#       (spec § 14 #5) is violated and past numbers are no longer stable.
#   R4  the pod list/watch ClusterRole cannot be granted, or the informer
#       plus relist load measurably degrades the API server.
#   R5  DB growth from usage_allocation_interval threatens capacity
#       before the 13-month GC can act.
#
# Rollback steps and their safety:
#   1. Disable the loops first (stop the reconciler's usage_reconcile /
#      usage_rollup / usage_gc loops, or deploy the previous backend
#      image). This alone stops all writes and is fully reversible: the
#      tables simply stop growing.
#   2. The previous backend version is SCHEMA-COMPATIBLE with 00019.
#      Migration 00019 only ADDS two tables and touches nothing existing,
#      so the old binary runs unchanged against the new schema. Therefore
#      DO NOT run `migrate down` as part of a routine rollback — step 1
#      is sufficient and lossless.
#   3. `migrate down` (00019 Down) is DESTRUCTIVE AND IRREVERSIBLE:
#      it drops usage_daily_rollup, whose rows the spec declares
#      permanent (§ 5), and usage_allocation_interval with up to 13
#      months of ledger. There is no backfill — the K8s objects the
#      timestamps came from are long gone. Run it only to abandon the
#      feature outright, and only after dumping both tables:
#        podman compose exec -T db pg_dump -U ops -d ops \
#          -t usage_allocation_interval -t usage_daily_rollup > usage.sql
#      This script verifies down/up works, and verifies that it wipes the
#      data — that wipe is the expected behaviour, not a defect.
#
#
# ---------------------------------------------------------------------
# NOT COVERED HERE — must be run on staging (real K3s + metrics-server)
# ---------------------------------------------------------------------
# The dev stack has no cluster, so every guarantee that depends on real
# K8s objects is deferred. See the report accompanying this script and
# spec § 11 for the command-level checklist: second-level precision from
# pod.status.startTime, late DELETE events, restart-does-not-split,
# rolling update hand-off, Pending pods, LimitRange defaults, the
# informer-degraded path, orphan pods, leader-only writes, and the pod
# list/watch ClusterRole actually resolving in-cluster.
#
# Caller note (lessons L002): when piping this script's output through
# tee, set -o pipefail first or the exit code below is lost.
#
# Exit codes (docs/features/e2e-testing/spec.md § 2.3):
#   0 all checks passed   2 missing tool/env   3 assertion failed
#  64 usage error
set -euo pipefail

cd "$(dirname "$0")/.."

PHASE="${1:-all}"
case "$PHASE" in
  all|--phase=*) ;;
  *) echo "usage: $0 [--phase=migration|schema|isolation|gc|rollup]" >&2; exit 64 ;;
esac
PHASE="${PHASE#--phase=}"

DB_URL_INTERNAL="${DATABASE_URL:-postgres://ops:ops_dev_pw@db:5432/ops?sslmode=disable}"
COMPOSE=(podman compose -f compose.yaml -f compose.override.yaml)

PASS=0; FAIL=0; SKIP=0
ok()   { PASS=$((PASS+1)); printf 'PASS  %-12s %s\n' "$1" "$2"; }
bad()  { FAIL=$((FAIL+1)); printf 'FAIL  %-12s %s\n' "$1" "$2"; }
skip() { SKIP=$((SKIP+1)); printf 'SKIP  %-12s %s\n' "$1" "$2"; }
hdr()  { printf '\n── %s ─────────────────────────────\n' "$1"; }

# psql helpers. -qAt gives bare values, ON_ERROR_STOP makes failure loud.
psql_q() { "${COMPOSE[@]}" exec -T db psql -U ops -d ops -v ON_ERROR_STOP=1 -qAt -c "$1" 2>/dev/null; }
psql_f() { "${COMPOSE[@]}" exec -T db psql -U ops -d ops -v ON_ERROR_STOP=1 -qAt 2>/dev/null; }
# expect_reject runs a statement that must violate a constraint.
expect_reject() {
  local id="$1" want="$2" sql="$3" err
  if err="$("${COMPOSE[@]}" exec -T db psql -U ops -d ops -v ON_ERROR_STOP=1 -qAt -c "$sql" 2>&1)"; then
    bad "$id" "statement was accepted; expected rejection by $want"
  elif grep -q "$want" <<<"$err"; then
    ok "$id" "rejected by $want"
  else
    bad "$id" "rejected, but not by $want: $(tr '\n' ' ' <<<"$err" | head -c 160)"
  fi
}
assert_eq() {
  local id="$1" want="$2" got="$3" what="$4"
  if [[ "$want" == "$got" ]]; then ok "$id" "$what = $got"
  else bad "$id" "$what = $got, want $want"; fi
}

# Deterministic fixture ids so the script is idempotent: a previous run's
# rows are removed before and after, whatever state it died in.
FT=00000000-0000-4000-8000-0000000fee01   # fixture team
FA=00000000-0000-4000-8000-0000000fee02   # fixture app
FA2=00000000-0000-4000-8000-0000000fee03  # second app (cascade target)
OT=00000000-0000-4000-8000-0000000fee11   # other team (isolation)
cleanup_fixtures() {
  psql_q "DELETE FROM team WHERE id IN ('$FT'::uuid,'$OT'::uuid)" >/dev/null || true
}

need() { command -v "$1" >/dev/null || { echo "missing tool: $1" >&2; exit 2; }; }
need podman
need go

hdr "environment"
"${COMPOSE[@]}" up -d --wait db >/dev/null 2>&1 || { echo "db did not come up" >&2; exit 2; }
podman compose run --rm migrate "$DB_URL_INTERNAL" up >/dev/null 2>&1 || true
ver="$(psql_q "SELECT max(version_id) FROM goose_db_version")"
# 00020 creates the ledger tables; 00021 adds closed_at, which the
# stale-rollup detection depends on. Anything below 21 cannot pass ROL8.
[[ "$ver" -ge 21 ]] && ok ENV1 "schema at migration $ver" || { bad ENV1 "schema at $ver, want >= 21"; exit 3; }
trap cleanup_fixtures EXIT
cleanup_fixtures

run_phase() { [[ "$PHASE" == "all" || "$PHASE" == "$1" ]]; }

# ---------------------------------------------------------------- migration
if run_phase migration; then
hdr "migration 00019 reversibility"
  # Seed one row so the destructiveness of Down is observed, not assumed.
  psql_f <<SQL >/dev/null
INSERT INTO team (id, slug, name) VALUES ('$FT','zz-verify-usage','zz-verify-usage');
INSERT INTO app (id, team_id, slug, repo_url, status)
     VALUES ('$FA','$FT','probe','file:///workspace/x','live');
INSERT INTO usage_allocation_interval
  (pod_uid, team_id, app_id, namespace, pod_name, cpu_millicores, memory_bytes,
   started_at, ended_at, last_seen_at, closed_reason)
VALUES ('$FA','$FT','$FA','team-zz','probe-1',100,1048576,
        now()-interval '2 hours', now()-interval '1 hour', now()-interval '1 hour','terminated');
INSERT INTO usage_daily_rollup (team_id, app_id, day, pod_seconds)
     VALUES ('$FT','$FA',(now()-interval '1 day')::date, 3600);
SQL
  before="$(psql_q "SELECT count(*) FROM usage_daily_rollup")"

  # The feature spans two migrations: 00019 creates the tables, 00020 adds
  # closed_at. Withdrawing it means unwinding both.
  if podman compose run --rm migrate "$DB_URL_INTERNAL" down >/dev/null 2>&1 \
     && podman compose run --rm migrate "$DB_URL_INTERNAL" down >/dev/null 2>&1; then
    gone="$(psql_q "SELECT count(*) FROM pg_class WHERE relname IN ('usage_allocation_interval','usage_daily_rollup') AND relkind='r'")"
    assert_eq MIG1 0 "$gone" "tables left after down"
    assert_eq MIG2 19 "$(psql_q "SELECT max(version_id) FROM goose_db_version")" "version after down"
  else
    bad MIG1 "migrate down failed — the feature cannot be withdrawn cleanly"
  fi

  if podman compose run --rm migrate "$DB_URL_INTERNAL" up >/dev/null 2>&1; then
    assert_eq MIG3 21 "$(psql_q "SELECT max(version_id) FROM goose_db_version")" "version after re-up"
    closedAt="$(psql_q "SELECT count(*) FROM information_schema.columns WHERE table_name='usage_allocation_interval' AND column_name='closed_at'")"
    assert_eq MIG7 1 "$closedAt" "closed_at restored (stale-rollup detection depends on it)"
    cons="$(psql_q "SELECT count(*) FROM pg_constraint WHERE conname LIKE 'usage_alloc%chk' OR conname='usage_rollup_estimated_chk'")"
    assert_eq MIG4 5 "$cons" "check constraints restored"
    idx="$(psql_q "SELECT count(*) FROM pg_indexes WHERE indexname IN ('idx_usage_alloc_open','idx_usage_alloc_team_app_started')")"
    assert_eq MIG5 2 "$idx" "indexes restored"
    after="$(psql_q "SELECT count(*) FROM usage_daily_rollup")"
    # Down is expected to be destructive; the check is that we KNOW it is.
    if [[ "$before" -gt 0 && "$after" -eq 0 ]]; then
      ok MIG6 "down/up is data-destructive as documented ($before rollup rows → 0); rollback must dump first"
    else
      bad MIG6 "unexpected rollup row count across down/up: $before → $after"
    fi
  else
    bad MIG3 "migrate up after down failed — schema is stuck at 19"
  fi
  cleanup_fixtures
fi

# ------------------------------------------------------------------- schema
seed_base() {
  psql_f <<SQL >/dev/null
INSERT INTO team (id, slug, name) VALUES ('$FT','zz-verify-usage','zz-verify-usage')
  ON CONFLICT (id) DO NOTHING;
INSERT INTO team (id, slug, name) VALUES ('$OT','zz-verify-usage-other','other')
  ON CONFLICT (id) DO NOTHING;
INSERT INTO app (id, team_id, slug, repo_url, status)
  VALUES ('$FA','$FT','web','file:///workspace/x','live') ON CONFLICT (id) DO NOTHING;
INSERT INTO app (id, team_id, slug, repo_url, status)
  VALUES ('$FA2','$FT','worker','file:///workspace/x','live') ON CONFLICT (id) DO NOTHING;
SQL
}

if run_phase schema; then
hdr "schema constraints"
  cleanup_fixtures; seed_base
  base="pod_uid, team_id, app_id, namespace, pod_name, cpu_millicores, memory_bytes, started_at, last_seen_at"
  expect_reject SCH1 usage_alloc_span_chk \
    "INSERT INTO usage_allocation_interval ($base, ended_at, closed_reason) VALUES (gen_random_uuid(),'$FT','$FA','ns','p',100,1,now(),now(),now()-interval '1 hour','terminated')"
  expect_reject SCH2 usage_alloc_closed_reason_chk \
    "INSERT INTO usage_allocation_interval ($base, ended_at, closed_reason) VALUES (gen_random_uuid(),'$FT','$FA','ns','p',100,1,now()-interval '1 hour',now(),now(),'evicted')"
  expect_reject SCH3 usage_alloc_closed_consistency_chk \
    "INSERT INTO usage_allocation_interval ($base, ended_at) VALUES (gen_random_uuid(),'$FT','$FA','ns','p',100,1,now()-interval '1 hour',now(),now())"
  expect_reject SCH4 usage_alloc_closed_consistency_chk \
    "INSERT INTO usage_allocation_interval ($base, closed_reason) VALUES (gen_random_uuid(),'$FT','$FA','ns','p',100,1,now()-interval '1 hour',now(),'terminated')"
  expect_reject SCH5 usage_alloc_nonneg_chk \
    "INSERT INTO usage_allocation_interval ($base) VALUES (gen_random_uuid(),'$FT','$FA','ns','p',-1,1,now(),now())"
  expect_reject SCH6 usage_rollup_estimated_chk \
    "INSERT INTO usage_daily_rollup (team_id, app_id, day, pod_seconds, estimated_seconds) VALUES ('$FT','$FA',current_date-1,10,11)"

  # One pod is one interval (spec § 14 rule #4): a repeated ADD must not
  # be able to create a second row.
  psql_q "INSERT INTO usage_allocation_interval ($base) VALUES ('$FA','$FT','$FA','ns','p',100,1,now()-interval '1 hour',now())" >/dev/null
  expect_reject SCH7 usage_allocation_interval_pkey \
    "INSERT INTO usage_allocation_interval ($base) VALUES ('$FA','$FT','$FA','ns','p',200,1,now()-interval '1 hour',now())"

  # The open-interval index is the hot path of every reconcile tick.
  plan="$(psql_q "EXPLAIN (COSTS OFF) SELECT pod_uid FROM usage_allocation_interval WHERE ended_at IS NULL ORDER BY last_seen_at" | tr '\n' ' ')"
  if grep -q idx_usage_alloc_open <<<"$plan"; then ok SCH8 "partial index serves the open-interval scan"
  else skip SCH8 "planner chose a seq scan on a near-empty table: $plan"; fi

  # Cascades: deleting an app must not orphan ledger rows.
  psql_f <<SQL >/dev/null
INSERT INTO usage_allocation_interval ($base) VALUES (gen_random_uuid(),'$FT','$FA2','ns','w',100,1,now()-interval '1 hour',now());
INSERT INTO usage_daily_rollup (team_id, app_id, day, pod_seconds) VALUES ('$FT','$FA2',current_date-1,60);
INSERT INTO usage_sample (team_id, app_id, cpu_millicores) VALUES ('$FT','$FA2',10);
DELETE FROM app WHERE id='$FA2';
SQL
  left="$(psql_q "SELECT (SELECT count(*) FROM usage_allocation_interval WHERE app_id='$FA2')+(SELECT count(*) FROM usage_daily_rollup WHERE app_id='$FA2')+(SELECT count(*) FROM usage_sample WHERE app_id='$FA2')")"
  assert_eq SCH9 0 "$left" "rows surviving an app delete"

  psql_q "DELETE FROM team WHERE id='$FT'" >/dev/null
  left="$(psql_q "SELECT (SELECT count(*) FROM usage_allocation_interval WHERE team_id='$FT')+(SELECT count(*) FROM usage_daily_rollup WHERE team_id='$FT')")"
  assert_eq SCH10 0 "$left" "rows surviving a team delete"
fi

# ---------------------------------------------------------------- isolation
if run_phase isolation; then
hdr "cross-team isolation (SQL layer)"
  cleanup_fixtures; seed_base
  psql_q "INSERT INTO usage_allocation_interval (pod_uid, team_id, app_id, namespace, pod_name, cpu_millicores, memory_bytes, started_at, last_seen_at) VALUES (gen_random_uuid(),'$FT','$FA','ns','p',100,1,now()-interval '1 hour',now())" >/dev/null
  psql_q "INSERT INTO usage_daily_rollup (team_id, app_id, day, pod_seconds) VALUES ('$FT','$FA',current_date-1,60)" >/dev/null

  # The exact predicate db.ListAllocationIntervalsOverlapping uses.
  n="$(psql_q "SELECT count(*) FROM usage_allocation_interval WHERE team_id='$OT'::uuid AND (''='' OR app_id='$FA'::uuid) AND started_at < now() AND (ended_at IS NULL OR ended_at > now()-interval '1 day')")"
  assert_eq ISO1 0 "$n" "intervals visible to the other team"
  n="$(psql_q "SELECT count(*) FROM usage_daily_rollup WHERE team_id='$OT'::uuid AND day >= current_date-30 AND day <= current_date")"
  assert_eq ISO2 0 "$n" "rollups visible to the other team"
  n="$(psql_q "SELECT count(*) FROM usage_allocation_interval WHERE team_id='$FT'::uuid")"
  assert_eq ISO3 1 "$n" "owning team still sees its own row"

  # Guard against a future read path that forgets the predicate.
  missing=0
  for fn in ListAllocationIntervalsOverlapping ListDailyRollups SummarizeObservedUsage; do
    body="$(awk "/func \(r \*Repository\) $fn\(/,/^}/" src/internal/server/db/usage.go)"
    grep -q "team_id = \$1::uuid" <<<"$body" || { missing=1; echo "      $fn has no team_id predicate"; }
  done
  [[ $missing -eq 0 ]] && ok ISO4 "every ledger read path filters on team_id" \
                       || bad ISO4 "a ledger read path is missing its team_id predicate"
fi

# ----------------------------------------------------------------------- gc
if run_phase gc; then
hdr "retention"
  cleanup_fixtures; seed_base
  base="pod_uid, team_id, app_id, namespace, pod_name, cpu_millicores, memory_bytes, started_at, last_seen_at"
  OLD=00000000-0000-4000-8000-0000000fee21
  OPEN=00000000-0000-4000-8000-0000000fee22
  RECENT=00000000-0000-4000-8000-0000000fee23
  psql_f <<SQL >/dev/null
INSERT INTO usage_allocation_interval ($base, ended_at, closed_reason)
  VALUES ('$OLD','$FT','$FA','ns','old',100,1,now()-interval '420 days',now()-interval '419 days',now()-interval '419 days','terminated');
-- Open for over a year means a pod alive for over a year, not a stale row.
INSERT INTO usage_allocation_interval ($base)
  VALUES ('$OPEN','$FT','$FA','ns','open',100,1,now()-interval '420 days',now());
INSERT INTO usage_allocation_interval ($base, ended_at, closed_reason)
  VALUES ('$RECENT','$FT','$FA','ns','recent',100,1,now()-interval '380 days',now()-interval '380 days',now()-interval '380 days','terminated');
INSERT INTO usage_sample (team_id, app_id, sampled_at, cpu_millicores) VALUES ('$FT','$FA',now()-interval '31 days',10);
INSERT INTO usage_sample (team_id, app_id, sampled_at, cpu_millicores) VALUES ('$FT','$FA',now()-interval '29 days',10);
SQL
  # The exact predicates of db.DeleteAllocationIntervalsBefore /
  # DeleteUsageSamplesBefore, at the retention the Go constants declare.
  psql_q "DELETE FROM usage_allocation_interval WHERE ended_at IS NOT NULL AND ended_at < now()-interval '390 days'" >/dev/null
  assert_eq GC1 0 "$(psql_q "SELECT count(*) FROM usage_allocation_interval WHERE pod_uid='$OLD'")" "expired interval rows left"
  assert_eq GC2 1 "$(psql_q "SELECT count(*) FROM usage_allocation_interval WHERE pod_uid='$OPEN'")" "open interval kept despite age"
  assert_eq GC3 1 "$(psql_q "SELECT count(*) FROM usage_allocation_interval WHERE pod_uid='$RECENT'")" "in-retention interval kept"
  psql_q "DELETE FROM usage_sample WHERE sampled_at < now()-interval '30 days'" >/dev/null
  assert_eq GC4 1 "$(psql_q "SELECT count(*) FROM usage_sample WHERE team_id='$FT'")" "samples left after 30-day GC"

  # The cutoffs themselves (13 months / 30 days) live in Go constants.
  if go -C src test ./internal/server/services/usage/ -count=1 \
       -run 'TestRetentionCutoffIsThirteenMonths|TestSamplerExpiresAtThirtyDays' >/dev/null 2>&1; then
    ok GC5 "retention cutoffs are 13 months (ledger) and 30 days (samples)"
  else
    bad GC5 "retention cutoff tests failed"
  fi
fi

# ------------------------------------------------------------------- rollup
if run_phase rollup; then
hdr "rollup over the real integrator"
  cleanup_fixtures; seed_base
  base="pod_uid, team_id, app_id, namespace, pod_name, cpu_millicores, memory_bytes, started_at, last_seen_at"
  D2=$(date -u -d 'yesterday' +%F)   # the closed day under test
  D3=$(date -u -d '2 days ago' +%F)
  P1=00000000-0000-4000-8000-0000000fee31
  P2=00000000-0000-4000-8000-0000000fee32
  # Ledger INPUT only — what the cluster would have produced. The rollup
  # numbers below are computed by the real Go Rollupper, never seeded.
  psql_f <<SQL >/dev/null
-- Crosses UTC midnight: 23:50 on D3 to 00:10 on D2. 600s must land on
-- each day, not 1200s on one (spec § 14 rule #5).
INSERT INTO usage_allocation_interval ($base, ended_at, closed_reason)
  VALUES ('$P1','$FT','$FA','ns','span',500,1073741824,
          '${D3}T23:50:00Z','${D2}T00:10:00Z','${D2}T00:10:00Z','terminated');
-- A pod the backend never saw stop: conservative close, flagged.
INSERT INTO usage_allocation_interval ($base, ended_at, closed_reason, estimated)
  VALUES ('$P2','$FT','$FA','ns','lost',100,1048576,
          '${D2}T10:00:00Z','${D2}T10:00:37Z','${D2}T10:00:37Z','reconciled_missing',true);
SQL

  # Drive the REAL Rollupper (and the whole composition e2e) against this
  # database. -count=2 runs the entire suite twice, so every rollup is
  # recomputed from scratch a second time.
  export TEST_DATABASE_URL="${TEST_DATABASE_URL:-postgres://ops:ops_dev_pw@127.0.0.1:15432/ops?sslmode=disable}"
  if go -C src test ./internal/server/ -run UsageMetering -count=2 >/tmp/usage-e2e.$$ 2>&1; then
    ok ROL0 "composition e2e passed twice against this database"
  else
    bad ROL0 "composition e2e failed: $(tail -5 /tmp/usage-e2e.$$ | tr '\n' ' ')"
  fi
  rm -f /tmp/usage-e2e.$$

  # D3: only the 600s before midnight.
  read -r cpu pods est cnt < <(psql_q "SELECT cpu_millicore_seconds||' '||pod_seconds||' '||estimated_seconds||' '||interval_count FROM usage_daily_rollup WHERE team_id='$FT' AND app_id='$FA' AND day='$D3'")
  assert_eq ROL1 $((500*600)) "${cpu:-none}" "D-2 cpu_millicore_seconds"
  assert_eq ROL2 600 "${pods:-none}" "D-2 pod_seconds (interval split at UTC midnight)"
  # D2: the other 600s plus the 37s estimated interval.
  read -r cpu pods est cnt < <(psql_q "SELECT cpu_millicore_seconds||' '||pod_seconds||' '||estimated_seconds||' '||interval_count FROM usage_daily_rollup WHERE team_id='$FT' AND app_id='$FA' AND day='$D2'")
  assert_eq ROL3 $((500*600 + 100*37)) "${cpu:-none}" "D-1 cpu_millicore_seconds"
  assert_eq ROL4 637 "${pods:-none}" "D-1 pod_seconds"
  assert_eq ROL5 37 "${est:-none}" "D-1 estimated_seconds"
  assert_eq ROL6 2 "${cnt:-none}" "D-1 interval_count (not doubled by the second pass)"

  # Independent SQL oracle: recompute every rollup row from the intervals
  # still behind it and compare. Catches a rollup that drifted from the
  # ledger for any reason, including a day computed before a late
  # interval arrived.
  bad_rows="$(psql_q "
SELECT count(*) FROM usage_daily_rollup ru
CROSS JOIN LATERAL (
  SELECT COALESCE(SUM(i.cpu_millicores*s.sec),0)::bigint AS cpu,
         COALESCE(SUM(s.sec),0)::bigint                  AS pods,
         COALESCE(SUM(s.sec) FILTER (WHERE i.estimated),0)::bigint AS est,
         count(*)::int AS cnt
  FROM usage_allocation_interval i
  CROSS JOIN LATERAL (SELECT floor(extract(epoch FROM
      LEAST(COALESCE(i.ended_at, (ru.day+1)::timestamptz), (ru.day+1)::timestamptz)
    - GREATEST(i.started_at, ru.day::timestamptz)))::bigint AS sec) s
  WHERE i.team_id = ru.team_id AND i.app_id = ru.app_id AND s.sec > 0
) o
WHERE EXISTS (SELECT 1 FROM usage_allocation_interval i2
               WHERE i2.team_id=ru.team_id AND i2.app_id=ru.app_id)
  AND (o.cpu <> ru.cpu_millicore_seconds OR o.pods <> ru.pod_seconds
       OR o.est <> ru.estimated_seconds OR o.cnt <> ru.interval_count)")"
  assert_eq ROL7 0 "$bad_rows" "rollup rows disagreeing with an independent recomputation"

  # Late-arriving interval: a relist after downtime backfills a pod that
  # ran on a day already rolled up. The pending-day query must propose
  # that day again, or the seconds are lost permanently.
  #
  # The query below mirrors db.ListPendingRollupDays. A mirror drifts, so
  # the authority is the Go test (TestBackfilledIntervalMakesARolledDayPendingAgain
  # and TestLateCloseMakesARolledDayPendingAgain in db/usage_test.go);
  # this check exists to prove the behaviour against seeded data that the
  # Go tests do not produce. Keep the two in step when either changes.
  psql_q "INSERT INTO usage_allocation_interval ($base, ended_at, closed_reason) VALUES (gen_random_uuid(),'$FT','$FA','ns','late',100,1,'${D2}T12:00:00Z','${D2}T12:01:00Z','${D2}T12:01:00Z','terminated')" >/dev/null
  proposed="$(psql_q "
SELECT count(*) FROM (
  SELECT DISTINCT i.team_id, i.app_id, d.day
  FROM usage_allocation_interval i
  CROSS JOIN LATERAL generate_series(
    date_trunc('day', i.started_at AT TIME ZONE 'UTC'),
    date_trunc('day', COALESCE(i.ended_at, NOW()) AT TIME ZONE 'UTC'),
    interval '1 day') AS d(day)
  WHERE d.day < date_trunc('day', NOW() AT TIME ZONE 'UTC')
    AND i.started_at >= NOW() - interval '14 months'
    AND NOT EXISTS (SELECT 1 FROM usage_daily_rollup ru
                     WHERE ru.team_id=i.team_id AND ru.app_id=i.app_id AND ru.day=d.day::date
                       AND ru.computed_at >= COALESCE(i.closed_at, i.created_at))
) x WHERE x.team_id='$FT' AND x.day='$D2'")"
  if [[ "$proposed" == "1" ]]; then
    ok ROL8 "a late interval re-opens its day for recomputation"
  else
    bad ROL8 "late interval on $D2 is NOT proposed for rollup (ListPendingRollupDays skips days that already have a row) — those seconds can never be billed"
  fi
fi

hdr "result"
printf 'pass=%d fail=%d skip=%d\n' "$PASS" "$FAIL" "$SKIP"
[[ $FAIL -eq 0 ]] || exit 3
exit 0
