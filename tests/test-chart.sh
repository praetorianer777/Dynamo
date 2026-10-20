#!/usr/bin/env bash
# The umbrella chart installs exactly what versions.yaml pins, and its realm
# is the canonical one. Rendering and installing it is make test-chart's job.
#
# Needs bash, cmp and sed only; no cluster and no network.
set -u

SRC="$(cd "$(dirname "$0")/.." && pwd)"
CHART="${SRC}/deploy/charts/dynamo"
FAILED=0
pass() { echo "   ✅ $1"; }
fail() { echo "   ❌ $1"; FAILED=1; }

echo "🧪 umbrella chart"

for t in stator armature exciter; do
  pin=$(sed -n "s/^${t}: *//p" "$SRC/versions.yaml")
  dep=$(sed -n "/- name: ${t}\$/{n;s/^ *version: *//p}" "$CHART/Chart.yaml")
  [[ "$dep" == "$pin" ]] && pass "the chart depends on ${t} ${pin}, the pin" \
    || fail "Chart.yaml depends on ${t} '${dep}', but versions.yaml pins ${pin}; change both together."
done

cmp -s "$CHART/files/realm.json" "$SRC/deploy/keycloak/realm.json" \
  && pass "the bundled Keycloak imports the canonical realm" \
  || fail "deploy/charts/dynamo/files/realm.json differs from deploy/keycloak/realm.json, the canonical copy; copy it over."

exit "$FAILED"
