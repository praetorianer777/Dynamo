# What scripts/chart-test.sh and scripts/chart-upgrade-test.sh share, sourced
# by both with CLUSTER, NS, RELEASE, WORK and KUBECONFIG set.
# shellcheck shell=bash

# shellcheck disable=SC2034 # the sourcing scripts create their cluster with it
NODE_IMAGE=kindest/node:v1.34.0

say() { echo "🧪 $*"; }

# diagnose: what a failed run leaves to find out why: every Job that failed,
# every pod that is not ready with its events, the databases, and whether the
# node ran short. A gate prints only its last lines, so the databases come last.
diagnose() {
  [[ -s "$KUBECONFIG" ]] || return 0
  echo "--- what the cluster says"
  kubectl -n "$NS" get pods,jobs -o wide 2>/dev/null || true
  echo "--- the node"
  kubectl describe nodes 2>/dev/null | sed -n '/^Conditions:/,/^Addresses:/p;/^Allocated resources:/,/^Events:/p' || true
  for job in $(kubectl -n "$NS" get jobs -o jsonpath='{range .items[?(@.status.failed)]}{.metadata.name}{" "}{end}' 2>/dev/null); do
    echo "--- job $job"
    kubectl -n "$NS" get job "$job" -o jsonpath='{range .status.conditions[*]}{.type}: {.reason}: {.message}{"\n"}{end}' 2>/dev/null || true
    kubectl -n "$NS" logs "job/$job" --tail=40 2>/dev/null || true
  done
  for pod in $(kubectl -n "$NS" get pods --no-headers 2>/dev/null | awk '$3 != "Completed" { split($2, r, "/"); if (r[1] != r[2]) print $1 }' | head -8); do
    echo "--- pod $pod, not ready"
    kubectl -n "$NS" get events --field-selector "involvedObject.name=$pod" --sort-by=.lastTimestamp 2>/dev/null | tail -8 || true
    kubectl -n "$NS" logs "$pod" --all-containers --tail=15 2>/dev/null || true
  done
  for pod in $(kubectl -n "$NS" get pods --no-headers -o custom-columns=:metadata.name 2>/dev/null | grep -- '-postgresql-'); do
    echo "--- database $pod"
    kubectl -n "$NS" logs "$pod" --all-containers --tail=20 2>/dev/null || true
  done
}

# tool_chart <tool> <owner/repo@ref> <dir>: packages the tool's chart as it is
# at ref into dir, for versions whose chart was never published.
tool_chart() {
  local src="$WORK/src/$1-${2#*@}"
  # Armature leaves its SeaweedFS subchart to be fetched, by repository URL.
  helm repo add seaweedfs https://seaweedfs.github.io/seaweedfs/helm >/dev/null
  mkdir -p "$src" "$3"
  curl -fsSL "https://codeload.github.com/${2%@*}/tar.gz/${2#*@}" | tar -xz -C "$src" --strip-components=1
  if [[ ! -f "$src/deploy/charts/$1/Chart.yaml" ]]; then
    echo "   ${2%@*} has no chart at deploy/charts/$1 at ${2#*@}; name a ref that has one."
    return 1
  fi
  helm dependency build "$src/deploy/charts/$1" >/dev/null
  helm package "$src/deploy/charts/$1" -d "$3" >/dev/null
}

# images_to_node <chart> <helm values args>...: the tools' images the chart
# runs go to the node, loaded from the host's Docker where it has them and
# pulled otherwise, and every other image is pulled before the install: the
# tools' roles Jobs give their database 900s from the moment Helm creates
# them, which a node still pulling Keycloak and three Postgres images on a
# slow line can use up.
images_to_node() {
  local chart=$1 images img i start pull=() pids=()
  shift
  images=$(helm template "$RELEASE" "$chart" -n "$NS" "$@" \
    | sed -nE 's/^\s*image: "?([^" ]+)"?.*/\1/p' | sort -u)
  # Only the tools' own images: kind cannot load a multi-platform image a host
  # pulled for one platform. PULL_ALWAYS, a pattern, names those the host may
  # hold under the same tag built from source, which must come from the registry.
  for img in $(grep -E '^ghcr\.io/(praetorianer777|cloudster1)/' <<< "$images"); do
    if [[ -z "${DYNAMO_FROM_RELEASE:-}" && ! "$img" =~ ${PULL_ALWAYS:-^$} ]] && docker image inspect "$img" >/dev/null 2>&1; then
      kind load docker-image --name "$CLUSTER" "$img" >/dev/null && echo "   loaded $img"
    else
      pull+=("$img")
    fi
  done
  mapfile -t -O "${#pull[@]}" pull < <(grep -vE '^(ghcr\.io/(praetorianer777|cloudster1)/|$)' <<< "$images")
  start=$SECONDS
  for img in "${pull[@]}"; do
    docker exec "$CLUSTER-control-plane" crictl pull "$img" >/dev/null &
    pids+=($!)
  done
  for i in "${!pids[@]}"; do
    wait "${pids[$i]}" || { echo "   The node could not pull ${pull[$i]}; check that the registry is reachable from here and run again."; return 1; }
  done
  echo "   the node pulled ${#pull[@]} images in $((SECONDS - start))s"
}
