#!/usr/bin/env bash
# spec: docs/features/self-hosted-runner/spec.md § 5
# 在 PROD_HOST 上安裝 actions-runner + pack + podman + zstd，
# 註冊為 label=0ops-builder 的 self-hosted runner，systemd 服務化。
#
# 入口：./manage.sh prod-install-runner
#
# 冪等：runner 已 online → 印 message 後 exit 0；重跑會 refresh label。

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

ENV_FILE="${ENV_FILE:-deploy/bootstrap/.env.prod}"
[ -f "$ENV_FILE" ] || { echo "ERROR: $ENV_FILE not found" >&2; exit 1; }

set -a
# shellcheck disable=SC1090
. "$ENV_FILE"
set +a

: "${PROD_HOST:?}"
: "${PROD_SSH_KEY:?}"

GHA_REPO="${GHA_REPO:-wusung/0ops}"
GHA_RUNNER_NAME="${GHA_RUNNER_NAME:-0ops-prod-1}"
GHA_RUNNER_LABEL="${GHA_RUNNER_LABEL:-0ops-builder}"
GHA_RUNNER_VERSION="${GHA_RUNNER_VERSION:-2.319.1}"
PACK_VERSION="${PACK_VERSION:-0.36.0}"

log() { printf '\033[1;36m[install-runner]\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31m[install-runner]\033[0m %s\n' "$*" >&2; exit 1; }

command -v gh >/dev/null 2>&1 || die "gh CLI not installed locally (brew install gh / pacman -S github-cli)"
gh auth status >/dev/null 2>&1 || die "gh CLI not authenticated; run 'gh auth login' first"

log "fetching ephemeral registration token from GitHub"
REG_TOKEN=$(gh api -X POST "/repos/${GHA_REPO}/actions/runners/registration-token" -q '.token')
[ -n "$REG_TOKEN" ] || die "failed to obtain registration-token"

REMOTE_SCRIPT=$(mktemp)
trap 'rm -f "$REMOTE_SCRIPT"' EXIT

cat >"$REMOTE_SCRIPT" <<REMOTE
#!/usr/bin/env bash
set -euo pipefail

GHA_REPO="${GHA_REPO}"
GHA_RUNNER_NAME="${GHA_RUNNER_NAME}"
GHA_RUNNER_LABEL="${GHA_RUNNER_LABEL}"
GHA_RUNNER_VERSION="${GHA_RUNNER_VERSION}"
PACK_VERSION="${PACK_VERSION}"
REG_TOKEN="${REG_TOKEN}"

RUNNER_USER=ghrunner
RUNNER_HOME=/opt/0ops-runner

# ----- system pkgs (zstd / jq / curl / tar / podman) -----
if command -v apt-get >/dev/null 2>&1; then
  sudo apt-get update -qq
  sudo apt-get install -y --no-install-recommends zstd jq curl tar ca-certificates podman uidmap
elif command -v pacman >/dev/null 2>&1; then
  sudo pacman -Sy --noconfirm --needed zstd jq curl tar ca-certificates podman
elif command -v dnf >/dev/null 2>&1; then
  sudo dnf install -y zstd jq curl tar ca-certificates podman
else
  echo "unsupported pkg manager (apt/pacman/dnf required)" >&2
  exit 1
fi

# ----- runner user -----
if ! id "\$RUNNER_USER" >/dev/null 2>&1; then
  sudo useradd -m -d "\$RUNNER_HOME" -s /bin/bash "\$RUNNER_USER"
fi
sudo mkdir -p "\$RUNNER_HOME"
sudo chown -R "\$RUNNER_USER:\$RUNNER_USER" "\$RUNNER_HOME"
sudo chmod 0700 "\$RUNNER_HOME"

# ----- actions-runner -----
ARCH=\$(uname -m)
case "\$ARCH" in
  x86_64|amd64) RUNNER_ARCH=x64 ;;
  aarch64|arm64) RUNNER_ARCH=arm64 ;;
  *) echo "unsupported arch: \$ARCH" >&2; exit 1 ;;
