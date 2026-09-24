# UI testing in a Tart VM

How to test System 1 Arcade's window (start screen, Settings, the agent panel, the game canvas)
inside a disposable macOS VM on cameron, instead of on the Mac someone is using.

**Status:** plan. Nothing here has been run for System 1 Arcade yet. Commands marked
**Unverified** have not been run anywhere; everything else is either checked on cameron or taken
from the proven Tart setup in `~/Developer/Homelab/cameron/tart-ui-test-vm.md` (Changeover) and
`~/Developer/brennanMKE/Git/Switchyard/docs/ui-test-automation-vm.md`.

## Why a VM

UI automation never runs on cameron's host. On gordon, XCUITest's `XCTAutomationSupport` loaded
into other running apps and crashed Batty, taking about 38 terminal sessions with it. UI automation
on the host also needs Screen Recording, Accessibility and Automation prompts that someone has to
approve, and those grants pile up.

A Tart guest is a separate macOS with its own WindowServer, apps and TCC (privacy permission)
database. Nothing that runs in it can attach to host apps, and a clone that is deleted after every
run can't collect permission grants.

What stays on the host and what goes in the guest:

| On the host (fine) | Only in the guest |
|---|---|
| `scripts/build.sh`, `go test ./internal/...` | launching the app window to test it |
| `go run ./cmd/headless` and API tests against it (no window) | synthesized keys or clicks (`osascript`, Playwright) |
| packing the built `.app` for the guest | `screencapture`, `scripts/screenshot.sh` in a test run |

Using the app yourself, `wails dev`, and taking a screenshot on purpose with
[`scripts/screenshot.sh`](screenshots.md) are normal use, not automation.

## What "UI testing" means for this app

This is a Go + Wails app, not an Xcode project, so there is no XCUITest target. The UI is a web
page (`frontend/`) in a `WKWebView`, and everything it shows comes from the Go engine. That gives
three layers, from cheapest to fullest:

1. **Engine and API, no window.** `go test ./internal/...` already covers the games, the engine
   and the `/v1` API (`internal/api/server_test.go`). This runs on the host and needs no VM.
