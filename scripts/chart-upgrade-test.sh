#!/usr/bin/env bash
# Upgrades the umbrella chart in a kind cluster as an operator would, from the
# combination versions-upgrade-from.yaml names to the chart of this checkout,
# so to the pins, and checks what scripts/upgrade-test.sh checks in Compose
# (README.md, "Testing an upgrade"):
#
#   1. the tools' charts at the previous versions, which they never
#      published, are packaged from their git tags, and the umbrella chart is
#      installed with them and those versions' released images; its Jobs
#      connect the three and import sparkplug;
#   2. from a pod in the cluster, TestUpgradeSeedsThePreviousVersions gives
#      four people of the realm different access and records what they and
#      the sample are;
#   3. helm upgrade to this checkout's chart: the tools' migration Jobs run as
#      its pre-upgrade hooks, and a failed one fails the upgrade;
#   4. the connect and sample-data Jobs find nothing to change, the same pod
#      holds the upgraded suite to the record with
#      TestUpgradeKeepsWhatWasThere, and helm test runs the stack checks.
#
# Runs in the image from scripts/kind/Dockerfile (make test-chart-upgrade),
# as scripts/chart-test.sh does, and like it packages the pinned tools'
# charts from their main branches with DYNAMO_CHARTS_FROM_SOURCE=1.
set -euo pipefail

CLUSTER="${DYNAMO_CLUSTER:-dynamo-chart-upgrade}"
NS=dynamo
RELEASE=dynamo
CHECK=dynamo-upgrade-check
FROM=/work/versions-upgrade-from.yaml
WORK=$(mktemp -d)
export KUBECONFIG="$WORK/kubeconfig"
# shellcheck source=scripts/chart-lib.sh
source "$(dirname "$0")/chart-lib.sh"
VALUES=(-f /work/scripts/chart-test-values.yaml -f /work/scripts/chart-upgrade-values.yaml)

