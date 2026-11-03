#!/usr/bin/env bash
# Local development stack: PostgreSQL 16, LiveKit OSS server, control plane, Tier-1 agent.
#
#   tools/dev/stack.sh up             start everything (idempotent) and print endpoints
#   tools/dev/stack.sh down           stop everything and delete runtime state
#   tools/dev/stack.sh status
#   tools/dev/stack.sh restart        rebuild and restart the control plane and the agent
#   tools/dev/stack.sh agent-kill     SIGKILL the agent process group (failure tests)
#   tools/dev/stack.sh agent-start    start the agent again
#   tools/dev/stack.sh web-up         build (if needed) and start the web UI on http://localhost:3000
#   tools/dev/stack.sh web-stop
#   tools/dev/stack.sh env            print the endpoints as shell assignments
#
# Development only: synthetic data, development placeholder secrets ("ati-dev-" prefix, refused
# in production), APP_ENV=dev, scripted voice engine. Never use it with real candidates (ADR-020).
# LiveKit listens on the node IP so that browsers accept its ICE candidates; do not run this on a
# shared network.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
STATE="$ROOT/.cache/stack"
LOGS="$STATE/logs"
BIN="$STATE/bin"
LK_VERSION="1.13.11"
declare -A LK_SHA256=(
  [amd64]="1e9ee90ca301e1842b2887fc77cb122759756a9c8aeb4334ee91de3325ec43fb"
  [arm64]="2af6ce091d6b3c79d1677f8ff1bbb91a8007f40b2325c059b52d38383c0830b6"
)
CP_PUBLIC="127.0.0.1:${ATI_DEV_CP_PUBLIC_PORT:-18080}"
CP_INTERNAL="127.0.0.1:${ATI_DEV_CP_INTERNAL_PORT:-18081}"
AGENT_HEALTH_PORT="${ATI_DEV_AGENT_HEALTH_PORT:-18082}"
LK_PORT="${ATI_DEV_LIVEKIT_PORT:-7880}"
WEB_PORT="${ATI_DEV_WEB_PORT:-3000}"
PUBLIC_ORIGIN="${ATI_DEV_PUBLIC_ORIGIN:-http://localhost:$WEB_PORT}"
NODE_IP="${ATI_DEV_NODE_IP:-$(hostname -I 2>/dev/null | awk '{print $1}')}"
NODE_IP="${NODE_IP:-127.0.0.1}"
PG_PORT="${ATI_PG_PORT:-55432}"
PG_BIN="${ATI_PG_BIN:-$(ls -d /usr/lib/postgresql/*/bin 2>/dev/null | sort -V | tail -1)}"

# Development placeholders only.
LK_API_KEY="devapikey"
LK_API_SECRET="ati-dev-livekit-api-secret-0123456789abcdef"
LK_WEBHOOK_KEY="devwebhookkey"
LK_WEBHOOK_SECRET="ati-dev-livekit-webhook-secret-0123456789ab"
AGENT_TOKEN="ati-dev-agent-service-token-0123456789abcdef"
AGENT_NAME="ati-tier1-interviewer"

export NO_PROXY="localhost,127.0.0.1,::1,$NODE_IP${NO_PROXY:+,$NO_PROXY}"
export no_proxy="$NO_PROXY"

log() { echo "stack: $*" >&2; }

pid_alive() { [ -f "$1" ] && kill -0 "$(cat "$1")" 2>/dev/null; }

# start NAME DIR CMD... : run in its own session (process group); the pidfile holds the real PID.
start() {
  local name="$1" dir="$2"
  shift 2
  if pid_alive "$STATE/$name.pid"; then return 0; fi
  # The whole subshell is redirected: no process may keep the caller's stdout/stderr open, or a
  # caller that captures output would wait for the service to exit.
  (cd "$dir" && exec setsid sh -c 'echo $$ > "$0"; exec "$@"' "$STATE/$name.pid" "$@") >>"$LOGS/$name.log" 2>&1 </dev/null &
  for _ in $(seq 1 50); do [ -s "$STATE/$name.pid" ] && return 0; sleep 0.1; done
  log "$name did not start"; return 1
}

