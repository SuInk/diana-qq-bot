#!/usr/bin/env bash
set -euo pipefail

ROOT="${DIANA_UPDATE_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
TARGET_COMMIT="${DIANA_UPDATE_TARGET_COMMIT:-$(git -C "$ROOT" rev-parse HEAD)}"
RUNNING_EXECUTABLE="${DIANA_RUNNING_EXECUTABLE:-}"
FRONTEND_TARGET="${FRONTEND_DIST:-$ROOT/frontend/dist}"
GO_BIN="${GO:-go}"
NPM_BIN="${NPM:-npm}"

if [[ ! -d "$ROOT/.git" ]]; then
	echo "Update root is not a Git checkout: $ROOT" >&2
	exit 1
fi
if [[ ! -f "$ROOT/frontend/package-lock.json" ]]; then
	echo "Frontend lockfile is missing from update root." >&2
	exit 1
fi

TARGET_APP=""
TARGET_EXECUTABLE="${DIANA_APP_EXECUTABLE:-$RUNNING_EXECUTABLE}"
if [[ "$TARGET_EXECUTABLE" == */Contents/MacOS/diana-qq-bot-webui ]]; then
	TARGET_APP="${TARGET_EXECUTABLE%/Contents/MacOS/diana-qq-bot-webui}"
fi
if [[ -z "$TARGET_EXECUTABLE" ]]; then
	TARGET_EXECUTABLE="$ROOT/dist/diana-qq-bot-webui"
fi

FRONTEND_PARENT="$(dirname "$FRONTEND_TARGET")"
mkdir -p "$FRONTEND_PARENT"
STAGED_FRONTEND="$(mktemp -d "$FRONTEND_PARENT/.diana-frontend.new.XXXXXX")"

STAGED_APP=""
STAGED_EXECUTABLE=""
cleanup() {
	[[ -z "$STAGED_FRONTEND" || ! -e "$STAGED_FRONTEND" ]] || rm -rf "$STAGED_FRONTEND"
	[[ -z "$STAGED_APP" || ! -e "$STAGED_APP" ]] || rm -rf "$STAGED_APP"
	[[ -z "$STAGED_EXECUTABLE" || ! -e "$STAGED_EXECUTABLE" ]] || rm -f "$STAGED_EXECUTABLE"
}
trap cleanup EXIT

echo "Installing frontend dependencies..."
(
	cd "$ROOT/frontend"
	"$NPM_BIN" ci
	./node_modules/.bin/vue-tsc --noEmit
	./node_modules/.bin/vite build --configLoader runner --outDir "$STAGED_FRONTEND" --emptyOutDir
)

echo "Building Diana QQ Bot at $TARGET_COMMIT..."
if [[ -n "$TARGET_APP" ]]; then
	APP_PARENT="$(dirname "$TARGET_APP")"
	APP_NAME="$(basename "$TARGET_APP")"
	mkdir -p "$APP_PARENT"
	STAGED_APP="$APP_PARENT/.${APP_NAME%.app}.update.$$.app"
	DIANA_UPDATE_TARGET_COMMIT="$TARGET_COMMIT" \
		DIANA_BUILD_SOURCE_ROOT="$ROOT" \
		GO="$GO_BIN" \
		"$ROOT/scripts/build-local-mac.sh" "$STAGED_APP"
else
	EXECUTABLE_PARENT="$(dirname "$TARGET_EXECUTABLE")"
	mkdir -p "$EXECUTABLE_PARENT"
	STAGED_EXECUTABLE="$EXECUTABLE_PARENT/.$(basename "$TARGET_EXECUTABLE").update.$$"
	if [[ "$(uname -s)" == "Darwin" ]]; then
		DIANA_UPDATE_TARGET_COMMIT="$TARGET_COMMIT" \
			DIANA_BUILD_SOURCE_ROOT="$ROOT" \
			GO="$GO_BIN" \
			"$ROOT/scripts/build-local-mac.sh" "$STAGED_EXECUTABLE"
	else
		LDFLAGS="-X 'main.buildCommit=$TARGET_COMMIT' -X 'main.buildSourceRoot=$ROOT'"
		(
			cd "$ROOT"
			"$GO_BIN" build -trimpath -ldflags "$LDFLAGS" -o "$STAGED_EXECUTABLE" ./cmd/webui
		)
	fi
fi

APP_BACKUP=""
EXECUTABLE_BACKUP=""
FRONTEND_BACKUP="$FRONTEND_TARGET.backup"
app_swapped=false
executable_swapped=false
frontend_swapped=false

rollback() {
	set +e
	if [[ "$frontend_swapped" == "true" ]]; then
		rm -rf "$FRONTEND_TARGET"
		[[ ! -e "$FRONTEND_BACKUP" ]] || mv "$FRONTEND_BACKUP" "$FRONTEND_TARGET"
	fi
	if [[ "$app_swapped" == "true" ]]; then
		rm -rf "$TARGET_APP"
		[[ -z "$APP_BACKUP" || ! -e "$APP_BACKUP" ]] || mv "$APP_BACKUP" "$TARGET_APP"
	fi
	if [[ "$executable_swapped" == "true" ]]; then
		rm -f "$TARGET_EXECUTABLE"
		[[ -z "$EXECUTABLE_BACKUP" || ! -e "$EXECUTABLE_BACKUP" ]] || mv "$EXECUTABLE_BACKUP" "$TARGET_EXECUTABLE"
	fi
	app_swapped=false
	executable_swapped=false
	frontend_swapped=false
}

trap 'rollback' ERR

if [[ -n "$TARGET_APP" ]]; then
	APP_BACKUP="$TARGET_APP.backup"
	rm -rf "$APP_BACKUP"
	if [[ -e "$TARGET_APP" ]]; then
		mv "$TARGET_APP" "$APP_BACKUP"
	fi
	if ! mv "$STAGED_APP" "$TARGET_APP"; then
		[[ ! -e "$APP_BACKUP" ]] || mv "$APP_BACKUP" "$TARGET_APP"
		echo "Failed to replace the macOS app bundle." >&2
		exit 1
	fi
	STAGED_APP=""
	app_swapped=true
else
	EXECUTABLE_BACKUP="$TARGET_EXECUTABLE.backup"
	rm -f "$EXECUTABLE_BACKUP"
	if [[ -e "$TARGET_EXECUTABLE" ]]; then
		mv "$TARGET_EXECUTABLE" "$EXECUTABLE_BACKUP"
	fi
	if ! mv "$STAGED_EXECUTABLE" "$TARGET_EXECUTABLE"; then
		[[ ! -e "$EXECUTABLE_BACKUP" ]] || mv "$EXECUTABLE_BACKUP" "$TARGET_EXECUTABLE"
		echo "Failed to replace the server executable." >&2
		exit 1
	fi
	STAGED_EXECUTABLE=""
	executable_swapped=true
fi

rm -rf "$FRONTEND_BACKUP"
if [[ -e "$FRONTEND_TARGET" ]]; then
	mv "$FRONTEND_TARGET" "$FRONTEND_BACKUP"
fi
frontend_swapped=true
if ! mv "$STAGED_FRONTEND" "$FRONTEND_TARGET"; then
	rollback
	echo "Failed to replace the frontend bundle; the application was restored." >&2
	exit 1
fi
STAGED_FRONTEND=""

trap - EXIT
trap - ERR
echo "Update applied at commit $TARGET_COMMIT. Restart Diana QQ Bot to run the new version."
