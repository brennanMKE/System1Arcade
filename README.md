# System 1 Arcade

Tetris, Frogger and Space Invaders in Go + [Wails](https://wails.io). You can play them with the
keyboard, and an agent can play them through a local HTTP API. The API is built for System 1
decision models like [Laya](https://github.com/wdobry/laya-playground) (open weights) and TypeSafe's
Jev: they take a state plus typed questions and return calibrated probabilities in a single forward
pass, with no text generation.

```
┌──────────── Wails window ────────────┐        ┌───────── agent process ──────────┐
│ canvas renderer  ◀── "update" event ─┤        │ GET  /v1/laya                    │
│ keyboard ──▶ KeyDown/KeyUp ─┐        │        │   → laya.predict(state, questions)│
└─────────────────────────────┼────────┘        │ POST /v1/action {action, meta}   │
                              ▼                 └───────────────┬──────────────────┘
        ┌──────────── engine (60 ticks/s) ─────────────┐        │
        │ human buttons ∪ agent buttons → game.Input   │◀── HTTP :8765 (internal/api)
        │ game.Tick(input) → Frame / Observe / Status  │
        └──────────────────────────────────────────────┘
```

## Design

- **The agent is just another controller.** Games never see who pressed a button. Agent actions
  are named groups of virtual buttons (`left`, `fire`, `hard_drop`) held for a few ticks, and they
  go through the same input path as the keyboard. A human and an agent can even share control.
- **Games are deterministic, fixed-timestep simulations** (`internal/game.Game`). The same seed and
  inputs always produce the same game, so runs are reproducible and comparable.
- **Two clocks.** `realtime` runs at 60 ticks/s whether or not the model keeps up, so latency is
  part of the test. That is the point of a System 1 model. `lockstep` advances only when the agent
  calls `/v1/step`, so latency is free and you measure decision quality alone.
- **Observations are written for the model.** Each game's `Observe()` is compact, labeled JSON
  sized for Laya's ~512-token window: an ASCII board plus derived features (column heights, holes,
  neighbor cells, bomb dx/dy, target dx). `/v1/laya` wraps it with a `choice` question over the
  legal actions, ready to send to Laya as-is.
- **Games draw with draw lists.** Each game emits rects, sprites and text in its own logical pixel
  space. The frontend is one small canvas renderer, and Go owns all game logic.

## Layout

| Path | What |
|---|---|
| `internal/game` | `Game` interface, buttons, actions, draw primitives |
| `internal/games/{tetris,frogger,invaders}` | the games |
| `internal/engine` | fixed-step loop, input merging, modes, pub/sub, Laya request builder |
| `internal/api` | agent HTTP API |
| `app.go`, `main.go`, `frontend/` | Wails desktop shell |
| `cmd/headless` | same engine and API with no window, for batch runs and CI |
| `agents/laya_agent.py` | reference agent loop: Laya in-process, a Laya HTTP sidecar, or random |

## Run

```sh
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails dev                 # hot reload
wails build               # → build/bin/System 1 Arcade.app (use -platform windows/amd64, linux/amd64 on those hosts)
go run ./cmd/headless -game frogger -mode lockstep   # no window
go test ./internal/...
```

Keys: arrows/WASD to move, ↑ to rotate or hop, Z to rotate back, Space/X to fire or hard drop,
Enter to restart after game over, P to pause, R to restart.

The API listens on `127.0.0.1:8765`. Set `SYSTEM1_ADDR` to change it.

## Agents

The app opens paused on a launch screen: pick a game, then **Play it yourself** or **Watch the
agent play**. The side panel's **Start agent / Stop agent** button does the same at any time. The
game stays paused until the agent's first answer, then the agent plays whatever game is on screen,
follows the Realtime/Lockstep setting, and restarts after each game over.

**Settings** (in the side panel or on the launch screen) choose the agent:

- **Built-in agent:** Laya, run by the app as `agents/laya_server.py` with the project's `.venv`
  Python (`pip install laya` there). Set `SYSTEM1_ROOT` if the app can't find the project folder.
- **Custom agent:** your own HTTP service at a URL you enter, with an optional API key sent as a
  Bearer token. The app POSTs each prompt and plays the answers:

  ```
  POST <url>  {"state": "...", "questions": {name: {type, instructions, criteria?}}}
           -> {"answers": {name: {type, choice?, probabilities?, noul?, score?}}}
  ```

  That is the shape Laya's and Jev's predict endpoints use. Tetris sends one short state per landing
  spot as `{"batch": {key: {state, questions}}}` and expects `{"answers": {"key.name": ...}}`;
  turn off **Send several short states in one request** for endpoints that only take single states,
  and the app sends them one per request. `agents/custom_agent_example.py` is a standard-library
  starting point. **Test connection** checks an endpoint before you save.

Agents can also drive the game themselves through the app's own API (below), as
`agents/laya_agent.py` does from a terminal:

```sh
.venv/bin/python agents/laya_agent.py --game frogger -v                # realtime
.venv/bin/python agents/laya_agent.py --game tetris --lockstep -v      # the game waits for each decision
.venv/bin/python agents/laya_agent.py --policy oracle --game invaders  # perfect answers: the ceiling
.venv/bin/python agents/eval_questions.py --game frogger               # per-question accuracy vs the oracle
```

## Agent API

| Method | Path | Body | Notes |
|---|---|---|---|
| GET | `/v1/games` | | ids, controls, actions |
| POST | `/v1/load` | `{game, seed?}` | seed 0 = random |
| POST | `/v1/reset` | `{seed?}` | |
| POST | `/v1/pause` | `{paused?}` | omit to toggle |
| POST | `/v1/mode` | `{mode}` | `realtime` or `lockstep` |
| GET | `/v1/state` | | status, observation, legal actions, tick, seed |
| GET | `/v1/laya` | | `{state, questions}` for Laya `predict` / `/predict` |
| POST | `/v1/action` | `{action, hold_ticks?, hold_ms?, meta?}` | realtime; applies on the next tick |
| POST | `/v1/press` | `{buttons, hold_ticks?}` | raw buttons: left right up down a b start |
| POST | `/v1/step` | `{action?, ticks?, meta?}` | lockstep only; returns the new state |
| GET | `/v1/stream?every=N` | | Server-Sent Events of state every N ticks |

Example `/v1/laya` for Space Invaders (trimmed):

```json
{
  "state": {"cannon_x": 25, "can_fire": true, "in_danger": false,
            "threats": [{"dx": -3, "dy": 40}], "targets": [{"dx": 7, "kind": "octopus", "points": 10}],
            "invaders_left": 55, "lives": 3, "score": 0},
  "questions": {"action": {"type": "choice",
    "instructions": "You are the laser cannon at the bottom in Space Invaders. ...",
    "criteria": {"noop": "hold position this turn", "left": "slide the cannon left",
                 "right": "slide the cannon right", "fire": "shoot straight up; ..."}}}
}
```

## Ideas for experiments

- Ask more than one question per call, for example a `noul` for "is the cannon in danger?" or a
  `score` for stack risk. Laya answers them all in one pass.
- Compare the same seed in realtime and lockstep to measure what latency costs.
- Replace low-level taps with target-level choices (for example "which column should this Tetris
  piece go to?") and let a small controller carry out the taps.
- Add a game: implement `game.Game` and register it in `internal/games/registry.go`.
