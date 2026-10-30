#!/usr/bin/env bash
# Installs deploy/charts/dynamo into a kind cluster, as an operator would, and
# checks that the three tools come up connected: the connect Job ran, helm
# test finds one sign-in through the realm opens all three and every webhook
# delivers, the sample project is in all three, the suite's one Kroki is in
# secure mode and reaches nothing, and an upgrade runs the Jobs again without
# anything left to change.
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
NS=dynamo
RELEASE=dynamo
WORK=$(mktemp -d)
export KUBECONFIG="$WORK/kubeconfig"
# shellcheck source=scripts/chart-lib.sh
source "$(dirname "$0")/chart-lib.sh"

cleanup() {
  local rc=$?
  (( rc == 0 )) || diagnose
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
  for t in "stator praetorianer777/Stator@main" "armature Cloudster1/Armature@main" "exciter Cloudster1/Exciter@main"; do
    set -- $t
    # DYNAMO_<TOOL>_SOURCE=owner/repo@branch tries a tool's branch instead.
    src="$(printenv "DYNAMO_${1^^}_SOURCE" || echo "$2")"
    tool_chart "$1" "$src" "$WORK/chart/charts"
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
images_to_node "$WORK/chart" -f /work/scripts/chart-test-values.yaml

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
# A release older than the sample data has no such Job, nor checks it.
sample=1
if ! helm get hooks "$RELEASE" -n "$NS" | grep -q "name: $RELEASE-sample-data"; then
  if [[ -z "${DYNAMO_FROM_RELEASE:-}" ]]; then
    echo "   the chart made no sample-data Job, though the test values enable it"
    exit 1
  fi
  sample=
  echo "   this release imports no sample data"
else
  kubectl -n "$NS" logs "job/$RELEASE-sample-data" | sed 's/^/   /'
fi
for t in stator armature exciter; do
  kubectl -n "$NS" rollout status "deploy/$t-api" --timeout=300s >/dev/null
  kubectl -n "$NS" rollout status "deploy/$t-web" --timeout=300s >/dev/null
done

say "helm test"
helm test "$RELEASE" -n "$NS" --logs --timeout 10m | sed 's/^/   /'

say "Kroki, in secure mode and with no way out"
if [[ -n "${DYNAMO_FROM_RELEASE:-}" ]] && ! kubectl -n "$NS" get deploy kroki >/dev/null 2>&1; then
  echo "   this release runs no Kroki of the suite's own"
else
  krokis=$(kubectl -n "$NS" get deploy -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.spec.template.spec.containers[*].image}{"\n"}{end}' | grep -c 'yuzutech/kroki:' || true)
  [[ "$krokis" == 1 ]] || { echo "   the release runs $krokis Kroki Deployments; the tools' own must stay off beside the suite's one"; exit 1; }
  mode=$(kubectl -n "$NS" get deploy kroki -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="KROKI_SAFE_MODE")].value}')
  [[ "$mode" == secure ]] || { echo "   Kroki runs with KROKI_SAFE_MODE '$mode', not secure"; exit 1; }
  kubectl -n "$NS" rollout status deploy/kroki --timeout=300s >/dev/null
  draw() { # pod, PlantUML source
    kubectl -n "$NS" exec "$1" -- wget -q -O - -T 10 --header 'Content-Type: text/plain' --post-data "$2" http://kroki:8000/plantuml/svg 2>&1
  }
  api=$(kubectl -n "$NS" get pod -l app.kubernetes.io/name=stator,app.kubernetes.io/component=api -o jsonpath='{.items[0].metadata.name}')
  svg=$(draw "$api" $'@startuml\nAlice -> Bob: hello\n@enduml') || true
  grep -q '<svg' <<< "$svg" || { echo "   Stator's api got no drawing from Kroki: ${svg:0:300}"; exit 1; }
  echo "   Stator's api reaches Kroki and gets a PlantUML drawing"
  # In secure mode the include is dropped without a word and the rest draws.
  svg=$(draw "$api" $'@startuml\n!include /etc/passwd\nAlice -> Bob: hello\n@enduml') || true
  if ! grep -q '<svg' <<< "$svg" || grep -q 'root:' <<< "$svg"; then
    echo "   a PlantUML include of /etc/passwd was not dropped, so Kroki is not in secure mode: ${svg:0:300}"
    exit 1
  fi
  echo "   secure mode keeps PlantUML from reading Kroki's files"
  kroki=$(kubectl -n "$NS" get pod -l app.kubernetes.io/name=kroki -o jsonpath='{.items[0].metadata.name}')
  # Any answer at all counts as reached, so no -f.
  reach() { kubectl -n "$NS" exec "$1" -- curl -sS -o /dev/null -m 5 "$2" 2>&1; }
  reach "$kroki" http://localhost:8000/health >/dev/null \
    || { echo "   curl in the Kroki pod could not reach Kroki itself, so the checks below would prove nothing"; exit 1; }
  s3=$(kubectl -n "$NS" get svc s3 -o jsonpath='{.spec.clusterIP}')
  for to in http://1.1.1.1/ "http://$s3:8333/"; do
    if reach "$kroki" "$to" >/dev/null; then
      echo "   Kroki reached $to, though its NetworkPolicy lets nothing leave"
      exit 1
    fi
  done
  echo "   Kroki reaches neither the internet nor the cluster's other Services"
  store=$(kubectl -n "$NS" get pod -l app.kubernetes.io/name=object-store -o jsonpath='{.items[0].metadata.name}')
  if reach "$store" http://kroki:8000/health >/dev/null; then
    echo "   the object store reached Kroki, though its NetworkPolicy lets in the tools' apis alone"
    exit 1
  fi
  echo "   a pod that is no tool's api cannot reach Kroki"
fi

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
if [[ -n "$sample" ]]; then
  log=$(kubectl -n "$NS" logs "job/$RELEASE-sample-data")
  if grep -q "imported already; nothing changed" <<< "$log"; then
    echo "   the sample-data Job ran again and found the sample there"
  else
    echo "   the sample-data Job changed something on the upgrade, so the install did not finish the import:"
    sed 's/^/   /' <<< "$log"
    exit 1
  fi
fi

say "the chart installs all three connected"