cleanup() {
  local rc=$?
  (( rc == 0 )) || diagnose
  [[ -n "${DYNAMO_KEEP_CLUSTER:-}" ]] || kind delete cluster --name "$CLUSTER" --kubeconfig "$KUBECONFIG" >/dev/null 2>&1 || true
  rm -rf "$WORK"
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 143' TERM INT HUP

pin() { sed -nE "s/^$1: *\"?([^\" ]+)\"?.*/\1/p" "$2"; }

# checks <test>: the stack checks of this checkout's dynamo-connect image,
# run in the pod that keeps the record between the two halves.
checks() {
  kubectl -n "$NS" exec "$CHECK" -- /usr/local/bin/dynamo-check -test.v -test.count=1 -test.run "$1" \
    | grep -vE '^=== (RUN|PAUSE|CONT)'
}

# expect_unchanged <job> <line>: the hook Job ran again after the upgrade and
# found nothing to change.
expect_unchanged() {
  local log
  log=$(kubectl -n "$NS" logs "job/$RELEASE-$1")
  if grep -q "$2" <<< "$log"; then
    echo "   the $1 Job ran again and found nothing to change"
  else
    echo "   the $1 Job changed something after the upgrade, so the upgraded suite was not what the install left:"
    sed 's/^/   /' <<< "$log"
    exit 1
  fi
}

say "the charts of versions-upgrade-from.yaml, from the tools' tags"
cp -r /work/deploy/charts/dynamo "$WORK/from"
rm -rf "$WORK/from/charts" "$WORK/from/Chart.lock"
for t in "stator praetorianer777/Stator" "armature Cloudster1/Armature" "exciter Cloudster1/Exciter"; do
  read -r tool repo <<< "$t"
  version=$(pin "$tool" "$FROM")
  [[ -n "$version" ]] || { echo "   versions-upgrade-from.yaml names no version of $tool; give it one line, $tool: <version>."; exit 2; }
  tool_chart "$tool" "$repo@v$version" "$WORK/from/charts"
  # dynamo-connect refuses a tool that runs another version than this says.
  sed -i "/- name: $tool\$/{n;s/^\( *version:\).*/\1 $version/}" "$WORK/from/Chart.yaml"
  echo "   $tool $version packaged from $repo at v$version"
done

say "the chart of this checkout"
cp -r /work/deploy/charts/dynamo "$WORK/to"
if [[ -n "${DYNAMO_CHARTS_FROM_SOURCE:-}" ]]; then
  rm -rf "$WORK/to/charts" "$WORK/to/Chart.lock"
  for t in "stator praetorianer777/Stator@main" "armature Cloudster1/Armature@main" "exciter Cloudster1/Exciter@main"; do
    read -r tool default <<< "$t"
    src="$(printenv "DYNAMO_${tool^^}_SOURCE" || echo "$default")"
    tool_chart "$tool" "$src" "$WORK/to/charts"
    echo "   $tool packaged from $src"
  done
else
  helm dependency build "$WORK/to"
fi

say "kind cluster $CLUSTER"
kind delete cluster --name "$CLUSTER" --kubeconfig "$KUBECONFIG" >/dev/null 2>&1 || true
kind create cluster --name "$CLUSTER" --image "$NODE_IMAGE" --kubeconfig "$KUBECONFIG" --wait 180s

say "install Stator $(pin stator "$FROM"), Armature $(pin armature "$FROM"), Exciter $(pin exciter "$FROM")"
# scripts/images-from-source.sh tags what it builds as the pins, which may
# name a version the previous combination runs too.
PULL_ALWAYS='^ghcr\.io/(praetorianer777/stator|cloudster1/armature|cloudster1/exciter)-' images_to_node "$WORK/from" "${VALUES[@]}"
kubectl create namespace "$NS" >/dev/null
start=$SECONDS
helm install "$RELEASE" "$WORK/from" -n "$NS" "${VALUES[@]}" --timeout 30m
echo "   installed and connected in $((SECONDS - start))s"
kubectl -n "$NS" logs "job/$RELEASE-connect" | sed 's/^/   /'
kubectl -n "$NS" logs "job/$RELEASE-sample-data" | sed 's/^/   /'
for t in stator armature exciter; do
  kubectl -n "$NS" rollout status "deploy/$t-api" --timeout=300s >/dev/null
  kubectl -n "$NS" rollout status "deploy/$t-web" --timeout=300s >/dev/null
done

say "people with different access to the sample, and what they may do"
# The pod helm test runs the stack checks in, made to stay, with a volume for
# the record, which the checks after the upgrade read back.
helm template "$RELEASE" "$WORK/to" -n "$NS" "${VALUES[@]}" --show-only templates/tests/connected.yaml \
  | kubectl create --dry-run=client -o json -f - \
  | jq --arg name "$CHECK" '
      .metadata.name = $name
      | .metadata.labels["app.kubernetes.io/name"] = $name
      | del(.metadata.annotations, .metadata.namespace)
      | .spec.volumes = [{name: "record", emptyDir: {}}]
      | .spec.containers[0].command = ["sleep", "86400"]
      | .spec.containers[0].env += [{name: "DYNAMO_UPGRADE_SNAPSHOT", value: "/record/before.json"}]
      | .spec.containers[0].volumeMounts = [{name: "record", mountPath: "/record"}]
      | .spec.containers[0].resources.limits.memory = "256Mi"' \
  | kubectl -n "$NS" apply -f - >/dev/null
kubectl -n "$NS" wait --for=condition=Ready "pod/$CHECK" --timeout=300s >/dev/null
checks '^TestUpgradeSeedsThePreviousVersions$'

say "helm upgrade to the chart of this checkout"
images_to_node "$WORK/to" "${VALUES[@]}"
# A hook Job is replaced when it runs again, so a new uid shows it ran.
declare -A installed
for t in stator armature exciter; do
  installed[$t]=$(kubectl -n "$NS" get job "$t-migrate" -o jsonpath='{.metadata.uid}' 2>/dev/null || true)
done
start=$SECONDS
if ! helm upgrade "$RELEASE" "$WORK/to" -n "$NS" "${VALUES[@]}" --timeout 30m; then
  echo "   helm upgrade failed; a migration Job that failed shows below with its log."
  exit 1
fi
echo "   upgraded in $((SECONDS - start))s"
for t in stator armature exciter; do
  read -r uid succeeded image < <(kubectl -n "$NS" get job "$t-migrate" \
    -o jsonpath='{.metadata.uid} {.status.succeeded} {.spec.template.spec.containers[0].image}{"\n"}' 2>/dev/null || echo)
  api=$(kubectl -n "$NS" get deploy "$t-api" -o jsonpath='{.spec.template.spec.containers[0].image}')
  if [[ -z "$uid" || "$uid" == "${installed[$t]}" ]]; then
    echo "   $t's migration Job did not run on the upgrade; its chart must run it as a pre-upgrade hook."
    exit 1
  fi
  if [[ "$succeeded" != 1 || "${image##*:}" != "${api##*:}" ]]; then
    echo "   $t's migration Job ran $image, not the upgraded api's version ${api##*:}, or did not succeed (succeeded: '$succeeded')."
    exit 1
  fi
  echo "   $t migrated to ${api##*:} in its pre-upgrade Job"
done
for t in stator armature exciter; do
  kubectl -n "$NS" rollout status "deploy/$t-api" --timeout=300s >/dev/null
  kubectl -n "$NS" rollout status "deploy/$t-web" --timeout=300s >/dev/null
done

say "dynamo-connect again, with nothing to change"
expect_unchanged connect "connected already; nothing changed"
expect_unchanged sample-data "imported already; nothing changed"

say "everything the record holds, after the upgrade"
checks '^TestUpgradeKeepsWhatWasThere$'

say "the stack checks, after the upgrade"
helm test "$RELEASE" -n "$NS" --logs --timeout 10m | sed 's/^/   /'

say "the chart's upgrade from $(pin stator "$FROM")/$(pin armature "$FROM")/$(pin exciter "$FROM") kept everything"
