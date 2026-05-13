#!/usr/bin/env bash
set -euo pipefail

# render-and-push-gitops.sh
# Renders kustomization manifests and pushes to 0ops-gitops repo
# Expects environment variables:
#   - TEAM_SLUG, APP_SLUG, COMMIT_SHA, IMAGE_REF
#   - GITOPS_DEPLOY_KEY (SSH private key)
#   - GITOPS_REPO (optional, defaults to 0ops-gitops)

GITOPS_REPO="${GITOPS_REPO:-0ops-gitops}"
GITOPS_URL="${GITOPS_URL:-git@github.com:winshare/${GITOPS_REPO}.git}"
RETRY_MAX=5
RETRY_COUNT=0

# Setup SSH key
mkdir -p ~/.ssh
echo "$GITOPS_DEPLOY_KEY" > ~/.ssh/gitops_key
chmod 600 ~/.ssh/gitops_key
ssh-keyscan -t rsa github.com >> ~/.ssh/known_hosts 2>/dev/null

# Clone gitops repo
git clone -b main --depth 1 "$GITOPS_URL" gitops-repo || {
echo "Failed to clone gitops repo"
exit 1
}
cd gitops-repo

# Render manifests (placeholder)
mkdir -p "apps/${TEAM_SLUG}/${APP_SLUG}"
cat > "apps/${TEAM_SLUG}/${APP_SLUG}/kustomization.yaml" <<-KUSTOMIZATION
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

images:
  - name: app
    newName: ${IMAGE_REF}

namespace: team-${TEAM_SLUG}
KUSTOMIZATION

# Commit & push with retry + rebase
git config user.email "ops@0ops.local"
git config user.name "0ops-bot"
git add "apps/${TEAM_SLUG}/${APP_SLUG}"

while [ $RETRY_COUNT -lt $RETRY_MAX ]; do
if git commit -m "Deploy ${TEAM_SLUG}/${APP_SLUG}:${COMMIT_SHA}"; then
if GIT_SSH_COMMAND="ssh -i ~/.ssh/gitops_key" git push origin main; then
echo "Pushed to gitops repo"
exit 0
fi
# Push conflict: rebase
GIT_SSH_COMMAND="ssh -i ~/.ssh/gitops_key" git pull --rebase origin main || true
fi
RETRY_COUNT=$((RETRY_COUNT + 1))
done

echo "Failed to push gitops after $RETRY_MAX retries"
exit 1
