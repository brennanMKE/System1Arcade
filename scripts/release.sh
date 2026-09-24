#!/usr/bin/env zsh
# Build a notarized, stapled, verified System 1 Arcade DMG for a GitHub release.
# Adapted from Curator's scripts/release.sh.
#
#   scripts/release.sh     ->  dist/System1Arcade-<version>.dmg and .sha256
#
# Steps: preflight -> build and sign with Developer ID (make-dmg.sh) -> check the built
# version -> notarize -> staple -> verify-dmg.sh -> rename. Tagging and publishing are
# separate steps; see docs/releasing.md.
#
# Notarizing uploads the DMG to Apple, so a person starts this script.
#
# Notary credentials: the App Store Connect API key file, passed straight to notarytool.
# Override with ASC_KEY_PATH / ASC_KEY_ID / ASC_ISSUER, or set NOTARY_PROFILE to use an
# existing notarytool keychain profile instead.

set -euo pipefail

APP_NAME="System 1 Arcade"
FILE_NAME="System1Arcade"
REPO_ROOT="${0:A:h:h}"
DIST_DIR="$REPO_ROOT/dist"
ASC_KEY_ID="${ASC_KEY_ID:-DWLP54ACTJ}"
ASC_ISSUER="${ASC_ISSUER:-69a6de6e-9f19-47e3-e053-5b8c7c11a4d1}"
ASC_KEY_PATH="${ASC_KEY_PATH:-$HOME/.appstoreconnect/AuthKey_$ASC_KEY_ID.p8}"

log()  { print -r -- "==> $*"; }
fail() { print -u2 -r -- "error: $*"; exit 1; }

log "Preflight"
"$REPO_ROOT/scripts/preflight.sh" || fail "preflight failed; fix the FAILs above"

VERSION="$(plutil -extract info.productVersion raw -o - "$REPO_ROOT/wails.json")"
log "Releasing $APP_NAME $VERSION from $(git -C "$REPO_ROOT" rev-parse --short HEAD)"

# --- Build and sign ----------------------------------------------------------

"$REPO_ROOT/scripts/make-dmg.sh"
BUILT="$(ls -t "$DIST_DIR"/$FILE_NAME-$VERSION-*.dmg | head -1)"

# Notarize under a file name equal to the volume name: macOS can rename a DMG whose file name
# and volume name differ during the notary round trip. Rename only at the end.
WORK_DMG="$DIST_DIR/$APP_NAME.dmg"
FINAL_DMG="$DIST_DIR/$FILE_NAME-$VERSION.dmg"
rm -f "$WORK_DMG" "$FINAL_DMG" "$FINAL_DMG.sha256"
mv "$BUILT" "$WORK_DMG"

# --- The built app must carry exactly this version ---------------------------

MOUNT="$(hdiutil attach -nobrowse -readonly "$WORK_DMG" | awk -F'\t' '/\/Volumes\// { print $NF }')"
plist="$MOUNT/$APP_NAME.app/Contents/Info.plist"
built_version="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$plist")"
hdiutil detach "$MOUNT" -quiet
[[ "$built_version" == "$VERSION" ]] || fail "built app says version $built_version, expected $VERSION"
log "Built app is $built_version"

# --- Notarize and staple -----------------------------------------------------

log "Submitting to Apple's notary service (usually a few minutes)"
if [[ -n "${NOTARY_PROFILE:-}" ]]; then
    xcrun notarytool submit "$WORK_DMG" --keychain-profile "$NOTARY_PROFILE" --wait --timeout 30m
else
    xcrun notarytool submit "$WORK_DMG" --key "$ASC_KEY_PATH" --key-id "$ASC_KEY_ID" --issuer "$ASC_ISSUER" \
        --wait --timeout 30m
fi

log "Stapling the ticket"
xcrun stapler staple "$WORK_DMG"

log "Verifying"
"$REPO_ROOT/scripts/verify-dmg.sh" "$WORK_DMG" || fail "verification failed; don't publish this DMG"

mv "$WORK_DMG" "$FINAL_DMG"
(cd "$DIST_DIR" && shasum -a 256 "${FINAL_DMG:t}" > "${FINAL_DMG:t}.sha256")
log "Done: $FINAL_DMG"
cat "$FINAL_DMG.sha256"
print "Next: scripts/tag-release.sh --push, then scripts/publish-release.sh."
