#!/usr/bin/env zsh
# Build a universal System 1 Arcade.app signed with Developer ID and package it in a
# drag-to-Applications DMG. Adapted from Curator's scripts/make-dmg.sh.
#
#   scripts/make-dmg.sh   ->  dist/System1Arcade-<version>-<sha>.dmg
#
# NOT notarized: scripts/release.sh builds with this script, then notarizes, staples and
# verifies. A DMG that isn't notarized is fine on your own Macs if you copy it with scp or a
# file share, which don't add the quarantine flag.

set -euo pipefail

# --- CONFIG ------------------------------------------------------------------
APP_NAME="System 1 Arcade"          # the .app and the DMG's volume name
FILE_NAME="System1Arcade"           # the DMG's file name, without spaces
SIGN_IDENTITY="Developer ID Application: Brennan Stehling (XV8BAAVZ6V)"
# -----------------------------------------------------------------------------

REPO_ROOT="${0:A:h:h}"
DIST_DIR="$REPO_ROOT/dist"
STAGING="$REPO_ROOT/build/dmg"
APP_PATH="$REPO_ROOT/build/bin/$APP_NAME.app"

if ! security find-identity -p codesigning -v | grep -qF "$SIGN_IDENTITY"; then
    print -u2 "error: signing identity not found: $SIGN_IDENTITY"
    exit 1
fi

VERSION="$(plutil -extract info.productVersion raw -o - "$REPO_ROOT/wails.json")"
GIT_SHA="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || print unknown)"

print "==> Building $APP_NAME $VERSION ($GIT_SHA) for Apple silicon and Intel"
"$REPO_ROOT/scripts/build.sh" --clean --universal
[[ -d "$APP_PATH" ]] || { print -u2 "error: built app not found at $APP_PATH"; exit 1; }

print "==> Signing with Developer ID (hardened runtime, secure timestamp)"
codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" "$APP_PATH"
codesign --verify --deep --strict "$APP_PATH"
codesign -dvv "$APP_PATH" 2>&1 | grep -E "^(Authority|TeamIdentifier|Timestamp|CodeDirectory)" | sed 's/^/    /'
lipo -archs "$APP_PATH/Contents/MacOS/"* | sed 's/^/    architectures: /'

# Name the DMG after its volume while building; macOS can rename a DMG whose file name and
# volume name differ. Rename once at the end.
WORK_DMG="$DIST_DIR/$APP_NAME.dmg"
DMG_PATH="$DIST_DIR/$FILE_NAME-$VERSION-$GIT_SHA.dmg"

print "==> Creating DMG"
rm -rf "$STAGING"
mkdir -p "$STAGING" "$DIST_DIR"
ditto "$APP_PATH" "$STAGING/$APP_NAME.app"
ln -s /Applications "$STAGING/Applications"
rm -f "$WORK_DMG" "$DMG_PATH"
hdiutil create -volname "$APP_NAME" -srcfolder "$STAGING" -fs HFS+ -format UDZO -ov "$WORK_DMG" -quiet

print "==> Signing DMG"
codesign --force --sign "$SIGN_IDENTITY" --timestamp "$WORK_DMG"
codesign --verify --strict "$WORK_DMG"

mv "$WORK_DMG" "$DMG_PATH"
rm -rf "$STAGING"

print "==> Done: $DMG_PATH"
print "    Version $VERSION. Signed with Developer ID, not notarized."
