#!/usr/bin/env bash
# Initialize examples/node-demo as a git repository so the file:// inspector
# (per ADR-0012 § 5.3) can read HEAD / default branch.
set -euo pipefail
cd "$(dirname "$0")"
if [ -d .git ]; then
  echo "examples/node-demo already initialized"
  exit 0
fi
git init -q -b main
git -c user.email=dev@0ops.local -c user.name=dev add .
git -c user.email=dev@0ops.local -c user.name=dev commit -q -m "initial node-demo"
echo "examples/node-demo initialized as git repo"
