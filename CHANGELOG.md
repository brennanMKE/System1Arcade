# Changelog

Release notes for each version. `scripts/publish-release.sh` uses the section for the version
being released.

## Unreleased

The built-in agent runs on the GPU on Apple silicon Macs.

- **Laya on the GPU with Metal.** laya-go (updated to `1c1b85a`) adds a Metal engine, and the
  built-in agent uses it on Macs with Apple silicon (M1 or newer) when the GPU passes a self-test
  at load; otherwise, including Intel Macs, some virtual machines, Windows and Linux, it runs on
  the CPU as before. New sentences take about half as long as on the CPU (Tetris: 38–64 ms at the
  median instead of 89–99 ms), with the same scores at the default settings.
- **Less memory.** The app uses about 1 GB while the built-in agent plays on the GPU, down from
  about 1.8 GB (the Metal engine keeps the model's fp16 weights as they are).
- **Shows where it runs.** The side panel says "It runs on the GPU (Metal)" (or the CPU) while the
  agent plays, the log says "Laya loaded on the GPU (Metal) in 99ms", with the reason when the GPU
  engine didn't start, and **Test connection** names the engine. `LAYA_ENGINE=native` or
  `LAYA_ENGINE=metal` chooses one for testing.
- `cmd/headless` also reports the median and p99 of the decisions over 10 ms (about the ones with
  a new sentence).

## 0.2.0

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
- The app logs the model download's progress (every 10%) and, at the end of each game, how
  many decisions the agent made and how long they took (median, p99, slowest).
- **`SYSTEM1_AUTOSTOP_AFTER=<duration>`**, with `SYSTEM1_AUTOSTART`, stops the agent that long
  after launch, for unattended memory checks. Test-only and off unless set.
- `scripts/run-agent-vm.sh --builtin` plays with the built-in agent in a VM with no Python on the
  app's PATH, watching that no Python process ever starts; `--fresh-download` makes the app fetch
  the model itself, and `--stop-after` measures memory after the agent stops. Verified: all three
  games played in a VM with no Python, the app started no process, a fresh download matched the
  host's copy, and stopping the agent took the app from 1.7 GB to 186 MB. In a VM the model runs
  2.5–6 times slower than on the host, which costs Frogger points (and whole games when the host
  is busy); Tetris and Space Invaders played as on the host.
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
