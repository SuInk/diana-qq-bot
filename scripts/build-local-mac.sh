#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT="${1:-$ROOT/dist/diana-qq-bot-webui}"
IDENTIFIER="${DIANA_MACOS_CODE_IDENTIFIER:-com.suink.diana-qq-bot}"
GO_BIN="${GO:-go}"
BUILD_SOURCE_ROOT="${DIANA_BUILD_SOURCE_ROOT:-$ROOT}"
BUILD_COMMIT="${DIANA_UPDATE_TARGET_COMMIT:-$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || true)}"
BUILD_LDFLAGS="-X 'main.buildSourceRoot=$BUILD_SOURCE_ROOT'"
if [[ -n "$BUILD_COMMIT" ]]; then
	BUILD_LDFLAGS+=" -X 'main.buildCommit=$BUILD_COMMIT'"
fi

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "build-local-mac.sh must run on macOS" >&2
  exit 1
fi

cd "$ROOT"

if [[ "$OUTPUT" == *.app ]]; then
  APP_PARENT="$(dirname "$OUTPUT")"
  APP_NAME="$(basename "$OUTPUT")"
  mkdir -p "$APP_PARENT"
	TEMP_APP="$APP_PARENT/.$APP_NAME.new.$$"
	APP_BINARY="$TEMP_APP/Contents/MacOS/diana-qq-bot-webui"
	PDF_HELPER="$TEMP_APP/Contents/MacOS/diana-pdf-vision"
	mkdir -p "$TEMP_APP/Contents/MacOS"
  trap 'rm -rf "$TEMP_APP"' EXIT

	cp "$ROOT/packaging/macos/Info.plist" "$TEMP_APP/Contents/Info.plist"
	"$GO_BIN" build -trimpath -ldflags "$BUILD_LDFLAGS" -o "$APP_BINARY" ./cmd/webui
	MACOS_ARCH="$(uname -m)"
	xcrun swiftc \
		-O \
		-target "${MACOS_ARCH}-apple-macos12.0" \
		-framework AppKit \
		-framework PDFKit \
		-framework Vision \
		"$ROOT/native/macos/diana_pdf_vision.swift" \
		-o "$PDF_HELPER"
	codesign \
		--force \
		--sign - \
		--identifier "$IDENTIFIER.pdf-vision" \
		"$PDF_HELPER"
	codesign \
    --force \
    --deep \
    --sign - \
    --identifier "$IDENTIFIER" \
    --requirements "=designated => identifier \"$IDENTIFIER\"" \
    "$TEMP_APP"
  codesign --verify --deep --strict "$TEMP_APP"

  rm -rf "$OUTPUT"
  mv "$TEMP_APP" "$OUTPUT"
  trap - EXIT
  echo "Built and signed $OUTPUT ($IDENTIFIER)"
  exit 0
fi

mkdir -p "$(dirname "$OUTPUT")"
TEMP_OUTPUT="$(dirname "$OUTPUT")/.$(basename "$OUTPUT").new.$$"
trap 'rm -f "$TEMP_OUTPUT"' EXIT

"$GO_BIN" build -trimpath -ldflags "$BUILD_LDFLAGS" -o "$TEMP_OUTPUT" ./cmd/webui

# A stable designated requirement lets macOS keep Files & Folders/App Data
# permission across local rebuilds without requiring an Apple developer cert.
codesign \
  --force \
  --sign - \
  --identifier "$IDENTIFIER" \
  --requirements "=designated => identifier \"$IDENTIFIER\"" \
  "$TEMP_OUTPUT"
codesign --verify --strict "$TEMP_OUTPUT"

mv -f "$TEMP_OUTPUT" "$OUTPUT"
trap - EXIT

echo "Built and signed $OUTPUT ($IDENTIFIER)"
