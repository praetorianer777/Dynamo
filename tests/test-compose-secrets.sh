#!/usr/bin/env bash
# deploy/compose-secrets.sh completes a server's settings: from nothing it
# makes them from deploy/.env.example with every secret generated, none twice
# the same; a second run changes nothing; a value someone set is kept. And
# compose accepts the result with no variable left unset. It never prints a
# value it did not make in that run.
#
# Needs bash, coreutils and docker compose; no stack is started.
set -u

SRC="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="$SRC/deploy/compose-secrets.sh"
FAILED=0
pass() { echo "   ✅ $1"; }
fail() { echo "   ❌ $1"; FAILED=1; }

echo "🧪 compose secrets"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
settings="$work/settings"

value() { grep -E "^$1=" "$2" | tail -n 1 | cut -d= -f2-; }
secrets=$(sed -n '/^PASSWORDS=(/,/^)/p; /^KEYS=(/p' "$SCRIPT" | tr -s ' ()' '\n' | grep -E '^[A-Z0-9_]+$' | grep -vE '^(PASSWORDS|KEYS)$')

first="$("$SCRIPT" "$settings" 2>/dev/null)"
[[ -f "$settings" ]] && pass "it makes the settings from .env.example" || fail "it made no settings file at $settings"
[[ "$(stat -c %a "$settings")" == 600 ]] && pass "only their owner may read them" \
  || fail "the settings are readable by others ($(stat -c %a "$settings")); they hold every password"

empty=""; seen=""; dup=""
for name in $secrets; do
  v="$(value "$name" "$settings")"
  [[ -z "$v" ]] && empty="$empty $name"
  grep -qxF -- "$v" <<< "$seen" && dup="$dup $name"
  seen="$seen"$'\n'"$v"
done
n=$(wc -w <<< "$secrets")
[[ -z "$empty" ]] && pass "all $n secrets are generated" || fail "these secrets are still empty:$empty"
[[ -z "$dup" ]] && pass "no two are the same" || fail "these secrets repeat another:$dup"
for name in STATOR_SECRET_KEY ARMATURE_SECRET_KEY EXCITER_SECRET_KEY; do
  [[ "$(value "$name" "$settings" | base64 -d 2>/dev/null | wc -c)" == 32 ]] || fail "$name is not 32 bytes in base64"
done
pass "the tools' keys are 32 bytes in base64"
[[ "$(value OIDC_ISSUER "$settings")" == "https://sso.example.com/realms/dynamo" && "$(value OIDC_HOST "$settings")" == keycloak ]] \
  && pass "sign-in follows from the bundled Keycloak's address" || fail "OIDC_ISSUER or OIDC_HOST were not set from KEYCLOAK_URL"

leftover=$(grep -E '^[A-Z0-9_]+=$' "$settings" | cut -d= -f1 | grep -vxE 'SMTP_USER|SMTP_PASSWORD|CADDY_GLOBAL_OPTIONS|DYNAMO_MEMBERS|GITHUB_TOKEN' | paste -sd' ')
[[ -z "$leftover" ]] && pass "nothing else is left empty" || fail "these are empty and neither optional nor generated: $leftover"

told=$(grep -cE '^  (KEYCLOAK_PASSWORD|[A-Z]+_ADMIN_PASSWORD) ' <<< "$first")
[[ "$told" == 4 ]] && pass "the first run tells the 4 sign-in passwords" || fail "the first run told $told of the 4 sign-in passwords"

before="$(sha256sum < "$settings")"
second="$("$SCRIPT" "$settings" 2>/dev/null)"
[[ "$(sha256sum < "$settings")" == "$before" ]] && pass "a second run changes nothing" || fail "a second run changed the settings"
grep -q 'PASSWORD ' <<< "$second" && fail "a second run tells passwords again" || pass "and tells no password again"

moved="$work/moved"
cp "$settings" "$moved"
sed -i 's|^KEYCLOAK_URL=.*|KEYCLOAK_URL=https://login.example.org|; /^SMTP_TLS=/d' "$moved"
"$SCRIPT" "$moved" > /dev/null 2>&1
[[ "$(value OIDC_ISSUER "$moved")" == "https://login.example.org/realms/dynamo" ]] \
  && pass "a changed KEYCLOAK_URL carries over to sign-in" || fail "OIDC_ISSUER stayed $(value OIDC_ISSUER "$moved") after KEYCLOAK_URL changed"
[[ "$(value SMTP_TLS "$moved")" == starttls ]] && pass "a setting the example names and the settings lack is added" \
  || fail "SMTP_TLS, missing from the settings, was not added from .env.example"

own="$work/own"
cp "$SRC/deploy/.env.example" "$own"
sed -i 's|^COMPOSE_PROFILES=.*|COMPOSE_PROFILES=connect,caddy|; s|^OIDC_ISSUER=.*|OIDC_ISSUER=https://id.example.org/realms/team|' "$own"
"$SCRIPT" "$own" > /dev/null 2>&1
[[ "$(value OIDC_ISSUER "$own")" == "https://id.example.org/realms/team" && "$(value OIDC_HOST "$own")" == id.example.org ]] \
  && pass "a Keycloak of one's own keeps its issuer" || fail "with a Keycloak of one's own, OIDC_ISSUER or OIDC_HOST came out wrong"

kept="$work/kept"
cp "$SRC/deploy/.env.example" "$kept"
sed -i 's|^STATOR_ADMIN_PASSWORD=$|STATOR_ADMIN_PASSWORD=chosen-by-hand|; s|^ARMATURE_SECRET_KEY=$|ARMATURE_SECRET_KEY="a quoted key"|' "$kept"
"$SCRIPT" "$kept" > /dev/null 2>&1
[[ "$(value STATOR_ADMIN_PASSWORD "$kept")" == chosen-by-hand && "$(value ARMATURE_SECRET_KEY "$kept")" == '"a quoted key"' ]] \
  && pass "a value set by hand is kept" || fail "a value set by hand was replaced"

# Compose reads these settings alone, so nothing of the caller's may leak in.
config_err="$(env -i PATH="$PATH" HOME="$HOME" docker compose -f "$SRC/deploy/docker-compose.yml" --env-file "$settings" \
  --profile sample-data config -q 2>&1)"
if [[ $? -eq 0 && -z "$config_err" ]]; then
  pass "compose accepts them, every variable set"
else
  unset_vars="$(grep -oE '[A-Z0-9_]+\\?" variable is not set' <<< "$config_err" | cut -d'\' -f1 | cut -d'"' -f1 | sort -u | paste -sd' ')"
  fail "compose finds these unset in the settings: ${unset_vars:-$(head -c 300 <<< "$config_err")}"
fi

for t in stator armature exciter; do
  v=$(sed -n "s/^${t}: *//p" "$SRC/versions.yaml")
  [[ "$(value "$(tr a-z A-Z <<< "$t")_VERSION" "$SRC/deploy/.env.example")" == "$v" ]] \
    || fail "deploy/.env.example names another ${t} than the pinned ${v}"
done
pass "the example pins the versions versions.yaml does"

exit "$FAILED"
