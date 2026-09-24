# UI testing in a Tart VM

How to run System 1 Arcade's real window inside a disposable macOS VM on cameron, instead of on
the Mac someone is using: to play games with a custom agent and collect scores, and to test the
start screen, Settings and the agent panel.

**Status:** the agent-scoring run ([`scripts/run-agent-vm.sh`](../scripts/run-agent-vm.sh)) works
end to end, verified on cameron on 2026-09-23. The browser-driven layer (Playwright against
`wails dev`) and the Linux guest are still plans; everything marked **Unverified** has not been
run. The Tart setup follows `~/Developer/Homelab/cameron/tart-ui-test-vm.md` (Changeover) and the
Curator and Switchyard runners.

## Why a VM

UI automation never runs on cameron's host. On gordon, XCUITest's `XCTAutomationSupport` loaded
into other running apps and crashed Batty, taking about 38 terminal sessions with it. UI automation
on the host also needs Screen Recording, Accessibility and Automation prompts that someone has to
approve, and those grants pile up.

A Tart guest is a separate macOS with its own WindowServer, apps and TCC (privacy permission)
database. Nothing that runs in it can attach to host apps, and a clone that is deleted after every
run can't collect permission grants.

| On the host (fine) | Only in the guest |
|---|---|
| `scripts/build.sh`, `go test ./internal/...`, `go test .` | launching the app window for a test run |
| `go run ./cmd/headless` and API tests against it (no window) | synthesized keys or clicks (`osascript`, Playwright) |
| staging the built `.app` for the guest | `screencapture` in a test run |

`scripts/vm-guest-play.py`, the part that launches the app, refuses to run unless
`sysctl kern.hv_vmm_present` is 1. Using the app yourself, `wails dev`, and taking a screenshot on
purpose with [`scripts/screenshot.sh`](screenshots.md) are normal use, not automation.

## Playing games with an agent: `scripts/run-agent-vm.sh`

One command builds the app, boots a clone, points the app at your agent, plays each game and
seed, and brings the scores back:

```sh
# The standard-library example agent (answers at random), Frogger seed 1, 60 s cap
scripts/run-agent-vm.sh --games frogger --seeds 1 --cap 60

# A server binary plus model weights, e.g. laya-go's laya-server
H=~/.cache/huggingface/hub/models--convaiinnovations--laya
scripts/run-agent-vm.sh --games frogger,tetris,invaders --seeds 1,2,3 --cap 600 \
  --server ../laya-go/bin/laya-server \
  --server-cmd './laya-server --port $PORT --model "$MODEL_DIR"' \
  --model-dir "$H/snapshots/$(cat $H/refs/main)"
```

`scripts/run-agent-vm.sh --help` lists every option. The ones that matter:

| Option | Default | Meaning |
|---|---|---|
| `--games`, `--seeds` | `frogger`, `1` | comma-separated; every game is played with every seed |
| `--cap SECS` | 300 | wall-clock seconds per game before it is stopped and recorded as `"ended": "cap"` |
| `--server PATH` | `agents/custom_agent_example.py` | a file or folder copied to `~/server` in the guest |
| `--server-cmd CMD` | `python3 custom_agent_example.py --port $PORT` | run in `~/server` by a login `zsh`, with `$PORT` and `$MODEL_DIR` set |
| `--model-dir DIR` | none | shared read-only; the guest sees it at `$MODEL_DIR` (`/Volumes/My Shared Files/model`) |
| `--port N` | 8000 | the app is set to `http://127.0.0.1:N/predict` |
| `--input-rate N`, `--no-batch` | 6, batch on | written to the guest's `settings.json`, the same as the Settings sheet |
| `--slot-timeout SECS` | 3600 | how long to wait for a free VM slot |
| `--no-build` | build | reuse `build/bin/System 1 Arcade.app` |
| `--verify FILE`, `--verify-every SECS` | none, 10 | check the agent's answers in the guest (below) |
| `--agent-url URL` | `http://127.0.0.1:<port>/predict` | point the app at an agent elsewhere, e.g. one on the host at `http://192.168.64.1:8000/predict` |

The server must be an arm64 macOS binary or a script the guest can run. The guest has no Go
toolchain, so build Go servers on the host (`GOOS=darwin GOARCH=arm64 go build`); a stub Go binary
was verified this way. Point `--model-dir` at one snapshot folder, not the Hugging Face cache root:
the script resolves symlinks (`cp -c -R -L`, APFS clones, so no extra disk), and the root would
bring every revision plus `blobs/`. The current Laya snapshot is 807 MB in 5 files.