2. **The real app, driven through `/v1`.** Launch the built `.app` in the guest, check
   `GET /v1/state` and `/v1/games`, play with `agents/laya_agent.py --policy oracle` (standard
   library only, no Laya), and capture the window. This tests the packaged app, its embedded
   frontend and its API server. Limit: the start screen and Settings are plain DOM with no API,
   so getting past the start screen needs a real key press (Enter), which needs Accessibility in
   the guest (see [Permissions](#permissions-in-the-guest)).
3. **The frontend, driven like a browser.** `wails dev` serves the app to a browser at
   `http://localhost:34115` (the Wails default, from `internal/project/project.go` in Wails
   v2.16.0), with Go bindings working over the dev server. Playwright can click Start, open
   Settings, press keys and screenshot the page with no TCC grants at all. Playwright's WebKit
   engine is the closest match to the `WKWebView` the app uses. **Unverified:** that Wails
   events (`EventsOn('update')`, which feeds the canvas) reach a browser tab in v2.16.0; check
   this first.

Layer 3 is the best fit for the start screen, Settings and the agent panel. Layer 2 is the smoke
test that the shipped `.app` launches and renders.

Things worth checking in layers 2 and 3:

- **Start screen:** three games listed, **You** / **Agent** choice, **Start** hides the overlay.
- **Settings:** switching to **Custom agent** enables the URL field; saving a non-URL shows
  "Enter an http:// or https:// URL."; **Test connection** against the example agent shows
  "Connected: …".
- **Agent panel:** with a custom agent running, the status line reads "The custom agent is
  playing…", the decision count climbs, and the **Model sees** section appears.
- **Agent API line:** shows `http://127.0.0.1:8765/v1`, not `disabled: …`.
- **Canvas:** not blank after a game starts (compare screenshots over time, or `GET /v1/frame`).

For agent tests, use the standard-library example agent, not the built-in one. The built-in agent
downloads Laya and PyTorch (about 1 GB) on first start, and a fresh clone would do that every run.

```sh
python3 agents/custom_agent_example.py --port 8000   # URL: http://127.0.0.1:8000/predict
```

Settings live in `~/Library/Application Support/System 1 Arcade/settings.json` (`os.UserConfigDir`
in `agent.go`), so a run can write one before launching the app:

```json
{"agent": "custom", "url": "http://127.0.0.1:8000/predict", "batch": true, "inputRate": 6}
```

## The golden image

Tart 2.32.1 is already installed on cameron, and these images exist (`tart list`, 2026-09-22):

```
local  changeover-uitest-golden   (macOS 26.6.2, Xcode 27.0, hardened, 6 CPU / 12 GB)
local  switchyard-uitest-golden
OCI    ghcr.io/cirruslabs/macos-tahoe-xcode:27
```

Don't run or change another project's golden. Clone one for this project, the way Switchyard did.
Cloning is APFS copy-on-write, so it costs only what changes:

```sh
tart clone changeover-uitest-golden system1-uitest-golden
```

**DECISION:** clone from `changeover-uitest-golden` (keeps its screen-lock, sleep and SSH
hardening) or from `ghcr.io/cirruslabs/macos-tahoe-xcode:27` (clean, but the hardening in the
Tart doc's Phase 2 must be applied again).

Then boot the new golden once to provision it. `tart exec` runs commands through the Tart Guest
Agent; the guest user is `admin` with passwordless `sudo`.

```sh
tart run system1-uitest-golden --no-graphics > /tmp/tart-system1-golden.log 2>&1 &
until tart exec system1-uitest-golden true 2>/dev/null; do sleep 5; done
tart exec system1-uitest-golden zsh -lc 'sw_vers; which go node npm python3 jq'
```

### What the guest already has

From Cirrus Labs' image templates (`cirruslabs/macos-image-templates`, `base.pkr.hcl` on master),
not yet confirmed in this image: Homebrew, `node@24` on `PATH` via `~/.zprofile`, `jq`, `git`.
**Not included: Go.** Python 3 is whatever Xcode's command line tools ship, which is fine for the
example agent and `laya_agent.py --policy oracle` (both standard library only) but not for Laya,
which needs 3.10 or newer.

### Provisioning, only for layer 3 (building in the guest)

Layer 2 needs nothing extra: the app is built on the host and copied in. Layer 3 builds and runs
`wails dev` in the guest, so bake the toolchain into the golden to avoid downloads on every run.
**Unverified** in the guest:

```sh
# inside the golden (tart exec system1-uitest-golden zsh -lc '…')
brew install go                                            # go.mod needs go 1.25 or newer
go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0  # the version pinned in go.mod
~/go/bin/wails doctor
```

For Playwright, install it and its WebKit browser once in the golden, in a folder the tests will
reuse (`npx playwright install webkit`; **unverified**, and the repo has no Playwright tests yet).

### Permissions in the guest

Automation Mode (already enabled without authentication in the Changeover golden) only covers
XCUITest. This app's tests need ordinary TCC grants instead:

| Test step | Needs | Layer |
|---|---|---|
| `curl` to `/v1`, `laya_agent.py` | nothing | 2 |
| `screencapture` of the app window | Screen Recording | 2 |
| `osascript` key presses (Enter on the start screen) | Accessibility | 2 |
| Playwright screenshots and input | nothing (it is its own browser) | 3 |

TCC grants have to be made by hand once in the golden, through the guest's screen:

```sh
tart run system1-uitest-golden --vnc     # then System Settings → Privacy & Security
```

**Unverified:** which process to grant. TCC charges the "responsible" process, and for commands
run through `tart exec` that is probably the guest agent (`/opt/homebrew/bin/tart-guest-agent`),
not the shell. To find out, run a capture through `tart exec` in a throwaway clone of the golden
and read the TCC log:

```sh
tart exec <clone> screencapture -x /tmp/probe.png
tart exec <clone> log show --last 2m --predicate 'subsystem == "com.apple.TCC"'
```

A capture made without Screen Recording shows only the desktop, with no app content. Also
**unverified:** whether macOS 26's periodic "still allow screen recording?" reminder appears in
clones made weeks after the grant.

**Also unverified:** that `open` from `tart exec` starts the app in admin's GUI session. Check with
`tart exec <clone> launchctl managername`, which should print `Aqua`. If it doesn't,
`sudo launchctl asuser $(id -u admin) open …` runs it in the GUI session.

Stop the golden when done. From then on it is only cloned, never run:

```sh
tart stop system1-uitest-golden
```

## A test run

Same shape as `~/Developer/brennanMKE/Changeover/run-ui-tests-vm.sh`. Copy that script, including
its memory preflight and its stale-clone sweep, which matches only its own run-ID pattern.

1. `set -euo pipefail`, a run ID, and a `trap` cleanup on `EXIT` before creating anything.
2. Build on the host and pack the app into a fresh export folder. Pack it with `ditto -c`, because
   Tart's shares don't pass symlinks (the current `.app` has none, but an archive is safe either
   way):

   ```sh
   scripts/build.sh
   ditto -c "build/bin/System 1 Arcade.app" "$EXPORT/app.cpio"
   git archive HEAD | tar -x -C "$EXPORT/src"     # agents/, tests; never the live working copy
   ```

3. Clone and boot headless with the export shared **read-only**:

   ```sh
   tart clone system1-uitest-golden "system1-uitest-$RUN_ID"
   tart run "system1-uitest-$RUN_ID" --no-graphics --dir=run:"$EXPORT":ro > "$EXPORT/tart-run.log" 2>&1 &
   until tart exec "system1-uitest-$RUN_ID" true 2>/dev/null; do sleep 5; done
   ```

4. In the guest: unpack and launch. The share appears at `/Volumes/My Shared Files/run`. Copy
   folders into their parent (`cp -R …/src /Users/admin/`), since copying onto an existing folder
   nests it. Streaming a tarball into the guest through `tart exec` stdin fails; use the share.

   ```sh
   # inside the guest
   ditto -x "/Volumes/My Shared Files/run/app.cpio" "/Applications/System 1 Arcade.app"
   cp -R "/Volumes/My Shared Files/run/src" /Users/admin/
   mkdir -p ~/results
   open "/Applications/System 1 Arcade.app"
   until curl -sf http://127.0.0.1:8765/v1/state > /dev/null; do sleep 1; done
   ```

5. Run the checks (layer 2 shown; **unverified**):

   ```sh
   curl -s http://127.0.0.1:8765/v1/games > ~/results/games.json
   screencapture -x ~/results/start-screen.png          # needs Screen Recording
   cd ~/src && python3 agents/laya_agent.py --game frogger --policy oracle --lockstep \
     --games 1 --max-decisions 200 -v | tee ~/results/oracle.log
   curl -s http://127.0.0.1:8765/v1/state > ~/results/state.json
   ```

   The guest shows only this app, so `scripts/screenshot.sh`'s bring-to-front step is not
   needed; `screencapture -x` of the whole screen, or `screencapture -l <id>` with the `windows`
   tool, both work. `windows` is an arm64 binary built for macOS 26.0, so it runs on the guest's
   26.6.2 if copied in through the export.

6. Bring results back through `tart exec`, not the share, which is read-only on purpose:

   ```sh
   tart exec "system1-uitest-$RUN_ID" tar -C /Users/admin/results -cf - . | tar -x -C "build/ui-tests/$RUN_ID"
   ```

7. Cleanup in the trap: `tart stop` and `tart delete` the clone, `rm -rf "$EXPORT"`.

For layer 3, step 2 exports only the source, and step 5 becomes `wails dev` in the background plus
`npx playwright test` against `http://localhost:34115`.

Per the Changeover and Switchyard docs, check the host before and after every run:

```sh
lsappinfo list | grep -E '^ *[0-9]+\) "'        # GUI apps alive, same ASNs
ls -lt ~/Library/Logs/DiagnosticReports | head  # no new crash reports
tart list                                       # no clones left behind
```

## Known limits

- Apple allows two macOS guests running at once per host. Run one test VM at a time.
- Memory is cameron's real limit: a guest takes 12 GB, and LM Studio's loaded models have
  OOM-killed a VM before. The Changeover script's memory preflight handles this; keep it.
- The guest GPU is paravirtualized. The canvas is simple 2D drawing, so it should render, but
  don't compare screenshots pixel for pixel against host captures.
- Built on the host with the macOS 27.0 SDK, the app's minimum macOS is 11.0 (`otool -l`), so it
  runs on the guest's 26.6.2.

## Linux

Tart runs Linux guests too (`tart create --linux`), and Cirrus Labs publishes Ubuntu images:
`ghcr.io/cirruslabs/ubuntu` has tags `20.04`, `22.04`, `24.04` and `latest` (registry checked
2026-09-22). On Apple silicon these are arm64 guests, so they test the arm64 Linux build only.
Linux guests don't count against the two-macOS-guest limit.

That would be the first real run of the Linux build path, which the README notes is untested.
**Unverified**, all of it:

```sh
tart clone ghcr.io/cirruslabs/ubuntu:24.04 system1-linux-golden
# in the guest
sudo apt install -y golang nodejs npm pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev \
  xvfb xdotool imagemagick
scripts/build.sh                                  # picks the webkit2_41 tag on 24.04
xvfb-run -a ./build/bin/System1 &                 # no desktop needed
import -window root ~/results/linux.png           # ImageMagick screenshot of the X display
```

Ubuntu's `golang` package may be older than go.mod's `go 1.25`; if so, install Go from go.dev
instead. Whether the Ubuntu images include the Tart Guest Agent (for `tart exec`) is unverified;
SSH to `tart ip` is the fallback. Playwright against `wails dev` works the same way here.

## Gaps

- **No run script yet.** `scripts/run-ui-tests-vm.sh`, adapted from Changeover's, would make this
  one command.
- **No UI tests yet.** Layer 3 needs a Playwright project (for example `frontend/e2e/`) and the
  Wails-events check above.
- **No way past the start screen without a key press.** A test-only switch (an environment
  variable or a `/v1` endpoint that starts play and hides the overlay) would let layer 2 run with
  no Accessibility grant.
- **`screenshot.sh` assumes a host desktop.** It needs the `windows` tool and brings the app to
  the front; a guest version can just call `screencapture`.