esac
RUNNER_TGZ="actions-runner-linux-\${RUNNER_ARCH}-\${GHA_RUNNER_VERSION}.tar.gz"

if [ ! -f "\$RUNNER_HOME/config.sh" ]; then
  echo "[install-runner] downloading actions-runner \$GHA_RUNNER_VERSION"
  sudo -u "\$RUNNER_USER" bash -c "cd '\$RUNNER_HOME' && curl -fsSL -o ar.tgz \\
    'https://github.com/actions/runner/releases/download/v\${GHA_RUNNER_VERSION}/\${RUNNER_TGZ}' && \\
    tar xzf ar.tgz && rm -f ar.tgz"
else
  echo "[install-runner] actions-runner already extracted at \$RUNNER_HOME — skip download"
fi

# config（冪等：--replace 會覆寫同名 runner）
echo "[install-runner] (re)registering runner '\$GHA_RUNNER_NAME' with label '\$GHA_RUNNER_LABEL'"
sudo -u "\$RUNNER_USER" bash -c "cd '\$RUNNER_HOME' && \\
  ./config.sh --unattended --replace \\
    --url 'https://github.com/\${GHA_REPO}' \\
    --token '\${REG_TOKEN}' \\
    --name '\${GHA_RUNNER_NAME}' \\
    --labels '\${GHA_RUNNER_LABEL}' \\
    --work _work"

# ----- pack CLI -----
if ! command -v pack >/dev/null 2>&1 || ! pack version 2>/dev/null | grep -q "\${PACK_VERSION}"; then
  echo "[install-runner] installing pack \$PACK_VERSION"
  curl -fsSL -o /tmp/pack.tgz \\
    "https://github.com/buildpacks/pack/releases/download/v\${PACK_VERSION}/pack-v\${PACK_VERSION}-linux.tgz"
  sudo tar -C /usr/local/bin -xzf /tmp/pack.tgz pack
  sudo chmod +x /usr/local/bin/pack
  rm -f /tmp/pack.tgz
else
  echo "[install-runner] pack already installed: \$(pack version | head -n1)"
fi

# ----- podman socket（給 pack 用） -----
sudo loginctl enable-linger "\$RUNNER_USER" 2>/dev/null || true
sudo -u "\$RUNNER_USER" XDG_RUNTIME_DIR="/run/user/\$(id -u \$RUNNER_USER)" \\
  systemctl --user enable --now podman.socket 2>/dev/null || true

# ----- systemd service -----
SERVICE=/etc/systemd/system/0ops-runner.service
sudo tee "\$SERVICE" >/dev/null <<UNIT
[Unit]
Description=0ops self-hosted GitHub Actions runner
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=\$RUNNER_USER
Group=\$RUNNER_USER
WorkingDirectory=\$RUNNER_HOME
ExecStart=\$RUNNER_HOME/run.sh
Restart=always
RestartSec=10
KillMode=process
KillSignal=SIGTERM
TimeoutStopSec=5min
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
UNIT

sudo systemctl daemon-reload
sudo systemctl enable --now 0ops-runner.service
sudo systemctl --no-pager status 0ops-runner.service | head -n 10 || true
REMOTE

log "running install on $PROD_HOST"
ssh -i "$PROD_SSH_KEY" "$PROD_HOST" 'bash -s' <"$REMOTE_SCRIPT"

log "verifying runner online via GitHub API (may take 30s)"
for i in $(seq 1 12); do
  status=$(gh api "/repos/${GHA_REPO}/actions/runners" \
    -q ".runners[] | select(.name==\"${GHA_RUNNER_NAME}\") | .status" || true)
  if [ "$status" = "online" ]; then
    log "runner online: ${GHA_RUNNER_NAME}"
    break
  fi
  printf '\r[install-runner] runner status=%s (try %d/12)' "${status:-unknown}" "$i" >&2
  sleep 5
done
[ "$status" = "online" ] || die "runner did not come online within 60s — check ssh logs"

log "next: gh variable set GHA_RUNNER_LABEL --repo ${GHA_REPO} --body ${GHA_RUNNER_LABEL}"
log "  (then next workflow run will use self-hosted)"
