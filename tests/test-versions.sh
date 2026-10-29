#!/usr/bin/env bash
# versions.yaml pins one released version of each tool and of the suite's
# Kroki, as X.Y.Z, and nothing else: the stack, the release workflow and the
# chart all read it line by line.
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

exit "$FAILED"
