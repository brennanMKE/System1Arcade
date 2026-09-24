# Releasing System 1 Arcade

Each release is a universal (Apple silicon and Intel) app, signed with Developer ID, notarized,
packaged in a DMG and published on GitHub with the DMG attached. The scripts are adapted from
Curator's (`../Curator/scripts/`), for a Wails build instead of Xcode and without Sparkle.

## Once per Mac: credentials

| Needed | Where | Used for |
|---|---|---|
| Developer ID Application certificate and private key | login keychain | signing |
| App Store Connect API key `AuthKey_DWLP54ACTJ.p8` | `~/.appstoreconnect/`, mode 600 | notarizing |
| `gh` signed in | `gh auth login` | GitHub releases |

`scripts/preflight.sh --credentials-only` checks all three. To use an existing notarytool
profile instead of the key file, set `NOTARY_PROFILE`.

## Each release

1. **Version:** set `info.productVersion` in `wails.json` (X.Y.Z). It's the only place the
   version lives; Wails writes it into the app's `Info.plist`.
2. **Notes:** add a `## X.Y.Z` section to `CHANGELOG.md`. It becomes the GitHub release notes.
3. **Commit** both on `main`, then check with `scripts/preflight.sh` (read-only).
4. **Build:** `scripts/release.sh`. It runs preflight, builds a universal app with
   `scripts/build.sh --universal`, signs it with Developer ID and the hardened runtime, checks
   the version, notarizes, staples and runs `scripts/verify-dmg.sh`. The result is
   `dist/System1Arcade-X.Y.Z.dmg` plus `.sha256`.
5. **Tag:** `scripts/tag-release.sh --push` pushes `main` and `vX.Y.Z`.
6. **GitHub:** `scripts/publish-release.sh` creates the release with the DMG and its `.sha256`
   attached. Add `--draft` to review first.

Steps 4 to 6 reach Apple and GitHub.

## Checking a DMG

```sh
scripts/verify-dmg.sh dist/System1Arcade-X.Y.Z.dmg
```

It checks the stapled ticket, Gatekeeper on the DMG, and a copy flagged as downloaded. Then it
checks the app inside: signature, hardened runtime, Developer ID, secure timestamp, no debugging
entitlement, Gatekeeper, and the Applications link.

## Quick builds for your own Macs

`scripts/make-dmg.sh` makes a Developer ID–signed DMG without notarizing it. Copy it with `scp`
so macOS doesn't flag it as downloaded.

## Windows and Linux

Not released yet. The Windows app cross-compiles from a Mac (`wails build -platform
windows/amd64`), but it isn't code-signed, so SmartScreen would warn. Linux needs GTK and
WebKitGTK, so it has to be built on Linux, in CI or a Linux VM.
