#!/usr/bin/env bash
# Builds the tools' images at the versions versions.yaml pins, from each
# tool's sources, for as long as a pinned version is not released yet: the
# stack and the chart can then be tested against what the tools' main
# branches hold instead of not at all. The images are tagged as the tools'
# release workflows tag them and never pushed.
#
#   scripts/images-from-source.sh                 all three tools
#   DYNAMO_ARMATURE_SOURCE=owner/Armature@branch scripts/images-from-source.sh
#
# DYNAMO_<TOOL>_SOURCE names another repository or branch, as for
# scripts/chart-test.sh.
set -euo pipefail
cd "$(dirname "$0")/.."

pin() { sed -nE "s/^$1: *\"?([^\" ]+)\"?.*/\1/p" versions.yaml; }

# build <tool> <default source> <image> <dockerfile> [target]
build() {
  local tool=$1 default=$2 image=$3 dockerfile=$4 target=${5:-}
  local src version
  src=$(printenv "DYNAMO_${tool^^}_SOURCE" || echo "$default")
  version=$(pin "$tool")
  # BuildKit leaves .git out of a git context unless asked, and the tools'
  # build stamps read it.
  docker build -q --build-arg BUILDKIT_CONTEXT_KEEP_GIT_DIR=1 \
    --build-arg "VERSION=$version" --build-arg "COMMIT=${src#*@}" \
    ${target:+--target "$target"} -f "$dockerfile" -t "$image:$version" \
    "https://github.com/${src%@*}.git#${src#*@}" > /dev/null
  echo "   $image:$version from $src"
}

stator() { build stator praetorianer777/Stator@main "ghcr.io/praetorianer777/stator-$1" "deploy/Dockerfile.$2"; }
armature() { build armature Cloudster1/Armature@main "ghcr.io/cloudster1/armature-$1" "deploy/Dockerfile.$2" "${3:-}"; }
exciter() { build exciter Cloudster1/Exciter@main "ghcr.io/cloudster1/exciter-$1" "deploy/Dockerfile.$2"; }

# together "<build>"...: runs the builds side by side and fails when any did,
# which a bare wait would not.
together() {
  local pids=() each failed=0
  for each in "$@"; do $each & pids+=($!); done
  for each in "${pids[@]}"; do wait "$each" || failed=1; done
  (( !failed )) || { echo "An image did not build; its error is above. Fix the tool's build or point DYNAMO_<TOOL>_SOURCE at a branch that builds." >&2; exit 1; }
}

# Three at a time: more only make the builds fight over the same cores.
together "stator backend backend" "armature api backend api" "exciter backend backend"
together "armature worker backend worker" "armature migrate backend migrate" "stator web web"
together "armature web web production" "exciter web web" "stator render render"
together "armature render render" "exciter render render"
