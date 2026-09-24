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
import threading
import time
import urllib.error
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


def usage(pids):
    """Resident memory (MB) and CPU (%) of pids and their children; (None, None) if none run."""
    pids = [str(p) for p in pids if p]
    if not pids:
        return None, None
    kids = subprocess.run(["pgrep", "-P", ",".join(pids)], capture_output=True, text=True).stdout.split()
    out = subprocess.run(["ps", "-o", "rss=,%cpu=", "-p", ",".join(pids + kids)], capture_output=True, text=True).stdout
    rows = [l.split() for l in out.splitlines() if len(l.split()) == 2]
    if not rows:
        return None, None
    return round(sum(int(r[0]) for r in rows) / 1024, 1), round(sum(float(r[1]) for r in rows), 1)


def app_pid():
    out = subprocess.run(["pgrep", "-x", APP_BIN], capture_output=True, text=True).stdout.split()
    return out[0] if out else None


def close_enough(a, b, tol):
    """a and b are the same JSON value, with numbers allowed to differ by tol."""
    if isinstance(a, bool) or isinstance(b, bool):
        return a == b
    if isinstance(a, (int, float)) and isinstance(b, (int, float)):
        return abs(a - b) <= tol
    if isinstance(a, dict) and isinstance(b, dict):
        if a.get("type") == "noul" and ("action" in a) != ("action" in b):
            # laya_server.py (and laya-server) cache answers by (state, question) across batch and
            # single-state requests, so a noul answer keeps the shape of the request that first
            # asked it: predict_many's (no "action") or Agent.predict's.
            a, b = {k: v for k, v in a.items() if k != "action"}, {k: v for k, v in b.items() if k != "action"}
        return list(a) == list(b) and all(close_enough(a[k], b[k], tol) for k in a)
    if isinstance(a, list) and isinstance(b, list):
        return len(a) == len(b) and all(close_enough(x, y, tol) for x, y in zip(a, b))
    return a == b


def choices(v):
    """Every "choice" in a response, by path; they must agree exactly."""
    out = {}
    def walk(x, path):
        if isinstance(x, dict):
            for k, y in x.items():
                if k == "choice":
                    out[path] = y
                walk(y, path + "/" + k)
        elif isinstance(x, list):
            for i, y in enumerate(x):
                walk(y, f"{path}/{i}")
    walk(v, "")
    return out


class Watcher:
    """Samples the agent server while games play, and checks its answers.

    Every --monitor-every seconds: the server's and the app's resident memory, and the server's
    GET /stats (laya-server and laya_server.py have it; other agents may not). With --verify, one
    reference request every --verify-every seconds is posted to the agent URL and its reply
    compared with the recorded one (status equal, every "choice" equal, numbers within --tol).
    """

    def __init__(self, args, log_dir):
        self.args, self.stop = args, threading.Event()
        self.game = None
        self.server_pids = []
        if args.server_pid_file and os.path.exists(args.server_pid_file):
            self.server_pids = open(args.server_pid_file).read().split()
        base = args.agent_url.rsplit("/", 1)[0] if args.agent_url else None
        self.stats_url = base + "/stats" if base else None
        self.monitor_out = open(os.path.join(log_dir, "monitor.jsonl"), "a")
        self.verify_out = open(os.path.join(log_dir, "verify.jsonl"), "a")
        self.refs = []
        if args.verify:
            self.refs = [json.loads(l) for l in open(args.verify) if l.strip()]
        self.next_ref = 0
        self.lock = threading.Lock()
        self.threads = [threading.Thread(target=self.monitor, daemon=True)]
        if self.refs and args.verify_every > 0:
            self.threads.append(threading.Thread(target=self.verifier, daemon=True))

    def start(self):
        for t in self.threads:
            t.start()

    def close(self):
        self.stop.set()
        for t in self.threads:
            t.join(timeout=30)

    def sample(self):
        srss, scpu = usage(self.server_pids)
        arss, acpu = usage([app_pid()])
        row = {"t": round(time.time(), 1), "game": self.game, "server_rss_mb": srss, "server_cpu": scpu,
               "app_rss_mb": arss, "app_cpu": acpu}
        if self.stats_url:
            try:
                row["stats"] = get_json(self.stats_url, timeout=5)
            except Exception as e:
                row["stats_error"] = str(e)
        return row

    def monitor(self):
        while not self.stop.is_set():
            row = self.sample()
            with self.lock:
                self.monitor_out.write(json.dumps(row) + "\n")
                self.monitor_out.flush()
            self.stop.wait(self.args.monitor_every)

    def check(self, ref, phase):
        body = json.dumps(ref["request"], ensure_ascii=False).encode()
        req = urllib.request.Request(self.args.agent_url, body, {"Content-Type": "application/json"})
        t = time.perf_counter()
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                status, data = r.status, r.read()
        except urllib.error.HTTPError as e:
            status, data = e.code, e.read()
        except Exception as e:
            status, data = None, str(e).encode()
        ms = round((time.perf_counter() - t) * 1000, 2)
        row = {"id": ref.get("id"), "phase": phase, "game": self.game, "ms": ms, "status": status}
        exact = data.decode(errors="replace") == ref["response"]
        ok = status == ref["status"] and exact
        if not ok and status == ref["status"]:
            try:
                got, want = json.loads(data), json.loads(ref["response"])
                ok = choices(got) == choices(want) and close_enough(got, want, self.args.tol)
            except ValueError:
                ok = False
        row.update(ok=ok, exact=exact)
        if not ok:
            row["got"] = data.decode(errors="replace")[:2000]
            row["want"] = ref["response"][:2000]
        with self.lock:
            self.verify_out.write(json.dumps(row) + "\n")
            self.verify_out.flush()
        return row

    def verifier(self):
        while not self.stop.wait(self.args.verify_every):
            if self.game is None:
                continue
            ref = self.refs[self.next_ref % len(self.refs)]
            self.next_ref += 1
            self.check(ref, "during")

    def verify_all(self):
        """Every reference request once more, after the games (most are cache misses)."""
        rows = [self.check(ref, "after") for ref in self.refs]
        return rows


