#!/usr/bin/env bash
# Tests for .claude/hooks/branch-guard.sh: the workflow rules it enforces
# (issue branches, no pushes to main, tests before a push) and the cases that
# have bypassed it before — quoted text, heredocs, command wrappers, worktrees.
#
# Runs against a throwaway clone with a stubbed run-tests.sh; nothing is built,
# pushed or tested for real.
set -u

SRC="$(cd "$(dirname "$0")/../../.." && pwd)"

# The fixtures commit, and a CI runner has no git identity of its own.
export GIT_AUTHOR_NAME=Tester GIT_AUTHOR_EMAIL=tester@example.com
export GIT_COMMITTER_NAME=Tester GIT_COMMITTER_EMAIL=tester@example.com

# A CI checkout is a detached HEAD, so a clone of it has no branch at all and no
# origin/main to track. Both fixtures need main and its upstream to exist, or
# every case in this file is judged against a branch that is not there.
fixture_main() {
  git checkout -q -B main
  git update-ref refs/remotes/origin/main HEAD
  git branch -q --set-upstream-to=origin/main main
}

# The hooks as they stand in the checkout, but not its worktrees: those hold
# whole repositories with read-only module caches, which the fixture's cleanup
# could not remove.
copy_claude() {
  mkdir -p .claude && cp -r "$SRC/.claude/hooks" "$SRC/.claude/skills" "$SRC/.claude/settings.json" .claude/
}

W=$(mktemp -d)
trap 'rm -rf "$W"' EXIT
git clone -q "$SRC" "$W/r" && cd "$W/r"
fixture_main && copy_claude && cp "$SRC/run-tests.sh" . && git add -A && git commit -qm fixture
export CLAUDE_PROJECT_DIR="$W/r"
G="git"; C="commit"
fail=0

check() { # expected tool arg label
  local exp="$1" tool="$2" arg="$3"
  local got
  got=$(jq -nc --arg t "$tool" --arg a "$arg" --arg cwd "$W/r" \
    'if $t=="Bash" then {tool_name:$t,cwd:$cwd,tool_input:{command:$a}} else {tool_name:$t,cwd:$cwd,tool_input:{file_path:$a}} end' \
    | .claude/hooks/branch-guard.sh | jq -r '.hookSpecificOutput.permissionDecision // empty')
  got=${got:-allow}
  if [[ "$got" == "$exp" ]]; then echo "ok    $exp  ${arg//$'\n'/⏎}"; else echo "FAIL  want $exp got $got  ${arg//$'\n'/⏎}"; fail=1; fi
}

echo "== on main"
check deny  Write "$W/r/README.md"
check allow Write "/tmp/x"
check allow Bash  "$G status"
check deny  Bash  "$G $C -m x"
check deny  Bash  "$G switch -c foo"
check deny  Bash  "$G switch -qc foo"
check deny  Bash  "$G checkout -qb foo"
check allow Bash  "$G merge --ff-only origin/main"
check allow Bash  "$G reset --hard origin/main"
check deny  Bash  "$G merge origin/main"
check deny  Bash  "$G merge --ff-only some-other-branch"
check deny  Bash  "$G reset --hard HEAD~1"
check allow Bash  "$G merge --ff-only origin/main 2>&1 | tail -1"
check allow Bash  "$G merge --ff-only origin/main > /tmp/out.txt"
check deny  Bash  "$G merge origin/main 2>&1"
check allow Bash  "$G switch -qc fix/42-audio && $G $C -m x"
check allow Bash  "$G switch -c fix/42-audio && $G add -A && $G $C -m x"
check allow Bash  "$G switch -qc release/v1.2.3 && $G add VERSION && $G $C -m x"
check deny  Bash  "$G switch -c release/1.2.3"
check deny  Bash  "$G switch -c release/v1.2"
check deny  Bash  "$G switch -c release/v1.2.3-rc1"
check deny  Bash  "$G switch -c release/next"
check deny  Bash  "$G push origin main"
check deny  Bash  "$G push origin fix/42-audio:main"
check ask   Bash  "gh pr merge 3"
check ask   Bash  "GH_PAGER=cat gh pr merge 3"
check allow Bash  "echo $G $C"
check allow Bash  "bash -c \"echo x && $G add -A\""
check allow Bash  "cat > f <<'EOF'
text
$G add -A
EOF
echo done"
check deny  Bash  "cat > f <<'EOF'
text
EOF
$G add -A"
check deny  Bash  "echo a
$G $C -m x"
check allow Bash  "$G -C /tmp/other $C -m x"
check deny  Bash  "$G -C . $C -m x"
check deny  Bash  "time $G $C -m x"
check deny  Bash  "env FOO=1 $G push origin main"
check deny  Bash  "sudo $G $C -m x"
check ask   Bash  "time gh pr merge 3"

