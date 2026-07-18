#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "start-napcat-mac.sh must run on macOS" >&2
  exit 1
fi

QQ_APP="${DIANA_QQ_APP:-/Applications/QQ.app}"
QQ_BINARY="$QQ_APP/Contents/MacOS/QQ"
EXPECTED_VERSION="${DIANA_QQ_EXPECTED_VERSION:-}"
ENFORCE_APP="${DIANA_QQ_ENFORCE_APP:-true}"

qq_processes() {
  /usr/bin/pgrep -x QQ 2>/dev/null || true
}

qq_running_with_napcat() {
  local pid command found=false

  while IFS= read -r pid; do
    [[ -n "$pid" ]] || continue
    command="$(/bin/ps -ww -o command= -p "$pid" 2>/dev/null || true)"
    if [[ "$command" == "$QQ_BINARY"* && "$command" == *" --no-sandbox"* ]]; then
      found=true
      continue
    fi
    return 1
  done < <(qq_processes)
  [[ "$found" == "true" ]]
}

if [[ ! -x "$QQ_BINARY" ]]; then
  echo "QQ app not found: $QQ_APP" >&2
  exit 1
fi

if [[ -n "$EXPECTED_VERSION" ]]; then
  PACKAGE_JSON="$QQ_APP/Contents/Resources/app/package.json"
  ACTUAL_VERSION="$(/usr/bin/plutil -extract version raw -o - "$PACKAGE_JSON" 2>/dev/null || true)"
  if [[ "$ACTUAL_VERSION" != "$EXPECTED_VERSION" ]]; then
    echo "QQ version mismatch: expected $EXPECTED_VERSION, got ${ACTUAL_VERSION:-unknown} ($QQ_APP)" >&2
    exit 3
  fi
fi

if qq_running_with_napcat; then
  echo "NapCat QQ is already running from $QQ_APP"
  exit 0
fi

if [[ -n "$(qq_processes)" ]]; then
  if [[ "$ENFORCE_APP" != "true" ]]; then
    echo "A different QQ process is running; quit it and restart Diana to load $QQ_APP" >&2
    exit 2
  fi
  echo "Stopping mismatched QQ process before starting $QQ_APP" >&2
  while IFS= read -r pid; do
    [[ -n "$pid" ]] && /bin/kill -TERM "$pid" 2>/dev/null || true
  done < <(qq_processes)
  for _ in {1..20}; do
    [[ -z "$(qq_processes)" ]] && break
    /bin/sleep 0.5
  done
  if [[ -n "$(qq_processes)" ]]; then
    echo "Mismatched QQ process did not exit" >&2
    exit 2
  fi
fi

launch_args=(--no-sandbox)
if [[ -n "${NAPCAT_QQ:-}" ]]; then
  launch_args+=(-q "$NAPCAT_QQ")
fi
/usr/bin/open -n "$QQ_APP" --args "${launch_args[@]}"

for _ in {1..30}; do
  if qq_running_with_napcat; then
    echo "NapCat QQ started"
    exit 0
  fi
  /bin/sleep 0.5
done

echo "QQ did not start with --no-sandbox" >&2
exit 1
