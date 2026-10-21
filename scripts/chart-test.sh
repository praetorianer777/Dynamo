#!/usr/bin/env bash
# Installs deploy/charts/dynamo into a kind cluster, as an operator would, and
# checks that the three tools come up connected: the connect Job ran, helm
# test finds one sign-in through the realm opens all three and every webhook
# delivers, and an upgrade runs the Job again without anything left to change.
#
# Runs in the image from scripts/kind/Dockerfile (make test-chart), with the
# repository at /work, the host's Docker socket and the host's network. A
# tool image present in the host's Docker is loaded into the cluster; any
# other is pulled from its registry. With DYNAMO_CHARTS_FROM_SOURCE=1 the
# tools' charts are packaged from their main branches instead of pulled.
# With DYNAMO_FROM_RELEASE=1 it installs as an operator does: the published
# chart at this checkout's version, and every image from its registry. With
# DYNAMO_EXISTING_SECRETS=1 every secret comes from a Secret made before the
# install, and none may show in what the release rendered.
set -euo pipefail

CLUSTER="${DYNAMO_CLUSTER:-dynamo-chart}"
NODE_IMAGE=kindest/node:v1.34.0
NS=dynamo
RELEASE=dynamo
WORK=$(mktemp -d)
export KUBECONFIG="$WORK/kubeconfig"

say() { echo "🧪 $*"; }
cleanup() {
  local rc=$?
  if (( rc != 0 )) && [[ -s "$KUBECONFIG" ]]; then
    echo "--- what the cluster says"
    kubectl -n "$NS" get pods,jobs 2>/dev/null || true
    for job in $(kubectl -n "$NS" get jobs -o jsonpath='{range .items[?(@.status.failed)]}{.metadata.name}{" "}{end}' 2>/dev/null); do
      echo "--- job $job"
      kubectl -n "$NS" logs "job/$job" --tail=40 2>/dev/null || true
    done
    for pod in $(kubectl -n "$NS" get pods --no-headers 2>/dev/null | awk '$3 ~ /CrashLoop|Error/ {print $1}' | head -4); do
      echo "--- pod $pod"
      kubectl -n "$NS" logs "$pod" --tail=15 2>/dev/null || true
    done
  fi
  [[ -n "${DYNAMO_KEEP_CLUSTER:-}" ]] || kind delete cluster --name "$CLUSTER" --kubeconfig "$KUBECONFIG" >/dev/null 2>&1 || true
  rm -rf "$WORK"
  exit "$rc"
}
trap cleanup EXIT

say "chart and its dependencies"
if [[ -n "${DYNAMO_FROM_RELEASE:-}" ]]; then
  version=$(sed -n 's/^version: *//p' /work/deploy/charts/dynamo/Chart.yaml)
  helm pull oci://ghcr.io/praetorianer777/charts/dynamo --version "$version" --untar -d "$WORK/pulled"
  mv "$WORK/pulled/dynamo" "$WORK/chart"
  echo "   dynamo $version pulled with its dependencies"
elif [[ -n "${DYNAMO_CHARTS_FROM_SOURCE:-}" ]]; then
  cp -r /work/deploy/charts/dynamo "$WORK/chart"
  mkdir -p "$WORK/chart/charts"
  # Armature leaves its SeaweedFS subchart to be fetched, by repository URL.
  helm repo add seaweedfs https://seaweedfs.github.io/seaweedfs/helm >/dev/null
  for t in "stator praetorianer777/Stator@main" "armature Cloudster1/Armature@main" "exciter Cloudster1/Exciter@main"; do
    set -- $t
    # DYNAMO_<TOOL>_SOURCE=owner/repo@branch tries a tool's branch instead.
    src="$(printenv "DYNAMO_${1^^}_SOURCE" || echo "$2")"
    mkdir -p "$WORK/src/$1"
    curl -fsSL "https://codeload.github.com/${src%@*}/tar.gz/${src#*@}" | tar -xz -C "$WORK/src/$1" --strip-components=1
    helm dependency build "$WORK/src/$1/deploy/charts/$1" >/dev/null
    helm package "$WORK/src/$1/deploy/charts/$1" -d "$WORK/chart/charts" >/dev/null
    echo "   $1 packaged from $src"
  done
