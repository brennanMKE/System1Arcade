# Contributing to System 1 Arcade

Thanks for helping. Bug reports, fixes, new games, agent experiments and documentation are all
welcome. This page covers how to report a problem, build and test, and what a pull request needs.

## Windows and Linux contributions are welcome

The maintainer only has Macs. The app is built to run on Windows and Linux too, but nobody has
run it there yet, so anything you can build, run, measure or fix on either platform helps,
including a PR that only reports what happened. Start with
[Windows and Linux](docs/windows-linux.md): what is known to build, how to test, where the
platform-specific code is, the numbers to compare against, and a checklist for a first PR.

The built-in agent's speed off macOS depends on [laya-go](https://github.com/brennanMKE/laya-go),
which runs the model. Faster CPU kernels for Windows and Linux belong there; see its
[CONTRIBUTING.md](https://github.com/brennanMKE/laya-go/blob/main/CONTRIBUTING.md).

## Reporting issues

Open an issue at <https://github.com/brennanMKE/System1Arcade/issues> with:

- what you did, what you expected, and what happened
- the version (`wails.json`'s `productVersion`, or the release you downloaded) or the commit
- OS and version, CPU (and GPU on a Mac), and how you got the app (release DMG, `scripts/build.sh`, `wails dev`)
- for the agent: the game, seed, clock (realtime or lockstep), agent (built-in or custom URL), and
  the log lines that start with `built-in agent:` or `agent:`
- for a game that played badly: whether the oracle (`GET /v1/oracle`, `--policy oracle`) plays it
  well, which separates a model problem from a game-logic problem

## Setting up

You need Go 1.27 or newer and Node.js. Python is optional: it's only for the development tools in
`agents/`. On Linux you also need GTK 3 and WebKitGTK development packages; see
[Windows and Linux](docs/windows-linux.md#prerequisites).

```sh
git clone https://github.com/brennanMKE/System1Arcade.git
cd System1Arcade
scripts/build.sh                 # macOS or Linux; installs the pinned Wails CLI if missing
scripts/build.sh --python-tools  # optional: .venv with Python Laya for agents/*.py
```

```powershell
powershell -ExecutionPolicy Bypass -File scripts\build.ps1   # Windows
```

`wails dev` runs the app with live reloading of the frontend. The built-in agent downloads the
Laya model (about 800 MB) into the Hugging Face cache the first time it runs.

## Running tests

```sh
go vet . ./internal/... ./cmd/...
go test ./internal/...          # games, engine, API and agent loop; no model, no window
go test . ./internal/...        # also the app package
```

Every game has an oracle-ceiling test, so a change to a game's descriptions or decision logic that
breaks it shows up here.

Some tests use the Laya model. They skip when it isn't in the Hugging Face cache and never
download it:

| Test | Needs |
|---|---|
| `TestBuiltinAgent` | the cached model; skipped with `-short` |
| `TestLocalMatchesPython`, `TestLocalCache` (`internal/agent`) | the cached model (they read its tokenizer) and laya-go's golden fixtures from the module cache |
| `TestLocalModelParity` (`internal/agent`) | `SYSTEM1_LAYA_PARITY=1`; also `SYSTEM1_LAYA_PARITY_URL` to compare with a running Python server. Slow |
| `TestAgentLive` | `SYSTEM1_TEST_AGENT_URL` set to a running agent's predict URL |

For agent and game changes, play headless as well, which needs no window:

```sh
go run ./cmd/headless -agent laya -game frogger -mode lockstep -seed 1 -games 3
go run ./cmd/headless -agent laya -game frogger -mode realtime -pace 10 -seed 1 -games 3
```

[Laya performance](docs/laya-performance.md) has the scores to compare with.

**Launching the app from a script or test:** always set `SYSTEM1_SOUND=off`, so an unattended run
makes no noise and doesn't change the saved sound setting. `SYSTEM1_AUTOSTART=<game>[:<seed>]`
starts the agent without a click, and `SYSTEM1_ADDR` moves the `/v1` API off its default port. On
macOS, UI automation (synthesized keys or clicks, scripted screenshots) runs only inside a
disposable VM, never on the host; see [UI testing in a Tart VM](docs/ui-testing-vm.md).

## Code style

- `gofmt` and `go vet` clean. Match the code around you rather than introducing a new pattern.
- The app must keep building for Windows from macOS with `CGO_ENABLED=0`
  (`wails build -platform windows/amd64`). Platform code goes in files named `_darwin.go`,
  `_windows.go`, `_linux.go`, with a no-op in an `_other.go` file guarded by a build tag, as
  `activity_darwin.go` and `activity_other.go` do. On Windows, call system APIs from Go
  (`golang.org/x/sys/windows`), not cgo.
- No Python in the app: the built-in agent runs Laya in Go through laya-go. Python stays in
  `agents/` as development tooling.
- Doc comments say what a thing does and why, in full sentences.
- To add a game, implement `game.Game` (and `game.Advisor` for agents) and register it in
  `internal/games/registry.go`; see the [README](README.md#project-layout).

### Writing docs

Docs here are plain and concrete: short sentences, real numbers with the machine and conditions
they came from, and exact commands. Mark anything you haven't run as **Unverified** or
**Estimate**. Prefer a table to a long list when comparing things. Update the README or the doc in
`docs/` that a change affects in the same PR.

## Commits

- A short subject in the imperative, without a trailing period: "Keep an agent's press until a tick
  has seen it".
- A body, wrapped at about 72 columns, that says why and what changed, with the measurements
  behind it when there are any.
- One topic per commit. Version bumps and `CHANGELOG.md` release sections are done by the
  maintainer when releasing; a PR can add a bullet under `## Unreleased`.

## Pull requests

- Describe what changed and how you checked it: the commands you ran and their results.
- Add or update tests for behavior changes. `go vet` and `go test . ./internal/...` should pass.
- For agent or game changes, include scores before and after (game, seed, clock, pace).
- For platform work (Windows, Linux, or a different Mac): the OS and version, CPU and GPU, Go,
  Node and Wails versions, the build command, test results, and the numbers from the
  [checklist](docs/windows-linux.md#what-a-first-windows-or-linux-pr-should-report). A PR that
  only adds a report, or fixes a doc that turned out wrong, is welcome.
- Keep PRs focused. Open an issue first for a large change, such as a new release pipeline or a
  new game, so we can agree on the approach.

## License

System 1 Arcade is under the [MIT License](LICENSE). By submitting a contribution you agree that
it is licensed under the same terms. laya-go, which the app depends on, is Apache-2.0.