git switch -qc fix/1-test
echo "== on fix/1-test"
check allow Write "$W/r/README.md"
check allow Bash  "$G checkout README.md && $G $C -m x"
check deny  Bash  "$G checkout main && $G $C -m x"
check deny  Bash  "$G push origin main"

# ── Detached HEAD during a rebase (#97) ─────────────────────────
# A conflicted rebase is judged by the branch being rebased, not by the
# detached HEAD it leaves behind, or it could never be finished or aborted.
conflict() { # branch-to-rebase onto
  git switch -q main && printf 'ours\n' > conflict.txt && git add conflict.txt && git commit -qm ours
  git switch -qc "$1" main~1 && printf 'theirs\n' > conflict.txt && git add conflict.txt && git commit -qm theirs
  git switch -q "$1" && git rebase -q "$2" >/dev/null 2>&1
  rebasing "$PWD"
}

# Without this the rebase cases would still pass if the rebase never stopped.
rebasing() {
  git -C "$1" symbolic-ref --quiet HEAD >/dev/null \
    && { echo "FAIL  no conflicted rebase in $1: HEAD is not detached"; fail=1; }
}

echo "== rebase of an issue branch stopped on a conflict"
conflict fix/2-rebase main
check allow Bash  "$G add -A"
check allow Bash  "$G rebase --continue"
check allow Bash  "$G rebase --abort"
check allow Write "$W/r/conflict.txt"
git rebase --abort >/dev/null 2>&1

echo "== rebase of main stopped on a conflict"
git switch -q main && git rebase -q fix/2-rebase >/dev/null 2>&1
rebasing "$W/r"
check deny  Bash  "$G add -A"
check deny  Write "$W/r/conflict.txt"
git rebase --abort >/dev/null 2>&1
git switch -q fix/1-test

printf '#!/bin/sh\necho stub ok\n' > run-tests.sh; chmod +x run-tests.sh
check allow Bash  "$G push -u origin HEAD"
printf '#!/bin/sh\necho "Failed: ScheduleEngine window boundary"; exit 1\n' > run-tests.sh
check deny  Bash  "$G push -u origin HEAD"

# ── One log line per invocation (#213) ───────────────────
echo "== every invocation is logged, without the command text"
LOGF="$W/r/.git/branch-guard.log"
logged() { # label pattern...
  local label="$1" line p; shift
  line="$(tail -n 1 "$LOGF" 2>/dev/null)"
  for p in "$@"; do
    [[ "$line" == *"$p"* ]] || { echo "FAIL  log for $label lacks '$p': $line"; fail=1; return; }
  done
  echo "ok    logged  $label"
}
rm -f "$LOGF"
check allow Bash  "$G status --short"
logged "allow" $'\tdecision=allow\t' $'\tgit=status' $'\ttool=Bash\t' "hook=$W/r/.claude/hooks/branch-guard.sh" "project_dir=$W/r" "cwd=$W/r"
check deny  Bash  "$G push origin main"
logged "deny" $'\tdecision=deny\t' $'\tgit=push'
check allow Write "/tmp/x"
logged "early exit" "decision=allow (outside repo)"
printf '#!/bin/sh\necho stub ok\n' > run-tests.sh
check allow Bash  "GH_TOKEN=s3cret $G push -u origin HEAD"
logged "gate" $'\tdecision=allow\t' $'\tgit=push' $'\tgate='"$W/r"$'\t' $'\tgate_secs=' $'\tgate_rc=0'
[[ "$(tail -n 2 "$LOGF" | head -n 1)" == *$'\tdecision=gating\t'* ]] \
  || { echo "FAIL  the gate's start was not logged"; fail=1; }
