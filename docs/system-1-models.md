# System 1 models and how they drive the games

This page explains what a System 1 model is, how it differs from a chat-style language model, and
how System 1 Arcade turns its answers into joystick input.

## Fast and slow thinking

The name comes from Daniel Kahneman's *Thinking, Fast and Slow*. **System 1** is fast, automatic
judgment: glancing at the road and knowing a car is too close. **System 2** is slow, deliberate
reasoning: working out a route or doing arithmetic.

Large language models (LLMs) usually work like System 2. They reason by generating text token by
token, which is flexible but slow and costly for a decision that has to be made many times a
second. A **System 1 model** does one narrow thing quickly: it reads a situation and answers
questions about it with probabilities, in a single forward pass, without generating any text.

## What a System 1 model does

You give it two things:

- **A state:** the situation, usually a short piece of text (it can also be JSON).
- **Typed questions** about that state. There are three types:

| Type | Asks | Answer |
|---|---|---|
| `choice` | Which of these options fits? Options are named, each with a description. | the most likely option and a probability for every option |
| `noul` | Yes or no? | the probability of yes |
| `score` | Where on this ordered scale? (e.g. calm, frustrated, very angry) | the expected level and a probability per level |

Example request and answer (Laya and Jev use this shape):

```json
{"state": "A bomb is falling straight at the cannon and will hit it in 0.4 seconds.",
 "questions": {"threat": {"type": "noul", "instructions": "Is a bomb about to hit the cannon?"}}}

{"answers": {"threat": {"type": "noul", "noul": 0.93}}}
```

Properties that matter for games:

- **Fast.** Laya answers in tens of milliseconds on a GPU. On an Apple M4 Pro, one System 1 Arcade
  decision (several questions in one batch) takes about 65–120 ms.
- **All questions at once.** Every question is answered in the same forward pass, so asking six
  questions costs little more than asking one.
- **Probabilities, not prose.** Answers are numbers the program can threshold and compare. There is
  no text to parse, and nothing to go off-script.
- **Calibrated.** A probability of 0.9 is meant to be right about 90% of the time, so a game can
  act on confident answers and treat uncertain ones cautiously.
- **Deterministic.** The same state and questions always give the same answers.

### How Laya works inside

[Laya](https://huggingface.co/convaiinnovations/laya) is an open-weights (Apache 2.0) System 1 model
with about 421M parameters. For each question it builds a sequence of the state, the question and a
marker for each option, and reads a score at each marker; a softmax turns the scores into the
option probabilities. Each sequence is up to 512 tokens. All the questions for a state run as one
batch through the network. The Python package is `pip install laya`, and there are community
ports for Apple's MLX and ONNX runtimes (see [Laya performance](laya-performance.md)).

[Jev](https://docs.typesafe.ai/api), from TypeSafe AI, is the proprietary model that defined this
kind of API. It is a hosted service with the same question types and answer format.

## How System 1 Arcade uses one

A System 1 model can't plan a sequence of moves, count, or compare numbers well. What it does well
is read a clear situation and judge it. So the work is split:

| The game does | The model does |
|---|---|
| Works out the facts: positions, timing, what happens if the frog hops up | Reads each fact in plain English |
| Asks simple questions about those facts | Judges them: safe or deadly, clean or messy, which way |
| Turns the answers into a plan, then into button presses | |

Every decision is one round trip:

```
┌────────── game ───────────┐                    ┌──── System 1 model ────┐
│ describe the situation     │── short states  ──▶│ answer every question  │
│ as short sentences         │   + questions      │ in one forward pass    │
│                            │                    │                        │
│ Decide: answers → plan     │◀── probabilities ──│                        │
│ plan → button presses      │                    └────────────────────────┘
│ (same controller as keys)  │
└────────────────────────────┘
```

The model never sees buttons. The game's `Decide` function maps its answers to a plan (for
example "dodge left", or "rotate, then shift right three times, then drop"), and the engine presses
those buttons on the same virtual controller the keyboard uses, at a speed you choose in Settings.

### A worked example: Space Invaders

For one decision the game sends six short states, each with one question:

| State | Question | Options |
|---|---|---|
| "No bomb is falling toward the cannon." | Is a bomb about to hit the cannon? | yes / no |
| "There is open space on both sides of the cannon." | Which way is the open space? | left / right |
| "The space to the left of the cannon is clear." | Is it safe to move left? | yes / no |
| "The space to the right of the cannon is clear." | Is it safe to move right? | yes / no |
| "An invader is directly above the cannon." | What is directly above the cannon? | an invader / empty space / the cannon's own shield |
| "The target is a little to the left of the cannon." | Where is the target? | to the left / to the right / lined up with the cannon |

Its `Decide` then applies a fixed strategy: if a bomb is about to hit, move toward the open space;
otherwise, if an invader is above and the gun is ready, fire; otherwise slide toward the target if
that side is safe.

Frogger and Tetris work the same way. Frogger describes where each possible hop would land and
asks *safe or deadly?* Tetris describes every place the falling piece could land in one sentence
and asks *clean or messy?*, then plays the spot with the highest probability of "clean".

Each game's page lists its rules and every state and question it sends:
[Tetris](games/tetris.md), [Frogger](games/frogger.md), [Space Invaders](games/space-invaders.md).

## Writing situations a System 1 model reads well

These rules came from measuring Laya's answers against each game's perfect answers (see
[Laya performance](laya-performance.md) for the numbers):

1. **Do the arithmetic in the game.** Say "a car will drive through that square in 0.3 seconds, so
   the frog would be hit", not `{"dx": -3, "dy": 40}`.
2. **One short state per question.** A single sentence is read far more reliably than a paragraph
   of facts. A decision can send several states at once.
3. **Let the options repeat the state's words.** "What is directly above the cannon? an invader /
   empty space / the cannon's own shield" was answered correctly on every test state. A yes/no
   question about a consequence, "Would a shot fired now hit an invader?", was answered "no" even
   when an invader was lined up.
4. **Keep strategy in code.** Ask the model to judge situations, not to choose between complex
   plans; a choice between four described Tetris placements was close to random.
5. **Describe what will happen, not only what is.** The model answers after a delay and the agent
   acts after another, so descriptions look ahead: Frogger checks a square stays safe until the
   frog can hop again, and Space Invaders aims where an invader will be when the shot arrives.

## Further reading

- [Laya performance and ways to improve it](laya-performance.md)
- [Connecting a custom agent](custom-agents.md)
- [TypeSafe's Jev API reference](https://docs.typesafe.ai/api)
- [Awesome Open System One](https://github.com/rupeshpoojary9/awesome-open-system-one), a list of
  open models, runtimes and benchmarks
