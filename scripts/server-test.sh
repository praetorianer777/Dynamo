#!/usr/bin/env bash
# Starts the suite the way a server does (docs/deploying-compose.md): settings
# from deploy/.env.example completed by deploy/compose-secrets.sh, then
# docker compose up alone, with the bundled Keycloak and its database, HTTPS
# by Caddy (its own CA instead of Let's Encrypt) and production mode. Then the
# stack checks sign a person of the realm in through HTTPS, test every
# webhook and look at the sample data; connect and sample-data run again
# without anything to change, and after Keycloak restarts the person still
# signs in.
#
# make test-server runs it with SERVER_PROJECT and SERVER_PORT, the HTTPS
# port, the next one being Caddy's HTTP port. DYNAMO_KEEP_SERVER=1 leaves the
# stack up afterwards.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
: "${SERVER_PROJECT:?make test-server sets it}" "${SERVER_PORT:?make test-server sets it}"
GO_IMAGE="${GO_IMAGE:-golang:1.26-bookworm}"
P="$SERVER_PORT"
work="$ROOT/.cache/server"
settings="$work/compose.env"
say() { echo "🧪 $*"; }

mkdir -p "$work"
rm -f "$settings" "$work/stack.env" "$work/root.crt"
cp "$ROOT/deploy/.env.example" "$settings"
value() { grep -E "^$1=" "$settings" | tail -n 1 | cut -d= -f2-; }
set_value() { sed -i "s|^$1=.*|$1=$2|" "$settings"; grep -qE "^$1=" "$settings" || printf '%s=%s\n' "$1" "$2" >> "$settings"; }

# What an operator sets, under names only the checks resolve, to Caddy; the services' own ports
# are left to Docker, since Caddy is the way in.
for kv in \
  "COMPOSE_PROJECT_NAME=$SERVER_PROJECT" \
  "COMPOSE_PROFILES=keycloak-server,connect,caddy,sample-data" \
  "DYNAMO_CONNECT_IMAGE=dynamo-connect:dev" \
  "STATOR_URL=https://stator.suite.test:$P" \
  "ARMATURE_URL=https://armature.suite.test:$P" \
  "EXCITER_URL=https://exciter.suite.test:$P" \
  "KEYCLOAK_URL=https://sso.suite.test:$P" \
  "STATOR_ADMIN_EMAIL=admin@stator.test" \
  "ARMATURE_ADMIN_EMAIL=admin@armature.test" \
  "EXCITER_ADMIN_EMAIL=admin@exciter.test" \
  "DYNAMO_MEMBERS=tester@suite.test=admin" \
  "CADDY_HTTPS_PORT=$P" "CADDY_HTTP_PORT=$((P + 1))" "CADDY_LISTEN=127.0.0.1:" \
  "CADDY_GLOBAL_OPTIONS=local_certs" \
  "KEYCLOAK_PORT=" "STATOR_WEB_PORT=" "ARMATURE_WEB_PORT=" "EXCITER_WEB_PORT=" "MAILPIT_PORT="; do
  set_value "${kv%%=*}" "${kv#*=}"
done
"$ROOT/deploy/compose-secrets.sh" "$settings" > /dev/null

dc() { env -i PATH="$PATH" HOME="$HOME" docker compose -f "$ROOT/deploy/docker-compose.yml" --env-file "$settings" "$@"; }
finish() {
  local rc=$?
  if [[ $rc -ne 0 ]]; then
    echo "--- what failed"
    dc ps -a --format '{{.Service}} {{.State}} {{.ExitCode}}' | grep -v ' running 0$' | sed 's/^/   /' || true
    for s in $(dc ps -a --format '{{.Service}} {{.State}} {{.ExitCode}}' | awk '$2 == "exited" && $3 != 0 {print $1}') connect caddy; do
      echo "--- $s"; dc logs --no-color --tail 30 "$s" 2>&1 | sed 's/^/   /' || true
    done
  fi
  [[ -n "${DYNAMO_KEEP_SERVER:-}" ]] || dc down -v --remove-orphans > /dev/null 2>&1 || true
  exit "$rc"
}
trap finish EXIT
trap 'exit 143' TERM INT HUP

