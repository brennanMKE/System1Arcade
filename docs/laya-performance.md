# Laya performance and how to improve it

This page records how well [Laya](https://huggingface.co/convaiinnovations/laya) plays System 1
Arcade today, what changed its results most, and ideas for making it better. Laya is the open-weights
System 1 model the built-in agent uses; see [System 1 models](system-1-models.md) for background.

All measurements were taken on an Apple M4 Pro (64 GB) using Laya 0.3.6 (the default English
checkpoint, about 421M parameters) on the GPU through PyTorch's MPS backend. Most come from single
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

## Speed

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
the same sentences. The Laya server (`agents/laya_server.py`, which the built-in agent runs) and
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
6. **Faster runtimes.** [laya-mlx](https://github.com/mizorewww/laya-mlx) runs Laya natively on Apple
   Silicon (its demo plays Snake; claims of large speedups come from posts comparing it with Jev's
   hosted API, not from the repo); [@receptron/laya](https://github.com/receptron/laya) runs it with ONNX Runtime from
   Node.js. Either could replace the PyTorch server behind the same endpoint.
7. **Count the model's delay in descriptions.** Frogger and Space Invaders already account for the
   input limit. They don't yet add the model's own 60–120 ms on a new sentence, which matters most
   once Frogger's traffic speeds up on level 3.

### Better measurement

8. **A repeatable benchmark.** `agents/oracle_selfplay.py` now plays many seeds in lockstep and
   reports the mean and spread of scores and the agreement per question, for Laya (base or tuned) or
   the oracle. It doesn't yet cover realtime play or random answers.
9. **Realtime accuracy checks.** `eval_questions.py` checks answers in lockstep. Checking them in
   realtime would show how often an answer is right when asked but wrong by the time it's used.
