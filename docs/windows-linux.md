# Windows and Linux

System 1 Arcade is built with Go and Wails, so it should run on Windows and Linux as well as macOS.
The maintainer only has Macs, so nobody has run it on either yet. This page is for someone who
can: what is known, how to build and test, where the platform-specific code is, and what a first
Windows or Linux pull request should report. Contributions for both are welcome; see
[CONTRIBUTING.md](../CONTRIBUTING.md) for the general rules.

Everything marked **Unverified** has not been run. Please correct this page in your PR when you
find out.

## Status

Checked on 2026-10-01 from a Mac (M4 Pro, Go 1.27.1, Wails 2.16.0):

| Check | Windows | Linux |
|---|---|---|
| `wails build -platform windows/amd64` (the full app, from macOS) | builds `build/bin/System1.exe` | n/a |
| `go vet` of the app, `./internal/...` and `./cmd/...` with `CGO_ENABLED=0` | passes (amd64, arm64) | `./internal/...` and `./cmd/...` pass (amd64); the app itself needs cgo and GTK, so it can't be checked from a Mac |
| `go build ./cmd/headless` with `CGO_ENABLED=0` | builds (amd64, arm64) | builds (amd64) |
| Building the app on the platform itself | **Unverified** | **never built** |
| Running the app, the tests or `cmd/headless` | **never run** | **never run** |
| NSIS installer (`wails build -nsis`) | **never built** (needs `makensis`) | n/a |
| Releases | none | none |

What is expected to differ from macOS:

