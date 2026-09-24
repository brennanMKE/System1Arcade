#!/usr/bin/env bash
# Capture a screenshot of the running System 1 Arcade window.
# Usage: scripts/screenshot.sh [output.png]
# Prints the path of the saved PNG on success.
set -euo pipefail

BUNDLE_ID="${BUNDLE_ID:-co.sstools.System1Arcade}"
APP_NAME="${APP_NAME:-^System ?1}"  # regex: "System 1 Arcade" (build) or "System1" (wails dev)
# Seconds to wait after raising the window so WebKit can repaint.
SETTLE="${SETTLE:-1}"
# Set ACTIVATE=0 to capture without raising the window (may come out blank).
ACTIVATE="${ACTIVATE:-1}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:-$ROOT/screenshots/$(date +%Y%m%d-%H%M%S).png}"

for tool in windows jq screencapture; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "error: '$tool' not found in PATH (see docs/screenshots.md)" >&2
    exit 1
  fi
done

# Match the bundle ID (wails build) or the app name prefix (wails dev may run an
# unbundled binary, so its bundle ID can be <none>).
WINDOW="$(windows --json | jq -c --arg bid "$BUNDLE_ID" --arg name "$APP_NAME" '
  [.[] | select(.bundleID == $bid or (.appName | test($name)))] | first // empty')"

if [[ -z "$WINDOW" ]]; then
  echo "error: no visible window for $BUNDLE_ID / $APP_NAME — is the app running and not minimized?" >&2
  exit 1
fi

WINDOW_ID="$(jq -r .windowID <<<"$WINDOW")"
WINDOW_PID="$(jq -r .pid <<<"$WINDOW")"
WINDOW_BUNDLE="$(jq -r .bundleID <<<"$WINDOW")"

front_bundle() {
  lsappinfo info -only bundleid "$(lsappinfo front)" 2>/dev/null |
    sed -n 's/.*[Bb]undle[Ii][Dd]"*="\([^"]*\)".*/\1/p'
}

activate() {
  if [[ "$1" != "<none>" && -n "$1" ]]; then
    open -b "$1"
  else
    osascript -e "tell application \"System Events\" to set frontmost of (first process whose unix id is $2) to true" >/dev/null
  fi
}

# WKWebView (which Wails renders into) stops painting while its window is
# occluded, so a background capture shows only the empty window. Raise it first.
PREV_BUNDLE=""
if [[ "$ACTIVATE" == "1" ]]; then
  PREV_BUNDLE="$(front_bundle || true)"
  if [[ "$PREV_BUNDLE" != "$WINDOW_BUNDLE" ]]; then
    activate "$WINDOW_BUNDLE" "$WINDOW_PID"
    sleep "$SETTLE"
  else
    PREV_BUNDLE=""
  fi
fi

mkdir -p "$(dirname "$OUT")"
# -l: window ID, -o: no drop shadow, -x: no shutter sound
screencapture -x -o -l "$WINDOW_ID" "$OUT"

# Hand focus back to whatever was in front before.
if [[ -n "$PREV_BUNDLE" ]]; then
  open -b "$PREV_BUNDLE" || true
fi

echo "$OUT"
