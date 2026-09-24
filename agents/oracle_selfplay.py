#!/usr/bin/env python3
"""Play seeds in lockstep, label every prompt with the game's oracle, and score the games.

Two jobs in one loop:

  * Data for fine-tuning: at every decision, each (state, question) prompt is
    recorded with the oracle's answer (GET /v1/oracle) and, if Laya is
    loaded, Laya's answer. Prompts are deduplicated by their exact text, with
    a count and the seeds they came from, and written as JSONL.
  * A benchmark: the score of every game, and how often Laya agreed with the
    oracle per question.

Which answers drive the game is set by --policy:

    laya     Laya's answers (the states Laya itself reaches)
    oracle   the oracle's answers (the best these questions can do)
    mix      each decision is Laya's with probability --laya-frac, else the oracle's,
             so the data covers states both reach

Run against the headless server:

    go run ./cmd/headless -addr 127.0.0.1:8799 &
    .venv/bin/python agents/oracle_selfplay.py --api http://127.0.0.1:8799/v1 \\
        --game frogger --seeds 1-10 --policy mix --out .laya-tuned/data/frogger.jsonl

--weights applies a tuned checkpoint (see agents/finetune.py) to benchmark it.
"""
import argparse
import collections
import json
import random
import statistics
import sys
import time
import urllib.request

sys.path.insert(0, __file__.rsplit("/", 1)[0])


