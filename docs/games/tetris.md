# Tetris

![Laya playing Tetris](../images/tetris.png)

## Gameplay

A NES-style falling-block game on a 10×20 board.

- **Pieces:** the seven tetrominoes (I, O, T, S, Z, J, L), dealt from a shuffled bag of all seven so
  none is starved. The next piece is shown.
- **Controls:** ←/→ move, ↑ rotates clockwise, Z rotates counterclockwise, ↓ soft-drops, Space
  hard-drops. Holding ←/→ repeats after 10 ticks, then every 3 ticks. Rotation nudges the piece up
  to two columns sideways, or one row up, if it would otherwise overlap.
- **Gravity:** the piece falls one row every 48 ticks (0.8 s) on level 0, speeding up each level to
  one row every 2 ticks from level 19. The level goes up every 10 lines.
- **Locking:** a piece locks as soon as it can't fall any further.
- **Scoring:** 40, 100, 300 or 1,200 points for clearing 1–4 lines at once, times (level + 1), plus
  1 point per soft-dropped row and 2 per hard-dropped row.
- **Game over:** a new piece has no room to appear, or a piece locks above the top.

The game runs at 60 ticks a second and is deterministic: the same seed and inputs always replay the
same game.

## What the agent is asked

A Tetris decision is **where to put the falling piece**, made once per piece.

### 1. The game lists every landing spot

For the falling piece, the game tries every rotation and every column it can actually reach by
rotating first and then sliding, and drops it straight down. Spots that end up with the same cells
are counted once. A piece has between 9 (the O piece) and 34 spots.

For each spot the game measures the board as it would be afterwards:

- **Holes:** empty cells the piece would cover over, compared with now.
- **Bump:** how much rougher the surface gets, or how far the piece sticks up above the average
  column, whichever is worse.
- **Lines:** rows the piece would complete.

### 2. Each spot becomes one sentence

A System 1 model can't count or compare numbers, so the game turns the measurements into words:

| Measurement | Words |
|---|---|
| holes | no holes, one hole, two holes, three holes, many holes |
| bump | no bump, a small bump, a big bump, a tall tower |
| lines | (nothing), one line, two lines, three lines, four lines |

> The piece leaves no holes under it and makes no bump on top.
>
> The piece leaves one hole under it and makes a tall tower on top.
>
> The piece leaves no holes under it and makes a small bump on top. It completes one line.

Spots that read the same share a sentence, so each distinct sentence is asked only once.

### 3. One short state per sentence, one question each

Every distinct sentence is its own state in a batch, with the same question:

```json
{"batch": {
  "p26s7": {"state": "The piece leaves no holes under it and makes no bump on top.",
            "questions": {"look": {"type": "choice",
              "instructions": "How does the stack look after the piece lands?",
              "criteria": {"clean": "flat with no holes", "messy": "holes or a tall tower"}}}},
  "p26s11": {"state": "The piece leaves one hole under it and makes a tall tower on top.",
             "questions": {"look": {…same question…}}},
  …
}}
```

Keys are `p<piece number>s<sentence number>`. The piece number means an answer that arrives after
the piece has landed is never applied to the next one.

In a real game with 17 distinct sentences for one piece, the perfect answer called exactly one of
them *clean*: "The piece leaves no holes under it and makes no bump on top."

### 4. The answers become a plan

`Decide` ranks the spots by their sentence's probability of *clean* and takes the best one the piece
can still reach from where it is now. It then plans the key presses a player would make:

1. rotate (clockwise once or twice, or counterclockwise once),
2. slide left or right one column at a time,
3. hard drop.

For example: `rotate_ccw left left hard_drop`. The presses go through the same controller as the
keyboard, at the agent input speed set in Settings (6 per second by default), so you can watch the
piece move into place. The game asks about each piece only once; while a plan is running, there's
nothing to ask.

## What the side panel shows

- **Model sees:** the falling piece and how many distinct landing spots Laya is reading.
- **Agent:** the planned presses and the chosen sentence with its P(clean), for example
  "P(clean) 0.95: The piece leaves no holes under it and makes no bump on top."
- One bar per sentence with Laya's answer (clean or messy, and its probability).

## Perfect answers

The oracle ranks spots with a standard Tetris heuristic (weights from Yiyuan Lee's near-perfect
player: aggregate height −0.51, lines +0.76, holes −0.36, bumpiness −0.18) and answers *clean* for
the best spot only.

## Notes

- Tetris rewards choosing the best spot nearly every time: with the heuristic's ranking, picking
  randomly between the two best spots scores about 1/350 of always picking the best.
- At slower input speeds a piece takes longer to slide into place, so at high levels, where gravity
  is fast, it can land before reaching its spot, as it would for a person.
- See [Laya performance](../laya-performance.md) for how earlier framings of this question fared.
