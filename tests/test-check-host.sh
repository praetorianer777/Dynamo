#!/usr/bin/env bash
# scripts/check-host.sh, which make up runs first, stops on what the stack
# cannot do without and only warns about a small or emulating machine, each
# time saying what to do. A stub docker plays the machine.
#
# Needs bash and coreutils; docker is never called.
set -u

SRC="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="$SRC/scripts/check-host.sh"
FAILED=0
pass() { echo "   ✅ $1"; }
fail() { echo "   ❌ $1"; FAILED=1; }

echo "🧪 host check"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin"
cat > "$work/bin/docker" <<'STUB'
#!/usr/bin/env bash
case "$1 ${2:-}" in
  "info "*) [[ "$STUB_INFO" == down ]] && exit 1; echo "$STUB_INFO" ;;
  "compose version") echo "$STUB_COMPOSE" ;;
  "compose ps") echo "${STUB_RUNNING:-}" ;;
  "buildx version") [[ -z "${STUB_NO_BUILDX:-}" ]] ;;
esac
STUB
chmod +x "$work/bin/docker"

GB=$((1024 * 1024 * 1024))
# Runs the check on a machine that has everything, but for what the caller
# changes; sets $out and $code.
check() {
  out="$(env PATH="$work/bin:$PATH" STUB_INFO="x86_64 $((16 * GB)) $work" STUB_COMPOSE=2.29.1 "$@" 2>&1)"
  code=$?
}
expect() { # name, wanted exit code, text the output must hold
  if [[ "$code" == "$2" ]] && grep -qF -- "$3" <<< "$out"; then pass "$1"; else fail "$1: exit $code, output:"$'\n'"$out"; fi
}

check bash "$SCRIPT"
expect "a machine with everything passes" 0 "Compose 2.29.1"

out="$(env PATH=/nonexistent "$BASH" "$SCRIPT" 2>&1)"; code=$?
expect "without Docker it says how to install it" 1 "Install Docker Engine"

check STUB_INFO=down bash "$SCRIPT"
expect "a Docker that does not answer is named, with the docker group" 1 "usermod -aG docker"

check STUB_COMPOSE= bash "$SCRIPT"
expect "without Compose it names the plugin" 1 "docker-compose-plugin"

check STUB_COMPOSE=v2.20.3 bash "$SCRIPT"
expect "an old Compose stops it" 1 "Compose is 2.20.3, and the stack needs 2.24.0"

check STUB_COMPOSE=2.24.0 bash "$SCRIPT"
expect "Compose 2.24.0 is enough" 0 "Compose 2.24.0"

check STUB_NO_BUILDX=1 bash "$SCRIPT"
expect "without buildx it names the plugin" 1 "docker-buildx-plugin"

check STUB_INFO="aarch64 $((16 * GB)) $work" bash "$SCRIPT"
expect "an arm64 machine only warns, and names the emulation" 0 "tonistiigi/binfmt"

check STUB_INFO="x86_64 $((2 * GB)) $work" bash "$SCRIPT"
expect "little memory only warns" 0 "Docker has 2 GB of memory, and the stack wants 4 GB"

check DISK_MIN_GB=99999999 bash "$SCRIPT"
expect "little disk only warns" 0 "docker system prune"

# A port of its own that answers, so the check sees it taken.
if command -v python3 > /dev/null; then
  python3 -c 'import socket,time; s=socket.socket(); s.bind(("127.0.0.1",0)); s.listen(); print(s.getsockname()[1], flush=True); time.sleep(30)' > "$work/port" &
  listener=$!
  for _ in $(seq 50); do [[ -s "$work/port" ]] && break; sleep 0.1; done
  port="$(cat "$work/port")"
  check bash "$SCRIPT" "$port"
  expect "a taken port stops it, with STACK_PORT_BASE" 1 "these ports are taken: $port"
  check STUB_RUNNING=abc123 bash "$SCRIPT" "$port"
  expect "a port the stack's own containers hold is not taken" 0 "Compose"
  kill "$listener" 2> /dev/null
else
  echo "   ⏭️  no python3 to listen on a port; the port check is not tested here"
fi

exit "$FAILED"
