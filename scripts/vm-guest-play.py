#!/usr/bin/env python3
"""Guest side of scripts/run-agent-vm.sh: play games in the real app and record scores.

Runs only inside a Tart VM (it refuses to run on a host Mac). For each game and seed it launches
the app with SYSTEM1_AUTOSTART=<game>:<seed>, which starts the agent chosen in Settings exactly as
pressing Start does, then polls GET /v1/state until the game is over or the time cap passes, and
appends one JSON line per game to <results>/results.jsonl. The app is quit between games, so each
game starts from a fresh launch.

Standard library only; the guest's /usr/bin/python3 is enough.
"""
import argparse
import json
import os
import subprocess
import sys
import time
import urllib.request

APP_BIN = "System1"  # the executable inside the .app (wails.json outputfilename)


def in_vm():
    try:
        out = subprocess.run(["sysctl", "-n", "kern.hv_vmm_present"], capture_output=True, text=True).stdout
        return out.strip() == "1"
    except OSError:
        return False


def get_json(url, timeout=2.0):
    with urllib.request.urlopen(url, timeout=timeout) as r:
        return json.load(r)


def quit_app():
    subprocess.run(["pkill", "-x", APP_BIN], capture_output=True)
    for _ in range(50):
        if subprocess.run(["pgrep", "-x", APP_BIN], capture_output=True).returncode != 0:
            return
        time.sleep(0.1)
    subprocess.run(["pkill", "-9", "-x", APP_BIN], capture_output=True)
    time.sleep(0.5)


def play(args, game, seed, log_dir):
    api = f"http://{args.api}/v1/state"
    quit_app()
    log = os.path.join(log_dir, f"app-{game}-{seed}.log")
    launched = time.monotonic()
    subprocess.run(["open", "-n", "-a", args.app,
                    "--env", f"SYSTEM1_AUTOSTART={game}:{seed}",
                    "--stdout", log, "--stderr", log], check=True)

    # Wait for the app's API, on the right game and seed.
    st, err = None, ""
    while time.monotonic() - launched < args.launch_timeout:
        try:
            st = get_json(api)
            if st["game"] == game and st["seed"] == seed:
                break
            err = f"app is on {st['game']} seed {st['seed']}"
        except Exception as e:  # not up yet
            err = str(e)
        st = None
        time.sleep(0.2)
    if st is None:
        quit_app()
        return {"game": game, "seed": seed, "error": f"app API not ready after {args.launch_timeout}s: {err}"}
    api_ready = time.monotonic() - launched

    # The game stays paused until the agent's first answer.
    first_answer = None
    last = st
    ended = "cap"
    start = time.monotonic()
    while time.monotonic() - start < args.cap:
        try:
            st = get_json(api)
        except Exception as e:
            ended = f"api error: {e}"
            break
        if st["seed"] != seed or st["game"] != game:
            # Missed the game-over frame: the agent loop restarts 2 s after a game over.
            ended = "over"
            break
        last = st
        if first_answer is None and not st["paused"]:
            first_answer = time.monotonic() - launched
            first_tick, first_at = st["tick"], time.monotonic()
        if st["status"]["over"]:
            ended = "over"
            break
        time.sleep(args.poll)
    played = time.monotonic() - start
    # Realtime aims for 60 ticks/s; a slow guest (or an agent that answers instantly and floods
    # the engine) can fall behind, so report what the game actually got.
    tick_rate = None
    if first_answer is not None and time.monotonic() - first_at > 0.5:
        tick_rate = round((last["tick"] - first_tick) / (time.monotonic() - first_at), 1)
    if args.screenshots:
        # The guest's whole screen shows only this app. Taken after the game ends, so on a game
        # over it may show the next game already restarting.
        subprocess.run(["screencapture", "-x", os.path.join(log_dir, f"screen-{game}-{seed}.png")],
                       capture_output=True)
    quit_app()

    s = last["status"]
    return {
        "game": game, "seed": seed, "mode": last["mode"],
        "score": s["score"], "level": s["level"], "lives": s["lives"],
        "over": ended == "over", "ended": ended, "message": s.get("message", ""),
        "ticks": last["tick"], "game_secs": round(last["tick"] / 60, 1),
        "wall_secs": round(played, 1), "ticks_per_sec": tick_rate, "api_ready_secs": round(api_ready, 1),
        "first_answer_secs": None if first_answer is None else round(first_answer, 1),
    }


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--app", default="/Applications/System 1 Arcade.app")
    p.add_argument("--games", default="frogger", help="comma-separated game ids")
    p.add_argument("--seeds", default="1", help="comma-separated seeds")
    p.add_argument("--cap", type=float, default=300, help="seconds of play per game before giving up")
    p.add_argument("--results", required=True, help="directory for results.jsonl and app logs")
    p.add_argument("--api", default="127.0.0.1:8765")
    p.add_argument("--poll", type=float, default=0.2)
    p.add_argument("--launch-timeout", type=float, default=60)
    p.add_argument("--no-screenshots", dest="screenshots", action="store_false",
                   help="skip the screenshot taken at the end of each game")
    args = p.parse_args()

    if not in_vm():
        sys.exit("vm-guest-play.py launches the app and must run inside a VM, never on a host Mac")

    os.makedirs(args.results, exist_ok=True)
    out = os.path.join(args.results, "results.jsonl")
    seeds = [int(s) for s in args.seeds.split(",") if s.strip()]
    for game in [g.strip() for g in args.games.split(",") if g.strip()]:
        for seed in seeds:
            r = play(args, game, seed, args.results)
            line = json.dumps(r)
            print(line, flush=True)
            with open(out, "a") as f:
                f.write(line + "\n")


if __name__ == "__main__":
    main()
