#!/usr/bin/env bash
# versions.yaml pins one released version of each tool and of the suite's
# Kroki, as X.Y.Z, and nothing else: the stack, the release workflow and the
# chart all read it line by line. versions-upgrade-from.yaml names the
# combination make test-upgrade upgrades from, no tool newer than its pin.
#
# Needs bash and grep only; no stack and no network.
set -u

SRC="$(cd "$(dirname "$0")/.." && pwd)"
FILE="${SRC}/versions.yaml"
FAILED=0
pass() { echo "   ✅ $1"; }
fail() { echo "   ❌ $1"; FAILED=1; }

echo "🧪 versions.yaml"

for t in stator armature exciter kroki; do
  n=$(grep -cE "^${t}:" "$FILE")
  v=$(sed -n "s/^${t}: *//p" "$FILE")
  if [[ "$n" == 1 && "$v" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    pass "${t} is pinned to ${v}"
  else
    fail "versions.yaml must pin ${t} exactly once as X.Y.Z, the tag its images carry; found '${v}' ${n} times."
  fi
done

others=$(grep -vE '^(#|$|(stator|armature|exciter|kroki): )' "$FILE")
[[ -z "$others" ]] && pass "nothing else is in it" \
  || fail "versions.yaml holds only comments and the four pins; remove: $others"

echo "🧪 versions-upgrade-from.yaml"
FROM="${SRC}/versions-upgrade-from.yaml"
for t in stator armature exciter; do
  n=$(grep -cE "^${t}:" "$FROM")
  v=$(sed -n "s/^${t}: *//p" "$FROM")
  pinned=$(sed -n "s/^${t}: *//p" "$FILE")
  if [[ "$n" != 1 || ! "$v" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    fail "versions-upgrade-from.yaml must name ${t} exactly once as X.Y.Z, a released tag; found '${v}' ${n} times."
  elif [[ "$(printf '%s\n' "$v" "$pinned" | sort -V | tail -n 1)" != "$pinned" ]]; then
    fail "versions-upgrade-from.yaml names ${t} ${v}, newer than the pin ${pinned}; it names what an installation upgrades from."
  else
    pass "the upgrade starts from ${t} ${v}"
  fi
done

others=$(grep -vE '^(#|$|(stator|armature|exciter): )' "$FROM")
[[ -z "$others" ]] && pass "nothing else is in it" \
  || fail "versions-upgrade-from.yaml holds only comments and the three tools' versions; remove: $others"

exit "$FAILED"