# finished waits for a one-shot service to exit and answers whether it succeeded.
finished() {
  local deadline=$((SECONDS + ${2:-900})) state
  while (( SECONDS < deadline )); do
    state="$(dc ps -a --format '{{.State}} {{.ExitCode}}' "$1" 2>/dev/null | head -n 1)"
    case "$state" in
      "exited 0") return 0 ;;
      exited*) echo "   $1 exited with ${state#exited }"; return 1 ;;
    esac
    sleep 3
  done
  echo "   $1 did not finish in time"
  return 1
}

say "up, as on a server"
docker build -q -t dynamo-connect:dev "$ROOT/connect" > /dev/null
start=$SECONDS
dc up -d --quiet-pull
finished connect
dc logs --no-color connect | sed 's/^/   /' | tail -n 25
finished sample-data
echo "   up, connected and the sample imported in $((SECONDS - start))s"

say "a person of the realm"
password="$(LC_ALL=C tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 24 || true)"
kc() { dc exec -T keycloak-server /opt/keycloak/bin/kcadm.sh "$@" --config /tmp/kcadm.config; }
kc config credentials --server http://localhost:8080 --realm master --user "$(value KEYCLOAK_ADMIN)" --password "$(value KEYCLOAK_PASSWORD)" > /dev/null
kc create users -r dynamo -s username=tester -s email=tester@suite.test -s emailVerified=true -s enabled=true \
  -s firstName=Test -s lastName=Person > /dev/null
kc set-password -r dynamo --username tester --new-password "$password" > /dev/null
echo "   tester@suite.test is in the realm and a member of all three"

for _ in $(seq 60); do
  dc cp caddy:/data/caddy/pki/authorities/local/root.crt "$work/root.crt" > /dev/null 2>&1 && break
  sleep 2
done
[[ -s "$work/root.crt" ]] || { echo "   Caddy made no certificate authority"; exit 1; }

{
  for t in STATOR ARMATURE EXCITER; do
    echo "${t}_URL=$(value "${t}_URL")"
    echo "${t}_ADMIN_EMAIL=$(value "${t}_ADMIN_EMAIL")"
    echo "${t}_ADMIN_PASSWORD=$(value "${t}_ADMIN_PASSWORD")"
  done
  echo "SIGNIN_EMAIL=tester@suite.test"
  echo "SIGNIN_PASSWORD=$password"
  echo "DYNAMO_SAMPLE_SOURCES=sparkplug"
} > "$work/stack.env"
chmod 600 "$work/stack.env"

hosts=""
for h in stator armature exciter sso; do hosts="$hosts${hosts:+,}$h.suite.test:$P=127.0.0.1:$P"; done
checks() {
  docker run --rm -u "$(id -u):$(id -g)" --network host \
    -v "$ROOT:/work" -v "$ROOT/.cache/go:/cache" \
    -e HOME=/tmp -e GOCACHE=/cache/build -e GOMODCACHE=/cache/mod -e GOFLAGS=-buildvcs=false -e GOTOOLCHAIN=local \
    -e DYNAMO_STACK_ENV=/work/.cache/server/stack.env -e SSL_CERT_FILE=/work/.cache/server/root.crt -e DYNAMO_HOSTS="$hosts" \
    -w /work/connect "$GO_IMAGE" go test -count=1 -tags stack -run "$1" -v ./... | grep -vE '^=== (RUN|PAUSE|CONT)'
}

say "the stack checks, over HTTPS"
checks Stack

say "again, with nothing to change"
dc run --rm -e DYNAMO_EXPECT_UNCHANGED=1 connect 2>&1 | tail -n 1 | sed 's/^/   /'
dc run --rm -e DYNAMO_EXPECT_UNCHANGED=1 sample-data 2>&1 | tail -n 1 | sed 's/^/   /'

say "Keycloak keeps its accounts"
dc restart keycloak-server > /dev/null
dc up -d --wait keycloak-server > /dev/null
checks StackOneSignIn

say "the suite runs on a server from deploy/.env.example"
