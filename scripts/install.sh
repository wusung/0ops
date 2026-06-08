#!/usr/bin/env sh
# 0ops one-line installer.
# spec: docs/features/end-user-onboarding/spec.md § 3
#
# Usage (zero-arg defaults the official SaaS):
#   curl -fsSL https://raw.githubusercontent.com/wusung/0ops/main/scripts/install.sh | sh
#
# Point at a self-host / staging / local backend:
#   OPS_HOST=https://api.my-domain.com  curl ... | sh
#   OPS_HOST=http://127.0.0.1:18080     curl ... | sh
#
# Env:
#   OPS_HOST      backend host. Triggers post-install `0ops onboard`
#                 (device-flow login + AI CLI auto-wire). Unset → install only.
#   OPS_VERSION   release tag (default: latest)
#   INSTALL_DIR   install target (default: $HOME/.local/bin)
#   OPS_REPO      override repo (default: wusung/0ops)
#   DRY_RUN=1     print actions without downloading/installing
#   NO_ONBOARD=1  skip post-install onboard even when OPS_HOST is set
#
# Installs:
#   $INSTALL_DIR/0ops
#   $INSTALL_DIR/0ops-mcp
# Then (when OPS_HOST set + interactive TTY + NO_ONBOARD unset):
#   $INSTALL_DIR/0ops onboard $OPS_HOST
#
# Verifies sha256 from the release's checksums.txt.

set -eu

OPS_REPO="${OPS_REPO:-wusung/0ops}"
OPS_VERSION="${OPS_VERSION:-latest}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
DRY_RUN="${DRY_RUN:-0}"

log() { printf '\033[1;36m[0ops-install]\033[0m %s\n' "$*" >&2; }
warn() { printf '\033[1;33m[0ops-install]\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31m[0ops-install]\033[0m ERROR: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "$1 not found in PATH"; }
need curl
need tar
need uname

# --- detect platform ---
uname_s="$(uname -s)"
uname_m="$(uname -m)"
case "$uname_s" in
  Linux)  OS=linux ;;
  Darwin) OS=darwin ;;
  *) die "unsupported OS: $uname_s (try the Windows zip from GitHub Release)" ;;
esac
case "$uname_m" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported arch: $uname_m" ;;
esac
log "detected $OS/$ARCH"

# --- resolve version ---
if [ "$OPS_VERSION" = "latest" ]; then
  api="https://api.github.com/repos/${OPS_REPO}/releases/latest"
else
  api="https://api.github.com/repos/${OPS_REPO}/releases/tags/${OPS_VERSION}"
fi
log "querying release: $api"
release_json="$(curl -fsSL "$api" 2>/dev/null)" || die "failed to fetch release info from $api"

# extract tag_name with sed/awk (no jq dependency)
tag="$(printf '%s' "$release_json" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
[ -n "$tag" ] || die "could not parse tag_name from release JSON"
log "version: $tag"

version_no_v="${tag#v}"
asset_name="0ops_${version_no_v}_${OS}_${ARCH}.tar.gz"
checksum_name="checksums.txt"

# Confirm the asset exists in the release payload (defensive — avoid 404 + half install).
if ! printf '%s' "$release_json" | grep -F "\"name\": \"${asset_name}\"" >/dev/null; then
  die "asset not found in release: $asset_name (release has different naming?)"
fi
asset_url="https://github.com/${OPS_REPO}/releases/download/${tag}/${asset_name}"
checksum_url="https://github.com/${OPS_REPO}/releases/download/${tag}/${checksum_name}"
log "asset: $asset_url"
log "checksums: $checksum_url"

if [ "$DRY_RUN" = "1" ]; then
  log "DRY_RUN=1 — would download $asset_url + $checksum_url"
  log "DRY_RUN=1 — would install to $INSTALL_DIR"
  exit 0
fi

# --- download ---
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM
cd "$tmp"
log "downloading…"
curl -fSL -o "$asset_name" "$asset_url" || die "download asset failed"
curl -fSL -o "$checksum_name" "$checksum_url" || die "download checksums failed"

