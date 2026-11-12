#!/usr/bin/env bash
# Builds CONTEXT and pushes it to the registry as IMAGE, from a BuildKit
# builder that has a store of its own. `docker login` for the registry comes
# first, in the caller.
# Usage: push-image.sh ghcr.io/owner/name:tag .
#
# Not `docker build` and `docker push`, and this is why. A hosted runner's
# Docker keeps images in containerd's store, which remembers every repository
# a layer was pulled from, and `docker push` asks the registry to mount a layer
# it knows from another repository of that registry rather than upload it. The
# runner comes with ghcr.io/github/github-mcp-server in that store, and that
# image shares the API's distroless base. The first deploy to the VPS
# (2026-11-12, Deploy run 43138440875) mounted those twelve layers from it,
# uploaded its own seven, and ghcr.io refused the image manifest: "error from
# registry: denied". The job had packages: write and the login had succeeded.
#
# A builder in a container knows only what this build pulled, so it has nothing
# to mount and uploads every layer. It cannot see the runner's local images
# either, so mirror-images.sh does nothing for it: Docker Hub's mirror is named
# to BuildKit instead, for the bases and for BuildKit's own image
# (docs/setup/ci.md, "Where CI pulls images from").
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: push-image.sh IMAGE CONTEXT" >&2
  exit 2
fi
image="$1"
context="$2"

config="$(mktemp)"
cat > "$config" <<'EOF'
[registry."docker.io"]
  mirrors = ["mirror.gcr.io"]
EOF

# The builder runs BuildKit's image, which is on Docker Hub too. One the mirror
# cannot serve is pulled from Docker Hub, as mirror-images.sh leaves a base.
buildkit="mirror.gcr.io/moby/buildkit:buildx-stable-1"
options=()
if docker pull --quiet "$buildkit" >/dev/null; then
  options+=(--driver-opt "image=$buildkit")
else
  echo "::warning::$buildkit could not be pulled; the builder will pull BuildKit from Docker Hub"
fi

# Named to buildx by --builder, never made the default: a `docker build` later
# in the same job still builds into the runner's store.
builder="$(docker buildx create --driver docker-container --buildkitd-config "$config" "${options[@]}")"
trap 'docker buildx rm --force "$builder" >/dev/null 2>&1 || true; rm -f "$config"' EXIT

docker buildx build --builder "$builder" --push --tag "$image" "$context"
