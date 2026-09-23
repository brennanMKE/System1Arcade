#!/usr/bin/env bash
# Build System 1 Arcade for the current platform (macOS or Linux; on Windows
# use scripts/build.ps1, or run this from Git Bash).
#
# Usage: scripts/build.sh [--agent] [--clean] [--debug] [--test]
#   --agent  also set up .venv with Laya for the built-in agent
#   --clean  remove previous build output first
#   --debug  build with devtools and debug logging
#   --test   run the Go tests before building
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

AGENT=0 CLEAN=0 DEBUG=0 TEST=0
for arg in "$@"; do
  case "$arg" in
    --agent) AGENT=1 ;;
    --clean) CLEAN=1 ;;
    --debug) DEBUG=1 ;;
    --test) TEST=1 ;;
    -h|--help) sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $arg (see --help)" >&2; exit 2 ;;
  esac
done

say() { printf '\033[1m==> %s\033[0m\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

case "$(uname -s)" in
  Darwin) OS=darwin ;;
  Linux) OS=linux ;;
  MINGW*|MSYS*|CYGWIN*) OS=windows ;;
  *) die "unsupported platform: $(uname -s)" ;;
esac

# --- Prerequisites -----------------------------------------------------------
have go || die "Go is required: https://go.dev/dl/"
have npm || die "Node.js and npm are required: https://nodejs.org/"

# Use the Wails CLI version that matches go.mod, installing it if needed.
WAILS_VERSION="$(awk '/github.com\/wailsapp\/wails\/v2 v/ && !/replace/ {print $2; exit}' go.mod)"
GOBIN="$(go env GOBIN)"; GOBIN="${GOBIN:-$(go env GOPATH)/bin}"
WAILS="$(command -v wails || true)"
[ -z "$WAILS" ] && [ -x "$GOBIN/wails" ] && WAILS="$GOBIN/wails"
if [ -z "$WAILS" ]; then
  say "Installing the Wails CLI $WAILS_VERSION"
  go install "github.com/wailsapp/wails/v2/cmd/wails@$WAILS_VERSION"
  WAILS="$GOBIN/wails"
fi

TAGS=""
case "$OS" in
  darwin)
    xcode-select -p >/dev/null 2>&1 || die "Xcode command line tools are required: xcode-select --install"
    ;;
  linux)
    have pkg-config || die "pkg-config is required (e.g. sudo apt install pkg-config)"
    pkg-config --exists gtk+-3.0 || die "GTK 3 development files are required (e.g. sudo apt install libgtk-3-dev)"
    if pkg-config --exists webkit2gtk-4.0; then
      :
    elif pkg-config --exists webkit2gtk-4.1; then
      TAGS="webkit2_41" # newer distributions (e.g. Ubuntu 24.04) ship only 4.1
    else
      die "WebKitGTK development files are required (e.g. sudo apt install libwebkit2gtk-4.1-dev)"
    fi
    ;;
esac

# --- Optional steps ----------------------------------------------------------
if [ "$TEST" = 1 ]; then
  say "Running tests"
  go test ./internal/...
fi

if [ "$AGENT" = 1 ]; then
  PY="$(command -v python3 || command -v python || true)"
  [ -n "$PY" ] || die "Python 3 is required for the built-in agent"
  VENV_PY=".venv/bin/python"; [ "$OS" = windows ] && VENV_PY=".venv/Scripts/python.exe"
  if [ ! -x "$VENV_PY" ]; then
    say "Creating .venv"
    "$PY" -m venv .venv
  fi
  say "Installing Laya into .venv (the model downloads on first use)"
  "$VENV_PY" -m pip install --quiet --upgrade pip
  "$VENV_PY" -m pip install --quiet laya
fi

# --- Build -------------------------------------------------------------------
ARGS=()
[ "$CLEAN" = 1 ] && ARGS+=(-clean)
[ "$DEBUG" = 1 ] && ARGS+=(-debug)
[ -n "$TAGS" ] && ARGS+=(-tags "$TAGS")

say "Building for $OS/$(go env GOARCH) with Wails $("$WAILS" version 2>/dev/null | grep -Eo 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1)"
"$WAILS" build ${ARGS[@]+"${ARGS[@]}"}

case "$OS" in
  darwin) OUT="build/bin/$(awk -F'"' '/"name"/ {print $4; exit}' wails.json).app" ;;
  windows) OUT="build/bin/$(awk -F'"' '/"outputfilename"/ {print $4; exit}' wails.json).exe" ;;
  *) OUT="build/bin/$(awk -F'"' '/"outputfilename"/ {print $4; exit}' wails.json)" ;;
esac
[ -e "$OUT" ] || die "build finished but $OUT is missing"
say "Built $OUT"
if [ "$OS" = darwin ]; then echo "    open \"$OUT\""; else echo "    \"./$OUT\""; fi
