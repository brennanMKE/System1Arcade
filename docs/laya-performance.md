# Laya performance and how to improve it

This page records how well [Laya](https://huggingface.co/convaiinnovations/laya) plays System 1
Arcade today, what changed its results most, and ideas for making it better. Laya is the open-weights
System 1 model the built-in agent uses; see [System 1 models](system-1-models.md) for background.

All measurements were taken on an Apple M4 Pro (64 GB) using Laya 0.3.6 (the default English
checkpoint, about 421M parameters). Unless a section says otherwise they ran on the GPU through
PyTorch's MPS backend, as `agents/laya_server.py` does; the built-in agent now runs Laya in Go,
on the GPU with Metal on Apple silicon and on the CPU elsewhere (see
[The built-in agent in Go](#the-built-in-agent-in-go) and [On the GPU with Metal](#on-the-gpu-with-metal)). Most come from single
runs on fixed seeds, not a formal benchmark, so treat them as indications rather than averages. The
Tetris results are over seeds 1–20, played headless in lockstep with no input speed limit.

## Summary

| Game | Laya's best observed run | With perfect answers | Main weakness |
|---|---|---|---|
| Tetris | 3,999 lines, level 399, 36,284,268 points (lockstep, seed 4, stopped at 10,000 pieces) | 598 lines on average over 20 seeds (3,000-piece cap) | Now and then a game ends early: the shortest of 20 ended at 392 lines |
| Frogger | 40 homes, reaching level 9, 24,890 points (realtime at 6 inputs/s, seed 2) | 11,000–23,000 points | Loses lives once traffic speeds up |
| Space Invaders | Reached wave 5, 4,900 points (realtime at 6 inputs/s, seed 4) | Waves 7–10, 6,050–9,130 points | Loses lives to bombs that perfect answers avoid |

"Perfect answers" is the *oracle*: the game answers its own questions from its true internal
state (`GET /v1/oracle`). It shows the best these questions and decision rules can do, which
separates a model problem from a game-logic problem. Tetris is the exception: its oracle ranks spots
with a fixed heuristic, and Laya now plays better than it (see
[What made the difference in Tetris](#what-made-the-difference-in-tetris)).

## The built-in agent in Go

Since this change, the built-in agent runs Laya inside the app with
[laya-go](https://github.com/brennanMKE/laya-go) (`internal/agent/local.go`) instead of starting
`agents/laya_server.py`. It first ran on the CPU (Apple Accelerate for the matrix multiplies on
macOS, plain Go elsewhere), which is what the rest of this section measures; on Apple silicon it
now runs on the GPU (see [On the GPU with Metal](#on-the-gpu-with-metal)), and the CPU engine is
the fallback.

**Same answers.** The local agent sends the prompt through the same JSON the HTTP client would
send (so criteria stay sorted by name, as the Python server received them), decodes it the way
the server does, asks laya-go once per decision (`PredictMany`) and rounds answers to 4 decimals
in the server's shapes. Checked against laya-go's golden fixtures, 343 recorded Tetris, Frogger and
Space Invaders requests answered by the Python reference (PyTorch fp32 CPU):

| Check | Result |
|---|---|
| App JSON for each request vs the recorded request | byte-identical, all 343 |
| Token ids and option markers the model reads | identical (every sequence found in the fixture) |
| Answers from Python's own logits, 343 batches and 3,605 single-state requests | identical to what the app decoded from Python (`TestLocalMatchesPython`) |
| Answers with laya-go's forward pass vs the Python reference | 17,978 of 18,003 fields equal at 4 decimals (99.86%), the rest 0.0001 apart; 3,476 of 3,476 choices agree |
| The same vs `laya_server.py --device cpu` over HTTP | the same figures |

The last two rows are `SYSTEM1_LAYA_PARITY=1 SYSTEM1_LAYA_PARITY_URL=… go test ./internal/agent -run ModelParity`.

**Speed.** A decision the answer cache hasn't seen, measured over golden requests with the cache
off:

| Decision | Built-in (Go, CPU) | Python server (MPS GPU), from above |
|---|---|---|
| Frogger or Space Invaders, 6 states | 120–137 ms | 63–110 ms |
| Tetris, 8–17 states | 310–470 ms | 100–130 ms |
| Answered from the cache | 0.05–0.1 ms | about 0.7–1 ms (HTTP included) |
| Loading the model (cached on disk) | 0.1–0.2 s | a few seconds, about 20 s from app start |

Fewer engine threads didn't help (8 threads: 120 ms; 4 threads: 137 ms for Frogger). In play, a
new sentence is usually only part of a decision, so misses cost 45–120 ms in Frogger and Space
Invaders and up to about 300 ms in Tetris, and more than 99% of decisions come from the cache.

**Scores at the default settings.** Realtime, 6 inputs per second, seeds 1–5, played headless with
`go run ./cmd/headless -agent <agent> -game <game> -mode realtime -pace 10 -seed 1 -games 3` (and
`-seed 4 -games 2`): the same agent loop the app runs, each agent's answer cache starting empty and
kept across its games. "Python/MPS" is `laya_server.py` on the GPU as a custom agent
(`-agent http://127.0.0.1:8766/predict`). Mean (sd):

| Game | Built-in (Go, CPU) | Python/MPS | Earlier, `laya_agent.py` in the app (above) |
|---|---|---|---|
| Frogger | 19,712 (5,708) | 19,712 (5,708) | 19,706 (5,748) |
| Space Invaders | 3,644 (1,197) | 2,692 (1,211) | 4,090 (742) |
| Tetris | 51,991 (10,425) | 55,906 (13,102) | 55,977 (12,927) |

Per seed:

| Seed | Frogger Go / MPS | Space Invaders Go / MPS | Tetris Go / MPS |
|---|---|---|---|
| 1 | 20,420 / 20,420 | 4,770 / 1,930 | 56,318 / 56,320 |
| 2 | 24,900 / 24,900 | 3,910 / 4,720 | 49,584 / 49,584 |
| 3 | 11,230 / 11,230 | 1,960 / 1,960 | 66,342 / 66,422 |
| 4 | 24,720 / 24,720 | 2,920 / 2,920 | 37,860 / 37,394 |
| 5 | 17,290 / 17,290 | 4,660 / 1,930 | 49,850 / 69,808 |

Frogger played the same games point for point. Space Invaders and Tetris games sometimes take a
different path from one timing difference and then diverge, in both directions; over these seeds
neither agent is ahead by more than the spread. So at the default input speed the CPU is fast
enough: the input limit and the cache, not the model, set the pace.

Two cautions:

- **Busy CPUs slow new sentences.** One Frogger run that overlapped a Go build ended at 570 points
  (misses took up to 440 ms), against 20,420 undisturbed.
- **App Nap slowed the app, not the game (fixed).** Launches of the built app with the built-in
  agent on Frogger seed 1 ended at 3,250–4,900 points, where headless play and the app with the
  Python/MPS server as a custom agent scored 20,420. The cause was App Nap: with the window hidden
  or the display asleep (unattended runs), macOS throttles the app about 30 s after launch, and
  its threads drop from priority 31 to 4. The engine still ticked at 60 Hz, but the forward pass,
  which now runs in the app's process, slowed: in a traced low run, misses took 200 ms–2.3 s
  (a 2.3 s miss let 79 ticks pass on an old answer), against at most 138 ms headless. A Python
  server runs in its own process, which isn't napped, so custom agents weren't affected. The app
  now holds an `NSProcessInfo` activity (user-initiated, latency-critical; idle system sleep still
  allowed) while any agent plays (`activity_darwin.go`), and releases it when the agent stops.

  Checks at the default settings (realtime, 6 inputs/s); the app runs were muted, unattended
  launches with `SYSTEM1_AUTOSTART`:

  | Run | Result |
  |---|---|
  | A test process wired like the app, no window (engine, JSON-encoding subscriber, app pace, built-in agent) | 20,420, 60.0 ticks/s |
  | App before the fix, Frogger seed 1, 6 launches | 4,460, 3,590, 4,900, 3,250, 4,890, and 20,420 once (slowest miss 186 ms: napped late or not at all) |
  | Same build with App Nap turned off (`NSAppSleepDisabled`) | 20,420, slowest miss 124 ms |
  | App with the fix, Frogger seed 1, 4 launches (the first traced) | 20,420 each; slowest miss 138 ms |
  | App with the fix, Space Invaders seed 1 | 3,830; headless the same night: 3,830 |
  | App with the fix, Tetris seed 1, 2 launches | 52,466 and 56,320; headless the same night: 56,298 |

  Tetris takes a different path now and then from one timing difference, headless too (56,298
  and 56,318 on the same seed), so one lower game is within its spread. Keeping the window
  visible avoided App Nap too, which may be why some earlier in-app runs were fine.

**Memory.** The built app used 1.80–1.85 GB (resident) while the built-in agent played (the
model's weights are held as float32). Stopping the agent frees the model: with the app's agent
manager in a test process, 1.70–1.72 GB while playing fell to 0.19–0.21 GB after stop, over two
start/stop rounds. In the app itself, in a VM (`SYSTEM1_AUTOSTOP_AFTER=60s`), 1.69 GB fell to
186 MB within 5 s of the stop; peaks while playing were 1.72–1.74 GB.

**In a VM, with no Python** (2026-09-27, [details](ui-testing-vm.md#the-built-in-agent---builtin)).
`scripts/run-agent-vm.sh --builtin` played all three games, seeds 1–3, twice, in the real app in a
Tart guest (6 CPUs) with no Python on the app's PATH; the app started no process in any game.
Default settings, realtime at 6 inputs/s, the game at 57–60 ticks/s:

| Game | Seeds 1 / 2 / 3, run A (no other VM) | Run B (another VM running) | Host, headless (above) |
|---|---|---|---|
| Frogger | 11,100 / 17,220 / 160 | 170 / 90 / 140 | 20,420 / 24,900 / 11,230 |
| Space Invaders | 2,940 / 2,710 / 3,910 | 3,060 / 2,950 / 4,850 | 4,770 / 3,910 / 1,960 |
| Tetris | 54,826 / 49,556 / 66,142 | 54,822 / 49,556 / 66,418 | 56,318 / 49,584 / 66,342 |

Decisions took 0.06–0.15 ms at the median (the cache), and the slowest per game 234–432 ms in
Space Invaders, 519–774 ms in Tetris, and in Frogger 287–349 ms in run A and 656–850 ms in run B,
against at most 138 ms on the host: the guest ran new sentences 2.5–6 times slower than the host,
more than the 1.5–2× expected, depending on what else the host was running. Tetris and Space
Invaders don't mind. Frogger does, as the Go-build caution above showed: it lost points in
run A, and when the host was loaded (other VMs and tests, load average around 30 on 12 cores),
games ended in the first 5–30 s. The app isn't the cause: under the same load, `laya-server` in
the guest as a custom agent (its own process) scored 220, 680 and 8,030, and the agent loop
headless in the guest, with no window, 140, 70 and 210. App Nap played no part: the app holds its
activity while an agent plays. With no model in the guest, the app downloaded 807 MB in about
10 s, showing `Downloading the Laya model (X of 807 MB)…`, checked every file's hash (they match
the host's copy), and was playing 12.3 s after launch (Frogger seed 1: 11,110).

### On the GPU with Metal

laya-go `1c1b85a` added a Metal engine: the whole forward pass on the GPU, with the checkpoint's
fp16 Linear weights kept as fp16 and everything else in fp32 (see laya-go's README, "Engines").
laya-go picks it by default ("auto") on Apple silicon when a Metal device is present and passes a
self-test at load, and falls back to the CPU engine otherwise; `LAYA_ENGINE=native` or `metal`
overrides that. The app logs which one runs (`built-in agent: Laya loaded on the GPU (Metal) in
99ms [metal fp16 weights (Apple M4 Pro)]`, with the reason when "auto" fell back), shows it in the
side panel while the agent plays, and **Test connection** names it.

**Scores at the default settings** (2026-09-27). Realtime, 6 inputs per second, seeds 1–5, with
`cmd/headless -agent laya … -seed 1 -games 3` and `-seed 4 -games 2` as above, one run at a time,
Metal (the default) against the CPU engine (`LAYA_ENGINE=native`), on the M4 Pro with a
light to moderate load from other work (load average 2–17 on 12 cores). Mean (sd):

| Game | Metal | CPU (`LAYA_ENGINE=native`) | CPU, earlier (above) | Python/MPS, earlier (above) |
|---|---|---|---|---|
| Frogger | 19,712 (5,708) | 19,712 (5,708) | 19,712 (5,708) | 19,712 (5,708) |
| Space Invaders, pass A | 4,112 (1,334) | 2,600 (1,837) | 3,644 (1,197) | 2,692 (1,211) |
| Space Invaders, pass B | 3,060 (1,548) | 3,206 (842) | | |
| Tetris | 55,994 (12,932) | 55,999 (12,941) | 51,991 (10,425) | 55,906 (13,102) |

Per seed:

| Seed | Frogger Metal / CPU | Space Invaders Metal / CPU, pass A; pass B | Tetris Metal / CPU |
|---|---|---|---|
| 1 | 20,420 / 20,420 | 4,510 / 950; 3,830 / 3,830 | 56,264 / 56,304 |
| 2 | 24,900 / 24,900 | 3,910 / 970; 3,910 / 3,910 | 49,584 / 49,598 |
| 3 | 11,230 / 11,230 | 1,960 / 1,960; 1,960 / 1,960 | 66,432 / 66,432 |
| 4 | 24,720 / 24,720 | 5,520 / 4,460; 940 / 3,610 | 37,882 / 37,852 |
| 5 | 17,290 / 17,290 | 4,660 / 4,660; 4,660 / 2,720 | 69,808 / 69,808 |

Frogger played the same games point for point on both engines, as it did on the CPU and on MPS
before, and Tetris within a few dozen points per seed. Space Invaders diverges from one timing
difference, in both directions: pass A favored Metal and pass B the CPU, and over the ten games
Metal averaged 3,586 (sd 1,470) against the CPU's 2,903 (1,384), within the spread. So at
the default input speed the engines score the same; the input limit and the answer cache set the
pace, as on the CPU before.

**Decision latency.** Over 99% of decisions come from the answer cache (median 0.06 ms in
Frogger and Space Invaders, 0.1–0.5 ms in Tetris) on either engine. The decisions over 10 ms are
about the ones with a new sentence, which run the model (`cmd/headless` now prints their median
and p99). Ranges over the per-game values of the five seeds (both Space Invaders passes):

| Game | Metal: median / p99 of new sentences | Metal: slowest | CPU: median / p99 | CPU: slowest |
|---|---|---|---|---|
| Frogger | 28–55 / 36–63 ms | 64 ms | 46–56 / 48–110 ms | 121 ms |
| Space Invaders | 31–61 / 57–76 ms | 76 ms | 47–88 / 50–143 ms | 143 ms |
| Tetris | 38–64 / 52–108 ms | 108 ms | 89–99 / 99–202 ms | 202 ms |

Metal answers new sentences in about half the CPU's time, and faster than the Python server on
MPS (63–110 ms for Frogger and Space Invaders, 100–130 ms for Tetris, above). Loading took
96–670 ms in the app and 0.1–0.2 s headless (the kernels are compiled at load), against
0.1–0.2 s on the CPU.

**Memory.** Peak resident memory of `cmd/headless` (from `/usr/bin/time -l`) was 0.90–0.98 GB
with Metal against 1.77–1.79 GB on the CPU. The built app peaked at 1.01–1.04 GB while the
built-in agent played each game on Metal, against 1.84 GB for the same app with
`LAYA_ENGINE=native` (and 1.80–1.85 GB before, above).

**In the app.** One muted, unattended launch per game (`SYSTEM1_SOUND=off`,
`SYSTEM1_AUTOSTART=<game>:1`, 90 s each) logged `Laya loaded on the GPU (Metal)`, and the agent
played: Frogger reached 10,090 points on level 3, Space Invaders 1,680, Tetris 10,380 on level 4
when stopped, with the slowest decision 62–95 ms. `LAYA_ENGINE=native` gave `Laya loaded on the
CPU`.

## Speed

These are the Python server's times on the GPU; the built-in agent's CPU times are
[above](#the-built-in-agent-in-go).

| Measurement | Result |
|---|---|
| Loading the model when the agent starts | a few seconds when cached; about 20 seconds from app start including Python and PyTorch |
| One call with one state, GPU (MPS) | 67–93 ms |
| The same call on the CPU | 165–740 ms |
| One Space Invaders decision (6 states) | about 63–65 ms, about 15 decisions per second |
| One Frogger decision (6 states) | about 64–110 ms |
| One Tetris decision (one state per distinct landing spot, often 10–17) | about 100–130 ms |
| A Frogger or Space Invaders decision answered from the cache | about 0.3 ms |

All the states for a decision go through Laya as one batch (`agents/laya_batch.py`), so a decision
costs little more than a single question. Running on the GPU matters: the CPU was 2–10 times slower.

### Answer cache

Laya always gives the same answer to the same prompt, and Frogger and Space Invaders keep repeating
the same sentences. The built-in agent, the Laya server (`agents/laya_server.py`) and
`agents/laya_agent.py` cache answers by the exact state and the question, and send only new ones to
the model, still as one batch. One minute per game through the server, seed 3:

| Game and clock | Cache | Decisions per second | Time per decision | Answers from the cache | Different answers seen |
|---|---|---|---|---|---|
| Frogger, realtime | off | 14.1 | 70 ms | — | — |
| Frogger, realtime | on | 1,087 | 0.3 ms | 99.98% (74 misses in 391,140) | 75 |
| Space Invaders, realtime | off | 15.1 | 65 ms | — | — |
| Space Invaders, realtime | on | 837 | 0.4 ms | 99.99% (21 misses in 301,176) | 96 |
| Frogger, lockstep | off | 12.5 | 77 ms | — | — |
| Frogger, lockstep | on | 943 | 0.4 ms | 99.96% (130 misses in 339,600) | 131 |
| Space Invaders, lockstep | off | 14.1 | 69 ms | — | — |
| Space Invaders, lockstep | on | 799 | 0.4 ms | 99.99% (26 misses in 287,658) | 157 |

With the cache on, the rest of the round trip (HTTP to the game and back) sets the pace; the model
ran for only 14–103 of about 50,000 decisions per game. The realtime rates include the agent asking
again before the next frame changes anything. In lockstep, where every decision is a new game step,
fewer than 160 different answers covered 32 Frogger games and 7 Space Invaders games in a minute.
Answers from a batch match answers asked alone to four decimal places, so a cached answer is the
same one the model would give. Tetris repeats too: in 30 seconds of lockstep play, 156 of 313,452
answers were misses, because its landing-spot sentences come from a small set of phrases.

`GET /stats` on the server reports hits and misses. Set `SYSTEM1_LAYA_CACHE=0` (or pass
`--cache-size 0`) to turn the cache off.

The agent's default input speed is 6 inputs per second (Settings → Agent input speed), well below
what Laya can decide, so in normal play the input limit, not the model, sets the pace.

#### Scores at the default settings

The real app, realtime at 6 inputs per second, driven by `agents/laya_agent.py` over `/v1`, seeds
1–5, mean (sd):

| Game | Before the cache | With the cache |
|---|---|---|
| Frogger | 11,600 (7,125) points, 19 homes | 19,706 (5,748) points, 32 homes |
| Space Invaders | 1,198 (505) points, wave 1–2 | 4,090 (742) points, waves 3–5 |
| Tetris | 49,100 (17,216) points, 117 lines | 55,977 (12,927) points, 139 lines |

At first the cache made Frogger and Space Invaders far worse (34 and 22 points on average). A
cached agent decides many times within one tick, and each decision let go of the previous press
before the game saw it, so taps (hops, fire) never landed. The engine now keeps a press until a
tick has seen it, and `TestFastAgentInRealtime` covers it. Tetris asks once per piece and wasn't
affected. At 6 inputs per second its games end at levels 9–16, when pieces fall faster than they
can be moved into place, so input speed limits Tetris here, not Laya's judgment.

## Question accuracy

`agents/eval_questions.py` plays a game with perfect answers and asks Laya the same questions at
every step, reporting how often Laya agrees. These results are over 400 states per game.

**Frogger:**

| Question | First wording | Final wording |
|---|---|---|
| Staying put is safe? | 51.5% | 100% |
| Where is the nearest empty home? | 84.2% | 100% |
| Hopping left is safe? | 96.2% | 100% |
| Hopping up is safe? | 97.8% | 100% |
| Hopping right / down is safe? | 100% | 100% |

The first wording was "Hopping up lands on turtles the frog can ride safely." with the question
"Is that safe for the frog?". The final wording says the category outright, "Hopping up lands in
a safe place: turtles the frog can ride safely.", and asks "What kind of place is it?" with the
options *a safe place* and *a deadly place*.

**Space Invaders:**

| Question | First wording | Final wording |
|---|---|---|
| Would a shot fired now hit an invader? → What is directly above the cannon? | 23.8% | 100% |
| Is a bomb about to hit the cannon? | 100% | 100% |
| Is it safe to move left / right? | 100% | 100% |
| Where is the target? | 100% | 100% |
| Which way is the open space? | 97.5% | 97.5% (misses only when no bomb is coming, when the answer isn't used) |

The yes/no question about a consequence was answered "no" whenever an invader was lined up, so the
first version never fired and scored 0. Replacing it with a choice whose options repeat the
state's own words ("an invader", "empty space", "the cannon's own shield") fixed it.

## What made the difference in Tetris

Tetris showed most clearly how much the framing of the question matters:

| Approach | Result |
|---|---|
| The board as JSON; one choice over tap actions (left, rotate, drop…) | often chose "do nothing"; 0–7 lines per game |
| One choice between the 4 best placements, each described with numbers | agreed with the best placement 27% of the time (chance is 25%) and chose option A in 36 of 40 cases |
| A yes/no or 3-level rating per placement | 11–19% agreement; 0–4 lines |
| One sentence per landing spot, placed in the question text | 4 lines |
| One sentence per landing spot as the state, asking "clean or messy?" | 116 lines in a 300-piece test; 442 lines in a full game; 328 lines on average over 20 seeds |
| **The same, plus whether the spot fits snugly and whether it leaves or fills a deep well; ties go to the flattest spot** | **1,124 lines on average over the same 20 seeds** |

The form follows [Laya's own Tetris demo](https://github.com/wdobry/laya-playground): each spot
becomes one sentence such as "The piece leaves no holes under it and makes a small bump on top. It
fits snugly.", and the piece goes to the spot with the highest P(clean).

### Richer sentences

With only holes, bump and lines, many spots read the same, and ties went to the leftmost spot. Each
change below was measured with Laya on the same seeds, in lockstep, with games capped at 3,000
pieces (about 1,200 lines):

| Sentences | Lines, mean of seeds 1–10 |
|---|---|
| Holes, bump and lines; ties to the leftmost spot | 369 |
| Ties to the flattest spot instead | 595 |
| "It clears one line" instead of "It completes one line" | 640 |
| Plus "It fits snugly" or "It fits loosely" | 811 |
| Plus "The next piece has no flat place to go." | 565 |
| Plus "The stack gets taller." (only 10 rows up or more) | 852 |
| Deep wells alone: "It leaves a deep well" or "It fills a deep well" | 1,020 (563 with ties to the leftmost spot) |
| **Clears, fit and deep wells** | **1,196: every game reached the cap** |

Over seeds 1–20 the final sentences averaged 1,124 lines (spread: 392 to 1,199, median 1,197; 17
of 20 games reached the cap), against 328 before (38 to 974, median 242). A piece now has about 14
distinct sentences instead of 11. With the cap raised to 10,000 pieces, the same 20 games averaged
2,788 lines (median 3,372); 8 reached the cap.

What Laya reacts to:

- **Short categorical phrases work; small modifiers don't.** "Makes the top a little bumpier"
  scored lower than "much bumpier", and "fills a gap, so the stack gets flatter" lower than "keeps
  the stack as flat as it was". "It fits snugly" and "It leaves a deep well" are yes-or-no facts,
  and Laya reads them reliably.
- **Fit is a comparison the game makes.** A spot fits snugly when no spot with the same holes and
  lines leaves a flatter surface. Like Frogger's "nearest empty home", the game compares and Laya
  gets the conclusion.
- **Not every true fact helps.** Describing whether the next piece would have a flat place to go
  cut the average from 811 to 565 lines.

Laya's answers now disagree with the oracle more, not less: `eval_questions.py` agreement on the
*clean or messy* question fell from 73.1% to 52.4% over 400 states, because Laya calls most spots
without holes *clean*. Only the ranking is used, and Laya's top spot matched the oracle's about as
often as before (86% of about 540 states, against 81%). The difference is which spots it disagrees
on. The oracle's heuristic doesn't count deep wells; Laya does, and it now outplays the oracle
(1,124 lines on average against 598).

Tetris is unforgiving about choosing well. Using the game's own placement ranking over five seeds:
always taking the best placement averaged about 2,050,000 points, picking randomly between the two
best averaged 5,823, and picking randomly among four averaged 1,191.

With the earlier sentences, realtime and lockstep gave the same result for the same game (seed 21
ended at 6,338 points after 81 pieces either way), so the gap was Laya's judgment, not speed.

## Input speed

Perfect-answer scores at different input speeds (seed 1, capped at 3,000 decisions):

| Game | Full speed | 6 inputs/s (default) | 2 inputs/s |
|---|---|---|---|
| Tetris | 132,586 | 49,006 | 19,090 |
| Frogger | 14,220 | 20,250 | 1,600 |
| Space Invaders | 2,300 | 2,140 | 410 |

Tetris loses the most, because at high levels pieces fall faster than a slow player can move them.
Frogger scores slightly higher at 6 inputs/s because its safety checks account for the delay.

## Fine-tuning on oracle labels

The oracle labels every question at every step, so self-play can produce training data at no cost.
This was tried for Frogger and Space Invaders, where the oracle is the ceiling, and it didn't make
Laya play better: base Laya already gives the oracle's answer to every question these games ask.

### Base Laya against the oracle

`agents/oracle_selfplay.py` plays seeds in lockstep, asks Laya and the oracle the same questions at
every step, and reports each game's score and the agreement per question. Seeds 1–20, games capped
at 100,000 decisions, with no input limit ("full speed") and at the app's default 6 inputs/s
(`go run ./cmd/headless -pace 10`):

| Game | Input speed | Oracle: mean score (spread) | Laya: mean score (spread) | Laya agrees with the oracle |
|---|---|---|---|---|
| Frogger | full speed | 24,136 (sd 5,505; 11,170–30,840) | 24,111 (sd 6,215; 5,060–30,980) | 100% of 36,227 decisions, all six questions |
| Frogger | 6 inputs/s | 20,907 (sd 3,836) | 21,488 (sd 4,454) | 100% of 35,656 decisions |
| Space Invaders | full speed | 8,128 (sd 2,350; 1,800–12,000) | the same games, point for point | 100%, except 99.77% on the open-space question |
| Space Invaders | 6 inputs/s | 5,765 (sd 1,264) | the same games | 100%, except 99.78% on the open-space question |

The one disagreement is "There is open space on both sides of the cannon.", where the oracle
sometimes answers *right*: the sentence doesn't say which side it means, so no model could learn it.
It never changed a game. In Space Invaders, Laya's games are identical to the oracle's. In Frogger
they differ only where both sides are safe: the frog takes the side with the higher P(safe), and
Laya's probabilities there differ slightly (turtles 0.9998, a log 0.994, safe ground 0.993, clear
road 0.986) where the oracle's are all 1. That makes no difference on average.

So the gap between Laya's realtime runs and the oracle is timing, not reading. In lockstep Laya
already plays as well as the oracle.

### What was trained

- **Data.** 80 games (seeds 1–40 at both input speeds) where each decision used Laya's answers or
  the oracle's at random, so states both reach are covered. Every prompt was labelled by the oracle.
  The games repeat a small set of sentences: 183 distinct prompts (155 Frogger, 28 Space Invaders),
  about 350 KB. Prompts whose label the sentence doesn't determine are dropped (none were, at the
  98% threshold). A fifth of the distinct prompts (46), chosen by a hash of the text, were kept out
  of training to check for memorized sentences.
- **Model.** Only the decision head: its 2 transformer layers, the question-type embedding and the
  scorer, 26.2M of 421M parameters. The ModernBERT encoder is frozen, so each prompt is encoded once.
  156 Tetris prompts from Laya's own games keep their base answers as targets, and an L2 pull toward
  the base head keeps it close to where it started.
- **Time.** 38 seconds for 40 epochs on the M4 Pro's GPU; about a minute including loading and
  encoding. The tuned head is a 100 MB file applied on top of the base model.

### Results

Answers (`agents/finetune.py` reports these before and after):

| Prompts | Base: accuracy, lowest P(right answer) | Tuned: accuracy, lowest P(right answer) |
|---|---|---|
| Training (137) | 100%, 0.495 | 100%, 0.930 |
| Held-out sentences (46) | 100%, 0.681 | 100%, 0.984 |
| Hand-written paraphrases not from the games (13) | 100%, 0.620 | 100%, 0.752 |

The tuned head is more confident: the least certain answer, "The target is a little to the right
of the cannon." at 0.495, went to 0.93, and sentences it never saw improved as much as ones it did.
But the answers were already all right, and the games only act on which answer wins.

Games, on held-out seeds 101–120 (Tetris on seeds 1–20, 3,000-piece cap), base head against tuned:

| Game | Base: mean (sd) | Tuned: mean (sd) | Seed by seed |
|---|---|---|---|
| Frogger, full speed | 25,762 (3,750) | 25,068 (2,857) | 8 better, 12 worse; mean change −694 (sd 3,853) |
| Frogger, 6 inputs/s | 21,798 (4,971) | 20,584 (6,112) | 9 better, 9 worse, 2 the same; mean change −1,215 (sd 5,281) |
| Space Invaders, full speed | 8,898 (1,977) | 8,898 (1,977) | identical games |
| Space Invaders, 6 inputs/s | 5,504 (1,453) | 5,504 (1,453) | identical games |
| Tetris, lines | 1,123 (median 1,197; 392–1,199) | 1,082 (median 1,197; 331–1,199) | 11 within 3 lines, 6 better, 3 worse (−867, −385, −289) |

Nothing improved. Space Invaders played the same games. Frogger's changes are within the spread:
the tuned head gives every safe move P(safe) = 1.00, so Frogger's left-or-right tie-break loses the
small preferences described above and games take different paths. Tetris moved slightly, even with
its prompts held to their base answers: 3 of 156 spots changed their top answer, and small changes
to P(clean) reorder spots, which changes whole games.

Tetris wasn't trained. Its oracle marks only the best spot for each piece as clean, so the same
sentence is labelled clean for one piece and messy for another (36 of 156 prompts had both labels),
and training toward it would teach the heuristic Laya already beats.

### Doing it again

```sh
go run ./cmd/headless -addr 127.0.0.1:8799 &            # add -pace 10 for the app's 6 inputs/s
.venv/bin/python agents/oracle_selfplay.py --api http://127.0.0.1:8799/v1 --game frogger \
    --seeds 1-30 --policy mix --out .laya-tuned/data/frogger-train.jsonl
.venv/bin/python agents/oracle_selfplay.py --api http://127.0.0.1:8799/v1 --game tetris \
    --seeds 201-210 --policy laya --out .laya-tuned/data/anchor-tetris.jsonl
.venv/bin/python agents/finetune.py --data '.laya-tuned/data/*-train.jsonl' \
    --anchor .laya-tuned/data/anchor-tetris.jsonl --out .laya-tuned/head
.venv/bin/python agents/oracle_selfplay.py --api http://127.0.0.1:8799/v1 --game frogger \
    --seeds 101-120 --policy laya --weights .laya-tuned/head
```

`.laya-tuned/` is ignored by git. `--weights` (or `SYSTEM1_LAYA_WEIGHTS`) loads a tuned head in
`agents/laya_server.py`, `agents/laya_agent.py` and `agents/oracle_selfplay.py`; without it they use
the base model. The app doesn't ship or download a tuned head: it wouldn't play better. The tooling is
worth rerunning when a new wording or a new game gives questions base Laya misreads.

## Ways to improve

### Better answers

1. **Fine-tune Laya on the games.** Tried: see [Fine-tuning on oracle labels](#fine-tuning-on-oracle-labels).
   In Frogger and Space Invaders base Laya already agrees with the oracle on every question and plays
   as well as it in lockstep, so a tuned head raised confidence but not scores. It becomes useful
   again if a new wording or game gives questions base Laya gets wrong.
2. **A better Tetris oracle.** Laya now outplays the oracle, so it's no longer a ceiling. Adding a
   deep-well penalty of 0.5 per well to the oracle's ranking raised it from 598 to 886 lines on
   average over seeds 1–20, but that weight was tuned on those same seeds.
3. **Try Laya's other checkpoints.** The package includes a `typed-decisions` fine-tune and a
   `multilingual` checkpoint (about 1.6 times faster, but uncalibrated). Neither has been tested here.
4. **Tune the thresholds.** Decisions use 0.5 for yes/no. Per-question thresholds, set with
   calibration tooling such as [jevcal](https://github.com/abhixhek/jevcal), could trade caution for
   speed where it matters (for example Frogger's safety checks).

### Faster decisions

5. **Cache answers.** Done: see [Answer cache](#answer-cache). More than 99.9% of answers in all
   three games now come from the cache, so a decision usually takes under a millisecond.
6. **Faster runtimes.** Done on Apple silicon: the built-in agent runs laya-go's Metal engine,
   which answers new sentences faster than the Python server on MPS and in about half the CPU
   engine's time (see [On the GPU with Metal](#on-the-gpu-with-metal)). Windows, Linux and Intel
   Macs still run on the CPU. [laya-mlx](https://github.com/mizorewww/laya-mlx) runs Laya natively on Apple
   Silicon (its demo plays Snake; claims of large speedups come from posts comparing it with Jev's
   hosted API, not from the repo); [@receptron/laya](https://github.com/receptron/laya) runs it with ONNX Runtime from
   Node.js. Either could replace the PyTorch server behind the same endpoint.
7. **Count the model's delay in descriptions.** Frogger and Space Invaders already account for the
   input limit. They don't yet add the model's own delay on a new sentence (30–75 ms on Metal,
   45–140 ms on the CPU), which matters most once Frogger's traffic speeds up on level 3.

### Better measurement

8. **A repeatable benchmark.** `agents/oracle_selfplay.py` now plays many seeds in lockstep and
   reports the mean and spread of scores and the agreement per question, for Laya (base or tuned) or
   the oracle. It doesn't yet cover realtime play or random answers.
9. **Realtime accuracy checks.** `eval_questions.py` checks answers in lockstep. Checking them in
   realtime would show how often an answer is right when asked but wrong by the time it's used.
