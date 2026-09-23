#!/usr/bin/env python3
"""Measure how often Laya answers each question like the game's oracle.

The game is played by the oracle so states are realistic; at each step Laya
answers the same prompts and every answer is compared. Disagreements are
printed with the state text so descriptions can be tuned.

    python3 agents/eval_questions.py --game invaders --steps 300
"""
import argparse
import collections
import json
import sys
import urllib.request

sys.path.insert(0, __file__.rsplit("/", 1)[0])
from laya_batch import predict_many  # noqa: E402


def http(api, method, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(api + path, data=data, method=method, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read())


def value(ans):
    if ans.get("type") == "choice":
        return ans.get("choice") or max(ans["probabilities"], key=ans["probabilities"].get)
    if ans.get("type") == "noul":
        return "yes" if ans.get("noul", 0) > 0.5 else "no"
    return round(ans.get("score", 0))


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--api", default="http://127.0.0.1:8799/v1")
    p.add_argument("--game", required=True)
    p.add_argument("--seed", type=int, default=100)
    p.add_argument("--steps", type=int, default=300)
    p.add_argument("--show", type=int, default=3, help="disagreements to print per question")
    args = p.parse_args()
    import laya
    model = laya.load("convaiinnovations/laya")

    http(args.api, "POST", "/load", {"game": args.game, "seed": args.seed})
    http(args.api, "POST", "/mode", {"mode": "lockstep"})
    total, right = collections.Counter(), collections.Counter()
    shown = collections.defaultdict(list)
    confusion = collections.defaultdict(collections.Counter)
    for _ in range(args.steps):
        if http(args.api, "GET", "/state")["status"]["over"]:
            http(args.api, "POST", "/reset", {"seed": args.seed + 1})
        req = http(args.api, "GET", "/laya")
        truth = http(args.api, "GET", "/oracle")["answers"]
        prompts = req["batch"] if "batch" in req else {"": {"state": req["state"], "questions": req["questions"]}}
        got = predict_many(model, prompts)
        for key, qs in got.items():
            for q, ans in qs.items():
                name = f"{key}.{q}" if key else q
                if name not in truth:
                    continue
                # Group per-question stats by the question, not the batch key.
                group = name.split(".")[0].rstrip("0123456789") if args.game != "tetris" else "look"
                want, have = value(truth[name]), value(ans)
                total[group] += 1
                right[group] += want == have
                confusion[group][f"{want}->{have}"] += 1
                if want != have and len(shown[group]) < args.show:
                    shown[group].append(f"want {want} got {have}: {prompts[key]['state']}")
        http(args.api, "POST", "/decide", {"answers": truth})
    for g in sorted(total):
        print(f"{g:8s} {right[g] / total[g]:6.1%} of {total[g]:4d}   {dict(confusion[g])}")
        for line in shown[g]:
            print("         ", line[:200])


if __name__ == "__main__":
    main()
