#!/usr/bin/env bash
# The umbrella chart installs exactly what versions.yaml pins, and its realm
# is the canonical one. Rendering and installing it is make test-chart's job.
#
# Needs bash, cmp, make and sed only; no cluster and no network.
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

# Two checkouts, seen through two links to this one, since make takes the
# checkout's path from where it finds the Makefile.
links=$(mktemp -d)
ln -s "$SRC" "$links/a"
ln -s "$SRC" "$links/b"
a=$(env -u DYNAMO_CLUSTER make -s -f "$links/a/Makefile" chart-cluster 2>&1)
b=$(env -u DYNAMO_CLUSTER make -s -f "$links/b/Makefile" chart-cluster 2>&1)
[[ "$a" == dynamo-chart-* && "$a" != "$b" ]] \
  && pass "two checkouts test the chart in two kind clusters, $a and $b" \
  || fail "two checkouts would share the kind cluster ('$a' and '$b'); mk/chart.mk must name it after the checkout."
c=$(DYNAMO_CLUSTER=mine make -s -f "$links/a/Makefile" chart-cluster 2>&1)
[[ "$c" == mine ]] && pass "DYNAMO_CLUSTER names the cluster when set" \
  || fail "DYNAMO_CLUSTER=mine gave the cluster '$c'; mk/chart.mk must keep a name that is set."
rm -rf "$links"

exit "$FAILED"
