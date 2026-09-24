# Changelog

Release notes for each version. `scripts/publish-release.sh` uses the section for the version
being released.

## 0.1.0

The first release: Tetris, Frogger and Space Invaders, played by you or by a System 1 model.

- **Three games** with keyboard controls, each playable by an agent through the same virtual
  controller: Tetris, Frogger and Space Invaders.
- **Built-in agent:** Laya runs locally. On first start the app sets up its own Python
  environment and downloads the model (about 1 GB, needs Python 3.10 or newer).
- **Custom agents:** point the app at any HTTP endpoint that answers typed questions with
  probabilities, such as a standalone Laya server or TypeSafe's Jev.
- **Answer cache:** Laya's answers are cached by state and question, so repeated sentences skip
  the model. At the default 6 inputs a second, Frogger averages about 19,700 points and Space
  Invaders reaches waves 3–5.
- **Richer Tetris sentences:** each landing spot says whether it fits snugly and whether it leaves
  or fills a deep well. In lockstep, Laya averages about 1,100 lines over 20 seeds.
- **Realtime and lockstep clocks**, an agent input speed setting, and a local `/v1` API for agents
  that drive the game themselves.