printf '#!/bin/sh\nexit 1\n' > run-tests.sh
check deny  Bash  "$G push -u origin HEAD"
logged "failed gate" $'\tdecision=deny\t' $'\tgate_rc=1'
check allow Bash  "$G -c user.name=x $C --allow-empty -m 'token s3cret' && $G log -1 | cat"
logged "chained" $'\tgit=commit,log'
grep -q -e s3cret -e ScheduleEngine "$LOGF" && { echo "FAIL  command text reached the log"; fail=1; }
(( $(wc -l < "$LOGF") == 8 )) || { echo "FAIL  want 8 log lines, got $(wc -l < "$LOGF")"; fail=1; }
printf '#!/bin/sh\nsleep 30\n' > run-tests.sh
jq -nc --arg cwd "$W/r" '{tool_name:"Bash",cwd:$cwd,tool_input:{command:"git push"}}' > "$W/push.json"
.claude/hooks/branch-guard.sh < "$W/push.json" > /dev/null & hook_pid=$!
sleep 1; kill -TERM "$hook_pid"; wait "$hook_pid"
logged "killed at the timeout" $'\tdecision=killed\t' $'\tgate='"$W/r"

true

# ── Worktrees (#27) ──────────────────────────────────────
W2=$(mktemp -d)
trap 'rm -rf "$W" "$W2"' EXIT
git clone -q "$SRC" "$W2/r" && cd "$W2/r"
fixture_main && copy_claude && cp "$SRC/run-tests.sh" . && git add -A && git commit -qm fixture
export CLAUDE_PROJECT_DIR="$W2/r"
G="git"; C="commit"

# The worktree is a second checkout of the same repo, on a valid issue branch.
WT="$W2/r/.claude/worktrees/agent-x"
git worktree add -q -b fix/99-agent-work "$WT" >/dev/null 2>&1
printf '#!/bin/sh\necho worktree tests ok\n' > "$WT/run-tests.sh"; chmod +x "$WT/run-tests.sh"
printf '#!/bin/sh\necho MAIN CHECKOUT TESTS RAN; exit 1\n' > "$W2/r/run-tests.sh"; chmod +x "$W2/r/run-tests.sh"

check() { # expected tool arg cwd
  local exp="$1" tool="$2" arg="$3" cwd="$4" got
  got=$(jq -nc --arg t "$tool" --arg a "$arg" --arg cwd "$cwd" \
    'if $t=="Bash" then {tool_name:$t,cwd:$cwd,tool_input:{command:$a}} else {tool_name:$t,cwd:$cwd,tool_input:{file_path:$a}} end' \
    | "$W2/r/.claude/hooks/branch-guard.sh" | jq -r '.hookSpecificOutput.permissionDecision // empty')
  got=${got:-allow}
  if [[ "$got" == "$exp" ]]; then echo "ok    $exp  ${arg//$'\n'/ } [cwd=${cwd##*/}]"
  else echo "FAIL  want $exp got $got  ${arg//$'\n'/ } [cwd=${cwd##*/}]"; fail=1; fi
}

echo "== worktree on issue branch, main checkout on main"
check allow Write "$WT/README.md" "$WT"
check deny  Write "$W2/r/README.md" "$W2/r"
check allow Bash  "$G add -A" "$WT"
check deny  Bash  "$G add -A" "$W2/r"
check allow Bash  "$G $C -m x" "$WT"
check deny  Bash  "$G push origin main" "$WT"
# The gate must run the worktree's own script: the main checkout's copy fails.
check allow Bash  "$G push -u origin HEAD" "$WT"
LOGF="$W2/r/.git/branch-guard.log"
logged "worktree push, in the common git dir" "cwd=$WT"$'\t' $'\tgate='"$WT"$'\t' $'\tgate_rc=0'

echo "== rebase in a worktree reads that worktree's head-name"
printf 'ours\n' > "$W2/r/conflict.txt"
git -C "$W2/r" add conflict.txt run-tests.sh && git -C "$W2/r" commit -qm ours
printf 'theirs\n' > "$WT/conflict.txt"
git -C "$WT" add conflict.txt run-tests.sh && git -C "$WT" commit -qm theirs
git -C "$WT" rebase -q main >/dev/null 2>&1
git -C "$WT" symbolic-ref --quiet HEAD >/dev/null \
  && { echo "FAIL  no conflicted rebase in the worktree: HEAD is not detached"; fail=1; }
check allow Bash  "$G add -A" "$WT"
check allow Bash  "$G -C $WT add -A" "$W2/r"
check deny  Bash  "$G add -A" "$W2/r"
git -C "$WT" rebase --abort >/dev/null 2>&1

echo "== worktree switched to main"
git -C "$WT" switch -q main 2>/dev/null || git -C "$WT" checkout -q --detach
check deny Bash "$G $C -m x" "$WT"

