#!/usr/bin/env bash
# Build a universal (arm64 + Intel) SuperAI.app and wrap it in a drag-to-install
# .dmg. Output: build/bin/SuperAI.app and build/bin/SuperAI.dmg.
#
# Optional code signing + notarization (needs an Apple Developer account):
#   SIGN_ID="Developer ID Application: Your Name (TEAMID)" \
#   AC_APPLE_ID="you@example.com" AC_TEAM_ID="TEAMID" AC_PASSWORD="app-specific-pw" \
#   ./scripts/package-macos.sh
set -euo pipefail
cd "$(dirname "$0")/.."

echo "==> wails build (darwin/universal)"
wails build -clean -platform darwin/universal

APP="build/bin/SuperAI.app"
DMG="build/bin/SuperAI.dmg"

if [[ -n "${SIGN_ID:-}" ]]; then
  echo "==> codesign (hardened runtime): $SIGN_ID"
  codesign --deep --force --options runtime --timestamp --sign "$SIGN_ID" "$APP"
  codesign --verify --strict --verbose=2 "$APP"
fi

echo "==> create .dmg"
STAGE="$(mktemp -d)"
cp -R "$APP" "$STAGE/"
ln -s /Applications "$STAGE/Applications"
rm -f "$DMG"
hdiutil create -volname "SuperAI" -srcfolder "$STAGE" -ov -format UDZO "$DMG" >/dev/null
rm -rf "$STAGE"

if [[ -n "${SIGN_ID:-}" ]]; then
  codesign --force --sign "$SIGN_ID" "$DMG"
fi

if [[ -n "${NOTARY_PROFILE:-}" ]]; then
  echo "==> notarize + staple (keychain profile: $NOTARY_PROFILE)"
  xcrun notarytool submit "$DMG" --keychain-profile "$NOTARY_PROFILE" --wait
  xcrun stapler staple "$DMG"
  xcrun stapler staple "$APP"
elif [[ -n "${AC_APPLE_ID:-}" && -n "${AC_TEAM_ID:-}" && -n "${AC_PASSWORD:-}" ]]; then
  echo "==> notarize + staple (apple-id)"
  xcrun notarytool submit "$DMG" --apple-id "$AC_APPLE_ID" --team-id "$AC_TEAM_ID" --password "$AC_PASSWORD" --wait
  xcrun stapler staple "$DMG"
  xcrun stapler staple "$APP"
fi

echo "==> done: $DMG"
hdiutil verify "$DMG" | tail -1
lipo -archs "$APP/Contents/MacOS/SuperAI"
