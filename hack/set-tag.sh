#!/usr/bin/env bash
set -euo pipefail

TAG=${1:-$(git describe --tags --always 2>/dev/null || git rev-parse --short HEAD)}
export TAG        # make TAG visible to yq

yq -i '.controllerManager.manager.image.tag = env(TAG)' helm/yamo/values.yaml
yq -i '.version = env(TAG) | .appVersion = env(TAG)' helm/yamo/Chart.yaml
yq -i '(.images[] | select(.name=="controller") | .newTag) = env(TAG)' config/manager/kustomization.yaml

echo "Set tag to $TAG"
