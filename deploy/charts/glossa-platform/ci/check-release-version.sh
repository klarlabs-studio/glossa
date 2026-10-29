#!/usr/bin/env bash
# Checks that a release's version is stated once and taken from the tag:
#   - .kiln.yaml: every image publish takes VERSION from ${kiln.version} and
#     REVISION from ${kiln.sha} (kiln >= 0.7.0 fills them from the tag and
#     commit it builds), so no version is hand-bumped there
#   - Chart.yaml: appVersion (the chart's default image tag is v<appVersion>)
#   - the release tag, when given (vX.Y.Z or X.Y.Z), matches appVersion
#
#   ci/check-release-version.sh            # .kiln.yaml's args
#   ci/check-release-version.sh v0.5.4     # ... and the tag about to be pushed
#
# Runs in CI (platform.yml's chart job) and belongs before tagging a release.
set -euo pipefail

root="$(cd "$(dirname "$0")/../../../.." && pwd)"
kiln="$root/.kiln.yaml"
chart="$root/deploy/charts/glossa-platform/Chart.yaml"

app=$(sed -nE 's/^appVersion:[[:space:]]*"?([^"[:space:]]+)"?.*/\1/p' "$chart" | head -1)
[ -n "$app" ] || { echo "no appVersion in $chart" >&2; exit 1; }

status=0
publishes=$(grep -cE '^[[:space:]]*-[[:space:]]*kind:[[:space:]]*image' "$kiln" || true)
[ "$publishes" -gt 0 ] || { echo "no image publishes in $kiln" >&2; exit 1; }
for want in 'VERSION: "${kiln.version}"' 'REVISION: "${kiln.sha}"'; do
  have=$(grep -cF "$want" "$kiln" || true)
  if [ "$have" != "$publishes" ]; then
    echo "error: $have of $publishes image publishes in .kiln.yaml set $want" >&2
    status=1
  fi
done

if [ $# -ge 1 ]; then
  tag_version="${1#v}"
  if [ "$tag_version" != "$app" ]; then
    echo "error: release tag '$1' does not match appVersion '$app'; bump the chart first, then tag" >&2
    status=1
  fi
fi

if [ "$status" -eq 0 ]; then
  echo "release version $app: .kiln.yaml takes it from the tag${1:+, and tag $1 matches Chart.yaml}"
fi
exit "$status"