def http(api, method, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(api + path, data=data, method=method, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.loads(r.read())


def seed_list(spec):
    """"1-10,15" -> [1, ..., 10, 15]"""
    out = []
    for part in spec.split(","):
        a, _, b = part.partition("-")
        out += list(range(int(a), int(b or a) + 1))
    return out


def label(ans):
    """The discrete answer a game acts on: a choice, or yes/no for noul."""
    if ans.get("type") == "choice":
        return ans.get("choice") or max(ans["probabilities"], key=ans["probabilities"].get)
    if ans.get("type") == "noul":
        return "yes" if ans.get("noul", 0) > 0.5 else "no"
    return str(round(ans.get("score", 0)))


def p_of(ans, want):
    """The probability an answer gives the label `want`."""
    if ans.get("type") == "noul":
        return ans["noul"] if want == "yes" else 1 - ans["noul"]
    return ans.get("probabilities", {}).get(want, 0.0)


def prompts_of(req):
    if "batch" in req:
        return req["batch"]
    return {"": {"state": req["state"], "questions": req["questions"]}}


def flat_name(key, qid):
    return f"{key}.{qid}" if key else qid


def question_group(game, key):
    """Per-question stats: Tetris asks the same question of every spot."""
    return "look" if game == "tetris" else key.rstrip("0123456789") or "q"


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--api", default="http://127.0.0.1:8799/v1")
    p.add_argument("--game", required=True, choices=["tetris", "frogger", "invaders"])
    p.add_argument("--seeds", default="1-5", help="e.g. 1-10 or 1,3,5")
    p.add_argument("--policy", choices=["laya", "oracle", "mix"], default="laya")
    p.add_argument("--laya-frac", type=float, default=0.5, help="share of Laya decisions with --policy mix")
    p.add_argument("--model", default=None, help="Laya checkpoint (default: $SYSTEM1_LAYA_MODEL or the base model)")
    p.add_argument("--weights", default=None, help="tuned weights from agents/finetune.py (default: $SYSTEM1_LAYA_WEIGHTS)")
    p.add_argument("--no-laya", action="store_true", help="oracle only: record labels without loading Laya")
    p.add_argument("--max-decisions", type=int, default=3000, help="end a game after this many decisions")
    p.add_argument("--out", help="write deduplicated labelled prompts here (JSONL)")
    p.add_argument("--scores", help="append one JSON line per game (seed, score, ...) here")
    p.add_argument("--show", type=int, default=0, help="disagreements to print per question")
    args = p.parse_args()
    api = args.api.rstrip("/")
    rng = random.Random(0)

    model = cache = None
    if not args.no_laya:
        from laya_batch import AnswerCache, answer_cached, load_model, predict_many
        model = load_model(args.model, args.weights)
        cache = AnswerCache(100000)
    elif args.policy != "oracle":
        sys.exit("--no-laya needs --policy oracle")

    records = {}  # cache key -> record
    total, right = collections.Counter(), collections.Counter()
    shown = collections.defaultdict(list)
    games = []
    http(api, "POST", "/load", {"game": args.game, "seed": seed_list(args.seeds)[0]})
    http(api, "POST", "/mode", {"mode": "lockstep"})
    for seed in seed_list(args.seeds):
        http(api, "POST", "/reset", {"seed": seed})
        decisions, t0 = 0, time.time()
        while True:
            st = http(api, "GET", "/state")["status"]
            if st["over"] or decisions >= args.max_decisions:
                break
            req = http(api, "GET", "/laya")
            prompts = prompts_of(req)
            if not prompts:
                time.sleep(1 / 240)  # nothing to decide yet (a Tetris piece is being placed)
                continue
            truth = http(api, "GET", "/oracle")["answers"]
            got = {}
            if model is not None:
                got = answer_cached(prompts, lambda ps: predict_many(model, ps), cache)
            for key, pr in prompts.items():
                for qid, qdef in pr["questions"].items():
                    name = flat_name(key, qid)
                    if name not in truth:
                        continue
                    want = label(truth[name])
                    ck = json.dumps([pr["state"], qdef], sort_keys=True, ensure_ascii=False)
                    r = records.get(ck)
                    if r is None:
                        r = records[ck] = {"game": args.game, "group": question_group(args.game, key),
                                           "state": pr["state"], "question": qdef, "labels": {},
                                           "count": 0, "seeds": []}
                    r["labels"][want] = r["labels"].get(want, 0) + 1
                    r["count"] += 1
                    if seed not in r["seeds"]:
                        r["seeds"].append(seed)
                    if key in got:
                        have = label(got[key][qid])
                        r["laya"] = have
                        r["laya_p"] = round(p_of(got[key][qid], want), 4)
                        g = r["group"]
                        total[g] += 1
                        right[g] += want == have
                        if want != have and len(shown[g]) < args.show:
                            shown[g].append(f"want {want} got {have}: {pr['state'][:160]}")
            if args.policy == "oracle" or (args.policy == "mix" and rng.random() >= args.laya_frac):
                answers = truth
            else:
                answers = {flat_name(k, q): a for k, qs in got.items() for q, a in qs.items()}
            http(api, "POST", "/decide", {"answers": answers})
            decisions += 1
        full = http(api, "GET", "/state")
        st = {**full["status"], **{k: v for k, v in (full.get("observation") or {}).items() if k == "lines"}}
        row = {"game": args.game, "seed": seed, "policy": args.policy, "weights": args.weights or "base",
               "score": st["score"], "over": st["over"], "decisions": decisions,
               "seconds": round(time.time() - t0, 1),
               **{k: st[k] for k in ("level", "lines", "lives", "wave") if k in st}}
        games.append(row)
        print(json.dumps(row), flush=True)
        if args.scores:
            with open(args.scores, "a") as f:
                f.write(json.dumps(row) + "\n")

    scores = [g["score"] for g in games]
    if len(scores) > 1:
        print(f"score mean {statistics.mean(scores):.0f} sd {statistics.stdev(scores):.0f} "
              f"median {statistics.median(scores):.0f} min {min(scores)} max {max(scores)} over {len(scores)} games")
    for g in sorted(total):
        print(f"{g:8s} agreement {right[g] / total[g]:7.2%} of {total[g]}")
        for line in shown[g]:
            print("         ", line)
    mixed = sum(1 for r in records.values() if len(r["labels"]) > 1)
    print(f"{len(records)} distinct prompts, {mixed} with more than one oracle label")
    if args.out:
        import os
        os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)
        with open(args.out, "w") as f:
            for r in records.values():
                f.write(json.dumps(r, ensure_ascii=False) + "\n")
        print(f"wrote {args.out}")


if __name__ == "__main__":
    main()