else
  cp -r /work/deploy/charts/dynamo "$WORK/chart"
  helm dependency build "$WORK/chart"
fi

say "kind cluster $CLUSTER"
kind delete cluster --name "$CLUSTER" --kubeconfig "$KUBECONFIG" >/dev/null 2>&1 || true
kind create cluster --name "$CLUSTER" --image "$NODE_IMAGE" --kubeconfig "$KUBECONFIG" --wait 180s

say "images"
images=$(helm template "$RELEASE" "$WORK/chart" -n "$NS" -f /work/scripts/chart-test-values.yaml \
  | sed -nE 's/^\s*image: "?([^" ]+)"?.*/\1/p' | sort -u)
# Only the tools' own images: the rest the node pulls itself, and kind cannot
# load a multi-platform image a host pulled for one platform.
for img in $(grep -E '^ghcr\.io/(praetorianer777|cloudster1)/' <<< "$images"); do
  if [[ -z "${DYNAMO_FROM_RELEASE:-}" ]] && docker image inspect "$img" >/dev/null 2>&1; then
    kind load docker-image --name "$CLUSTER" "$img" >/dev/null && echo "   loaded $img"
  else
    echo "   $img is pulled by the cluster"
  fi
done

say "install"
kubectl create namespace "$NS" >/dev/null
VALUES=(-f /work/scripts/chart-test-values.yaml)
if [[ -n "${DYNAMO_EXISTING_SECRETS:-}" ]]; then
  gen() { head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n'; }
  made=()
  mk() { # name key=value...
    local name=$1; shift
    local args=() kv
    for kv in "$@"; do args+=(--from-literal="$kv"); made+=("${kv#*=}"); done
    kubectl -n "$NS" create secret generic "$name" "${args[@]}" >/dev/null
  }
  mk test-oidc-clients "stator-client-secret=$(gen)" "armature-client-secret=$(gen)" "exciter-client-secret=$(gen)"
  mk test-keycloak-admin username=admin "password=$(gen)"
  mk test-armature-owner username=owner@armature.dynamo "password=$(gen)"
  mk test-s3 s3-access-key=dynamo "s3-secret-key=$(gen)"
  VALUES+=(-f /work/scripts/chart-test-existing-secrets.yaml)
  echo "   made every secret beforehand"
fi
# No --wait: the tools' post-install hooks must run before their pods can be
# ready, and the connect Job is the last of them, so a finished install is a
# connected suite.
start=$SECONDS
helm install "$RELEASE" "$WORK/chart" -n "$NS" "${VALUES[@]}" --timeout 30m
echo "   installed and connected in $((SECONDS - start))s"
kubectl -n "$NS" logs "job/$RELEASE-connect" | sed 's/^/   /'
for t in stator armature exciter; do
  kubectl -n "$NS" rollout status "deploy/$t-api" --timeout=300s >/dev/null
  kubectl -n "$NS" rollout status "deploy/$t-web" --timeout=300s >/dev/null
done

say "helm test"
helm test "$RELEASE" -n "$NS" --logs --timeout 10m | sed 's/^/   /'

if [[ -n "${DYNAMO_EXISTING_SECRETS:-}" ]]; then
  say "no secret in the release"
  rendered=$(helm get manifest "$RELEASE" -n "$NS"; helm get hooks "$RELEASE" -n "$NS"; helm get values "$RELEASE" -n "$NS" -a)
  for v in "${made[@]}"; do
    [[ ${#v} -lt 16 ]] && continue
    if grep -qF "$v" <<< "$rendered"; then
      echo "   a secret made beforehand shows in what the release rendered, so the chart copied it out of its Secret"
      exit 1
    fi
  done
  echo "   none of the secrets made beforehand shows in the release"
fi

say "upgrade"
helm upgrade "$RELEASE" "$WORK/chart" -n "$NS" "${VALUES[@]}" --timeout 20m >/dev/null
log=$(kubectl -n "$NS" logs "job/$RELEASE-connect")
if grep -q "nothing changed" <<< "$log"; then
  echo "   the connect Job ran again and found the tools connected"
else
  echo "   the connect Job changed something on the upgrade, so the install did not leave the tools connected:"
  sed 's/^/   /' <<< "$log"
  exit 1
fi

say "the chart installs all three connected"
