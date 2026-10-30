#!/usr/bin/env bash
# Writes the pinned API contract into api/ (D-082): openapi.yaml, testdata/
# and catalogs/ from @quizzivy/sdk at the version in api/sdk-version. The
# server generates its code from that copy and its tests read it, so api/ is
# never edited by hand: change the contract in Quizzivy/sdk, then bump the pin.
#
#   scripts/contract/sync.sh                 from the registry (GitHub Packages)
#   CONTRACT=../sdk/api scripts/contract/sync.sh
#                                            from a local sdk checkout, to try a
#                                            contract before it is published
#
# The registry needs a token even to read: NODE_AUTH_TOKEN (CI) or an
# ~/.npmrc entry for npm.pkg.github.com with read:packages (README.md).
set -euo pipefail

cd "$(dirname "$0")/../.."
PINNED=(openapi.yaml testdata catalogs)

if [[ -n ${CONTRACT:-} ]]; then
  source=$CONTRACT
  echo "contract: from $source (local, not the pin)"
else
  version=$(tr -d '[:space:]' < api/sdk-version)
  [[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+(-dev\.[0-9]+\.g[0-9a-f]+)?$ ]] \
    || { echo "api/sdk-version holds '$version', not an SDK version" >&2; exit 1; }
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  printf '@quizzivy:registry=https://npm.pkg.github.com\n//npm.pkg.github.com/:_authToken=${NODE_AUTH_TOKEN}\n' > "$tmp/.npmrc"
  if [[ -n ${NODE_AUTH_TOKEN:-} ]]; then export NPM_CONFIG_USERCONFIG=$tmp/.npmrc; fi
  (cd "$tmp" && npm pack --silent "@quizzivy/sdk@$version" >/dev/null)
  tar -xzf "$tmp"/quizzivy-sdk-*.tgz -C "$tmp"
  source=$tmp/package
  echo "contract: @quizzivy/sdk@$version"
fi

for entry in "${PINNED[@]}"; do
  [[ -e $source/$entry ]] || { echo "$source has no $entry" >&2; exit 1; }
  rm -rf "api/$entry"
  cp -R "$source/$entry" "api/$entry"
done
# A local sdk checkout keeps its catalogs' README beside them; the package does not.
rm -f api/catalogs/README.md
