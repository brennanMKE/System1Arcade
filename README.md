<p align="center"><img src="System1Arcade.png" alt="System 1 Arcade icon: a red arcade joystick" width="160"></p>

# System 1 Arcade

Tetris, Frogger and Space Invaders as a desktop app for experimenting with **System 1 decision
models**, such as the open-weights [Laya](https://huggingface.co/convaiinnovations/laya) and TypeSafe's
Jev. These models don't generate text. They take a short state and typed questions, and return
calibrated probabilities in one fast forward pass. Each game is playable with the keyboard, and an
agent plays it through the same virtual controller a person uses.

Built with Go and [Wails](https://wails.io), so it runs on macOS, Windows and Linux.

| Tetris | Frogger | Space Invaders |
|---|---|---|
| ![Laya playing Tetris in lockstep: 83 lines, level 8](docs/images/tetris.png) | ![Laya playing Frogger on level 2, lining up with an open home](docs/images/frogger.png) | ![Laya playing Space Invaders: 18 invaders left on wave 1](docs/images/invaders.png) |
| Lockstep, 83 lines in. Best run: 3,999 lines, level 399, stopped at 10,000 pieces | Realtime, level 2. Best run: 40 homes, reaching level 9 | Realtime, 18 invaders left. Best run: reached wave 5 |

In each screenshot, Laya is the player. The side panel shows its latest decision, its answer to each
question, and the plain-English description it was given ("Model sees").

## Documentation

- [System 1 models and how they drive the games](docs/system-1-models.md): what a System 1 model
  is, and how the app turns its answers into joystick input
- Per game, the rules and exactly what is sent to the model: [Tetris](docs/games/tetris.md),
  [Frogger](docs/games/frogger.md), [Space Invaders](docs/games/space-invaders.md)
- [Laya performance and how to improve it](docs/laya-performance.md): measured speed, accuracy
  and scores, and what to try next
- [Connecting a custom agent](docs/custom-agents.md): the endpoint contract, Settings, and
  connecting a standalone Laya server or TypeSafe's Jev
- [Screenshots](docs/screenshots.md): capturing the app window from scripts
- [Releasing](docs/releasing.md): the signed, notarized DMG and the GitHub release
- [UI testing in a Tart VM](docs/ui-testing-vm.md): running the app window inside a disposable
  macOS VM, never on the Mac in use, and scoring a custom agent there with `scripts/run-agent-vm.sh`

## Quick start

On a Mac, download the DMG from the [latest release](https://github.com/brennanMKE/System1Arcade/releases/latest),
open it and drag System 1 Arcade to Applications. To build it yourself:

You need Go 1.27 or newer and Node.js; no Python. On Linux you also need the GTK 3 and WebKitGTK
development packages (e.g. `libgtk-3-dev libwebkit2gtk-4.1-dev`).

```sh
scripts/build.sh                # macOS or Linux
open "build/bin/System 1 Arcade.app"
```

```powershell
powershell -ExecutionPolicy Bypass -File scripts\build.ps1   # Windows
build\bin\System1.exe
```

The build scripts install the Wails CLI version pinned in `go.mod` if it's missing, check the
platform's prerequisites, and build for the current platform. Options: `--clean`, `--debug`,
`--test` (`-Clean`, `-DebugBuild`, `-Test` on Windows), and `--python-tools` (`-PythonTools`) to set
up `.venv` with Python Laya for the development tools in `agents/`. The Laya model (about 800 MB)
downloads the first time the agent runs. For live reloading while developing, use `wails dev`.

The app opens paused on a start screen. Pick a game, choose who plays (**You** or **Agent**) and
press **Start**. With the agent selected, the game stays paused until the agent's first answer,
because loading the model takes a few seconds.

<img src="docs/images/launch.png" alt="Start screen: choose the game and who plays, then Start" width="560">

Keys: arrows or WASD to move, ↑ to rotate or hop, Z to rotate back, Space or X to fire or hard drop,
Enter to restart after a game over, P to pause, R to restart, M to turn sound on or off.

Each game has retro sound effects, made in the app with the Web Audio API. Sound is on by default;
the **Sound** button or M turns it off, and the app remembers the choice. `SYSTEM1_SOUND=off` starts
a muted session without changing that choice, for automated runs (with `SYSTEM1_AUTOSTART`).

## How an agent plays

Every decision is one round trip:

1. The game describes what a player would see as **short plain-English states**, each with a typed
   question.
2. The agent answers with probabilities.
3. The game turns the answers into button presses: taps and holds on the same virtual controller
   as the keyboard.

```
game ──"A bomb is falling straight at the cannon and will hit it in 0.4 seconds."──▶ model
     ◀──────────────── threat: yes ──────────────────────────────────────────────────
     ──▶ press left for 6 ticks
```

What made Laya play well, learned by measuring its answers against a perfect "oracle":

- **The game does the arithmetic and gives conclusions in words.** Laya can't count or compare
  numbers. "A car will drive through that square in 0.3 seconds, so the frog would be hit" works;
  `dx: -3, dy: 40` does not.
- **One short state per question.** Laya reads a single sentence far more reliably than a paragraph
  of facts. Games send several short states at once as a batch.
- **Options repeat the state's own words.** "What is directly above the cannon? an invader / empty
  space / the cannon's own shield" was answered correctly in all 400 test states. "Would a shot fired
  now hit an invader?" got "no" even when an invader was lined up.
- **The game holds the strategy; the model reads the situation.** Each game's `Decide` turns the
  answers into a plan, for example "dodge the bomb, else fire if an invader is above, else line up".

### What each game asks

| Game | States and questions per decision | How answers become input |
|---|---|---|
| **Tetris** | One sentence per distinct landing spot ("The piece leaves no holes under it and makes a small bump on top. It clears one line. It fits snugly."), asked *clean or messy?* | The piece goes to the spot with the highest P(clean), and to the flattest one when spots read the same: rotate, shift and hard drop, one tap per tick |
| **Frogger** | One sentence per move ("Hopping up lands in a deadly place: a square a car will drive through in 0.3 seconds") asked *safe or deadly?*, plus where the nearest empty home is | Hop toward the goal if it's safe, else wait if staying is safe, else take the safest move |
| **Space Invaders** | Whether a bomb is about to hit, which way is open, whether each side is safe, what is directly above the cannon, where the next target is | Dodge, else fire when an invader is above, else slide toward the target (it leads moving targets) |

Situations are predictive where it matters. Frogger checks whether a square stays safe for the next
0.4 seconds, not just right now, and Space Invaders aims where the invader will be when the shot
arrives.

## Clocks: realtime and lockstep

- **Realtime** runs at 60 ticks per second whether or not the agent keeps up, like a person playing.
  The agent asks, waits for its answer, acts, and asks again, so its speed sets the pace. On an
  Apple M4 Pro the built-in agent answers a Frogger or Space Invaders decision it hasn't seen
  before in about 50–120 ms and a Tetris piece in about 300 ms; with the answer cache, almost every
  decision takes well under a millisecond.
- **Lockstep** freezes the game until the agent decides, then advances just enough to carry out the
  decision. Thinking time is free, so it measures decision quality alone, and the same seed and
  answers always replay the same game.

Switch with the **Clock** toggle in the side panel, or `POST /v1/mode`.

## Agents

Open **Settings** from the side panel or the start screen to choose the agent.

<img src="docs/images/settings.png" alt="Agent settings: built-in Laya or a custom endpoint URL" width="560">

### Built-in agent

The app runs Laya inside its own process with [laya-go](https://github.com/brennanMKE/laya-go), a
pure-Go port of the Laya package: no Python, no PyTorch, nothing to install. On macOS the model's
matrix math runs on Apple's Accelerate framework; elsewhere it runs in plain Go. Its answers match
the Python package's (see [Laya performance](docs/laya-performance.md#the-built-in-agent-in-go)).

The first time you start the agent, the app downloads the model (about 800 MB) from Hugging Face
into the standard Hugging Face cache (`~/.cache/huggingface/hub`, or `$HF_HUB_CACHE` / `$HF_HOME`),
showing progress while the game waits. A model the Python package already downloaded is used as is.
Loading it takes well under a second; the model stays in memory (the app uses about 1.7 GB while
the agent plays) and is freed when you stop it. `HF_HUB_OFFLINE=1` never downloads, `HF_TOKEN` is sent to Hugging Face if
set, and `SYSTEM1_LAYA_MODEL` names another checkpoint (a folder or a Hugging Face repo).

The built-in agent caches Laya's answers by state and question, since Laya always answers the same
prompt the same way, so a repeated sentence skips the model. Set `SYSTEM1_LAYA_CACHE=0` to turn the
cache off, or to a number to change how many answers it keeps (default 10,000).

The Python server, `agents/laya_server.py`, still works as a custom agent. On a Mac it runs Laya on
the GPU through PyTorch's MPS backend, which is faster for new sentences than the built-in agent's
CPU; see [Connecting a custom agent](docs/custom-agents.md#laya-as-a-standalone-server).

### Custom agent

Point the app at your own HTTP service. For every decision the app POSTs a prompt and plays the
answers:

```
POST <url>
{"state": "Hopping up lands in a safe place: clear road with no traffic coming, which is safe.",
 "questions": {"a": {"type": "choice", "instructions": "What kind of place is it?",
                     "criteria": {"safe": "a safe place", "deadly": "a deadly place"}}}}

→ {"answers": {"a": {"type": "choice", "choice": "safe", "probabilities": {"safe": 0.98, "deadly": 0.02}}}}
```

- A single-state request is the same shape Laya's and Jev's predict endpoints take.
- Answers are `choice` (`choice`, `probabilities`), `noul` (`noul` = P(yes)) or `score`.
- When a decision has several states, the app sends `{"batch": {key: {state, questions}}}` and
  expects answers keyed `"key.question"`. Turn off **Send several short states in one request** for
  endpoints that only take single states, and the app sends one request per state instead.
- An optional API key is sent as a Bearer token, and an optional model name as `"model"` (TypeSafe's
  Jev API needs `jev-latest`). **Test connection** checks the endpoint before you save.

See [Connecting a custom agent](docs/custom-agents.md) for the full contract, the questions each game
asks, and setting up Jev or a standalone Laya server.

[`agents/custom_agent_example.py`](agents/custom_agent_example.py) is a standard-library starting
point: replace its `decide()` with your model.

```sh
python3 agents/custom_agent_example.py --port 8000   # then set http://127.0.0.1:8000/predict
```

### Agents that drive the game themselves

The app also serves a local API on `127.0.0.1:8765` (set `SYSTEM1_ADDR` to change it), so an agent
can pull prompts and push decisions on its own schedule. `agents/laya_agent.py` does this from a
terminal:

```sh
.venv/bin/python agents/laya_agent.py --game frogger -v                 # realtime
.venv/bin/python agents/laya_agent.py --game tetris --lockstep -v       # the game waits for each decision
.venv/bin/python agents/laya_agent.py --game invaders --policy oracle   # perfect answers: the ceiling
```

| Method | Path | Body | Notes |
|---|---|---|---|
| GET | `/v1/games` | | ids, titles, controls and actions |
| POST | `/v1/load` | `{game, seed?}` | switch game; seed 0 picks one |
| POST | `/v1/reset` | `{seed?}` | restart the current game |
| POST | `/v1/pause` | `{paused?}` | omit `paused` to toggle |
| POST | `/v1/mode` | `{mode}` | `realtime` or `lockstep` |
| GET | `/v1/laya` | | the prompt: `{state, questions}` or `{batch}` |
| POST | `/v1/decide` | `{answers, meta?}` | the game acts on the answers; in lockstep it also advances and returns the new state |
| GET | `/v1/oracle` | | the game's own correct answers to the current prompt |
| GET | `/v1/state` | | status, tick, seed, mode and a structured observation |
| GET | `/v1/frame` | | the current draw list |
| POST | `/v1/action` | `{action, hold_ticks?, hold_ms?, meta?}` | low-level: press a named action |
| POST | `/v1/press` | `{buttons, hold_ticks?}` | low-level: press raw buttons (`left right up down a b start`) |
| POST | `/v1/step` | `{action?, ticks?, meta?}` | low-level lockstep: act and advance |
| GET | `/v1/stream?every=N` | | Server-Sent Events of the state every N ticks |

## Measuring an agent

- **Oracle.** Every game can answer its own questions perfectly from its true internal state
  (`GET /v1/oracle`, `--policy oracle`). If the oracle scores low, the game's descriptions or
  decision logic are wrong. If the oracle scores high but the model doesn't, the model is misreading
  the descriptions.
- **Per-question accuracy.** `agents/eval_questions.py` plays with the oracle and compares the
  model's answer to every question, printing the sentences it gets wrong:

  ```sh
  .venv/bin/python agents/eval_questions.py --game frogger --api http://127.0.0.1:8799/v1
  ```
- **Headless runs.** `go run ./cmd/headless -addr 127.0.0.1:8799` serves the same API without a
  window, for benchmarks and CI (`-pace 10` limits the agent to the app's default 6 inputs/s).
  `agents/oracle_selfplay.py` plays many seeds there and reports the mean and spread of scores.
  With `-agent laya` the headless command plays by itself with the built-in agent, with no Python;
  `-agent <url>` uses a custom agent instead:

  ```sh
  go run ./cmd/headless -agent laya -game frogger -mode realtime -pace 10 -seed 1 -games 3
  ```
- **Screenshots.** `scripts/screenshot.sh` captures the app window; see
  [docs/screenshots.md](docs/screenshots.md).

## Project layout

| Path | What |
|---|---|
| `internal/game` | the `Game` interface, virtual buttons, draw lists, and the `Advisor` interface (prompt → answers → decision) |
| `internal/games/{tetris,frogger,invaders}` | the games and their advisors |
| `internal/engine` | fixed 60 Hz loop, merging keyboard and agent input, realtime and lockstep, decision queue |
| `internal/agent` | the loop that plays a game with an agent: the built-in Laya agent (`local.go`, laya-go in the app's process) and the HTTP client for custom agents |
| `internal/api` | the local `/v1` HTTP API |
| `app.go`, `agent.go`, `main.go`, `frontend/` | the Wails desktop app: canvas renderer, start screen, settings, agent panel |
| `cmd/headless` | the engine and API without a window |
| `agents/` | Python development tools: a Laya server (usable as a custom agent), terminal agent, custom agent example, batching helper, accuracy and fine-tuning tools |
| `scripts/` | `build.sh` and `build.ps1` for the current platform; `screenshot.sh` for window captures |

To add a game, implement `game.Game` (and `game.Advisor` for agents) and register it in
`internal/games/registry.go`.

```sh
go test . ./internal/...   # includes an oracle-ceiling test for every game and Laya parity tests
```

## Known limitations

- Laya's Tetris games still end early now and then. Of 20 seeds played to 10,000 pieces, 8 reached
  the cap, and the shortest game ended at 392 lines.
- Frogger's safety window doesn't yet account for decision latency, which costs lives once traffic
  speeds up on level 3.
- Space Invaders loses lives that perfect answers avoid; the oracle reaches waves 7–10.
- The built-in agent's first start downloads the Laya model (about 800 MB).
- The built-in agent runs Laya on the CPU. A decision the answer cache hasn't seen takes longer than
  on the GPU through the Python server (see [Laya performance](docs/laya-performance.md#the-built-in-agent-in-go)).
  Its speed on Windows and Linux, where no Accelerate framework exists, hasn't been measured.
- The build scripts are tested on macOS; the Linux and Windows paths haven't been run yet.