stop() {
  local pidfile="$STATE/$1.pid"
  if pid_alive "$pidfile"; then
    local pid; pid="$(cat "$pidfile")"
    kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
    for _ in $(seq 1 50); do kill -0 "$pid" 2>/dev/null || break; sleep 0.1; done
    kill -KILL -- "-$pid" 2>/dev/null || true
  fi
  rm -f "$pidfile"
}

wait_http() {
  local name="$1" url="$2"
  for _ in $(seq 1 150); do
    if curl -fsS --max-time 2 --noproxy '*' -o /dev/null "$url" 2>/dev/null; then return 0; fi
    sleep 0.2
  done
  log "$name is not ready ($url); see $LOGS/$name.log"; tail -n 20 "$LOGS/$name.log" >&2 || true; return 1
}

fetch_livekit() {
  local arch
  case "$(uname -m)" in x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) log "unsupported arch"; return 1 ;; esac
  local cache="$ROOT/.cache/livekit/$LK_VERSION"
  if [ -x "$cache/livekit-server" ]; then echo "$cache/livekit-server"; return 0; fi
  mkdir -p "$cache"
  local tgz="$cache/livekit.tar.gz"
  curl -fsSL -o "$tgz" "https://github.com/livekit/livekit/releases/download/v$LK_VERSION/livekit_${LK_VERSION}_linux_${arch}.tar.gz"
  echo "${LK_SHA256[$arch]}  $tgz" | sha256sum -c --quiet - || { rm -f "$tgz"; log "LiveKit checksum mismatch"; return 1; }
  tar -xzf "$tgz" -C "$cache" livekit-server
  rm -f "$tgz"
  echo "$cache/livekit-server"
}

write_livekit_config() {
  cat >"$STATE/livekit.yaml" <<EOF
port: $LK_PORT
bind_addresses: ["127.0.0.1", "$NODE_IP"]
rtc:
  tcp_port: $((LK_PORT + 1))
  port_range_start: 50000
  port_range_end: 50200
  use_external_ip: false
  node_ip: $NODE_IP
keys:
  $LK_API_KEY: $LK_API_SECRET
  $LK_WEBHOOK_KEY: $LK_WEBHOOK_SECRET
room:
  auto_create: false
webhook:
  api_key: $LK_WEBHOOK_KEY
  urls:
    - http://$CP_INTERNAL/internal/v1/livekit/webhook
logging:
  level: info
EOF
}

cp_env() {
  local token_sha; token_sha="$(printf '%s' "$AGENT_TOKEN" | sha256sum | cut -d' ' -f1)"
  cat <<EOF
APP_ENV=dev
ATI_PUBLIC_ADDR=$CP_PUBLIC
ATI_INTERNAL_ADDR=$CP_INTERNAL
ATI_PUBLIC_ORIGIN=$PUBLIC_ORIGIN
ATI_DATABASE_URL=postgres://ati_control_plane@127.0.0.1:$PG_PORT/ati?sslmode=disable
ATI_TIER1_CTX_DATABASE_URL=postgres://ati_tier1_ctx@127.0.0.1:$PG_PORT/ati?sslmode=disable
ATI_LIVEKIT_URL=ws://localhost:$LK_PORT
ATI_LIVEKIT_API_URL=http://127.0.0.1:$LK_PORT
ATI_LIVEKIT_API_KEY=$LK_API_KEY
ATI_LIVEKIT_API_SECRET=$LK_API_SECRET
ATI_LIVEKIT_WEBHOOK_API_KEY=$LK_WEBHOOK_KEY
ATI_LIVEKIT_WEBHOOK_API_SECRET=$LK_WEBHOOK_SECRET
ATI_AGENT_NAME=$AGENT_NAME
ATI_AGENT_SERVICE_TOKEN_SHA256=$token_sha
ATI_RETENTION_TRANSCRIPT_DAYS=${ATI_DEV_RETENTION_DAYS:-7}
ATI_RETENTION_SESSION_EVENTS_DAYS=${ATI_DEV_RETENTION_DAYS:-7}
ATI_RETENTION_ASSESSMENT_OUTPUT_DAYS=${ATI_DEV_RETENTION_DAYS:-7}
ATI_AGENT_DISPATCH_TIMEOUT=${ATI_DEV_AGENT_DISPATCH_TIMEOUT:-30s}
ATI_PAUSE_TIMEOUT=${ATI_DEV_PAUSE_TIMEOUT:-30m}
ATI_LOG_LEVEL=info
EOF
}

