#!/usr/bin/env bash
# Ephemeral local PostgreSQL 16 cluster for development and tests.
#   tools/dev/pg.sh start   -> prints the admin URL (also written to .cache/pg/admin_url)
#   tools/dev/pg.sh stop
#   tools/dev/pg.sh status
# PostgreSQL refuses to run as root, so the cluster runs as the postgres user in a
# postgres-owned temporary directory. Never point this at a shared or production server.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
STATE="$ROOT/.cache/pg"
PORT="${ATI_PG_PORT:-55432}"
PG_BIN="${ATI_PG_BIN:-$(ls -d /usr/lib/postgresql/*/bin 2>/dev/null | sort -V | tail -1)}"
mkdir -p "$STATE"

run_as_pg() {
  if [ "$(id -u)" = "0" ]; then runuser -u postgres -- "$@"; else "$@"; fi
}

case "${1:-}" in
  start)
    if [ -f "$STATE/datadir" ] && run_as_pg "$PG_BIN/pg_ctl" -D "$(cat "$STATE/datadir")/data" status >/dev/null 2>&1; then
      cat "$STATE/admin_url"; exit 0
    fi
    DIR="$(mktemp -d /tmp/ati-pg.XXXXXX)"
    if [ "$(id -u)" = "0" ]; then chown postgres:postgres "$DIR"; fi
    chmod 700 "$DIR"
    run_as_pg "$PG_BIN/initdb" -D "$DIR/data" -A trust -U postgres -E UTF8 --no-instructions >/dev/null
    run_as_pg "$PG_BIN/pg_ctl" -D "$DIR/data" -l "$DIR/server.log" -w \
      -o "-k $DIR -p $PORT -c listen_addresses=127.0.0.1 -c max_connections=300 -c fsync=off -c synchronous_commit=off" start >/dev/null
    echo "$DIR" > "$STATE/datadir"
    echo "postgres://postgres@127.0.0.1:$PORT/postgres?sslmode=disable" > "$STATE/admin_url"
    cat "$STATE/admin_url"
    ;;
  stop)
    if [ -f "$STATE/datadir" ]; then
      DIR="$(cat "$STATE/datadir")"
      run_as_pg "$PG_BIN/pg_ctl" -D "$DIR/data" -m fast -w stop >/dev/null 2>&1 || true
      rm -rf "$DIR" "$STATE/datadir" "$STATE/admin_url"
    fi
    echo "stopped"
    ;;
  status)
    if [ -f "$STATE/datadir" ] && run_as_pg "$PG_BIN/pg_ctl" -D "$(cat "$STATE/datadir")/data" status >/dev/null 2>&1; then
      echo "running: $(cat "$STATE/admin_url")"
    else
      echo "not running"; exit 1
    fi
    ;;
  *)
    echo "usage: $0 start|stop|status" >&2; exit 2
    ;;
esac
