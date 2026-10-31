#!/usr/bin/env bash
# deploy/keycloak/realm.json is the canonical copy of the realm the tools
# share; their copies are checked against it in their own repositories. Here:
# it is the realm dynamo, with a client for each tool and Rotor's two, each
# client's tokens carry the audiences of the partners it exchanges them at,
# and every issuer the stack names is that realm.
#
# And deploy/keycloak/realm.server.json, which a server's Keycloak imports, is
# the canonical realm as make realm-server makes it.
#
# Needs bash, grep, awk and docker; no stack.
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

for client in stator armature exciter rotor rotor-control-plane; do
  grep -qE "\"clientId\": \"${client}\"" "${SRC}/${REALM_FILE}" && pass "it has the client ${client}" \
    || fail "${REALM_FILE} has no client ${client}, so ${client} cannot sign in through it."
done

# Rotor compares its realm with this one whole, so what Rotor needs of it lives here.
for client in rotor rotor-control-plane; do
  grep -qE "\"serviceAccountClientId\": \"${client}\"" "${SRC}/${REALM_FILE}" && pass "${client} has its service account" \
    || fail "${REALM_FILE} has no user service-account-${client}, so ${client}'s service account has none of its roles; copy it from Rotor's realm."
done
grep -qE '"name": "rotor-administrators"' "${SRC}/${REALM_FILE}" && pass "it has the group rotor-administrators" \
  || fail "${REALM_FILE} has no group rotor-administrators, so nobody administers Rotor; add it (docs/keycloak.md)."
in_group=$(awk -F'"' '/"username":/ {user = $4} $2 == "/rotor-administrators" {print user}' "${SRC}/${REALM_FILE}")
grep -qx alice <<< "$in_group" && pass "alice administers Rotor" \
  || fail "alice is not in /rotor-administrators in ${REALM_FILE}, so the demo's administrator cannot administer Rotor."
grep -qE '"backchannel.logout.url": "http://rotor-api:8080/' "${SRC}/${REALM_FILE}" && pass "Keycloak signs people out of Rotor at rotor-api" \
  || fail "${REALM_FILE} gives the client rotor no back-channel logout at http://rotor-api:8080, the name Rotor's api has on the network suite, so signing out of Keycloak leaves Rotor's sessions open."

audiences=$(awk -F'"' '/"clientId":/ {client = $4} /"included.client.audience":/ {print client ":" $4}' "${SRC}/${REALM_FILE}")
for pair in stator:armature stator:exciter exciter:armature exciter:stator armature:exciter; do
  grep -qx "$pair" <<< "$audiences" && pass "${pair%:*}'s tokens are meant for ${pair#*:} too" \
    || fail "${REALM_FILE} has no audience mapper on the client ${pair%:*} for ${pair#*:}, so ${pair#*:} refuses ${pair%:*}'s token exchange; add one (docs/keycloak.md)."
done

# OpenBao is no client of the realm, so the audience is a custom one.
custom=$(awk -F'"' '/"clientId":/ {client = $4} /"included.custom.audience":/ {print client ":" $4}' "${SRC}/${REALM_FILE}")
grep -qx "rotor-control-plane:openbao" <<< "$custom" && pass "rotor-control-plane's tokens are meant for openbao" \
  || fail "${REALM_FILE} has no audience mapper on the client rotor-control-plane for openbao, so OpenBao refuses the control plane's sign-in; add one (docs/keycloak.md)."

# Keycloak matches a trailing wildcard as a plain prefix, so one inside the host
# lets in any host that merely starts with it, such as localhost.example.
loose=$(grep -noE '"https?://[^/:"]*\*"' "${SRC}/${REALM_FILE}")
[[ -z "$loose" ]] && pass "every return address ends its host before a wildcard" \
  || fail "${REALM_FILE} has a wildcard inside a host, which admits any host that starts the same: $(paste -sd' ' <<< "$loose"); write it as http://localhost:* and http://localhost/*."

stale=$(cd "$SRC" && grep -rnE '/realms/[A-Za-z0-9_-]+' deploy mk Makefile README.md \
  | grep -vE "/realms/(${REALM}|master)\b")
[[ -z "$stale" ]] && pass "every issuer of the stack names /realms/${REALM}" \
  || fail "these name another realm than ${REALM}: $(paste -sd' ' <<< "$stale")"

if diff -q <(make -s -C "$SRC" realm-server-print) "$SRC/deploy/keycloak/realm.server.json" >/dev/null; then
  pass "the server's realm is the canonical one, made for a server"
else
  fail "deploy/keycloak/realm.server.json differs from what the canonical realm makes; run make realm-server."
fi

exit "$FAILED"
