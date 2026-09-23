# Connecting a custom agent

System 1 Arcade can be played by your own agent instead of the built-in Laya agent. You run an
HTTP service; the app sends it each decision as a short description plus typed questions, and plays
its answers. This page covers the endpoint contract, setting it up in the app, and connecting
existing services such as a standalone Laya server or TypeSafe's Jev.

For background on the question format, see [System 1 models](system-1-models.md).

## Quick start

1. Start the example agent. It needs only Python's standard library, and answers at random:

   ```sh
   python3 agents/custom_agent_example.py --port 8000
   ```

2. In the app, open **Settings** (side panel or start screen) and choose **Custom agent**.
3. Set **Endpoint URL** to `http://127.0.0.1:8000/predict` and leave **Send several short states in
   one request** on.
4. Click **Test connection**. It sends one small question and shows the answer and round-trip time.
5. Click **Save**, then start the agent from the side panel or the start screen.

Because the example guesses, it loses quickly. Copy
[`agents/custom_agent_example.py`](../agents/custom_agent_example.py) and replace its `decide()`
function with your own logic or model.

## The endpoint contract

For every decision the app sends `POST <your URL>` with `Content-Type: application/json`.

### Single state

```json
{
  "state": "Hopping up lands in a deadly place: a square a car will drive through in 0.3 seconds, so the frog would be hit.",
  "questions": {
    "a": {
      "type": "choice",
      "instructions": "What kind of place is it?",
      "criteria": {"safe": "a safe place", "deadly": "a deadly place"}
    }
  }
}
```

Respond with `200` and an answer for every question, under the same key:

```json
{"answers": {"a": {"type": "choice", "choice": "deadly", "probabilities": {"safe": 0.02, "deadly": 0.98}}}}
```

### Batch

A decision usually has several short states. With **Send several short states in one request**
on, the app sends them together:

```json
{
  "batch": {
    "up":   {"state": "Hopping up lands in a deadly place: …", "questions": {"a": {…}}},
    "stay": {"state": "Staying put keeps the frog in a safe place: safe ground.", "questions": {"a": {…}}}
  }
}
```

Answer each one under `"<batch key>.<question key>"`:

```json
{"answers": {"up.a": {…}, "stay.a": {…}}}
```

Answers grouped by batch key (`{"answers": {"up": {"a": {…}}}}`) are accepted too.

With the setting off, the app sends each state as its own single-state request, all at once in
parallel. Use that for services that only take single states, such as Laya's and Jev's predict
APIs.

### Question and answer types

| Type | Question fields | Answer fields the app reads |
|---|---|---|
| `choice` | `instructions`, `criteria`: `{option: description}` | `choice` **and** `probabilities`: `{option: p}` |
| `noul` (yes/no) | `instructions` | `noul`: probability of yes (0–1) |
| `score` | `instructions`, `criteria`: list of levels, lowest first | `score`: expected level (not used by the current games) |

Every answer also has `"type"`. Include `probabilities` for choice questions: Tetris and Frogger
rank options by probability, not only by the chosen option.

### What each game asks

Knowing the questions helps when you build a specialized agent. All three games send batches:

| Game | Batch keys | Question | Options or answer |
|---|---|---|---|
| Tetris | one per distinct landing spot, e.g. `p12s3` (piece 12, sentence 3) | `look`: "How does the stack look after the piece lands?" | `clean` / `messy`; the piece goes to the spot with the highest P(clean) |
| Frogger | `up`, `stay`, `left`, `right`, `down` | `a`: "What kind of place is it?" | `safe` / `deadly` |
| Frogger | `goal` | `a`: "Where is the nearest empty home?" | `up` / `left` / `right` |
| Space Invaders | `threat` | `a`: "Is a bomb about to hit the cannon?" | yes/no (`noul`) |
| Space Invaders | `left`, `right` | `a`: "Is it safe to move left / right?" | yes/no (`noul`) |
| Space Invaders | `escape` | `a`: "Which way is the open space?" | `left` / `right` |
| Space Invaders | `above` | `a`: "What is directly above the cannon?" | `invader` / `empty` / `shield` |
| Space Invaders | `aim` | `a`: "Where is the target?" | `left` / `right` / `here` |

The wording of states and questions may change as the games improve; the keys and options are the
stable part.

### Timing and errors

- Each request times out after 10 seconds.
- A non-2xx response, or a response without an `"answers"` object, is shown under **Agent** in the
  side panel, and the app retries about once a second. The game keeps running in realtime mode.
- The game stays paused until your agent's first successful answer, so slow startup (such as
  loading a model) costs nothing.
- Speed matters in realtime mode: the world keeps moving while your agent thinks. The built-in
  agent takes about 65–130 ms per decision. Lockstep mode (the **Clock** toggle) makes thinking
  time free.

## Settings reference

| Setting | What it does |
|---|---|
| Built-in agent / Custom agent | Which agent plays when you start one |
| Endpoint URL | Where the app sends requests (`http://` or `https://`) |
| API key | Optional; sent as `Authorization: Bearer <key>` |
| Model | Optional; sent as `"model"` in every request, which TypeSafe's API requires |
| Send several short states in one request | Batch mode on or off (see above) |
| Agent input speed | How often the agent may press a new input, from full speed down to 2 per second; applies to any agent |

Settings are saved in `settings.json` in the app's support folder (`~/Library/Application
Support/System 1 Arcade` on macOS).

## Connecting existing services

### Laya as a standalone server

Run the built-in agent's server yourself, for example on a machine with a stronger GPU:

```sh
python3 -m venv .venv && .venv/bin/pip install laya
.venv/bin/python agents/laya_server.py --port 8000                 # this machine only
.venv/bin/python agents/laya_server.py --host 0.0.0.0 --port 8000  # reachable from other machines
```

Then use `http://<host>:8000/predict` as the endpoint URL, with batching on. Only expose it on a
network you trust: the server has no authentication.

The server caches answers by state and question and only sends new ones to the model. `GET /stats`
reports the cache's hits, misses and hit rate; `--stats-every 10` also prints them every 10 seconds,
and `--cache-size 0` turns the cache off. The app itself doesn't cache a custom endpoint's answers,
since another model may not answer the same prompt the same way.

### TypeSafe Jev

[Jev](https://docs.typesafe.ai/api) uses the same question types and answer format but takes one
state per request and requires a model name:

| Setting | Value |
|---|---|
| Endpoint URL | `https://api.typesafe.ai/v1/systemone` |
| API key | your TypeSafe API key |
| Model | `jev-latest` |
| Send several short states in one request | off |

Jev hasn't been tested with System 1 Arcade yet. Expect higher latency than a local model, since
each decision sends several requests over the internet, and note that it is billed per input token.

### Other compatible servers

Several open projects serve Jev-style `/v1/systemone` endpoints, for example
[LitJev](https://github.com/zhengxuyu/litjev) (any Qwen model) and
[simple-jev](https://github.com/featherless-ai/simple-jev) (any open model). Configure them like Jev:
their URL, the model name they expect, and batching off. None have been tested here yet.

## Alternative: drive the game yourself

Instead of waiting to be called, an agent can pull prompts and push decisions through the app's
local API on `127.0.0.1:8765`: `GET /v1/laya` for the prompt, then `POST /v1/decide` with the
answers. `agents/laya_agent.py` works this way. See the API table in the
[README](../README.md#agents-that-drive-the-game-themselves).
