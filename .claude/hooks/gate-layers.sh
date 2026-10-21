#!/usr/bin/env bash
# Which layers of ./run-tests.sh a push needs, from the files it changes. The
# one place that knows which path can affect which layer; a path it does not
# know runs every layer, so a new directory is tested until it is listed here.
#
#   gate-layers.sh [quick|full] < changed-files    one path a line
#
# Prints "run: <layers>" and "skipped: <layer (reason)>...". Quick, the
# default, leaves the stack of all three tools to CI, which blocks the merge
# anyway; full runs it when a change can affect it.
set -uo pipefail

mode="${1:-quick}"
[[ "$mode" == quick || "$mode" == full ]] || { echo "gate-layers.sh: the mode is quick or full, not $mode" >&2; exit 2; }

# The server test reads what the stack does, and its own script.
need_go=0 need_stack=0 need_server=0 need_chart=0 seen=0
while IFS= read -r path; do
  [[ -n "$path" ]] || continue
  seen=1
  case "$path" in
    *.md | NOTICE | LICENSE | docs/*) ;;
    connect/*) need_go=1 need_stack=1 need_server=1 need_chart=1 ;;
    deploy/charts/* | scripts/chart-test.sh | scripts/chart-test-values.yaml | scripts/kind/* | mk/chart.mk) need_chart=1 ;;
    versions.yaml) need_stack=1 need_server=1 need_chart=1 ;;
    deploy/* | mk/* | Makefile) need_stack=1 need_server=1 ;;
    scripts/server-test.sh) need_server=1 ;;
    tests/* | scripts/* | .github/* | .claude/*) ;;
    *) need_go=1 need_stack=1 need_server=1 need_chart=1 ;;
  esac
done

# No files known, such as a push whose base cannot be found, runs everything.
if (( !seen )); then need_go=1 need_stack=1 need_server=1 need_chart=1; fi

run=(shell check-format)
skipped=()
if (( need_go )); then run+=(check-go); else skipped+=("check-go (no file of dynamo-connect changed)"); fi
if [[ "$mode" == full ]]; then
  if (( need_stack )); then run+=(test-stack); else skipped+=("test-stack (no file the stack reads changed)"); fi
  if (( need_server )); then run+=(test-server); else skipped+=("test-server (no file the server setup reads changed)"); fi
  if (( need_chart )); then run+=(test-chart); else skipped+=("test-chart (no file the chart reads changed)"); fi
else
  skipped+=("test-stack, test-server and test-chart (left to CI; DYNAMO_GATE=full runs them)")
fi

echo "run: ${run[*]}"
echo "skipped: ${skipped[*]:-none}"
