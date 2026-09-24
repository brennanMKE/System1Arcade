#!/usr/bin/env zsh
# Tag HEAD as v<version> (info.productVersion in wails.json). Adapted from Curator's.
#
# Usage:
#   scripts/tag-release.sh           # tag locally
#   scripts/tag-release.sh --push    # tag, then push main and the tag to origin

set -euo pipefail

REPO_ROOT="${0:A:h:h}"
VERSION="$(plutil -extract info.productVersion raw -o - "$REPO_ROOT/wails.json")"
[[ -n "$VERSION" ]] || { print -u2 "error: no info.productVersion in wails.json"; exit 1; }
TAG="v$VERSION"

cd "$REPO_ROOT"
[[ -z "$(git status --porcelain)" ]] || { print -u2 "error: working tree isn't clean"; exit 1; }
[[ "$(git branch --show-current)" == "main" ]] || { print -u2 "error: tag releases from main"; exit 1; }
[[ -f "dist/System1Arcade-$VERSION.dmg" ]] || { print -u2 "error: dist/System1Arcade-$VERSION.dmg not found; run scripts/release.sh first"; exit 1; }

if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
    [[ "$(git rev-list -n1 "$TAG")" == "$(git rev-parse HEAD)" ]] \
        || { print -u2 "error: $TAG already exists on another commit"; exit 1; }
    print "==> $TAG already points at HEAD"
else
    git tag -a "$TAG" -m "System 1 Arcade $VERSION"
    print "==> Tagged $(git rev-parse --short HEAD) as $TAG"
fi

if [[ "${1:-}" == "--push" ]]; then
    print "==> Pushing main and $TAG to origin"
    git push origin main "$TAG"
else
    print "Not pushed. To publish: git push origin main $TAG"
fi