agent_env() {
  cat <<EOF
APP_ENV=dev
ATI_VOICE_ENGINE=${ATI_DEV_VOICE_ENGINE:-scripted}
ATI_LIVEKIT_URL=ws://127.0.0.1:$LK_PORT
ATI_LIVEKIT_API_KEY=$LK_API_KEY
ATI_LIVEKIT_API_SECRET=$LK_API_SECRET
ATI_AGENT_NAME=$AGENT_NAME
ATI_CONTROL_PLANE_INTERNAL_URL=http://$CP_INTERNAL
ATI_AGENT_SERVICE_TOKEN=$AGENT_TOKEN
ATI_AGENT_HEALTH_PORT=$AGENT_HEALTH_PORT
EOF
}

agent_start() {
  agent_env >"$STATE/agent.env"
  start agent "$ROOT/services/tier1-voice-agent" \
    env -i PATH="$PATH" HOME="$HOME" LANG=C.UTF-8 NO_PROXY="$NO_PROXY" no_proxy="$NO_PROXY" \
    $(grep -v '^#' "$STATE/agent.env" | xargs) \
    "$ROOT/services/tier1-voice-agent/.venv/bin/python" -m tier1_voice_agent
  wait_http agent "http://127.0.0.1:$AGENT_HEALTH_PORT/"
}

up() {
  mkdir -p "$STATE" "$LOGS" "$BIN"
  chmod 700 "$STATE"
  local admin; admin="$("$ROOT/tools/dev/pg.sh" start)"
  local psql=("$PG_BIN/psql" -v ON_ERROR_STOP=1 -qtA)
  if ! "${psql[@]}" "$admin" -c "SELECT 1 FROM pg_database WHERE datname = 'ati'" | grep -q 1; then
    log "building control plane"
    (cd "$ROOT/services/control-plane" && go build -o "$BIN/control-plane" ./cmd/control-plane && go build -o "$BIN/ati-admin" ./cmd/ati-admin)
    "$BIN/ati-admin" bootstrap-roles --admin-url "$admin"
    "${psql[@]}" "$admin" -c "CREATE DATABASE ati"
    local owner="${admin%/postgres*}/ati?sslmode=disable"
    "$BIN/ati-admin" migrate --url "$owner"
    "$BIN/ati-admin" seed-plans --url "$owner"
    "$BIN/ati-admin" create-tenant --url "$owner" --name "Synthetic development tenant" >"$STATE/tenant_id"
    (umask 077 && "$BIN/ati-admin" create-staff-token --url "$owner" --tenant "$(cat "$STATE/tenant_id")" --label dev-stack --role recruiter >"$STATE/staff_token")
  elif [ ! -x "$BIN/control-plane" ]; then
    (cd "$ROOT/services/control-plane" && go build -o "$BIN/control-plane" ./cmd/control-plane && go build -o "$BIN/ati-admin" ./cmd/ati-admin)
  fi
  local lk; lk="$(fetch_livekit)"
  write_livekit_config
  start livekit "$STATE" "$lk" --config "$STATE/livekit.yaml"
  wait_http livekit "http://127.0.0.1:$LK_PORT/"
  cp_env >"$STATE/control-plane.env"
  start control-plane "$STATE" env -i PATH="$PATH" HOME="$HOME" $(grep -v '^#' "$STATE/control-plane.env" | xargs) "$BIN/control-plane"
  wait_http control-plane "http://$CP_INTERNAL/readyz"
  if [ ! -x "$ROOT/services/tier1-voice-agent/.venv/bin/python" ]; then
    (cd "$ROOT/services/tier1-voice-agent" && uv sync --frozen)
  fi
  agent_start
  print_env
}

