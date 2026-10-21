#!/usr/bin/env bash
# deploy/keycloak/realm.json is the canonical copy of the realm the three tools
# share; their copies are checked against it in their own repositories. Here:
# it is the realm dynamo, with a client for each tool, each client's tokens
# carry the audiences of the partners it exchanges them at, and every issuer
# the stack names is that realm.
#
# Needs bash, grep and awk only; no stack and no network.
set -u

SRC="$(cd "$(dirname "$0")/.." && pwd)"
REALM_FILE="deploy/keycloak/realm.json"
REALM="dynamo"
FAILED=0
pass() { echo "   ✅ $1"; }
fail() { echo "   ❌ $1"; FAILED=1; }

echo "🧪 Keycloak realm"

name="$(grep -m1 -oE '^  "realm": "[^"]*"' "${SRC}/${REALM_FILE}" | cut -d'"' -f4)"
[[ "$name" == "$REALM" ]] && pass "the realm is called ${REALM}" \
  || fail "${REALM_FILE} defines the realm '${name}', but the tools sign in through '${REALM}'."

for client in stator armature exciter; do
  grep -qE "\"clientId\": \"${client}\"" "${SRC}/${REALM_FILE}" && pass "it has the client ${client}" \
    || fail "${REALM_FILE} has no client ${client}, so ${client} cannot sign in through it."
done

audiences=$(awk -F'"' '/"clientId":/ {client = $4} /"included.client.audience":/ {print client ":" $4}' "${SRC}/${REALM_FILE}")
for pair in stator:armature stator:exciter exciter:armature exciter:stator armature:exciter; do
  grep -qx "$pair" <<< "$audiences" && pass "${pair%:*}'s tokens are meant for ${pair#*:} too" \
    || fail "${REALM_FILE} has no audience mapper on the client ${pair%:*} for ${pair#*:}, so ${pair#*:} refuses ${pair%:*}'s token exchange; add one (docs/keycloak.md)."
done

stale=$(cd "$SRC" && grep -rnE '/realms/[A-Za-z0-9_-]+' deploy mk Makefile README.md \
  | grep -vE "/realms/(${REALM}|master)\b")
[[ -z "$stale" ]] && pass "every issuer of the stack names /realms/${REALM}" \
  || fail "these name another realm than ${REALM}: $(paste -sd' ' <<< "$stale")"

exit "$FAILED"
