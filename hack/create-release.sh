#!/usr/bin/env bash
set -euo pipefail

# Ask for version; allow empty to fall back to git describe/short SHA
read -rp "Target version (empty = git describe --tags --always): " TAG_INPUT
TAG=${TAG_INPUT:-$(git describe --tags --always 2>/dev/null || git rev-parse --short HEAD)}
export TAG

echo "Using version: $TAG"

# Update chart + kustomize references
yq -i '.controllerManager.manager.image.tag = env(TAG)' helm/yamo/values.yaml
yq -i '.version = env(TAG) | .appVersion = env(TAG)' helm/yamo/Chart.yaml
yq -i '(.images[] | select(.name=="controller") | .newTag) = env(TAG)' config/manager/kustomization.yaml

echo "Updated Helm and kustomize to version $TAG."

# Stage and commit version bump so the tag points to the updated manifests
git add helm/yamo/values.yaml helm/yamo/Chart.yaml config/manager/kustomization.yaml
if git diff --cached --quiet; then
  echo "No changes to commit."
else
  git commit -m "chore: bump version ${TAG}"
  echo "Committed version bump to $TAG."
fi

# Decide release vs. local image build
read -rp "Is this a release to be published? [y/N]: " RELEASE
RELEASE=${RELEASE:-N}

if [[ "$RELEASE" =~ ^[Yy]$ ]]; then
  
  git tag -a "$TAG" -m "Release $TAG"
  echo "Created git tag $TAG. Run 'git push --tags' when you're ready to publish."
else
  make docker-build IMG="ghcr.io/elninotech/yet-another-mysql-operator:${TAG}"
fi