echo "== -C into a nested worktree is judged by that worktree"
git -C "$WT" switch -q fix/99-agent-work
check allow Bash "$G -C $WT $C -m x" "$W2/r"
check allow Bash "$G -C $WT add -A" "$W2/r"
git -C "$WT" switch -q main 2>/dev/null || git -C "$WT" checkout -q --detach
check deny  Bash "$G -C $WT $C -m x" "$W2/r"

echo "== cd and pushd decide which checkout later segments act on (#207)"
git -C "$WT" switch -q fix/99-agent-work
# Allowing the push only shows the main checkout's failing script did not run;
# the marker shows the worktree's gate ran at all.
GATE="$W2/gate-ran"
OTHER_DIR="$W2/not-a-repo"; mkdir -p "$OTHER_DIR"
printf '#!/bin/sh\npwd > "%s"\n' "$GATE" > "$WT/run-tests.sh"
gate() { # expected-dir label
  if [[ "$(cat "$GATE" 2>/dev/null)" == "$1" ]]; then echo "ok    gate ran in ${1##*/}  $2"
  else echo "FAIL  gate did not run in ${1##*/}  $2"; fail=1; fi
  rm -f "$GATE"
}
check allow Bash "cd $WT && $G push -u origin HEAD" "$W2/r"
gate "$WT" "cd worktree && push"
check allow Bash "cd $WT; $G $C -m x" "$W2/r"
check allow Bash "cd .claude/worktrees/agent-x && $G push" "$W2/r"
gate "$WT" "relative cd && push"
check allow Bash "cd \"$WT\" && $G push" "$W2/r"
gate "$WT" "quoted cd && push"
check allow Bash "cd $WT && cd .claude && cd .. && $G push" "$W2/r"
gate "$WT" "cd sub && cd .. && push"
check allow Bash "cd .claude && cd .. && $G push" "$WT"
gate "$WT" "cd sub && cd .. && push, from the worktree"
check deny  Bash "cd ../../.. && $G $C -m x" "$WT"
check allow Bash "pushd $WT >/dev/null && $G push" "$W2/r"
gate "$WT" "pushd && push"
check deny  Bash "pushd $WT && popd && $G $C -m x" "$W2/r"
check deny  Bash "cd $WT && cd - && $G $C -m x" "$W2/r"
check deny  Bash "cd $W2/r && $G $C -m x" "$WT"
check allow Bash "cd $WT && $G push" "$OTHER_DIR"
gate "$WT" "cd into the repo from outside && push"
check deny  Bash "cd $W2/r && $G $C -m x" "$OTHER_DIR"
check allow Bash "cd /tmp && $G $C -m x" "$W2/r"
check deny  Bash "cd $W2/does-not-exist; $G $C -m x" "$W2/r"
check deny  Bash "cd \"\$X\" && $G push" "$WT"
check deny  Bash "cd \$X && $G $C -m x" "$WT"
check deny  Bash "cd ~nobody && $G push" "$WT"
check deny  Bash "cd \$($G rev-parse --show-toplevel) && $G push" "$WT"
check allow Bash "cd \"\$X\" && $G status" "$WT"
check allow Bash "cd \"\$X\" && $G -C $WT push" "$W2/r"
gate "$WT" "lost cd, then git -C && push"
check deny  Bash "$G -C \"\$X\" $C -m x" "$WT"
[[ -e "$GATE" ]] && { echo "FAIL  a refused push ran the gate"; fail=1; }
git -C "$WT" switch -q main 2>/dev/null || git -C "$WT" checkout -q --detach
check deny  Bash "cd $WT && $G push" "$W2/r"
git -C "$WT" switch -q fix/99-agent-work

echo "== unrelated repo is none of our business"
OTHER="$W2/other"; mkdir -p "$OTHER" && git -C "$OTHER" init -q && git -C "$OTHER" commit -q --allow-empty -m init
check allow Write "$OTHER/file.txt" "$OTHER"
check allow Bash  "$G $C -m x" "$OTHER"

git -C "$W2/r" worktree remove --force "$WT" >/dev/null 2>&1

