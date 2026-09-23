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
| Frogger | 13 homes, levels 1 and 2 cleared without losing a life, 7,580 points (realtime, seed 3) | 11,000–23,000 points | Loses lives once traffic speeds up on level 3 |
| Space Invaders | Cleared wave 1, 1,810 points (realtime, seed 7) | Waves 7–10, 6,050–9,130 points | Loses lives to bombs that perfect answers avoid |

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

## Ways to improve

### Better answers

1. **Fine-tune Laya on the games.** The oracle gives a perfect label for every question at every
   step, so self-play can produce unlimited training data at no cost. Fine-tuning on it (or training
   a small calibration layer) should fix answers the wording can't. `eval_questions.py` already
   measures the result.
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
   Silicon and is reported to be much faster in its Tetris demo (up to 50 times, in under 1 GB of
   memory); [@receptron/laya](https://github.com/receptron/laya) runs it with ONNX Runtime from
   Node.js. Either could replace the PyTorch server behind the same endpoint.
7. **Count the model's delay in descriptions.** Frogger and Space Invaders already account for the
   input limit. They don't yet add the model's own 60–120 ms on a new sentence, which matters most
   once Frogger's traffic speeds up on level 3.

### Better measurement

8. **A repeatable benchmark.** Apart from Tetris, the numbers above are single runs. A script that
   plays many seeds per game and reports the mean and spread for Laya, the oracle and random answers
   would make improvements measurable.
9. **Realtime accuracy checks.** `eval_questions.py` checks answers in lockstep. Checking them in
   realtime would show how often an answer is right when asked but wrong by the time it's used.
