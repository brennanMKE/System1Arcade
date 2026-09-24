# Screenshots

`scripts/screenshot.sh` captures the System 1 Arcade window as a PNG, so you (or an agent) can see what
the game looks like while it runs and give feedback on it.

## Prerequisites

- **macOS** with the built-in `screencapture` command.
- **`windows`**, a CLI tool that lists on-screen windows with their IDs
  ([brennanMKE/Windows](https://github.com/brennanMKE/Windows)). To build and install it to `~/bin/`:

  ```bash
  cd ~/Developer/brennanMKE/Windows && ./install.sh
  ```

- **`jq`**: `brew install jq`
- **Screen Recording permission** for the terminal app that runs the script (System Settings →
  Privacy & Security → Screen Recording).

## Usage

Start the app with `wails dev`, or open `build/bin/System 1 Arcade.app`. Then run:

```bash
scripts/screenshot.sh                  # → screenshots/YYYYMMDD-HHMMSS.png
scripts/screenshot.sh /tmp/frogger.png # explicit output path
```

The script prints the path of the saved PNG, so it's easy to hand the image to another tool:

```bash
open "$(scripts/screenshot.sh)"
```

`screenshots/` is git-ignored.

## How it works

1. `windows --json` lists every visible window with its CoreGraphics window ID, bundle ID and app name.
2. `jq` picks the first window whose bundle ID is `co.sstools.System1Arcade` (the `wails build` app
   bundle) or whose app name matches `^System ?1` ("System 1 Arcade", or "System1" under `wails dev`). The name match covers `wails dev`, which runs a
   binary outside an app bundle, so that window may have no bundle ID.
3. It brings the app to the front and waits `SETTLE` seconds (1 by default); see below for why.
4. `screencapture -x -o -l <windowID>` captures just that window, with no shutter sound (`-x`) and
   no drop shadow (`-o`).
5. It switches back to whichever app was in front before.

### Why it brings the app to the front

Wails draws the UI in a `WKWebView`. WebKit stops painting a web view whose window is covered by
other windows. `screencapture -l` can still capture a covered window, but the capture shows only
the empty window: the title bar over a flat dark background. Bringing the window to the front and
waiting briefly lets WebKit repaint before the capture.

### Environment variables

| Variable | Default | Purpose |
| --- | --- | --- |
| `BUNDLE_ID` | `co.sstools.System1Arcade` | Bundle ID to match |
| `APP_NAME` | `^System ?1` | Regular expression the app name must match |
| `SETTLE` | `1` | Seconds to wait after bringing the app to the front |
| `ACTIVATE` | `1` | Set to `0` to capture without bringing the app to the front (the capture may be blank) |

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| `no visible window` | The app isn't running, or its window is minimized or on another Space. |
| Title bar is visible but the content is blank | WebKit hadn't repainted yet. Raise `SETTLE` (e.g. `SETTLE=2`), and don't set `ACTIVATE=0`. If it's still blank, check the terminal's Screen Recording permission. |
| Focus jumps to the app for a moment | Expected: the script has to bring the window to the front to capture it. |
| `'windows' not found` | Install the `windows` tool (see Prerequisites) and make sure `~/bin` is on `PATH`. |

## Feedback loop

To review a game while it plays:

1. Launch the app and start a game (for example, Frogger in `realtime` mode).
2. Run `scripts/screenshot.sh` at the moments you want to review. Take several captures a second or
   two apart to see motion.
3. Look at the PNGs yourself, or give their paths to Claude to check the layout, rendering bugs and
   the game state.
