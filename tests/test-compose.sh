#!/usr/bin/env bash
# The stack is one compose file over the pinned images: compose must accept it
# with the variables mk/stack.mk exports, every tool image must be the pinned
# tag of that tool's registry, and nothing of a tool may be built here.
# The suite's one Kroki is pinned, in secure mode and without a way out.
#
# Needs docker compose and jq; no stack is started and nothing is pulled.
set -u

SRC="$(cd "$(dirname "$0")/.." && pwd)"
FAILED=0
pass() { echo "   ✅ $1"; }
fail() { echo "   ❌ $1"; FAILED=1; }

echo "🧪 compose stack"

if config="$(make -s -C "$SRC" compose-config 2>&1)"; then
  pass "compose accepts the file"
else
  fail "docker compose config refused deploy/docker-compose.yml: $config"
fi

images="$(make -s -C "$SRC" compose-config ARGS=--images 2>/dev/null)"
for t in stator armature exciter; do
  v=$(sed -n "s/^${t}: *//p" "$SRC/versions.yaml")
  bad=$(grep -E "/${t}-" <<< "$images" | grep -vE ":${v}$")
  n=$(grep -cE "/${t}-[a-z]+:${v}$" <<< "$images")
  [[ -z "$bad" && "$n" -gt 0 ]] && pass "${t}'s ${n} images are all at ${v}" \
    || fail "${t}'s images must all be the pinned ${v}; found: $(grep -E "/${t}-" <<< "$images" | paste -sd' ')"
done

json="$(make -s -C "$SRC" compose-config ARGS='--format json' 2>/dev/null)"
q() { jq -r "$1" <<< "$json" 2>/dev/null; }

kroki_pin=$(sed -n "s/^kroki: *//p" "$SRC/versions.yaml")
[[ "$(q '.services.kroki.image')" == "yuzutech/kroki:${kroki_pin}" ]] \
  && pass "the suite's Kroki is the pinned ${kroki_pin}" \
  || fail "the kroki service runs '$(q '.services.kroki.image')'; set it to yuzutech/kroki:${kroki_pin}, the kroki line of versions.yaml."
[[ "$(q '.services.kroki.environment.KROKI_SAFE_MODE')" == secure ]] \
  && pass "Kroki runs in secure mode" \
  || fail "the kroki service must set KROKI_SAFE_MODE to secure, or a diagram can read Kroki's files and addresses."
[[ "$(q '.services.kroki.ports // [] | length')" == 0 ]] \
  && pass "Kroki publishes no port" \
  || fail "the kroki service publishes a port; only the apis may reach it, so remove its ports."
open=$(q '. as $c | .services.kroki.networks // {} | keys[] | select($c.networks[.].internal != true)')
[[ -n "$(q '.services.kroki.networks // {} | keys[]')" && -z "$open" ]] \
  && pass "Kroki is only on internal networks, so it has no way out" \
  || fail "the kroki service is on networks with a way out ('$open'); keep it on the internal network kroki alone."
others=$(q '.services | to_entries[] | select(.value.networks // {} | has("kroki")) | .key' \
  | grep -vxE 'kroki|(stator|armature|exciter)-api' | paste -sd' ')
[[ -z "$others" ]] && pass "only the tools' apis share Kroki's network" \
  || fail "only the tools' apis may join the network kroki; remove it from: $others"
for t in stator exciter; do
  T=$(tr a-z A-Z <<< "$t")
  url=$(q ".services[\"${t}-api\"].environment.${T}_KROKI_URL")
  [[ "$url" == http://kroki:8000 && "$(q ".services[\"${t}-api\"].networks | has(\"kroki\")")" == true ]] \
    && pass "${t}'s api draws through the suite's Kroki" \
    || fail "${t}-api must set ${T}_KROKI_URL to http://kroki:8000 and join the network kroki; it has '${url}'."
done

built=$(grep -nE '^\s+build:' "$SRC/deploy/docker-compose.yml" | wc -l)
[[ "$built" == 1 ]] && pass "only dynamo-connect is built" \
  || fail "deploy/docker-compose.yml builds ${built} services; a tool runs from its released image only."

exit "$FAILED"
