#!/usr/bin/env bash
# The stack is one compose file over the pinned images: compose must accept it
# with the variables mk/stack.mk exports, every tool image must be the pinned
# tag of that tool's registry, and nothing of a tool may be built here.
#
# Needs docker compose; no stack is started and nothing is pulled.
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

built=$(grep -nE '^\s+build:' "$SRC/deploy/docker-compose.yml" | wc -l)
[[ "$built" == 1 ]] && pass "only dynamo-connect is built" \
  || fail "deploy/docker-compose.yml builds ${built} services; a tool runs from its released image only."

exit "$FAILED"
