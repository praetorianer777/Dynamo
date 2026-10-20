#!/usr/bin/env bash
# Installs deploy/charts/dynamo into a kind cluster, as an operator would, and
# checks that the three tools come up connected: the connect Job ran, one
# sign-in through the realm opens all three, every webhook delivers, and an
# upgrade runs the Job again without anything left to change.
#
# Runs in the image from scripts/kind/Dockerfile (make test-chart), with the
# repository at /work, the host's Docker socket and the host's network. A
# tool image present in the host's Docker is loaded into the cluster; any
# other is pulled from its registry. With DYNAMO_CHARTS_FROM_SOURCE=1 the
# tools' charts are packaged from their main branches instead of pulled.
set -euo pipefail

CLUSTER="${DYNAMO_CLUSTER:-dynamo-chart}"
NODE_IMAGE=kindest/node:v1.34.0
NS=dynamo
RELEASE=dynamo
WORK=$(mktemp -d)
export KUBECONFIG="$WORK/kubeconfig"
PF_PIDS=()

say() { echo "🧪 $*"; }
cleanup() {
  local rc=$?
  for p in "${PF_PIDS[@]}"; do kill "$p" 2>/dev/null || true; done
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
cp -r /work/deploy/charts/dynamo "$WORK/chart"
mkdir -p "$WORK/chart/charts"
if [[ -n "${DYNAMO_CHARTS_FROM_SOURCE:-}" ]]; then
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
  if docker image inspect "$img" >/dev/null 2>&1; then
    kind load docker-image --name "$CLUSTER" "$img" >/dev/null && echo "   loaded $img"
  else
    echo "   $img is pulled by the cluster"
  fi
done

say "install"
kubectl create namespace "$NS" >/dev/null
# No --wait: the tools' post-install hooks must run before their pods can be
# ready, and the connect Job is the last of them, so a finished install is a
# connected suite.
start=$SECONDS
helm install "$RELEASE" "$WORK/chart" -n "$NS" -f /work/scripts/chart-test-values.yaml --timeout 30m
echo "   installed and connected in $((SECONDS - start))s"
kubectl -n "$NS" logs "job/$RELEASE-connect" | sed 's/^/   /'
for t in stator armature exciter; do
  kubectl -n "$NS" rollout status "deploy/$t-api" --timeout=300s >/dev/null
  kubectl -n "$NS" rollout status "deploy/$t-web" --timeout=300s >/dev/null
done

say "port-forwards"
forward() { # service port local
  kubectl -n "$NS" port-forward "svc/$1" "$3:$2" >/dev/null 2>&1 &
  PF_PIDS+=($!)
}
forward keycloak 8080 39180
forward stator-web 80 39181
forward armature-web 80 39182
forward exciter-web 80 39183
for p in 39180 39181 39182 39183; do
  for _ in $(seq 60); do curl -s -o /dev/null "http://127.0.0.1:$p/" && break; sleep 1; done
done

secret() { kubectl -n "$NS" get secret "$1" -o "jsonpath={.data.$2}" | base64 -d; }
cat > "$WORK/stack.env" <<EOF
STATOR_URL=http://stator-web
ARMATURE_URL=http://armature-web
EXCITER_URL=http://exciter-web
STATOR_ADMIN_EMAIL=$(secret stator-bootstrap-admin username)
STATOR_ADMIN_PASSWORD=$(secret stator-bootstrap-admin password)
ARMATURE_ADMIN_EMAIL=$(secret "$RELEASE-armature-admin" username)
ARMATURE_ADMIN_PASSWORD=$(secret "$RELEASE-armature-admin" password)
EXCITER_ADMIN_EMAIL=$(secret exciter-bootstrap-admin username)
EXCITER_ADMIN_PASSWORD=$(secret exciter-bootstrap-admin password)
EOF

say "sign-in and webhooks"
DYNAMO_STACK_ENV="$WORK/stack.env" \
DYNAMO_HOSTS="keycloak:8080=127.0.0.1:39180,stator-web:80=127.0.0.1:39181,armature-web:80=127.0.0.1:39182,exciter-web:80=127.0.0.1:39183" \
  /work/.cache/stacktest -test.v -test.run Stack -test.count=1

say "upgrade"
helm upgrade "$RELEASE" "$WORK/chart" -n "$NS" -f /work/scripts/chart-test-values.yaml --timeout 20m >/dev/null
log=$(kubectl -n "$NS" logs "job/$RELEASE-connect")
if grep -q "nothing changed" <<< "$log"; then
  echo "   the connect Job ran again and found the tools connected"
else
  echo "   the connect Job changed something on the upgrade, so the install did not leave the tools connected:"
  sed 's/^/   /' <<< "$log"
  exit 1
fi

say "the chart installs all three connected"
