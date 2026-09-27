# Changelog

Release notes for each version. `scripts/publish-release.sh` uses the section for the version
being released.

## Unreleased

The built-in agent no longer needs Python.

- **Laya runs inside the app** through [laya-go](https://github.com/brennanMKE/laya-go), a pure-Go
  port of the Laya package: no Python, PyTorch or virtual environment to set up. The first start
  downloads the model (about 800 MB) into the standard Hugging Face cache, with progress in the
  side panel; a model the Python package already downloaded is used as is. The model stays loaded
  while the agent plays and is freed when you stop it.
- **Same answers as before.** Over 343 recorded game requests the built-in agent gives the Python
  server's answers: every choice the same and 99.86% of numbers identical to 4 decimals (the rest
  differ by 0.0001). The answer cache (`SYSTEM1_LAYA_CACHE`) works as before.
- **Runs on the CPU** (Apple Accelerate on macOS). `agents/laya_server.py` still works as a custom
  agent and runs Laya on the GPU.
- **Test connection** for the built-in agent asks the loaded model, or says whether the model still
  has to be downloaded.
- **The app keeps App Nap away while an agent plays.** With the window hidden or the display
  asleep, macOS throttled the app, and the built-in agent's new sentences took up to 2.3 s instead
  of about 0.1 s: Frogger seed 1 ended at 3,250–4,900 points in the app against 20,420 headless.
- The agent asks at most once per game tick in realtime; with answers from the cache it used to
  ask thousands of times a second and keep a CPU core busy.
- `cmd/headless -agent laya` plays with the built-in agent, and `-agent <url>` with a custom one,
  for benchmarks without Python or a window.
- **`SYSTEM1_SOUND=off`** mutes a session, for automated and autostarted runs, without changing
  the saved sound preference. `scripts/run-agent-vm.sh` sets it.
- `scripts/build.sh --agent` is now `--python-tools` (`-PythonTools` on Windows), and is only
  needed for the Python tools in `agents/`. `SYSTEM1_PYTHON` is gone. Building needs Go 1.27.

## 0.1.1

Sound for every game.

- **Sound effects** in Tetris, Frogger and Space Invaders: moves, line clears, hops, splashes,
  shots, the invaders' four-note march that speeds up as they do, and more. Sound is on by
  default; turn it off with the Sound button or the M key, and the app remembers your choice.
- **Autostart for testing:** set `SYSTEM1_AUTOSTART=<game>[:<seed>]` to start the agent at launch
  without pressing Start.
- The app now logs agent errors, not only the latest one in the side panel.

## 0.1.0

The first release: Tetris, Frogger and Space Invaders, played by you or by a System 1 model.

- **Three games** with keyboard controls, each playable by an agent through the same virtual
  controller: Tetris, Frogger and Space Invaders.
- **Built-in agent:** Laya runs locally. On first start the app sets up its own Python
  environment and downloads the model (about 1 GB, needs Python 3.10 or newer).
- **Custom agents:** point the app at any HTTP endpoint that answers typed questions with
  probabilities, such as a standalone Laya server or TypeSafe's Jev.
- **Answer cache:** Laya's answers are cached by state and question, so repeated sentences skip
  the model. At the default 6 inputs a second, Frogger averages about 19,700 points and Space
  Invaders reaches waves 3–5.
- **Richer Tetris sentences:** each landing spot says whether it fits snugly and whether it leaves
  or fills a deep well. In lockstep, Laya averages about 1,100 lines over 20 seeds.
- **Realtime and lockstep clocks**, an agent input speed setting, and a local `/v1` API for agents
  that drive the game themselves.