### What a run does

1. **Preflight.** Checks Tart, creates `system1-uitest-golden` from `curator-uitest-golden` if it
   is missing (a copy-on-write clone; Curator's golden is not changed), needs 20 GiB free, and
   sweeps stale `system1-agent-<yyyymmdd-hhmmss>-<pid>` clones left by a killed run. That pattern
   can't match a golden or another project's clone.
2. **Build and stage.** `scripts/build.sh`, then a temp folder with the `.app` as a zip (`ditto`),
   the server, `settings.json`, the server command and `vm-guest-play.py`.
3. **Wait for a slot.** Apple allows two macOS guests running at once per host, and other projects'
   test VMs are often running. The script counts running VMs whose OS is not `linux` (from
   `tart get --format json`) and polls every 15 s until one is free, printing which VMs hold the
   slots. After `--slot-timeout` it exits with "No free VM slot after …". If another VM takes the
   slot between the check and the boot, `tart run` exits and the script goes back to waiting.
4. **Clone and boot.** `tart clone`, `tart set --memory 8192` (the golden has 12 GB; the app and a
   small server need far less), `tart run --no-graphics` with the staging folder shared read-only,
   then waits for `tart exec` and for `/dev/console` to belong to `admin` (the GUI login).
5. **In the guest.** Unzips the app into `/Applications`, writes
   `~/Library/Application Support/System 1 Arcade/settings.json`:

   ```json
   {"agent": "custom", "url": "http://127.0.0.1:8000/predict", "batch": true, "inputRate": 6}
   ```

   starts the server with `nohup` and waits until `curl` gets any HTTP status from its URL. It
   then runs Settings' **Test connection** (`App.TestAgent`, the call the Settings sheet makes)
   and one decision per game, batched and one state per request, against the agent from the guest:
   `TestAgentLive` in `testagent_test.go`, built on the host with `go test -c` since the guest has
   no Go (output in `testagent.log`). Then it runs `vm-guest-play.py`. For each game and seed that launches the app fresh with
   `open -n -a … --env SYSTEM1_AUTOSTART=<game>:<seed>`, polls `GET /v1/state` every 0.2 s until
   `status.over` (or until the seed changes, since the agent loop restarts 2 s after a game over),
   screenshots the guest's screen, and quits the app.
6. **Results.** Everything in the guest's `~/results` is streamed back through `tart exec … tar`
   (the share is read-only on purpose) into `build/agent-vm/<run-id>/`.
7. **Cleanup** on `EXIT`, `INT` and `TERM`: `tart stop` and `tart delete` the clone, remove the
   staging folder. Verified by sending `TERM` mid-boot: no clone, `tart run` process or staging
   folder left.

### Results

`build/agent-vm/<run-id>/` holds `results.jsonl` (one line per game), `summary.txt`,
`timings.json`, `server.log`, `app-<game>-<seed>.log`, `screen-<game>-<seed>.png`, `tart-run.log`,
`build.log`, `testagent.log`, `monitor.jsonl` and, with `--verify`, `verify.jsonl`. A line:

```json
{"game": "frogger", "seed": 1, "mode": "realtime", "score": 100, "level": 0, "lives": 0,
 "over": true, "ended": "over", "message": "GAME OVER", "ticks": 397, "game_secs": 6.6,
 "wall_secs": 9.8, "ticks_per_sec": 39.9, "api_ready_secs": 1.6, "first_answer_secs": 1.6}
```

`game_secs` is ticks at 60 per second; `wall_secs` is the time from the API coming up to the end;
`ticks_per_sec` is the tick rate the game actually got after the agent's first answer;
`first_answer_secs` is how long after launch the game unpaused. An app that never came up is
recorded as `{"game", "seed", "error"}`. When the agent has `GET /stats` (laya-server and
`laya_server.py` do), each line also has `answers_per_sec`, `model_calls` and `misses` for that
game, and `server_rss_mb` / `app_rss_mb` at its end.

`monitor.jsonl` samples every 5 s: the server's and the app's resident memory and CPU, and the
server's `/stats`. The summary prints the server's memory range and any log line that looks like
a panic, a fatal error, an HTTP error or an `agent error:` (the app now logs the agent's errors,
which the UI only shows as the latest status).

**Checking answers (`--verify FILE`).** `FILE` is JSONL of `{"id", "request", "status",
"response"}`: requests with the replies a trusted run gave (for laya-server: the same binary on the
host with `--cache-size 0`, fed laya-go's `testdata/golden` requests). During play the guest posts
one every `--verify-every` seconds, and after the games all of them; a reply agrees when the status
matches, every `choice` matches and every number is within 1.5e-4 (answers are rounded to 4
decimals; the forward pass differs by float noise between machines). One known difference is not
counted: the answer cache is keyed by (state, question) across batch and single-state requests, as
in `laya_server.py`, so a noul answer keeps the shape of the request that first asked it, with or
without `"action"`. Each check's round trip is recorded, so misses measured during play show the
model's latency while the app is busy. Checks during play take the model for one pass each, so
keep `--verify-every` well above that (30 s is plenty).

### Verified runs, 2026-09-23

With the example agent (`agents/custom_agent_example.py`, random answers), Switchyard's VM
holding the other slot:

```
==> Sweeping stale clone: system1-agent-20200101-000000-1
==> Building the app on the host
==> Cloning system1-uitest-golden → system1-agent-20260923-185340-27523
==> Guest up in 24s, admin logged in to the GUI
==> Agent server answering after 9s
==> Playing games=frogger seeds=1,2,3 cap=60s
game       seed   score level lives ended   game s  wall s tick/s 1st ans s
frogger       1     100     0     0 over       6.6     9.8   39.9       1.6
frogger       2      30     0     0 over       4.0     6.2   37.3       0.9
frogger       3      30     0     0 over       3.4     3.7   55.1       0.3
==> Timings (s): {"build": 4, "stage": 0, "slot_wait": 0, "clone": 0, "boot": 24, "server_start": 9, "play": 33, "total": 74}
```

With a stub Go `laya-server` binary (always answers "safe"/"clean") and the Laya snapshot shared
as `--model-dir` (the server saw "5 files, 806 MB"), all three games, seeds 1 and 2, 45 s cap:
Frogger 60 and 50 (game over), Tetris 1,986 and 1,252 (capped), Space Invaders 0 and 0 (capped).
Total 291 s: boot 48, server 14, play 222.

Timings seen across runs: clone about 1 s (copy-on-write), boot to `tart exec` 24–81 s (slower
while another VM is busy), app API ready 0.2–4 s after `open`, cleanup a few seconds.

## The golden image

`system1-uitest-golden` is a copy-on-write clone of `curator-uitest-golden`, made by the script on
first use. Nothing was installed in it: the app is built on the host and copied in per run. It is
never run directly; every run clones it. Don't run or change another project's golden.

Checked in a clone on 2026-09-23 (`tart exec`):

| Item | Value |
|---|---|
| macOS | 26.6.2 (25G83), 6 CPU, 12 GB (runs use 8 GB), 1024×768 display |
| User | `admin`, auto-logged-in to the GUI; `stat -f %Su /dev/console` → `admin` within the boot wait |
| `tart exec` session | `launchctl managername` → `Aqua`, so `open` starts apps in the GUI session |
| Python | `python3` in a login shell is Homebrew 3.14.7; `/usr/bin/python3` (Xcode) also works |
| Tools | `jq`, `swift`, `curl`; **no Go** |

The app window is real: `CGWindowListCopyWindowInfo` in the guest lists "System 1 Arcade" at
layer 0, 1024×678, and `lsappinfo` shows it as a Foreground app.

### Prompts seen in the guest

Nobody answers these, and none of them stopped a run, but they show up in screenshots:

- **"tart-guest-agent is requesting to bypass the system private window picker…"** appears the
  first time `screencapture` runs through `tart exec`. The capture itself still contains the app
  (no Screen Recording grant was needed), and the alert stays on screen in later captures.
- **"Allow 'Python' to find devices on local networks?"** appeared once while the built-in agent
  (the default when no `settings.json` is written) was starting. Loopback (`127.0.0.1`) traffic is
  not blocked by Local Network privacy: the example agent and the stub both answered with it
  showing.
- **"Multiple Extensions Added"** (Xcode) notification, cosmetic.

To get clean screenshots, grant tart-guest-agent Screen Recording once in the golden through
`tart run system1-uitest-golden --vnc` (**unverified** that the grant survives cloning).

## `SYSTEM1_AUTOSTART`

The start screen needs a real click or Enter key, and synthesizing one needs Accessibility in
the guest. Instead, the app has a test-only switch, off unless the variable is set:

```sh
SYSTEM1_AUTOSTART=<game>[:<seed>]     # e.g. frogger:1; no seed or 0 picks one from the clock
```

At startup the app loads that game with that seed and calls `App.StartAgent`, the same call the
Start button makes with **Agent** selected, so the agent chosen in Settings (`settings.json`)
plays. The start screen hides itself when the agent is already running. An unknown game or a bad
seed is logged and the app opens on the start screen as usual. Tests: `go test .`
(`autostart_test.go`; needs `frontend/dist`, which `scripts/build.sh` creates).

Pass it with `open --env` (macOS 13+), since `open` does not otherwise pass the caller's
environment to the app.

## Other layers (not built yet)

This is a Go + Wails app, not an Xcode project, so there is no XCUITest target. The UI is a web
page (`frontend/`) in a `WKWebView`, and everything it shows comes from the Go engine:

1. **Engine and API, no window.** `go test ./internal/...` covers the games, the engine and the
   `/v1` API. Runs on the host.
2. **The real app, driven through `/v1`.** What `run-agent-vm.sh` does. The same clone could also
   run `agents/laya_agent.py --policy oracle` against `/v1` (standard library only).
3. **The frontend, driven like a browser.** `wails dev` serves the app at
   `http://localhost:34115`, where Playwright could click Start, open Settings, press keys and
   screenshot with no TCC grants. **Unverified:** that Wails events (`EventsOn('update')`, which
   feeds the canvas) reach a browser tab in v2.16.0. This needs Go and Wails in the guest
   (`brew install go`, `go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0`, **unverified**)
   and a Playwright project, which the repo doesn't have.

Worth checking in layers 2 and 3: the three games listed; **Custom agent** enabling the URL field;
"Enter an http:// or https:// URL." for a bad URL; **Test connection** showing "Connected: …";
the agent status line reading "The custom agent is playing…" (visible in the run's screenshots);
the API line showing `http://127.0.0.1:8765/v1`.

Per the Changeover and Switchyard docs, check the host before and after a run if in doubt:

```sh
lsappinfo list | grep -E '^ *[0-9]+\) "'        # GUI apps alive, same ASNs
ls -lt ~/Library/Logs/DiagnosticReports | head  # no new crash reports
tart list                                       # no system1-agent-* clones left behind
```

## Known limits

- **Two macOS guests per host.** The script waits for a slot rather than failing; with other
  projects' runs going, expect waits. There is no lock between two `run-agent-vm.sh` runs beyond
  that check.
- **Realtime isn't reproducible.** The default Clock is realtime, where the agent's latency and
  the guest's speed change the outcome, so the same seed can score differently. Compare
  distributions over several seeds. The script has no lockstep option yet; setting it through
  `POST /v1/mode` after launch would race the agent's first answer.
- **The guest can fall behind 60 ticks/s.** Measured 46–61 ticks/s with the example agent once
  the guest had settled, and 37–40 in games started right after boot while `dasd` and
  `duetexpertd` were busy. A stub that answers instantly pushed Space Invaders down to about 20.
  Each result records `ticks_per_sec`. A slower clock gives a realtime agent more time per tick,
  so it can flatter slow agents slightly.
- **Memory.** Runs use an 8 GB guest (`--memory`). Curator's runner has a memory-signal preflight
  for LM Studio pressure; this one only checks disk.
- **Screenshots** are of the guest's whole 1024×768 screen, taken after the game ended; after a
  game over the next game may already be restarting.
- The guest GPU is paravirtualized; don't compare screenshots pixel for pixel with host captures.
- Built on the host with the macOS 27.0 SDK, the app's minimum macOS is 11.0, so it runs on the
  guest's 26.6.2.

## Linux

Tart runs Linux guests too (`tart create --linux`), and Cirrus Labs publishes Ubuntu images
(`ghcr.io/cirruslabs/ubuntu`: `20.04`, `22.04`, `24.04`, `latest`, checked 2026-09-22). On Apple
silicon these are arm64 guests, and they don't count against the two-macOS-guest limit. That would
be the first real run of the Linux build path. **Unverified**, all of it:

```sh
tart clone ghcr.io/cirruslabs/ubuntu:24.04 system1-linux-golden
# in the guest
sudo apt install -y golang nodejs npm pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev \
  xvfb xdotool imagemagick
scripts/build.sh                                  # picks the webkit2_41 tag on 24.04
SYSTEM1_AUTOSTART=frogger:1 xvfb-run -a ./build/bin/System1 &
import -window root ~/results/linux.png           # ImageMagick screenshot of the X display
```

Ubuntu's `golang` package may be older than go.mod's `go 1.25`; if so, install Go from go.dev.
Whether the Ubuntu images include the Tart Guest Agent (for `tart exec`) is unverified; SSH to
`tart ip` is the fallback.