# ── Which layers a push runs (#350) ──────────────────────
echo "== the layers follow the files a push changes"
LAYERS="$SRC/.claude/hooks/gate-layers.sh"
layers_for() { # mode files...
  local mode="$1"; shift
  printf '%s\n' "$@" | "$LAYERS" "$mode" | sed -n 's/^run: //p'
}
expect_layers() { # label expected mode files...
  local label="$1" exp="$2" mode="$3" got; shift 3
  got="$(layers_for "$mode" "$@")"
  if [[ "$got" == "$exp" ]]; then echo "ok    layers  $label"; else echo "FAIL  layers  $label: want '$exp' got '$got'"; fail=1; fi
}
expect_layers "documentation alone"       "shell check-format"                    quick README.md docs/compatibility.md
expect_layers "documentation, full"       "shell check-format"                    full  docs/compatibility.md
expect_layers "dynamo-connect, quick"     "shell check-format check-go"           quick connect/main.go
expect_layers "dynamo-connect, full"      "shell check-format check-go test-stack test-server test-chart" full connect/main.go
expect_layers "the chart, full"           "shell check-format test-chart"         full  deploy/charts/dynamo/values.yaml
expect_layers "the pins, quick"           "shell check-format"                    quick versions.yaml
expect_layers "the pins, full"            "shell check-format test-stack test-server test-chart" full versions.yaml
expect_layers "the compose stack, full"   "shell check-format test-stack test-server" full deploy/docker-compose.yml
expect_layers "the server test, full"     "shell check-format test-server"        full  scripts/server-test.sh
expect_layers "a make file, full"         "shell check-format test-stack test-server test-chart" full mk/stack.mk
expect_layers "the shell tests"           "shell check-format"                    full  tests/test-versions.sh
expect_layers "a path nobody listed"      "shell check-format check-go"           quick something/new.txt
expect_layers "no files known, full"      "shell check-format check-go test-stack test-server test-chart" full
"$LAYERS" bogus < /dev/null > /dev/null 2>&1 && { echo "FAIL  an unknown mode was accepted"; fail=1; }

echo "== the gate is given those layers, and says what it skipped"
W3=$(mktemp -d)
trap 'rm -rf "$W" "$W2" "$W3"' EXIT
git clone -q "$SRC" "$W3/r" && cd "$W3/r"
# A clone of a worktree checks out the main checkout's branch, so the copied
# files may differ from it; main takes them in, or they would count as changed.
copy_claude && cp "$SRC/run-tests.sh" . && git add -A && git commit -q --allow-empty -m fixture && fixture_main
export CLAUDE_PROJECT_DIR="$W3/r"
git switch -qc fix/7-gate-layers
mkdir -p connect && printf 'words\n' > NOTES.md && printf 'package x\n' > connect/x.go && git add NOTES.md connect && git commit -qm "words and a go file"
printf '#!/bin/sh\necho "$@" > "$GATE_ARGS"\n' > run-tests.sh; chmod +x run-tests.sh
export GATE_ARGS="$W3/args"
push_json() { jq -nc --arg cwd "$W3/r" --arg cmd "${1:-git push -u origin HEAD}" '{tool_name:"Bash",cwd:$cwd,tool_input:{command:$cmd}}'; }
LOG3="$W3/r/.git/branch-guard.log"
FULL="shell check-format check-go test-stack test-server test-chart"
ran() { # label expected-layers
  if [[ "$(cat "$GATE_ARGS" 2>/dev/null)" == "$2" ]]; then echo "ok    gate  $1"; else echo "FAIL  gate  $1 ran '$(cat "$GATE_ARGS" 2>/dev/null)'"; fail=1; fi
}
gating_logged() { # label pattern
  if grep $'\tdecision=gating\t' "$LOG3" | tail -n 1 | grep -qF -- "$2"; then echo "ok    gate  $1 logged"
  else echo "FAIL  gate  $1: the gate's first line lacks '$2': $(grep $'\tdecision=gating\t' "$LOG3" | tail -n 1)"; fail=1; fi
}
out="$(push_json | env -u DYNAMO_GATE -u DYNAMO_GATE_LIMIT .claude/hooks/branch-guard.sh)"
ran "ran the layers of the files the branch changed since main" "shell check-format check-go"
if jq -e '.systemMessage | contains("Skipped:") and contains("test-stack")' <<< "$out" > /dev/null; then echo "ok    gate  said what it skipped"; else echo "FAIL  the skipped layers were not said: $out"; fail=1; fi
gating_logged "default mode and limit" $'\tgate_mode=quick (default)\tgate_limit=1200 (default)'
out="$(push_json | DYNAMO_GATE=full .claude/hooks/branch-guard.sh)"
ran "DYNAMO_GATE=full in the environment runs the stack" "$FULL"
gating_logged "mode from the environment" $'\tgate_mode=full (env)\tgate_limit=1700 (default)'

