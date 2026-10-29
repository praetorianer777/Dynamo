#!/usr/bin/env bash
# Checks that this machine can run the stack before make up pulls anything:
# what is missing stops it, what is merely small or slow only warns.
#
#   scripts/check-host.sh [port...]   the ports the stack publishes
#
# Needs bash, coreutils and docker; it runs before any container does.
set -u

# As docs/deploying-compose.md asks: optional dependencies (required: false)
# and COMPOSE_ENV_FILES, which the Makefile sets, need a recent Compose.
COMPOSE_MIN=2.24.0
# The pinned images take about 11 GB, Kroki and Stator's Gotenberg 6 GB of it;
# the running stack about 2 GB of memory.
DISK_MIN_GB=${DISK_MIN_GB:-20}
MEMORY_MIN_GB=4

FAILED=0
ok() { echo "   ✅ $1"; }
warn() { echo "   ⚠️  $1"; }
fail() { echo "   ❌ $1"; FAILED=1; }

echo "🔎 Checking this machine for the stack"

if ! command -v docker > /dev/null; then
  fail "Docker is not installed. Install Docker Engine with its Compose plugin (https://docs.docker.com/engine/install/), or Docker Desktop, then run make up again."
  exit 1
fi

if ! info="$(docker info --format '{{.Architecture}} {{.MemTotal}} {{.DockerRootDir}}' 2> /dev/null)"; then
  fail "Docker does not answer. Start it (sudo systemctl start docker, or open Docker Desktop); if it runs, add yourself to the group docker (sudo usermod -aG docker \$USER) and sign in again."
  exit 1
fi
read -r arch memory root <<< "$info"
ok "Docker answers"

compose="$(docker compose version --short 2> /dev/null)"
compose="${compose#v}"
if [[ -z "$compose" ]]; then
  fail "Docker has no Compose plugin. Install docker-compose-plugin (https://docs.docker.com/compose/install/linux/), or update Docker Desktop."
elif [[ "$(printf '%s\n' "$COMPOSE_MIN" "$compose" | sort -V | head -n 1)" != "$COMPOSE_MIN" ]]; then
  fail "Compose is $compose, and the stack needs $COMPOSE_MIN or newer. Update docker-compose-plugin or Docker Desktop."
else
  ok "Compose $compose"
fi

# make up builds dynamo-connect, and Compose builds only through buildx.
if docker buildx version > /dev/null 2>&1; then
  ok "buildx, for building dynamo-connect"
else
  fail "Docker has no buildx plugin, which make up needs to build dynamo-connect. Install docker-buildx-plugin (https://docs.docker.com/engine/install/), or update Docker Desktop."
fi

case "$arch" in
  x86_64 | amd64) ok "the machine is amd64, as the tools' images are" ;;
  *) warn "the machine is $arch, and the tools publish their images for amd64 only. Docker Desktop runs them emulated, slowly; Docker Engine needs QEMU first (docker run --privileged --rm tonistiigi/binfmt --install amd64)." ;;
esac

gb=$((memory / 1024 / 1024 / 1024))
if [[ "$memory" =~ ^[0-9]+$ ]] && ((gb < MEMORY_MIN_GB)); then
  warn "Docker has ${gb} GB of memory, and the stack wants ${MEMORY_MIN_GB} GB. Give Docker Desktop more under Settings, Resources, or expect the tools to start slowly."
else
  ok "Docker has ${gb} GB of memory"
fi

# The root directory belongs to root under Docker Engine; when df cannot see
# it, the check says nothing rather than guess.
if free="$(df -Pk "$root" 2> /dev/null | awk 'NR == 2 {print int($4 / 1024 / 1024)}')" && [[ -n "$free" ]]; then
  if ((free < DISK_MIN_GB)); then
    warn "Docker has ${free} GB free in $root, and the images take about 11 GB. Free some room, for example with docker system prune, before the pull runs out."
  else
    ok "Docker has ${free} GB free for images"
  fi
fi

# A port that answers belongs to something else, unless it is this stack's
# own, which make up starts again.
taken=""
running="$(docker compose ps --quiet 2> /dev/null)"
for port in "$@"; do
  [[ -n "$running" ]] && break
  (exec 3<> "/dev/tcp/127.0.0.1/$port") 2> /dev/null && taken="$taken $port"
done
if [[ -n "$taken" ]]; then
  fail "these ports are taken:$taken. Stop what listens there, or give the stack others with make up STACK_PORT_BASE=<port>, which uses that port and the next four."
elif (($# > 0)); then
  ok "the stack's ports are free"
fi

exit "$FAILED"