def quit_app():
    subprocess.run(["pkill", "-x", APP_BIN], capture_output=True)
    for _ in range(50):
        if subprocess.run(["pgrep", "-x", APP_BIN], capture_output=True).returncode != 0:
            return
        time.sleep(0.1)
    subprocess.run(["pkill", "-9", "-x", APP_BIN], capture_output=True)
    time.sleep(0.5)


def play(args, game, seed, log_dir, watcher):
    api = f"http://{args.api}/v1/state"
    quit_app()
    before = watcher.sample()
    watcher.game = f"{game}:{seed}"
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
    after = watcher.sample()
    watcher.game = None
    quit_app()

    s = last["status"]
    extra = {"app_rss_mb": after.get("app_rss_mb"), "server_rss_mb": after.get("server_rss_mb")}
    sb, sa = before.get("stats"), after.get("stats")
    if sb and sa and played > 0:
        answers = (sa["hits"] + sa["misses"]) - (sb["hits"] + sb["misses"])
        extra.update(answers_per_sec=round(answers / played, 1),
                     model_calls=sa["model_calls"] - sb["model_calls"],
                     misses=sa["misses"] - sb["misses"])
    return {
        "game": game, "seed": seed, "mode": last["mode"],
        "score": s["score"], "level": s["level"], "lives": s["lives"],
        "over": ended == "over", "ended": ended, "message": s.get("message", ""),
        "ticks": last["tick"], "game_secs": round(last["tick"] / 60, 1),
        "wall_secs": round(played, 1), "ticks_per_sec": tick_rate, "api_ready_secs": round(api_ready, 1),
        "first_answer_secs": None if first_answer is None else round(first_answer, 1),
        **extra,
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
    p.add_argument("--agent-url", help="the agent's predict URL, for --verify and its /stats")
    p.add_argument("--server-pid-file", help="file with the agent server's pid, for its memory")
    p.add_argument("--monitor-every", type=float, default=5, help="seconds between memory/stats samples")
    p.add_argument("--verify", help="JSONL of {request, status, response} to check the agent against")
    p.add_argument("--verify-every", type=float, default=10, help="seconds between checks during play (0 = only after)")
    p.add_argument("--tol", type=float, default=1.5e-4, help="allowed difference in answer numbers")
    p.add_argument("--no-screenshots", dest="screenshots", action="store_false",
                   help="skip the screenshot taken at the end of each game")
    args = p.parse_args()

    if not in_vm():
        sys.exit("vm-guest-play.py launches the app and must run inside a VM, never on a host Mac")

    os.makedirs(args.results, exist_ok=True)
    out = os.path.join(args.results, "results.jsonl")
    seeds = [int(s) for s in args.seeds.split(",") if s.strip()]
    watcher = Watcher(args, args.results)
    watcher.start()
    try:
        for game in [g.strip() for g in args.games.split(",") if g.strip()]:
            for seed in seeds:
                r = play(args, game, seed, args.results, watcher)
                line = json.dumps(r)
                print(line, flush=True)
                with open(out, "a") as f:
                    f.write(line + "\n")
    finally:
        watcher.close()
    if watcher.refs:
        rows = watcher.verify_all()
        bad = [r for r in rows if not r["ok"]]
        ms = sorted(r["ms"] for r in rows)
        print(f"verify: {len(rows) - len(bad)}/{len(rows)} agree ({sum(r['exact'] for r in rows)} byte-identical), "
              f"median {ms[len(ms) // 2]} ms, max {ms[-1]} ms", flush=True)


if __name__ == "__main__":
    main()
