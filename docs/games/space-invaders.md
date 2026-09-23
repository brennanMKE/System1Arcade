# Space Invaders

![Laya playing Space Invaders](../images/invaders.png)

## Gameplay

Defend the ground from a marching formation of invaders.

- **The invaders:** 5 rows of 11. Squids score 30 points, crabs 20 and octopuses 10. The formation
  steps sideways 2 pixels at a time and drops 8 pixels whenever it reaches a wall. It speeds up as
  invaders are destroyed; the last one moves every tick. If it reaches the cannon's row, the game
  is over.
- **Bombs:** invaders drop bombs from the bottom of their columns, half of the time from the
  column nearest the cannon. At most 3 are falling at once on the first wave, up to 6 later, and
  they get faster each wave.
- **The cannon:** ←/→ slide it 1 pixel per tick; Space fires. Only one shot can be in the air at a
  time, so the gun is "reloading" until the shot hits something or leaves the screen.
- **Shields:** four shields stand above the cannon. Bombs and shots chip them away, and invaders
  chew through them. They're rebuilt each wave.
- **The UFO** crosses the top every 25 seconds and is worth 50, 100, 150 or 300 points.
- **Lives:** 3. Clearing a wave starts the next one a little lower, with faster bombs.

## What the agent is asked

Each decision asks about danger, room to move, and the target. The game sends six short states,
each with one question.

| Key | Example state | Question | Options |
|---|---|---|---|
| `threat` | "A bomb is falling straight at the cannon and will hit it in 0.4 seconds." or "No bomb is falling toward the cannon." | Is a bomb about to hit the cannon? | yes/no |
| `escape` | "The open space away from the falling bomb is to the left of the cannon." | Which way is the open space? | left / right |
| `left` | "The space to the left of the cannon is clear." or "A bomb is about to land to the left of the cannon, or the wall is there." | Is it safe to move left? | yes/no |
| `right` | (the same, for the right) | Is it safe to move right? | yes/no |
| `above` | "An invader is directly above the cannon.", "The cannon's own shield is directly above the cannon." or "The space directly above the cannon is empty." | What is directly above the cannon? | an invader / empty space / the cannon's own shield |
| `aim` | "The target is a little to the left of the cannon.", "The target is far to the right of the cannon." or "The cannon is lined up with its target." | Where is the target? | to the left / to the right / lined up with the cannon |

How the game works out each fact:

- **A bomb about to hit** is one falling over the cannon (not stopped by a shield first) that will
  land within 50 ticks (about 0.8 s), plus the time until the agent's next input is allowed at the
  chosen input speed. That leaves time to slide clear.
- **The open space** is the shorter way out of that bomb's path, avoiding the walls.
- **A side is safe** if sliding one step that way keeps the cannon clear of every bomb about to
  land.
- **What is directly above** is what a shot fired now would meet. The game predicts where the
  formation will be when the shot gets there, so "an invader" means the shot would hit.
- **The target** is the nearest position the cannon could fire from and hit an invader, allowing
  for the time to slide there and the shot's flight, and predicting the formation's march,
  including its turns at the walls. It prefers invaders in the outermost columns, as arcade players
  do: clearing the ends gives the formation farther to march before each drop. Within 16 pixels
  it's "a little" to that side; farther is "far".

The request, taken from a real game:

```json
{"batch": {
  "threat": {"state": "No bomb is falling toward the cannon.",
             "questions": {"a": {"type": "noul", "instructions": "Is a bomb about to hit the cannon?"}}},
  "escape": {"state": "There is open space on both sides of the cannon.",
             "questions": {"a": {"type": "choice", "instructions": "Which way is the open space?",
                                 "criteria": {"left": "to the left", "right": "to the right"}}}},
  "left":   {"state": "The space to the left of the cannon is clear.",
             "questions": {"a": {"type": "noul", "instructions": "Is it safe to move left?"}}},
  "right":  {"state": "The space to the right of the cannon is clear.",
             "questions": {"a": {"type": "noul", "instructions": "Is it safe to move right?"}}},
  "above":  {"state": "An invader is directly above the cannon.",
             "questions": {"a": {"type": "choice", "instructions": "What is directly above the cannon?",
                                 "criteria": {"invader": "an invader", "empty": "empty space",
                                              "shield": "the cannon's own shield"}}}},
  "aim":    {"state": "The cannon is lined up with its target.",
             "questions": {"a": {"type": "choice", "instructions": "Where is the target?",
                                 "criteria": {"left": "to the left", "right": "to the right",
                                              "here": "lined up with the cannon"}}}}
}}
```

With these answers the cannon fires if its gun is ready; otherwise it waits in position.

## How the answers become moves

`Decide` applies a fixed strategy, in order:

1. **A bomb is about to hit:** slide toward the open space.
2. **An invader is directly above and the gun is ready:** fire.
3. **The target is lined up:** wait for the gun or for the invader to arrive.
4. **The target is to one side:** slide that way if that side is safe; otherwise wait.

Slides are held for 6 ticks, or until the next input is allowed at slower input speeds. If the
next decision wants the same direction, the cannon keeps moving, and a different decision lets go
early, as a player would with a joystick. Everything goes through the same controller as the
keyboard, at the agent input speed set in Settings.

## What the side panel shows

- **Model sees:** all six sentences.
- **Agent:** the move and why, for example "bomb incoming → dodge left", "lined up → fire" or
  "in position → wait for the shot".
- One bar per question with Laya's answer and its probability.

## Perfect answers

The oracle answers from the same checks that write the sentences. With perfect answers the cannon
reaches waves 7–10.

## Notes

- Laya answers these questions the same way as the oracle (100% over 400 test states, except the
  open-space question when no bomb is coming, where its answer isn't used). It still loses lives
  that perfect answers avoid, so the gap is in timing rather than reading; see
  [Laya performance](../laya-performance.md).
- An earlier version asked "Would a shot fired now hit an invader?" as yes/no. Laya answered "no"
  even when an invader was lined up, so the cannon never fired. Asking what is directly above, with
  options that repeat the state's words, fixed it.
