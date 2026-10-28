#!/usr/bin/env bash
# Build a universal (arm64 + Intel) SuperLeo.app and wrap it in a drag-to-install
# .dmg. Output: build/bin/SuperLeo.app and build/bin/SuperLeo.dmg.
#
# Optional code signing + notarization (needs an Apple Developer account):
#   SIGN_ID="Developer ID Application: Your Name (TEAMID)" \
#   NOTARY_PROFILE=<keychain profile from `xcrun notarytool store-credentials`> \
#   ./scripts/package-macos.sh
# or, instead of the profile, AC_APPLE_ID, AC_TEAM_ID and AC_PASSWORD.
#
# The app is notarized and stapled before it goes into the .dmg, and the .dmg
# after: the app copied out to Applications then opens with no warning even
# offline, not only the image it came in.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/version.sh

echo "==> wails build (darwin/universal)"
wails build -clean -platform darwin/universal

APP="build/bin/SuperLeo.app"
DMG="build/bin/SuperLeo.dmg"

# notarize submits a file and waits for Apple's answer; false when no
# credentials are set.
notarize() {
  if [[ -n "${NOTARY_PROFILE:-}" ]]; then
    xcrun notarytool submit "$1" --keychain-profile "$NOTARY_PROFILE" --wait
  elif [[ -n "${AC_APPLE_ID:-}" && -n "${AC_TEAM_ID:-}" && -n "${AC_PASSWORD:-}" ]]; then
    xcrun notarytool submit "$1" --apple-id "$AC_APPLE_ID" --team-id "$AC_TEAM_ID" --password "$AC_PASSWORD" --wait
  else
    return 1
  fi
}
can_notarize() {
  [[ -n "${NOTARY_PROFILE:-}" || ( -n "${AC_APPLE_ID:-}" && -n "${AC_TEAM_ID:-}" && -n "${AC_PASSWORD:-}" ) ]]
}

if [[ -n "${SIGN_ID:-}" ]]; then
  echo "==> codesign (hardened runtime): $SIGN_ID"
  codesign --deep --force --options runtime --timestamp --sign "$SIGN_ID" "$APP"
  codesign --verify --strict --verbose=2 "$APP"
  if can_notarize; then
    echo "==> notarize + staple the app"
    ZIP="$(mktemp -d)/SuperLeo.zip"
    ditto -c -k --keepParent "$APP" "$ZIP"
    notarize "$ZIP"
    rm -f "$ZIP"
    xcrun stapler staple "$APP"
  fi
fi

echo "==> create .dmg"
STAGE="$(mktemp -d)"
cp -R "$APP" "$STAGE/"
ln -s /Applications "$STAGE/Applications"
rm -f "$DMG"
hdiutil create -volname "SuperLeo" -srcfolder "$STAGE" -ov -format UDZO "$DMG" >/dev/null
rm -rf "$STAGE"

if [[ -n "${SIGN_ID:-}" ]]; then
  codesign --force --timestamp --sign "$SIGN_ID" "$DMG"
  if can_notarize; then
    echo "==> notarize + staple the .dmg"
    notarize "$DMG"
    xcrun stapler staple "$DMG"
  fi
fi

echo "==> done: $DMG"
hdiutil verify "$DMG" | tail -1
lipo -archs "$APP/Contents/MacOS/SuperLeo"
