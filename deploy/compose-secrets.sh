#!/usr/bin/env bash
# Completes the settings docker compose reads for the suite on a server
# (docs/deploying-compose.md): makes .env beside this script from
# .env.example when there is none, adds what a newer .env.example names, and
# gives every secret left empty a random value, once. Run it before every
# docker compose up; a value already set, by hand or by an earlier run, is
# never changed, except the sign-in settings that follow from KEYCLOAK_URL
# with the bundled Keycloak.
#
#   ./compose-secrets.sh [settings file]
#
# Needs bash, coreutils and sed only.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
file="${1:-$here/.env}"

# Each secret the compose file reads, as a password (letters and digits, so it
# fits in a database URL) or a key (32 random bytes in base64, what the tools
# seal their secrets with).
PASSWORDS=(
  KEYCLOAK_PASSWORD KEYCLOAK_DB_PASSWORD S3_SECRET_KEY
  STATOR_OIDC_CLIENT_SECRET ARMATURE_OIDC_CLIENT_SECRET EXCITER_OIDC_CLIENT_SECRET
  ROTOR_OIDC_CLIENT_SECRET ROTOR_CONTROL_PLANE_OIDC_CLIENT_SECRET
  STATOR_ADMIN_PASSWORD ARMATURE_ADMIN_PASSWORD EXCITER_ADMIN_PASSWORD
  STATOR_POSTGRES_PASSWORD STATOR_APP_DB_PASSWORD STATOR_ADMIN_DB_PASSWORD STATOR_VALKEY_PASSWORD
  ARMATURE_POSTGRES_PASSWORD ARMATURE_APP_DB_PASSWORD ARMATURE_ADMIN_DB_PASSWORD ARMATURE_VALKEY_PASSWORD
  EXCITER_POSTGRES_PASSWORD EXCITER_APP_DB_PASSWORD EXCITER_ADMIN_DB_PASSWORD EXCITER_VALKEY_PASSWORD
)
KEYS=(STATOR_SECRET_KEY ARMATURE_SECRET_KEY EXCITER_SECRET_KEY)
# The passwords people sign in with, said once when they are made.
TOLD=(KEYCLOAK_PASSWORD STATOR_ADMIN_PASSWORD ARMATURE_ADMIN_PASSWORD EXCITER_ADMIN_PASSWORD)

if [[ ! -e "$file" ]]; then
  cp "$here/.env.example" "$file"
  echo "Made $file from .env.example."
fi
chmod 600 "$file"

# A setting a newer .env.example brings, at the example's value.
added=()
while IFS= read -r line; do
  name="${line%%=*}"
  grep -qE "^$name=" "$file" && continue
  printf '%s\n' "$line" >> "$file"
  added+=("$name")
done < <(grep -E '^[A-Z0-9_]+=' "$here/.env.example")
[[ ${#added[@]} -eq 0 ]] || echo "Added from .env.example: ${added[*]}."

# value is a setting's value as compose reads it, without surrounding quotes.
value() {
  local v
  v="$(grep -E "^$1=" "$file" | tail -n 1 | cut -d= -f2-)" || true
  v="${v%\"}"; v="${v#\"}"; v="${v%\'}"; v="${v#\'}"
  printf '%s' "$v"
}

set_value() {
  if grep -qE "^$1=" "$file"; then
    sed -i "s|^$1=.*|$1=$2|" "$file"
  else
    printf '%s=%s\n' "$1" "$2" >> "$file"
  fi
}

password() { LC_ALL=C tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 32 || true; }
key() { head -c 32 /dev/urandom | base64 | tr -d '\n'; }

made=()
for name in "${PASSWORDS[@]}"; do
  [[ -n "$(value "$name")" ]] && continue
  set_value "$name" "$(password)"
  made+=("$name")
done
for name in "${KEYS[@]}"; do
  [[ -n "$(value "$name")" ]] && continue
  set_value "$name" "$(key)"
  made+=("$name")
done

# With the bundled Keycloak, sign-in follows from its address on every run,
# so a changed KEYCLOAK_URL carries over; the tools and dynamo-connect reach
# it inside.
keycloak="$(value KEYCLOAK_URL)"
if [[ ",$(value COMPOSE_PROFILES)," == *,keycloak-server,* && -n "$keycloak" ]]; then
  set_value OIDC_ISSUER "${keycloak%/}/realms/dynamo"
  set_value OIDC_BACKCHANNEL "${keycloak%/}=http://keycloak:8080"
  set_value OIDC_HOST keycloak
  set_value KEYCLOAK_ADMIN_URL http://keycloak:8080
fi
# The host the tools fetch the issuer's keys from, which their outbound
# guards must let through when it is a private address.
if [[ -z "$(value OIDC_HOST)" && -n "$(value OIDC_ISSUER)" ]]; then
  reach="$(value OIDC_BACKCHANNEL)"
  reach="${reach##*=}"
  [[ -n "$reach" ]] || reach="$(value OIDC_ISSUER)"
  host="${reach#*://}"; host="${host%%/*}"; host="${host%%:*}"
  set_value OIDC_HOST "$host"
fi

if [[ ${#made[@]} -eq 0 ]]; then
  echo "Every secret in $file is set; nothing changed."
else
  echo "Made ${#made[@]} secrets in $file."
  for name in "${TOLD[@]}"; do
    if [[ " ${made[*]} " == *" $name "* ]]; then
      printf '  %-24s %s\n' "$name" "$(value "$name")"
    fi
  done
  echo "Those are said only now; they stay in $file."
fi

for name in STATOR_URL ARMATURE_URL EXCITER_URL KEYCLOAK_URL STATOR_ADMIN_EMAIL; do
  if [[ "$(value "$name")" == *example.com* ]]; then
    echo "$name is still the example's $(value "$name"); set it in $file before docker compose up." >&2
  fi
done