print_env() {
  cat <<EOF
ATI_E2E_PUBLIC_URL=http://$CP_PUBLIC
ATI_E2E_INTERNAL_URL=http://$CP_INTERNAL
ATI_E2E_PUBLIC_ORIGIN=$PUBLIC_ORIGIN
ATI_E2E_LIVEKIT_API_URL=http://127.0.0.1:$LK_PORT
ATI_E2E_LIVEKIT_API_KEY=$LK_API_KEY
ATI_E2E_LIVEKIT_API_SECRET=$LK_API_SECRET
ATI_E2E_DB_ADMIN_URL=postgres://postgres@127.0.0.1:$PG_PORT/ati?sslmode=disable
ATI_E2E_STATE_DIR=$STATE
ATI_E2E_WEB_URL=$PUBLIC_ORIGIN
EOF
}

down() {
  stop web
  stop agent
  stop control-plane
  stop livekit
  "$ROOT/tools/dev/pg.sh" stop >/dev/null
  rm -rf "$STATE"
  log "stopped"
}

web_up() { # build (once per source state) and start the web UI's production server
  local web="$ROOT/apps/web" stamp
  [ -d "$web/node_modules" ] || (cd "$web" && pnpm install --frozen-lockfile >/dev/null)
  stamp="$(cd "$web" && find . \( -path ./node_modules -o -path ./.next \) -prune -o -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | cut -c1-16)"
  if [ ! -f "$STATE/web.built" ] || [ "$(cat "$STATE/web.built")" != "$stamp" ]; then
    log "building web UI"
    (cd "$web" && ATI_CONTROL_PLANE_URL="http://$CP_PUBLIC" NEXT_TELEMETRY_DISABLED=1 pnpm build >"$LOGS/web-build.log" 2>&1) || { tail -n 30 "$LOGS/web-build.log" >&2; return 1; }
    echo "$stamp" >"$STATE/web.built"
  fi
  start web "$web" env -i PATH="$PATH" HOME="$HOME" APP_ENV=dev NODE_ENV=production NEXT_TELEMETRY_DISABLED=1 \
    ATI_LIVEKIT_PUBLIC_URL="ws://localhost:$LK_PORT" "$web/node_modules/.bin/next" start -H 127.0.0.1 -p "${WEB_PORT}"
  wait_http web "http://127.0.0.1:${WEB_PORT}/"
}

restart() { # rebuild the control plane and restart it and the agent (PostgreSQL and LiveKit keep running)
  stop agent
  stop control-plane
  (cd "$ROOT/services/control-plane" && go build -o "$BIN/control-plane" ./cmd/control-plane && go build -o "$BIN/ati-admin" ./cmd/ati-admin)
  cp_env >"$STATE/control-plane.env"
  start control-plane "$STATE" env -i PATH="$PATH" HOME="$HOME" $(grep -v '^#' "$STATE/control-plane.env" | xargs) "$BIN/control-plane"
  wait_http control-plane "http://$CP_INTERNAL/readyz"
  agent_start
}

status() {
  for n in livekit control-plane agent web; do
    if pid_alive "$STATE/$n.pid"; then echo "$n: running (pid $(cat "$STATE/$n.pid"))"; else echo "$n: stopped"; fi
  done
  "$ROOT/tools/dev/pg.sh" status || true
}

case "${1:-}" in
  up) up ;;
  down) down ;;
  status) status ;;
  restart) restart ;;
  env) print_env ;;
  agent-kill)
    if pid_alive "$STATE/agent.pid"; then kill -KILL -- "-$(cat "$STATE/agent.pid")" 2>/dev/null || true; fi
    rm -f "$STATE/agent.pid"
    ;;
  agent-start) agent_start ;;
  web-up) web_up ;;
  web-stop) stop web ;;
  *) echo "usage: $0 up|down|status|restart|env|agent-kill|agent-start|web-up|web-stop" >&2; exit 2 ;;
esac
