#!/usr/bin/env zsh
# Read-only checks that this Mac and this commit are ready to release System 1 Arcade.
# Adapted from Curator's scripts/preflight.sh. Changes nothing; the notary check makes one
# read-only request to Apple.
#
# Usage:
#   scripts/preflight.sh                     # everything
#   scripts/preflight.sh --credentials-only  # can this Mac sign, notarize and publish?
#
# Exit code is 0 only if nothing FAILs. WARNs don't block a release.

set -uo pipefail

REPO_ROOT="${0:A:h:h}"
SIGN_IDENTITY="Developer ID Application: Brennan Stehling (XV8BAAVZ6V)"
ASC_KEY_ID="${ASC_KEY_ID:-DWLP54ACTJ}"
ASC_ISSUER="${ASC_ISSUER:-69a6de6e-9f19-47e3-e053-5b8c7c11a4d1}"
ASC_KEY_PATH="${ASC_KEY_PATH:-$HOME/.appstoreconnect/AuthKey_$ASC_KEY_ID.p8}"

CREDENTIALS_ONLY=0
[[ "${1:-}" == "--credentials-only" ]] && CREDENTIALS_ONLY=1

FAILS=0
WARNS=0
section() { print "\n== $*"; }
pass()    { print "  PASS  $*"; }
warn()    { print "  WARN  $*"; WARNS=$((WARNS + 1)); }
fail()    { print "  FAIL  $*"; FAILS=$((FAILS + 1)); }

# --- Tools -------------------------------------------------------------------

section "Tools"
for tool in go npm codesign hdiutil ditto lipo spctl git gh openssl plutil; do
    command -v "$tool" >/dev/null && pass "$tool" || fail "$tool not found"
done
for tool in notarytool stapler; do
    xcrun --find "$tool" >/dev/null 2>&1 && pass "$tool" || fail "xcrun can't find $tool"
done

# --- Signing -----------------------------------------------------------------

section "Signing"
if security find-identity -v -p codesigning | grep -qF "$SIGN_IDENTITY"; then
    pass "identity with private key: $SIGN_IDENTITY"
    end_date="$(security find-certificate -c "$SIGN_IDENTITY" -p | openssl x509 -noout -enddate | cut -d= -f2)"
    end_epoch="$(date -j -f "%b %e %T %Y %Z" "$end_date" +%s 2>/dev/null || print 0)"
    days=$(( (end_epoch - $(date +%s)) / 86400 ))
    if (( end_epoch == 0 )); then
        warn "couldn't read the certificate's expiry ($end_date)"
    elif (( days < 0 )); then
        fail "Developer ID certificate expired on $end_date"
    elif (( days < 30 )); then
        warn "Developer ID certificate expires in $days days ($end_date)"
    else
        pass "certificate valid until $end_date ($days days)"
    fi
else
    fail "no usable '$SIGN_IDENTITY' (certificate plus private key) in the keychain"
fi

# --- Notarization --------------------------------------------------------------

section "Notarization"
if [[ -n "${NOTARY_PROFILE:-}" ]]; then
    if xcrun notarytool history --keychain-profile "$NOTARY_PROFILE" >/dev/null 2>&1; then
        pass "notarytool profile '$NOTARY_PROFILE' works"
    else
        fail "notarytool profile '$NOTARY_PROFILE' is missing or invalid"
    fi
elif [[ -f "$ASC_KEY_PATH" ]]; then
    pass "App Store Connect key at $ASC_KEY_PATH"
    perms="$(stat -f %Lp "$ASC_KEY_PATH")"
    [[ "$perms" == "600" || "$perms" == "400" ]] && pass "key file readable only by you ($perms)" \
        || warn "key file mode is $perms; chmod 600 it"
    if xcrun notarytool history --key "$ASC_KEY_PATH" --key-id "$ASC_KEY_ID" --issuer "$ASC_ISSUER" >/dev/null 2>&1; then
        pass "Apple's notary service accepts the key"
    else
        fail "notarytool couldn't authenticate with the key (or Apple is unreachable)"
    fi