echo "== the gate takes DYNAMO_GATE and DYNAMO_GATE_LIMIT from the push command (#413)"
rm -f "$GATE_ARGS"
out="$(push_json "DYNAMO_GATE=full git push -u origin HEAD" | env -u DYNAMO_GATE .claude/hooks/branch-guard.sh)"
ran "DYNAMO_GATE=full git push runs the stack" "$FULL"
gating_logged "mode from the command" $'\tgate_mode=full (command)'
out="$(push_json "DYNAMO_GATE=quick git push" | DYNAMO_GATE=full .claude/hooks/branch-guard.sh)"
ran "the command overrides the environment" "shell check-format check-go"
out="$(push_json "env DYNAMO_GATE='full' DYNAMO_GATE_LIMIT=900 git push" | DYNAMO_GATE_LIMIT=600 .claude/hooks/branch-guard.sh)"
ran "env DYNAMO_GATE='full' git push" "$FULL"
gating_logged "limit from the command" $'\tgate_mode=full (command)\tgate_limit=900 (command)'
for push in "git -C $W3/r push" "git -c x=y push" "git -C $W3/r -c x=y push -u origin HEAD"; do
  rm -f "$GATE_ARGS"
  out="$(push_json "DYNAMO_GATE=full $push" | env -u DYNAMO_GATE .claude/hooks/branch-guard.sh)"
  ran "DYNAMO_GATE=full $push (#417)" "$FULL"
  rm -f "$GATE_ARGS"
  out="$(push_json "DYNAMO_GATE=quick $push" | DYNAMO_GATE=full .claude/hooks/branch-guard.sh)"
  ran "DYNAMO_GATE=quick $push (#417)" "shell check-format check-go"
done
rm -f "$GATE_ARGS"
out="$(push_json "cd /tmp && DYNAMO_GATE=full git -C $W3/r push" | env -u DYNAMO_GATE .claude/hooks/branch-guard.sh)"
ran "DYNAMO_GATE=full git -C <dir> push from outside the checkout (#417)" "$FULL"
out="$(push_json | env -u DYNAMO_GATE DYNAMO_GATE_LIMIT=600 .claude/hooks/branch-guard.sh)"
gating_logged "limit from the environment" $'\tgate_limit=600 (env)'
for bad in "DYNAMO_GATE=yes git push" "DYNAMO_GATE_LIMIT=2400 git push" "DYNAMO_GATE_LIMIT=10m git push" "DYNAMO_GATE_LIMIT=0 git push" 'DYNAMO_GATE=$MODE git push'; do
  rm -f "$GATE_ARGS"
  got="$(push_json "$bad" | .claude/hooks/branch-guard.sh | jq -r '.hookSpecificOutput.permissionDecision // "allow"')"
  if [[ "$got" == deny && ! -e "$GATE_ARGS" ]]; then echo "ok    gate  refused '$bad'"; else echo "FAIL  gate  '$bad' gave $got"; fail=1; fi
done
got="$(push_json | DYNAMO_GATE=yes .claude/hooks/branch-guard.sh | jq -r '.hookSpecificOutput.permissionDecision // "allow"')"
[[ "$got" == deny ]] && echo "ok    gate  refused DYNAMO_GATE=yes in the environment" || { echo "FAIL  gate  DYNAMO_GATE=yes in the environment gave $got"; fail=1; }

echo "== a gate that does not finish blocks the push (#328)"
printf '#!/bin/sh\nexec sleep 37\n' > run-tests.sh
got="$(push_json "DYNAMO_GATE_LIMIT=3 git push -u origin HEAD" | env -u DYNAMO_GATE_LIMIT .claude/hooks/branch-guard.sh | jq -r '.hookSpecificOutput | .permissionDecision + " " + .permissionDecisionReason')"
if [[ "$got" == deny\ *"did not finish within"*"DYNAMO_GATE_LIMIT=<seconds> git push"* ]]; then echo "ok    timeout  denied the push"; else echo "FAIL  a gate that ran out of time gave: $got"; fail=1; fi
sleep 1
if pgrep -f '^sleep 37$' > /dev/null; then echo "FAIL  timeout  the gate was left running"; fail=1; pkill -f '^sleep 37$'; else echo "ok    timeout  nothing was left running"; fi
grep -q $'\tgate_limit=3 (command)\t.*\tgate_timeout=3' "$LOG3" && echo "ok    timeout  logged" || { echo "FAIL  timeout  not logged"; fail=1; }
cd "$W"

if (( fail )); then
  echo "❌ branch-guard tests failed"
  exit 1
fi
echo "✅ branch-guard tests passed"
