#!/usr/bin/env bash
# Upgrades a running suite as an operator would, from the combination
# versions-upgrade-from.yaml names to the pins of versions.yaml, and checks
# that nothing was lost on the way (README.md, "Testing an upgrade"):
#
#   1. the previous combination comes up from its released images and
#      dynamo-connect connects it;
#   2. sample-data imports sparkplug, and the seed gives four people of the
#      realm different access to it and records what they may do, the
#      sample and the links between the tools;
#   3. the stack moves to the pins, Armature first as docs/compatibility.md
#      asks: Compose pulls the new images and recreates what changed, and the
#      tools migrate as they do in production;
#   4. dynamo-connect runs again and must find nothing to change, the record
#      is checked against the upgraded stack, and so are the stack checks.
#
# make test-upgrade runs it with the stack's variables under a project of its
# own, which it starts empty and removes at the end; DYNAMO_KEEP_UPGRADE=1
# leaves it up.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
: "${COMPOSE_PROJECT_NAME:?make test-upgrade sets it}" "${STATOR_URL:?make test-upgrade sets it}"
GO_IMAGE="${GO_IMAGE:-golang:1.26-bookworm}"
FROM="$ROOT/versions-upgrade-from.yaml"
TO="$ROOT/versions.yaml"
work="$ROOT/.cache/upgrade"
say() { echo "🧪 $*"; }
pin() { sed -nE "s/^$1: *\"?([^\" ]+)\"?.*/\1/p" "$2"; }

# alice administers all three, bob and carol are members; dave is let in by
# his realm group alone, which the seed grants before he signs in.
export DYNAMO_MEMBERS="alice@suite.test=admin,bob@suite.test=member,carol@suite.test=member"

# at <versions file>: the stack and the checks run the versions it names.
at() {
  export STATOR_VERSION ARMATURE_VERSION EXCITER_VERSION
  STATOR_VERSION="$(pin stator "$1")"
  ARMATURE_VERSION="$(pin armature "$1")"
  EXCITER_VERSION="$(pin exciter "$1")"
  if [[ -z "$STATOR_VERSION" || -z "$ARMATURE_VERSION" || -z "$EXCITER_VERSION" ]]; then
    echo "$1 must name a version of stator, armature and exciter, one line each." >&2
    exit 2
  fi
  {
    for t in STATOR ARMATURE EXCITER; do
      printf '%s_URL=%s\n' "$t" "$(printenv "${t}_URL")"
      printf '%s_ADMIN_EMAIL=%s\n' "$t" "$(printenv "${t}_ADMIN_EMAIL")"
      printf '%s_ADMIN_PASSWORD=%s\n' "$t" "$(printenv "${t}_ADMIN_PASSWORD")"
      printf '%s_VERSION=%s\n' "$t" "$(printenv "${t}_VERSION")"
    done
    echo "DYNAMO_SAMPLE_SOURCES=sparkplug"
    echo "KROKI_DRAWS=stator,exciter"
  } > "$work/stack.env"
  echo "   Stator $STATOR_VERSION, Armature $ARMATURE_VERSION, Exciter $EXCITER_VERSION"
}

finish() {
  local rc=$?
  if [[ $rc -ne 0 ]]; then
    echo "--- what failed"
    docker compose ps -a --format '{{.Service}} {{.Image}} {{.State}} {{.ExitCode}}' | grep -v ' running 0$' | sed 's/^/   /' || true
    for s in $(docker compose ps -a --format '{{.Service}} {{.State}} {{.ExitCode}}' | awk '$2 == "exited" && $3 != 0 {print $1}'); do
      echo "--- $s"; docker compose logs --no-color --tail 30 "$s" 2>&1 | sed 's/^/   /' || true
    done
  fi
  [[ -n "${DYNAMO_KEEP_UPGRADE:-}" ]] || docker compose down -v --remove-orphans > /dev/null 2>&1 || true
  exit "$rc"
}
trap finish EXIT
trap 'exit 143' TERM INT HUP

checks() {
  docker run --rm -u "$(id -u):$(id -g)" --network host \
    -v "$ROOT:/work" -v "$ROOT/.cache/go:/cache" \
    -e HOME=/tmp -e GOCACHE=/cache/build -e GOMODCACHE=/cache/mod -e GOFLAGS=-buildvcs=false -e GOTOOLCHAIN=local \
    -e DYNAMO_STACK_ENV=/work/.cache/upgrade/stack.env -e DYNAMO_UPGRADE_SNAPSHOT=/work/.cache/upgrade/before.json \
    -w /work/connect "$GO_IMAGE" go test -count=1 -tags stack -run "$1" -v ./... | grep -vE '^=== (RUN|PAUSE|CONT)'
}

# run_connect <args of docker compose run>: dynamo-connect once, its last line shown.
run_connect() {
  local log
  if ! log="$(docker compose run --rm "$@" 2>&1)"; then
    printf '%s\n' "$log" | tail -n 25 | sed 's/^/   /'
    return 1
  fi
  printf '%s\n' "$log" | tail -n 1 | sed 's/^/   /'
}

mkdir -p "$work" "$ROOT/.cache/go"
rm -f "$work/before.json" "$work/stack.env"
docker compose down -v --remove-orphans > /dev/null 2>&1 || true
"$ROOT/scripts/check-host.sh" "$KEYCLOAK_PORT" "$STATOR_WEB_PORT" "$ARMATURE_WEB_PORT" "$EXCITER_WEB_PORT" "$MAILPIT_PORT"

say "the previous combination, from versions-upgrade-from.yaml"
at "$FROM"
docker build -q -t "$DYNAMO_CONNECT_IMAGE" "$ROOT/connect" > /dev/null
docker compose up -d --wait --quiet-pull
run_connect connect
run_connect connect sample-data

say "people with different access to the sample, and what they may do"
checks '^TestUpgradeSeedsThePreviousVersions$'

say "to the pins of versions.yaml, Armature first"
at "$TO"
start=$SECONDS
docker compose up -d --wait --quiet-pull armature-api armature-worker armature-web armature-render
docker compose up -d --wait --quiet-pull
echo "   upgraded in $((SECONDS - start))s"

say "dynamo-connect again, with nothing to change"
run_connect -e DYNAMO_EXPECT_UNCHANGED=1 connect

say "everything the record holds, after the upgrade"
checks '^TestUpgradeKeepsWhatWasThere$'

say "the stack checks, after the upgrade"
checks Stack
run_connect -e DYNAMO_EXPECT_UNCHANGED=1 connect sample-data

say "the upgrade from $(pin stator "$FROM")/$(pin armature "$FROM")/$(pin exciter "$FROM") kept everything"
