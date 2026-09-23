#!/usr/bin/env bash
# e2e: resource-usage-metering
#
# AGENTS.md requires one e2e per feature that exercises the headline
# guarantee against the assembled system. For this feature that is a
# Go composition test (src/internal/server/e2e_usage_metering_test.go):
# real Postgres, real reconciler, real rollup, real router and RBAC,
# with only the Kubernetes API substituted — the one external
# dependency AGENTS.md permits mocking.
#
# It is not a compose-stack e2e because the dev stack runs with
# namespace isolation disabled and therefore has no cluster to read
# pods from; substituting the K8s API inside the stack would mock the
# same boundary with more moving parts, not fewer.
#
# Deferred to staging (see docs/features/resource-usage-metering/spec.md
# § 11): the real metrics-free path against a live K3s with real pod
# lifecycles, and the RBAC grant actually resolving in-cluster.
set -euo pipefail

cd "$(dirname "$0")/.."

: "${TEST_DATABASE_URL:=${DATABASE_URL:-}}"
if [[ -z "${TEST_DATABASE_URL}" ]]; then
  echo "e2e-usage-metering: set DATABASE_URL or TEST_DATABASE_URL (compose stack DSN)" >&2
  exit 2
fi
export TEST_DATABASE_URL="${TEST_DATABASE_URL/@db:5432/@127.0.0.1:15432}"

echo "e2e-usage-metering: allocation ledger → rollup → HTTP contract"
go -C src test ./internal/server/ -run UsageMetering -count=1 -v
