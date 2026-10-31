#!/usr/bin/env bash
# Makes the Secrets the umbrella chart can read instead of values: the OIDC
# clients' secrets, Keycloak's and Armature's administrators, and the S3 keys.
# Asks for each value; Enter generates a random one. Prints the values that
# name the Secrets for the chart.
#
#   scripts/create-secrets.sh [-n namespace] [--own-s3] [--print]
#
# --own-s3   one key pair per tool for an S3 of your own, instead of one pair
#            for the bundled object store that all three share
# --print    write the Secrets as YAML to stdout instead of applying them, for
#            Sealed Secrets, SOPS or a vault; the values go to stderr
set -euo pipefail

NS=dynamo OWN_S3=0 PRINT=0
while (( $# )); do
  case "$1" in
    -n) NS=$2; shift 2 ;;
    --own-s3) OWN_S3=1; shift ;;
    --print) PRINT=1; shift ;;
    *) echo "Unknown argument $1; see the top of $0." >&2; exit 2 ;;
  esac
done
command -v kubectl >/dev/null || { echo "kubectl is needed to make the Secrets; install it or run this where it is." >&2; exit 1; }
command -v openssl >/dev/null || { echo "openssl is needed to generate secrets; install it or type every value." >&2; exit 1; }

ask() { # prompt default
  local v
  read -r -p "$1 [$2]: " v </dev/tty
  printf '%s' "${v:-$2}"
}
ask_secret() { # prompt
  local v
  read -r -s -p "$1 (Enter generates one): " v </dev/tty
  echo >&2
  printf '%s' "${v:-$(openssl rand -hex 32)}"
}
secret() { # name key=value...
  local name=$1; shift
  local args=()
  for kv in "$@"; do args+=(--from-literal="$kv"); done
  if (( PRINT )); then
    echo "---"
    kubectl create secret generic "$name" -n "$NS" "${args[@]}" --dry-run=client -o yaml
  else
    kubectl create secret generic "$name" -n "$NS" "${args[@]}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
    echo "made $NS/$name" >&2
  fi
}

echo "== OIDC clients" >&2
oidc=()
# Rotor's two clients are in the bundled realm before Rotor joins the chart.
for t in stator armature exciter rotor rotor-control-plane; do
  oidc+=("$t-client-secret=$(ask_secret "  $t's client secret")")
done
echo "== Keycloak's administrator" >&2
kc_user=$(ask "  username" admin)
kc_pass=$(ask_secret "  password")
echo "== Armature's owner" >&2
ar_user=$(ask "  email" admin@armature.dynamo)
ar_pass=$(ask_secret "  password")

(( PRINT )) || kubectl get namespace "$NS" >/dev/null 2>&1 || kubectl create namespace "$NS" >/dev/null
secret dynamo-oidc-clients "${oidc[@]}"
secret dynamo-keycloak-admin-login "username=$kc_user" "password=$kc_pass"
secret dynamo-armature-owner "username=$ar_user" "password=$ar_pass"

if (( OWN_S3 )); then
  for t in stator armature exciter; do
    echo "== $t's S3 keys" >&2
    key=$(ask "  access key" "$t")
    secret "$t-s3-credentials" "s3-access-key=$key" "s3-secret-key=$(ask_secret "  secret key")"
  done
else
  echo "== The bundled object store's S3 keys" >&2
  key=$(ask "  access key" dynamo)
  secret dynamo-s3-credentials "s3-access-key=$key" "s3-secret-key=$(ask_secret "  secret key")"
fi

{
  echo
  echo "# Save as secrets-values.yaml and pass it after your values: -f values.yaml -f secrets-values.yaml"
  echo "oidc: { existingSecret: dynamo-oidc-clients }"
  echo "keycloak: { admin: { existingSecret: dynamo-keycloak-admin-login } }"
  echo "armatureAdmin: { existingSecret: dynamo-armature-owner }"
  if (( OWN_S3 )); then
    echo "objectStore: { enabled: false }"
    for t in stator armature exciter; do
      echo "$t: { secrets: { create: false, existingSecret: $t-s3-credentials } }"
    done
  else
    echo "objectStore: { existingSecret: dynamo-s3-credentials }"
    for t in stator armature exciter; do
      echo "$t: { secrets: { create: false, existingSecret: dynamo-s3-credentials } }"
    done
  fi
} >&2
