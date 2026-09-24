#!/usr/bin/env zsh
# Verify a DMG is ready to hand out: signed, notarized, stapled and accepted by Gatekeeper,
# including a simulation of the quarantine flag a browser download adds.
# Adapted from Curator's scripts/verify-dmg.sh.
#
# Usage: scripts/verify-dmg.sh <path/to/System1Arcade.dmg>
#
# Exit code is 0 only if every check passes.

set -uo pipefail

if [[ $# -ne 1 || ! -f "$1" ]]; then
    print -u2 "usage: $0 <path/to/file.dmg>"
    exit 2
fi
DMG="$1"

FAILS=0
MOUNT_POINT=""
TEST_DIR=""
cleanup() {
    [[ -n "$MOUNT_POINT" && -d "$MOUNT_POINT" ]] && hdiutil detach "$MOUNT_POINT" -quiet 2>/dev/null
    [[ -n "$TEST_DIR" ]] && rm -rf "$TEST_DIR"
}
trap cleanup EXIT INT TERM

step() { print "\n==> $*"; }
pass() { print "    PASS: $*"; }
fail() { print "    FAIL: $*"; FAILS=$((FAILS + 1)); }

step "Stapled notarization ticket"
if xcrun stapler validate "$DMG" >/dev/null 2>&1; then
    pass "ticket present and valid"
else
    fail "no stapled ticket; Macs without internet will warn"
fi

step "Gatekeeper (DMG)"
out=$(spctl --assess --type open --context context:primary-signature -vv "$DMG" 2>&1)
print -r -- "$out" | sed 's/^/    /'
if print -r -- "$out" | grep -q "source=Notarized Developer ID"; then
    pass "notarized and signed"
elif print -r -- "$out" | grep -q "source=Developer ID"; then
    fail "signed but NOT notarized"
else
    fail "not accepted by Gatekeeper"
fi

step "DMG signature"
codesign --verify --verbose=2 "$DMG" >/dev/null 2>&1 && pass "valid" || fail "invalid or missing"

step "Quarantine simulation (as if downloaded in Safari)"
TEST_DIR="$(mktemp -d)"
cp "$DMG" "$TEST_DIR/"
copy="$TEST_DIR/${DMG:t}"
xattr -w com.apple.quarantine "0083;$(printf '%x' "$(date +%s)");Safari;|com.apple.Safari" "$copy"
if spctl --assess --type open --context context:primary-signature -vv "$copy" 2>&1 | grep -q "source=Notarized Developer ID"; then
    pass "quarantined copy accepted: no warning when opened"
else
    fail "quarantined copy rejected: people WILL see a Gatekeeper warning"
fi

step "The app inside"
attach=$(hdiutil attach -nobrowse -noautoopen -readonly "$DMG" 2>&1)
MOUNT_POINT=$(print -r -- "$attach" | awk -F'\t' '/\/Volumes\// { print $NF }' | tail -1)
if [[ -z "$MOUNT_POINT" || ! -d "$MOUNT_POINT" ]]; then
    fail "couldn't mount the DMG"
else
    APP=$(/bin/ls -d "$MOUNT_POINT"/*.app 2>/dev/null | head -1)
    if [[ -z "$APP" ]]; then
        fail "no .app in the DMG"
    else
        codesign --verify --deep --strict "$APP" >/dev/null 2>&1 && pass "signature valid (deep, strict)" || fail "signature invalid"
        cs=$(codesign -dvv --entitlements - "$APP" 2>&1)
        print -r -- "$cs" | grep -q "flags=.*runtime" && pass "hardened runtime" || fail "hardened runtime missing"
        print -r -- "$cs" | grep -q "Authority=Developer ID Application" && pass "signed with Developer ID" || fail "not signed with Developer ID"
        print -r -- "$cs" | grep -q "^Timestamp=" && pass "secure timestamp" || fail "no secure timestamp"
        print -r -- "$cs" | grep -q "get-task-allow" && fail "has the debugging entitlement (get-task-allow)" || pass "no debugging entitlement"
        if spctl --assess --type execute -vv "$APP" 2>&1 | grep -q "source=Notarized Developer ID"; then
            pass "Gatekeeper accepts the app"
        else
            fail "Gatekeeper doesn't accept the app"
        fi
        plist="$APP/Contents/Info.plist"
        print "    Version: $(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$plist") (build $(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$plist"))"
        [[ -L "$MOUNT_POINT/Applications" ]] && pass "Applications link for drag-to-install" || fail "no Applications link"
    fi
fi

print ""
if (( FAILS == 0 )); then
    print "All checks passed: $DMG"
else
    print "$FAILS check(s) failed: $DMG"
fi
exit $(( FAILS > 0 ))