else
    fail "no App Store Connect key at $ASC_KEY_PATH (see docs/releasing.md)"
fi

# --- GitHub ------------------------------------------------------------------

section "GitHub"
if gh auth status >/dev/null 2>&1; then
    pass "gh is signed in"
    repo="$(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null)"
    [[ -n "$repo" ]] && pass "repository $repo" || fail "gh can't see this repository"
else
    fail "gh isn't signed in (gh auth login)"
fi

if (( CREDENTIALS_ONLY )); then
    print ""
    (( FAILS == 0 )) && print "Credentials OK ($WARNS warnings)." || print "$FAILS credential check(s) failed."
    exit $(( FAILS > 0 ))
fi

# --- Version -----------------------------------------------------------------

section "Version"
VERSION="$(plutil -extract info.productVersion raw -o - "$REPO_ROOT/wails.json" 2>/dev/null)"
if [[ "$VERSION" =~ '^[0-9]+\.[0-9]+\.[0-9]+$' ]]; then
    pass "version $VERSION (info.productVersion in wails.json)"
else
    fail "info.productVersion '$VERSION' in wails.json isn't X.Y.Z"
fi

TAG="v$VERSION"
if git -C "$REPO_ROOT" rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
    if [[ "$(git -C "$REPO_ROOT" rev-list -n1 "$TAG")" == "$(git -C "$REPO_ROOT" rev-parse HEAD)" ]]; then
        pass "tag $TAG already points at HEAD"
    else
        fail "tag $TAG exists on another commit; bump the version"
    fi
else
    pass "tag $TAG is free"
fi

if gh release view "$TAG" >/dev/null 2>&1; then
    fail "GitHub release $TAG already exists"
else
    pass "no GitHub release $TAG yet"
fi
latest="$(gh release list --limit 1 --json tagName -q '.[0].tagName' 2>/dev/null | sed 's/^v//')"
if [[ -z "$latest" ]]; then
    pass "first release"
elif [[ "$(printf '%s\n%s\n' "$latest" "$VERSION" | sort -V | tail -1)" == "$VERSION" && "$latest" != "$VERSION" ]]; then
    pass "$VERSION is newer than the latest release ($latest)"
else
    fail "$VERSION isn't newer than the latest release ($latest)"
fi

if awk -v v="$VERSION" '$0 == "## " v || index($0, "## " v " ") == 1 { found = 1 } END { exit !found }' "$REPO_ROOT/CHANGELOG.md" 2>/dev/null; then
    pass "CHANGELOG.md has a section for $VERSION"
else
    fail "CHANGELOG.md has no '## $VERSION' section (the release notes come from it)"
fi

# --- Git ---------------------------------------------------------------------

section "Git"
if [[ -z "$(git -C "$REPO_ROOT" status --porcelain)" ]]; then
    pass "working tree clean"
else
    fail "uncommitted changes; release from a clean commit"
fi
branch="$(git -C "$REPO_ROOT" branch --show-current)"
[[ "$branch" == "main" ]] && pass "on main" || fail "on '$branch', not main"
if git -C "$REPO_ROOT" fetch -q origin 2>/dev/null; then
    if git -C "$REPO_ROOT" merge-base --is-ancestor origin/main HEAD; then
        ahead="$(git -C "$REPO_ROOT" rev-list --count origin/main..HEAD)"
        pass "up to date with origin/main ($ahead local commit(s) to push)"
    else
        fail "origin/main has commits this branch doesn't; pull first"
    fi
else
    warn "couldn't fetch origin"
fi

print ""
if (( FAILS == 0 )); then
    print "Ready to release $VERSION ($WARNS warnings)."
else
    print "$FAILS check(s) failed; not ready to release."
fi
exit $(( FAILS > 0 ))
