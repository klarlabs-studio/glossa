#!/usr/bin/env bash
# A release must not restart the database. Renders the chart twice, as
# it is and with its version and appVersion bumped, and fails when a
# StatefulSet's pod template differs between the two: a changed pod
# template rolls the pod, so Postgres or MinIO would restart on every
# release although nothing about them changed (#106).
#
#   ci/check-stateful-rollout.sh [values.yaml ...]   (default: the ci/*-values.yaml that render one)
set -euo pipefail

chart=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

cp -R "$chart" "$work/bumped"
sed -i.bak -E \
  -e 's/^version: .*/version: 99.0.0/' \
  -e 's/^appVersion: .*/appVersion: "99.0.0"/' \
  "$work/bumped/Chart.yaml"

if (($# == 0)); then set -- "$chart"/ci/*-values.yaml; fi

templates() { # chart values -> each StatefulSet's name and pod template, one JSON line each
  helm template t "$1" --namespace glossa-platform -f "$2" | python3 -c '
import json, sys, yaml
for doc in yaml.safe_load_all(sys.stdin):
    if doc and doc.get("kind") == "StatefulSet":
        print(json.dumps([doc["metadata"]["name"], doc["spec"]["template"]], sort_keys=True))
'
}

checked=0
for values in "$@"; do
  before=$(templates "$chart" "$values")
  [[ -n $before ]] || continue
  after=$(templates "$work/bumped" "$values")
  if [[ $before != "$after" ]]; then
    echo "::error::$values: a release bump changes a StatefulSet pod template, so every release restarts it:" >&2
    diff <(printf '%s\n' "$before" | python3 -m json.tool --json-lines) \
      <(printf '%s\n' "$after" | python3 -m json.tool --json-lines) >&2 || true
    exit 1
  fi
  checked=$((checked + 1))
done
((checked > 0)) || { echo "::error::no values file renders a StatefulSet; nothing was checked" >&2; exit 1; }
echo "stateful pod templates unchanged by a release bump ($checked values files)"
