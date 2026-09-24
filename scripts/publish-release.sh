#!/usr/bin/env zsh
# Create the GitHub release for v<version>, attaching the notarized DMG and its SHA-256.
# Release notes come from that version's section in CHANGELOG.md. Adapted from Curator's.
#
# Usage: scripts/publish-release.sh [--draft]
#
# Run after scripts/release.sh and scripts/tag-release.sh --push. The release is public
# unless --draft, so a person runs this.

set -euo pipefail

REPO_ROOT="${0:A:h:h}"
cd "$REPO_ROOT"
VERSION="$(plutil -extract info.productVersion raw -o - wails.json)"
TAG="v$VERSION"
DMG="dist/System1Arcade-$VERSION.dmg"

fail() { print -u2 -r -- "error: $*"; exit 1; }

[[ -f "$DMG" && -f "$DMG.sha256" ]] || fail "$DMG (and .sha256) not found; run scripts/release.sh"
(cd dist && shasum -a 256 -c "${DMG:t}.sha256" >/dev/null) || fail "$DMG doesn't match its .sha256"
git ls-remote --exit-code --tags origin "refs/tags/$TAG" >/dev/null || fail "$TAG isn't on origin; run scripts/tag-release.sh --push"
gh release view "$TAG" >/dev/null 2>&1 && fail "release $TAG already exists"

print "==> Re-verifying the DMG"
scripts/verify-dmg.sh "$DMG" >/dev/null || fail "$DMG no longer verifies; run scripts/verify-dmg.sh $DMG"

NOTES="$(mktemp)"
trap 'rm -f "$NOTES"' EXIT
awk -v v="$VERSION" '
    $0 == "## " v || index($0, "## " v " ") == 1 { on = 1; next }
    on && /^## / { exit }
    on { print }
' CHANGELOG.md > "$NOTES"
[[ -n "$(tr -d '[:space:]' < "$NOTES")" ]] || fail "no notes under '## $VERSION' in CHANGELOG.md"
cat >> "$NOTES" <<NOTES_END

---

**Install:** download \`System1Arcade-$VERSION.dmg\`, open it and drag System 1 Arcade to
Applications. It's a universal app (Apple silicon and Intel), signed with Developer ID and
notarized by Apple. Requires macOS 11 or later.

**Built-in agent:** needs Python 3.10 or newer. On first start it creates its own Python
environment and downloads Laya and PyTorch (about 1 GB) while the game waits.
NOTES_END

args=(--title "System 1 Arcade $VERSION" --notes-file "$NOTES" --verify-tag)
[[ "${1:-}" == "--draft" ]] && args+=(--draft)
print "==> Creating GitHub release $TAG"
gh release create "$TAG" "$DMG" "$DMG.sha256" "${args[@]}"