# --- verify sha256 ---
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$asset_name" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$asset_name" | awk '{print $1}')"
else
  die "neither sha256sum nor shasum available; cannot verify checksum"
fi
expected="$(grep -F "$asset_name" "$checksum_name" | awk '{print $1}' | head -n1)"
[ -n "$expected" ] || die "checksum for $asset_name not in $checksum_name"
if [ "$actual" != "$expected" ]; then
  die "checksum mismatch: expected $expected got $actual"
fi
log "checksum OK"

# --- extract + install ---
mkdir -p "$INSTALL_DIR" || die "cannot create $INSTALL_DIR"
tar -xzf "$asset_name"
moved=0
for bin in 0ops 0ops-mcp; do
  src=""
  for cand in "$bin" "./$bin" "*/$bin"; do
    found="$(find . -maxdepth 3 -type f -name "$bin" 2>/dev/null | head -n1)"
    if [ -n "$found" ]; then
      src="$found"
      break
    fi
    # cand kept to satisfy SC2034
    : "$cand"
  done
  [ -n "$src" ] || { warn "$bin not in archive — skipping"; continue; }
  cp "$src" "$INSTALL_DIR/$bin" || die "install $bin to $INSTALL_DIR failed (permission?)"
  chmod +x "$INSTALL_DIR/$bin"
  log "installed: $INSTALL_DIR/$bin"
  moved=$((moved + 1))
done
[ "$moved" -gt 0 ] || die "no binaries installed"

# --- PATH check ---
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    warn "$INSTALL_DIR not in PATH. Add to your shell rc:"
    printf '  bash/zsh: export PATH="%s:$PATH"\n' "$INSTALL_DIR" >&2
    printf '  fish:     fish_add_path %s\n' "$INSTALL_DIR" >&2
    ;;
esac

# --- post-install onboard ---
# One-liner UX: if OPS_HOST is set + user didn't opt out, run `0ops onboard`.
# Device-flow login prints a code + URL (no stdin needed); mcp setup runs with
# --yes default so it doesn't prompt. Works under `curl ... | sh` (no TTY).
ops_bin="$INSTALL_DIR/0ops"
should_onboard=1
[ -z "${OPS_HOST:-}" ]      && should_onboard=0
[ "${NO_ONBOARD:-0}" = "1" ] && should_onboard=0
[ -x "$ops_bin" ]            || should_onboard=0

if [ "$should_onboard" = "1" ]; then
  log "running: 0ops onboard $OPS_HOST"
  if "$ops_bin" onboard "$OPS_HOST"; then
    cat >&2 <<EOF

================================================================================
DONE — 0ops $tag installed + onboarded to ${OPS_HOST}.
Restart your AI CLI to load the 0ops MCP server.
EOF
  else
    warn "onboard step failed (binary may be an older release without 'onboard'); re-run after upgrading:"
    printf '  %s onboard %s\n' "$ops_bin" "$OPS_HOST" >&2
    cat >&2 <<EOF

================================================================================
DONE — 0ops $tag installed; onboard FAILED.
Manual steps:
  $ops_bin auth login --host=$OPS_HOST
  $ops_bin mcp setup claude-code     # or: codex
EOF
  fi
else
  cat >&2 <<EOF

================================================================================
DONE — 0ops $tag installed.

Next:
  0ops auth login --host=<your-0ops-backend>     # eg. https://api.winshare.tw
  0ops mcp setup claude-code                      # 接 Claude Code
  # 或：
  0ops mcp setup codex                            # 接 Codex CLI

Hint: set OPS_HOST before piping to skip these steps:
  OPS_HOST=https://api.winshare.tw curl -fsSL https://raw.githubusercontent.com/${OPS_REPO}/main/scripts/install.sh | sh

Quickstart: https://github.com/${OPS_REPO}/blob/main/docs/quickstart.md
EOF
fi
