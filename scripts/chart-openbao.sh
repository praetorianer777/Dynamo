# The suite's OpenBao as Rotor needs it, checked by scripts/chart-test.sh,
# which sources this with NS, WORK and KUBECONFIG set.
# shellcheck shell=bash

# Rotor's OpenBao script at the commit checked here, until Rotor's chart joins
# the umbrella chart (#101) and runs it in its own configure Job.
ROTOR_REF=2e96e2aa7110d1c6f53194bee1a47e52528135c0
OPENBAO_TIMEOUT=300s

# bao in OpenBao's pod, as the admin token the pod was given.
# shellcheck disable=SC2016 # the pod's shell expands it, so it never leaves the pod
bao_admin() {
  kubectl -n "$NS" exec deploy/openbao -- sh -c 'BAO_TOKEN=$DYNAMO_OPENBAO_ADMIN_TOKEN exec bao "$@"' bao "$@"
}

openbao_unsealed() {
  local status
  # bao status exits 2 while sealed, which the JSON says too.
  status=$(kubectl -n "$NS" exec deploy/openbao -- bao status -format=json 2>&1) || true
  if ! jq -e '.initialized and (.sealed | not) and .type == "static"' <<< "$status" >/dev/null 2>&1; then
    echo "   OpenBao is not initialized and unsealed by its static key: ${status:0:300}"
    return 1
  fi
}

openbao_secret() {
  kubectl -n "$NS" get deploy openbao \
    -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="DYNAMO_OPENBAO_ADMIN_TOKEN")].valueFrom.secretKeyRef.name}'
}

# A sum of the Secret's keys, so an upgrade that made new ones shows.
openbao_secret_sum() {
  kubectl -n "$NS" get secret "$(openbao_secret)" -o json | jq -r '.data | tojson' | sha256sum | cut -c1-16
}

# login <service account>: the policies OpenBao gives its token, or nothing.
login() {
  local jwt
  jwt=$(kubectl -n "$NS" create token "$1")
  kubectl -n "$NS" exec deploy/openbao -- bao write -format=json auth/kubernetes/login \
    role=rotor-control-plane "jwt=$jwt" 2>/dev/null | jq -r '.auth.policies | join(",")' || true
}

check_openbao() {
  local out token secret image
  kubectl -n "$NS" rollout status deploy/openbao --timeout="$OPENBAO_TIMEOUT" >/dev/null
  openbao_unsealed
  echo "   OpenBao initialized itself and unsealed with its static key"
  out=$(bao_admin token lookup -format=json | jq -r '[(.data.policies | join(",")), .data.ttl, .data.orphan] | join(" ")')
  [[ "$out" == "root 0 true" ]] \
    || { echo "   the admin token from the Secret is not an orphan root token that never expires: $out"; return 1; }
  echo "   the admin token from the Secret works and never expires"
  out=$(bao_admin audit list -format=json | jq -r '[.[].type] | join(",")')
  [[ ",$out," == *,file,* ]] || { echo "   OpenBao has no file audit device, only '$out'"; return 1; }
  echo "   a file audit device logs every request"
  out=$(bao_admin read -field=kubernetes_host auth/kubernetes/config 2>&1) || true
  [[ "$out" == https://* ]] || { echo "   OpenBao's Kubernetes auth is not configured: $out"; return 1; }
  echo "   Kubernetes auth is on, reviewing tokens at $out"

  say "Rotor's OpenBao script against the suite's OpenBao"
  mkdir -p "$WORK/rotor"
  curl -fsSL "https://codeload.github.com/Cloudster1/Rotor/tar.gz/$ROTOR_REF" \
    | tar -xz -C "$WORK/rotor" --strip-components=3 "Rotor-$ROTOR_REF/deploy/openbao"
  kubectl -n "$NS" create configmap rotor-openbao \
    --from-file="$WORK/rotor/init.sh" --from-file="$WORK/rotor/policies" >/dev/null
  secret=$(openbao_secret)
  image=$(kubectl -n "$NS" get deploy openbao -o jsonpath='{.spec.template.spec.containers[0].image}')
  sed -e "s|@IMAGE@|$image|" -e "s|@SECRET@|$secret|" /work/scripts/chart-test-rotor-openbao.yaml \
    | kubectl -n "$NS" apply -f - >/dev/null
  if ! kubectl -n "$NS" wait --for=condition=complete job/rotor-openbao --timeout="$OPENBAO_TIMEOUT" >/dev/null; then
    echo "   Rotor's init.sh did not finish against the suite's OpenBao:"
    kubectl -n "$NS" logs job/rotor-openbao 2>&1 | sed 's/^/   /'
    return 1
  fi
  kubectl -n "$NS" logs job/rotor-openbao | sed 's/^/   /'
  kubectl -n "$NS" create serviceaccount rotor-api >/dev/null
  kubectl -n "$NS" create serviceaccount not-rotor >/dev/null
  out=$(login rotor-api)
  [[ ",$out," == *,rotor-control-plane,* ]] \
    || { echo "   Rotor's api could not sign in with Kubernetes auth (policies '$out'); OpenBao's ServiceAccount must be allowed system:auth-delegator"; return 1; }
  echo "   Rotor's api signs in with its ServiceAccount, which OpenBao has the cluster review"
  out=$(login not-rotor)
  [[ -z "$out" ]] || { echo "   a ServiceAccount the role does not name signed in, with policies $out"; return 1; }
  echo "   a ServiceAccount the role does not name is refused"

  token=$(kubectl -n "$NS" get secret "$secret" -o json | jq -r '.data["admin-token"]' | base64 -d)
  if kubectl -n "$NS" logs deploy/openbao | grep -qF "$token"; then
    echo "   the admin token shows in OpenBao's log, so its audit log does not hash secrets"
    return 1
  fi
  echo "   the audit log hashes the admin token"

  say "OpenBao after a restart"
  OPENBAO_SECRET_SUM=$(openbao_secret_sum)
  kubectl -n "$NS" rollout restart deploy/openbao >/dev/null
  kubectl -n "$NS" rollout status deploy/openbao --timeout="$OPENBAO_TIMEOUT" >/dev/null
  openbao_unsealed
  bao_admin read transit/keys/rotor-run-data >/dev/null \
    || { echo "   Rotor's transit key is gone after the restart, so OpenBao lost its data"; return 1; }
  out=$(login rotor-api)
  [[ ",$out," == *,rotor-control-plane,* ]] || { echo "   Rotor's api cannot sign in after the restart"; return 1; }
  echo "   the new pod unsealed itself with nobody there, and Rotor's configuration and sign-in are kept"
}

# After helm upgrade: the same Secret, so the same key, and still unsealed.
check_openbao_upgraded() {
  [[ "$(openbao_secret_sum)" == "$OPENBAO_SECRET_SUM" ]] \
    || { echo "   the upgrade changed OpenBao's Secret, so its next start cannot open the data"; return 1; }
  openbao_unsealed
  echo "   OpenBao kept its unseal key and admin token through the upgrade"
}
