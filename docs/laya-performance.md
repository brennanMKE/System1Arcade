# Laya performance and how to improve it

This page records how well [Laya](https://huggingface.co/convaiinnovations/laya) plays System 1
Arcade today, what changed its results most, and ideas for making it better. Laya is the open-weights
System 1 model the built-in agent uses; see [System 1 models](system-1-models.md) for background.

All measurements were taken on an Apple M4 Pro (64 GB) using Laya 0.3.6 (the default English
checkpoint, about 421M parameters) on the GPU through PyTorch's MPS backend. They come from single
runs on fixed seeds, not a formal benchmark, so treat them as indications rather than averages.

## Summary

| Game | Laya's best observed run | With perfect answers | Main weakness |
|---|---|---|---|
| Tetris | 442 lines, level 44, 697,102 points (lockstep, seed 5) | several hundred thousand points | Varies by game: seed 21 ends at 21 lines |
| Frogger | 13 homes, levels 1 and 2 cleared without losing a life, 7,580 points (realtime, seed 3) | 11,000–23,000 points | Loses lives once traffic speeds up on level 3 |
| Space Invaders | Cleared wave 1, 1,810 points (realtime, seed 7) | Waves 7–10, 6,050–9,130 points | Loses lives to bombs that perfect answers avoid |

"Perfect answers" is the *oracle*: the game answers its own questions from its true internal
state (`GET /v1/oracle`). It shows the best these questions and decision rules can do, which
separates a model problem from a game-logic problem.

## Speed

| Measurement | Result |
|---|---|
| Loading the model when the agent starts | a few seconds when cached; about 20 seconds from app start including Python and PyTorch |
| One call with one state, GPU (MPS) | 67–93 ms |
| The same call on the CPU | 165–740 ms |
| One Space Invaders decision (6 states) | about 63–65 ms, about 15 decisions per second |
| One Frogger decision (6 states) | about 64–110 ms |
| One Tetris decision (one state per distinct landing spot, often 10–17) | about 100–130 ms |

All the states for a decision go through Laya as one batch (`agents/laya_batch.py`), so a decision
costs little more than a single question. Running on the GPU matters: the CPU was 2–10 times slower.

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
| **One sentence per landing spot as the state, asking "clean or messy?"** | **116 lines in a 300-piece test; 442 lines in a full game** |

The winning form follows [Laya's own Tetris demo](https://github.com/wdobry/laya-playground): each
spot becomes one sentence such as "The piece leaves no holes under it and makes a small bump on
top. It completes one line.", and the piece goes to the spot with the highest P(clean).

Tetris is unforgiving about choosing well. Using the game's own placement ranking over five seeds:
always taking the best placement averaged about 2,050,000 points, picking randomly between the two
best averaged 5,823, and picking randomly among four averaged 1,191.

Realtime and lockstep now give the same result for the same game (seed 21 ends at 6,338 points
after 81 pieces either way), so the remaining gap is Laya's judgment, not speed.

## Input speed

Perfect-answer scores at different input speeds (seed 1, capped at 3,000 decisions):

| Game | Full speed | 6 inputs/s (default) | 2 inputs/s |
|---|---|---|---|
| Tetris | 265,042 | 46,134 | 19,506 |
| Frogger | 14,220 | 20,250 | 1,600 |
| Space Invaders | 2,300 | 2,140 | 410 |

Tetris loses the most, because at high levels pieces fall faster than a slow player can move them.
Frogger scores slightly higher at 6 inputs/s because its safety checks account for the delay.

## Ways to improve

### Better answers

1. **Fine-tune Laya on the games.** The oracle gives a perfect label for every question at every
   step, so self-play can produce unlimited training data at no cost. Fine-tuning on it (or training
   a small calibration layer) should close most of the Tetris gap. `eval_questions.py` already
   measures the result.
2. **Richer Tetris sentences.** Many landing spots currently read the same ("no holes, a small
   bump"), and ties go to the leftmost spot. More distinctions in words (stack height, deep wells,
   filling a gap, what the next piece needs) would let Laya separate good spots from great ones.
3. **Try Laya's other checkpoints.** The package includes a `typed-decisions` fine-tune and a
   `multilingual` checkpoint (about 1.6 times faster, but uncalibrated). Neither has been tested here.
4. **Tune the thresholds.** Decisions use 0.5 for yes/no. Per-question thresholds, set with
   calibration tooling such as [jevcal](https://github.com/abhixhek/jevcal), could trade caution for
   speed where it matters (for example Frogger's safety checks).

### Faster decisions

5. **Cache answers.** Laya is deterministic, and Frogger and Space Invaders repeat the same
   sentences many times ("No bomb is falling toward the cannon."). Caching answers by state and
   question would skip most model calls.
6. **Faster runtimes.** [laya-mlx](https://github.com/mizorewww/laya-mlx) runs Laya natively on Apple
   Silicon and is reported to be much faster in its Tetris demo (up to 50 times, in under 1 GB of
   memory); [@receptron/laya](https://github.com/receptron/laya) runs it with ONNX Runtime from
   Node.js. Either could replace the PyTorch server behind the same endpoint.
7. **Count the model's delay in descriptions.** Frogger and Space Invaders already account for the
   input limit. They don't yet add the model's own 60–120 ms, which matters most once Frogger's
   traffic speeds up on level 3.

### Better measurement

8. **A repeatable benchmark.** The numbers above are single runs. A script that plays many seeds
   per game and reports the mean and spread for Laya, the oracle and random answers would make
   improvements measurable.
9. **Realtime accuracy checks.** `eval_questions.py` checks answers in lockstep. Checking them in
   realtime would show how often an answer is right when asked but wrong by the time it's used.