- **The built-in agent runs on the CPU with portable Go kernels.** There is no Metal or Accelerate
  on Windows or Linux, so laya-go uses its plain-Go matrix multiplies. On the M4 Pro those take
  about 5–10 times as long as Accelerate (below), which is too slow for realtime Frogger. Lockstep
  play is unaffected. Making this fast is laya-go's work, not the app's: see
  [laya-go's Windows and Linux guide](https://github.com/brennanMKE/laya-go/blob/main/docs/windows-linux.md).
- **The window is WebView2 on Windows and WebKitGTK on Linux**, not WKWebView, so audio, keys and
  scaling need checking (see [Likely gaps](#platform-specific-code-and-likely-gaps)).

## Prerequisites

| | Windows | Linux |
|---|---|---|
| Go | 1.27 or newer | 1.27 or newer |
| Node.js and npm | yes (the frontend build) | yes |
| C compiler | not needed: Wails on Windows is pure Go | gcc, for cgo (e.g. `build-essential`) |
| Web view | WebView2 runtime (part of Windows 11; **Unverified** on Windows 10) | GTK 3 and WebKitGTK development packages |
| Wails CLI | installed by the build script, at the version pinned in `go.mod` (v2.16.0) | same |
| Installer | NSIS (`makensis` on `PATH`), only for `-nsis` | n/a |
| Python | not needed; only for the tools in `agents/` (`-PythonTools` / `--python-tools`) | same |

Linux packages, by distribution. Only the Ubuntu names come from the build script; the rest are
**Unverified**:

| Distribution | Packages |
|---|---|
| Ubuntu 24.04, Debian 13 | `build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev` |
| Ubuntu 22.04 | the same, or `libwebkit2gtk-4.0-dev` |
| Fedora | `gcc pkg-config gtk3-devel webkit2gtk4.1-devel` |
| Arch | `base-devel gtk3 webkit2gtk-4.1` |

`wails doctor` reports what Wails finds missing.

## Building

The build scripts install the pinned Wails CLI if it's missing, check prerequisites and build for
the current platform.

```powershell
# Windows
powershell -ExecutionPolicy Bypass -File scripts\build.ps1            # build\bin\System1.exe
powershell -ExecutionPolicy Bypass -File scripts\build.ps1 -Test      # run go test ./internal/... first
powershell -ExecutionPolicy Bypass -File scripts\build.ps1 -DebugBuild -Clean
```

```sh
# Linux
scripts/build.sh                 # build/bin/System1
scripts/build.sh --test --clean
```

On Linux, `scripts/build.sh` uses WebKitGTK 4.0 when it is installed, and otherwise 4.1 with the
`webkit2_41` build tag (newer distributions ship only 4.1). By hand that is:

```sh
wails build -tags webkit2_41
```

Other `wails build` targets:

| Command | What |
|---|---|
| `wails build -platform windows/amd64` | Windows x64; also works from macOS (verified) and should from Linux (**Unverified**) |
| `wails build -platform windows/arm64` | Windows on Arm (**Unverified**) |
| `wails build -nsis` | also builds an NSIS installer from `build/windows/installer/project.nsi`; needs `makensis` (**Unverified**) |
| `wails build -windowsconsole` | keeps a console window, so the app's log is visible (a normal Windows build is a GUI program with no console) |
| `wails build -debug` | devtools and debug logging |
| `wails dev` | live reload while editing the frontend |

`build/windows/` holds the icon, `info.json` (version details from `wails.json`), the manifest
(per-monitor DPI awareness) and the NSIS installer script. The version lives only in
`wails.json`'s `info.productVersion`.

## Running and testing

### Go tests

```sh
go test ./internal/...      # games, engine, API, agent loop: no model, no window
go test . ./internal/...    # also the app package (on Linux this needs the GTK packages)
go vet . ./internal/... ./cmd/...
```

Tests that need the Laya model skip when it isn't in the Hugging Face cache; they never download
it. Once it is cached (the app or `cmd/headless -agent laya` downloads it on first use, about
800 MB into `%USERPROFILE%\.cache\huggingface\hub` or `~/.cache/huggingface/hub`):

| Test | Runs when |
|---|---|
| `TestBuiltinAgent` (app package): loads Laya, plays, stops, frees the model | the model is cached and `-short` isn't set |
| `TestLocalMatchesPython`, `TestLocalCache` (`internal/agent`): the app's prompts and answers against laya-go's golden fixtures | the model is cached |
| `TestLocalModelParity` (`internal/agent`): the model over the 343 golden game requests | `SYSTEM1_LAYA_PARITY=1`; minutes on a fast CPU, likely much longer on portable kernels |
| `TestAgentLive` (app package) against a running agent | `SYSTEM1_TEST_AGENT_URL=http://127.0.0.1:8000/predict` |

### Headless play, no window

`cmd/headless` runs the same engine, games and agent loop as the app, without Wails. It is the
quickest way to check the built-in agent on a new platform, and the numbers can be compared
directly with the Mac's.

```sh
go run ./cmd/headless -agent laya -game frogger -mode lockstep -seed 1 -games 3
go run ./cmd/headless -agent laya -game invaders -mode lockstep -seed 1 -games 3
go run ./cmd/headless -agent laya -game frogger -mode realtime -pace 10 -seed 1 -games 3
```

It prints where Laya runs (`Laya loaded on the CPU in 105ms [native (portable)]`), then one line
per game with the score, decisions and their latency. The decisions over 10 ms are about the ones
that ran the model, so their median and p99 (in parentheses) are the model's speed. Pass
`-addr 127.0.0.1:8799` if the app is open, since both default to port 8765.

**Lockstep scores must match exactly.** The game waits for each answer, so the score depends only
on the answers, not on speed. On the M4 Pro these were identical with Metal, Accelerate and the
portable Go kernels (`LAYA_ENGINE=native LAYA_KERNELS=portable`, what Windows and Linux use):

| Command (`-mode lockstep -seed 1 -games 3`) | Seed 1 | Seed 2 | Seed 3 | Cache misses, running total |
|---|---|---|---|---|
| `-game frogger` | 26,340 (level 8) | 23,160 (level 7) | 26,210 (level 8) | 105 / 109 / 111 |
| `-game invaders` | 6,050 (level 6) | 9,130 (level 9) | 8,790 (level 8) | 26 / 26 / 26 |

The answer cache carries over between games (the misses are a running total), so run the three
seeds in one command as shown. A
different lockstep score means different answers, which is a bug worth reporting with the
output.

**Realtime scores depend on speed.** At the app's default pace (`-pace 10`, 6 inputs per second),
Frogger seeds 1–5 scored 20,420 / 24,900 / 11,230 / 24,720 / 17,290 on Metal, Accelerate and
PyTorch on the GPU alike, with new sentences taking 28–56 ms (Metal) or 46–56 ms (Accelerate) at
the median ([Laya performance](laya-performance.md#the-built-in-agent-in-go)). With the portable
kernels on the same Mac, new sentences took about 0.5 s at the median and Frogger seed 1 ended
at 60 points within 7 seconds. Expect something similar on Windows and Linux until laya-go has
faster kernels; Tetris and Space Invaders are less sensitive. Please report what you get.

### The app

```powershell
# Windows, PowerShell: a muted, unattended run of Frogger seed 1 with the built-in agent
$env:SYSTEM1_SOUND = 'off'
$env:SYSTEM1_AUTOSTART = 'frogger:1'
$env:SYSTEM1_ADDR = '127.0.0.1:8799'
build\bin\System1.exe
```

```sh
# Linux
SYSTEM1_SOUND=off SYSTEM1_AUTOSTART=frogger:1 SYSTEM1_ADDR=127.0.0.1:8799 ./build/bin/System1
```

| Variable | Effect |
|---|---|
| `SYSTEM1_SOUND=off` | mutes this session without changing the saved preference. Use it for every automated launch |
| `SYSTEM1_AUTOSTART=<game>[:<seed>]` | loads the game and starts the agent chosen in Settings, as the Start button does |
| `SYSTEM1_AUTOSTOP_AFTER=90s` | with `SYSTEM1_AUTOSTART`, stops the agent after that long, to check that memory is freed |
| `SYSTEM1_ADDR=host:port` | where the local `/v1` API listens (default `127.0.0.1:8765`) |
| `LAYA_ENGINE=native` | the CPU engine (the only one off macOS) |
| `SYSTEM1_LAYA_CACHE=0` | turns off the answer cache, so every decision runs the model |
| `HF_HUB_OFFLINE=1`, `HF_HUB_CACHE`, `HF_HOME`, `HF_TOKEN` | the model download, as for Python |

While it plays, the `/v1` API shows what the window shows:

```sh
curl http://127.0.0.1:8799/v1/state     # status, tick, score and mode
curl http://127.0.0.1:8799/v1/laya      # the prompt the agent is answering
curl -X POST http://127.0.0.1:8799/v1/mode -d '{"mode":"lockstep"}'
```

On Windows, `curl.exe` is built in; in PowerShell, `Invoke-RestMethod
http://127.0.0.1:8799/v1/state` works too. The [README](../README.md#agents-that-drive-the-game-themselves)
lists every endpoint.

Then check by hand: the start screen, **You** and **Agent**, Settings and **Test connection**
(it should say the agent runs "on the CPU"), the agent panel, sound on and off with M, every key,
pause, restart, and stopping the agent.

## Platform-specific code and likely gaps

| Area | Where | macOS | Windows | Linux |
|---|---|---|---|---|
| Keeping the agent fast with the window hidden | `activity_darwin.go`, `activity_other.go` | an `NSProcessInfo` activity stops App Nap from throttling the app while an agent plays | no-op. Windows 11 can throttle background processes ("efficiency mode", EcoQoS); opting out with `SetProcessInformation(ProcessPowerThrottling)` is the likely equivalent. Needed? **Unverified** | no-op; no App Nap equivalent is known to apply. **Unverified** |
| Settings file | `supportDir()` in `agent.go` (`os.UserConfigDir`) | `~/Library/Application Support/System 1 Arcade/` | `%AppData%\System 1 Arcade\settings.json` | `$XDG_CONFIG_HOME/System 1 Arcade/` or `~/.config/System 1 Arcade/`; a name with spaces is unusual on Linux |
| Model cache | laya-go's `hub` | `~/.cache/huggingface/hub` | `%USERPROFILE%\.cache\huggingface\hub`, where Python's `huggingface_hub` also looks | `~/.cache/huggingface/hub` |
| Sound | `frontend/src/audio.js` | Web Audio, unlocked by the first key or click | WebView2 (Chromium) autoplay rules: check the first sound plays after a key press | WebKitGTK's Web Audio needs GStreamer plugins; check sound works at all |
| Keys | `frontend/src/main.js` | arrows, WASD, Z, X, Space, Enter, P, R, M | check none are swallowed by the web view and that Space doesn't press a focused button | same, plus key repeat when holding an arrow |
| Scaling | `build/windows/wails.exe.manifest`, the canvas in `main.js` (`devicePixelRatio`) | Retina | per-monitor DPI v2; check 125%, 150% and moving between monitors | check `GDK_SCALE` and fractional scaling |
| Memory | laya-go `internal/offheap` | weights off the Go heap (about 1.8 GB on the CPU) | weights on the Go heap, so the peak may be higher; please measure | off the heap, as on macOS |
| Logs | `log.Printf` | stderr | a GUI build has no console: build with `-windowsconsole` to see them | stderr |
| Signing | `scripts/release.sh` | Developer ID, notarized | none yet: SmartScreen will warn | n/a |

Licensing: the app is MIT and laya-go is Apache-2.0, so a Windows installer or Linux package has
to include laya-go's `LICENSE` and `NOTICE` (see [Licensing](laya-go-port.md#licensing)).

## Releasing: a proposal

Nothing here exists yet. The likely path is GitHub Actions, since it has Windows and Linux runners
and nobody has to own a machine:

1. **CI on every PR.** A matrix of `windows-latest`, `ubuntu-24.04` and, later, the Arm runners:
   install Go and Node, install the GTK and WebKitGTK packages on Linux, run `go vet` and
   `go test . ./internal/...` (the model tests skip), then build with `scripts/build.ps1` or
   `scripts/build.sh` and keep the binary as an artifact. A nightly job could cache the model and
   run the lockstep headless checks above.
2. **Windows release.** `wails build -nsis` for the installer, and the bare `.exe` zipped. Sign
   both with Authenticode, for example through Azure Trusted Signing or a certificate in a
   GitHub secret; unsigned downloads get a SmartScreen warning until they build reputation.
   WebView2 is part of Windows 11; the installer's default `-webview2 download` strategy fetches
   it where it's missing.
3. **Linux release.** Wails v2 makes only a bare binary. An AppImage (bundles WebKitGTK, large), a
   `.deb` made with a tool such as nfpm (depends on the system's `libwebkit2gtk-4.1-0`), or a
   Flatpak on the GNOME runtime are the options; one of them is enough to start.
4. `scripts/publish-release.sh` attaches the macOS DMG today; it would attach the Windows and
   Linux files from the CI run too.

A PR that sets up CI for one platform is a good first step, even before any release.

## What a first Windows or Linux PR should report

Copy this into the PR description and fill in what you ran:

- [ ] OS and version (e.g. Windows 11 24H2, Ubuntu 24.04), and x64 or Arm
- [ ] CPU (model, cores), RAM, and GPU if relevant
- [ ] Go, Node and Wails versions; on Linux, the WebKitGTK version (`pkg-config --modversion webkit2gtk-4.1`)
- [ ] Build: the exact command, and whether it worked as-is or what you changed
- [ ] `go vet . ./internal/... ./cmd/...` and `go test . ./internal/...` results
- [ ] Lockstep headless scores for Frogger and Space Invaders, seeds 1–3, against the table above
- [ ] Realtime headless Frogger, seeds 1–3 at `-pace 10`: scores and the latency of decisions
      over 10 ms
- [ ] The app: launches, plays each game with the keyboard and with the built-in agent, sound,
      keys, scaling, and what **Test connection** says
- [ ] Memory while the agent plays and after Stop (Task Manager, `Get-Process System1`, or `ps`)
- [ ] Anything on this page that turned out wrong, with the fix in the same PR
