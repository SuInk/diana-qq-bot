#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PID_FILE="$ROOT/data/diana-qq-bot.pid"
RUNTIME_LOG="$ROOT/logs/runtime.log"

mkdir -p "$ROOT/data" "$ROOT/logs"

if [[ -f "$PID_FILE" ]]; then
  old_pid="$(cat "$PID_FILE" 2>/dev/null || true)"
  if [[ -n "$old_pid" ]] && kill -0 "$old_pid" 2>/dev/null; then
    echo "diana-qq-bot already running: $old_pid"
    exit 0
  fi
fi

cd "$ROOT"
nohup "$ROOT/scripts/start-local-mac.sh" > "$RUNTIME_LOG" 2>&1 &
pid="$!"
echo "$pid" > "$PID_FILE"

sleep 1
if ! kill -0 "$pid" 2>/dev/null; then
  echo "diana-qq-bot failed to start; recent log:"
  tail -80 "$RUNTIME_LOG" || true
  exit 1
fi

echo "diana-qq-bot started: $pid"
