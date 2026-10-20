#!/usr/bin/env bash
# The one entry point for the whole suite: the branch-guard hook runs it with
# the layers a push needs, CI with every layer, each job naming its own.
#
#   ./run-tests.sh                   every layer
#   ./run-tests.sh shell check-go    only these
#   ./run-tests.sh --list            the layers, in order
set -euo pipefail
cd "$(dirname "$0")"

LAYERS=(shell check-format check-go test-stack test-chart)

if [[ "${1:-}" == --list ]]; then
  printf '%s\n' "${LAYERS[@]}"
  exit 0
fi

selected=("$@")
[[ ${#selected[@]} -gt 0 ]] || selected=("${LAYERS[@]}")
for layer in "${selected[@]}"; do
  [[ " ${LAYERS[*]} " == *" $layer "* ]] \
    || { echo "There is no layer called $layer. Name some of: ${LAYERS[*]}." >&2; exit 2; }
done
wanted() { [[ " ${selected[*]} " == *" $1 "* ]]; }

shell_tests() {
  ./tests/test-versions.sh && ./tests/test-realm.sh && ./tests/test-chart.sh && ./tests/test-compose.sh \
    && ./.claude/hooks/tests/branch-guard-test.sh
}

stack_started=""
finish() {
  local rc=$?
  [[ -z "$stack_started" ]] || make stack-down >/dev/null 2>&1 || true
  exit "$rc"
}
# A gate stopped from outside still takes its stack down.
trap 'exit 143' TERM INT HUP
trap finish EXIT

run_layer() {
  local name="$1" start=$SECONDS
  shift
  echo "🧪 $name"
  if ! "$@"; then
    echo "❌ $name failed after $((SECONDS - start))s"
    exit 1
  fi
  echo "✅ $name ($((SECONDS - start))s)"
}

wanted shell && run_layer shell shell_tests
wanted check-format && run_layer check-format make --no-print-directory check-format
wanted check-go && run_layer check-go make --no-print-directory check-go
if wanted test-stack; then
  stack_started=1
  run_layer test-stack make --no-print-directory test-stack
  make stack-down >/dev/null 2>&1 || true
  stack_started=""
fi
wanted test-chart && run_layer test-chart make --no-print-directory test-chart
exit 0
