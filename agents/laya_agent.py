#!/usr/bin/env python3
"""Drive a System 1 Arcade game with Laya (or any Jev-style System 1 model).

Each decision is one round trip:

    GET  /v1/laya    -> {"state": "<plain-English situation>", "questions": {name: {type, instructions, ...}}}
                        or {"batch": {key: {"state": ..., "questions": ...}}} (several short states)
    Laya predict(state, questions)
    POST /v1/decide  {"answers": ..., "meta": {"latency_ms": ...}}; the game turns answers into button presses

Batch answers are flattened to "<key>.<question>".

Policies:
    --policy laya       in-process: pip install laya
    --policy sidecar    HTTP: POST {state, questions} to --sidecar (e.g. a Laya /predict server)
    --policy oracle     the game's own correct answers: the best these questions can do
    --policy random     random answers: the floor

With --policy laya, answers are cached by (state, question) because Laya
answers the same prompt the same way; --cache-size 0 or SYSTEM1_LAYA_CACHE=0
turns that off. A Laya server behind --policy sidecar keeps its own cache.

Clock:
    --lockstep          the game waits for each decision (latency is free)
    default realtime    the game keeps running while the model thinks
"""

import argparse
import json
import random
import sys
import time
import urllib.request


def http(method, url, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, method=method, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read())


def field(obj, key):
    """Read key from a dict or attribute-style result object."""
    if isinstance(obj, dict):
        return obj.get(key)
    return getattr(obj, key, None)


def answers_from(result):
    """Normalize a Laya result (dict or object) to {question: answer dict}."""
    answers = field(result, "answers") or result
    out = {}
    for name, ans in (answers.items() if isinstance(answers, dict) else vars(answers).items()):
        out[name] = ans if isinstance(ans, dict) else vars(ans)
    return out


def flatten(batch_answers):
    return {f"{k}.{q}": a for k, qs in batch_answers.items() for q, a in qs.items()}


def random_answers(req):
    if "batch" in req:
        return flatten({k: random_answers(p) for k, p in req["batch"].items()})
    out = {}
    for name, q in req["questions"].items():
        if q["type"] == "choice":
            opts = list(q["criteria"])
            out[name] = {"type": "choice", "choice": random.choice(opts), "probabilities": {o: 1 / len(opts) for o in opts}}
        elif q["type"] == "noul":
            out[name] = {"type": "noul", "noul": random.random()}
        else:
            out[name] = {"type": "score", "score": random.random() * (len(q["criteria"]) - 1)}
    return out


def make_policy(args, api):
    """Returns (policy, cache); cache is None unless answers are cached here."""
    if args.policy == "random":
        return random_answers, None
    if args.policy == "oracle":
        return lambda req: http("GET", f"{api}/oracle")["answers"], None
    if args.policy == "sidecar":
        def sidecar(req):
            if "batch" in req:
                return flatten({k: answers_from(http("POST", args.sidecar, p)) for k, p in req["batch"].items()})
            return answers_from(http("POST", args.sidecar, req))
        return sidecar, None

    import laya  # pip install laya
    from laya_batch import AnswerCache, answer_cached, cache_size_from_env, predict_many

    model = laya.load(args.model, device=args.device)
    cache = AnswerCache(cache_size_from_env() if args.cache_size is None else args.cache_size)

    def single(prompts):
        return {k: answers_from(model.predict(p["state"], p["questions"])) for k, p in prompts.items()}

    def local(req):
        if "batch" in req:
            return flatten(answer_cached(req["batch"], lambda ps: predict_many(model, ps), cache))
        return answer_cached({"": req}, single, cache)[""]
    return local, (cache if cache.size > 0 else None)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--api", default="http://127.0.0.1:8765/v1", help="System 1 Arcade agent API")
    p.add_argument("--game", choices=["tetris", "frogger", "invaders"], help="load this game first")
    p.add_argument("--seed", type=int, default=0)
    p.add_argument("--policy", choices=["laya", "sidecar", "oracle", "random"], default="laya")
    p.add_argument("--model", default="convaiinnovations/laya", help="Laya checkpoint for --policy laya")
    p.add_argument("--device", help="torch device for --policy laya: mps, cuda or cpu (default: auto)")
    p.add_argument("--cache-size", type=int, help="answers to cache for --policy laya, 0 = off (default: $SYSTEM1_LAYA_CACHE or 10000)")
    p.add_argument("--sidecar", default="http://127.0.0.1:8000/predict", help="predict URL for --policy sidecar")
    p.add_argument("--lockstep", action="store_true", help="pause the game between decisions")
    p.add_argument("--games", type=int, default=1, help="stop after this many games end (0 = keep playing)")
    p.add_argument("--keep-mode", action="store_true", help="leave the app's realtime/lockstep setting alone")
    p.add_argument("--pause-after-game", type=float, default=0, help="seconds to show GAME OVER before restarting")
    p.add_argument("--max-decisions", type=int, default=0, help="end a game after this many decisions (0 = play to game over)")
    p.add_argument("--verbose", "-v", action="store_true")
    args = p.parse_args()

    api = args.api.rstrip("/")
    pick, cache = make_policy(args, api)

    if args.game:
        http("POST", f"{api}/load", {"game": args.game, "seed": args.seed})
    if not args.keep_mode:
        http("POST", f"{api}/mode", {"mode": "lockstep" if args.lockstep else "realtime"})
    print("agent ready", flush=True)

    finished, decisions, total_ms, scores = 0, 0, 0.0, []
    try:
        while True:
            st = http("GET", f"{api}/state")["status"]
            capped = args.max_decisions and decisions >= args.max_decisions
            if st["over"] or capped:
                finished += 1
                scores.append(st["score"])
                avg = total_ms / max(decisions, 1)
                how = "capped" if capped and not st["over"] else "over"
                print(f"game {finished} {how}: score {st['score']} after {decisions} decisions, {avg:.0f}ms avg decision")
                if cache:
                    print(f"  {cache.summary()}")
                if args.games and finished >= args.games:
                    break
                time.sleep(args.pause_after_game)
                # With --seed, game i uses seed+i so policies can be compared on identical games.
                http("POST", f"{api}/reset", {"seed": args.seed + finished if args.seed else 0})
                decisions, total_ms = 0, 0.0
                continue

            req = http("GET", f"{api}/laya")
            if req.get("batch") == {}:
                time.sleep(1 / 60)  # nothing to decide yet (e.g. a Tetris piece is being placed)
                continue
            t0 = time.perf_counter()
            answers = pick(req)
            latency = (time.perf_counter() - t0) * 1000
            total_ms += latency
            res = http("POST", f"{api}/decide", {"answers": answers, "meta": {"latency_ms": latency}})
            decisions += 1
            d = res["decision"]
            if args.verbose and not d.get("keep"):
                print(f"{decisions:5d} {latency:6.1f}ms  {' '.join(d.get('actions') or []):<28} {d['note']}")
    except KeyboardInterrupt:
        pass
    except urllib.error.URLError as e:
        sys.exit(f"cannot reach {api}: {e} (is System 1 Arcade or cmd/headless running?)")
    if cache and not scores:
        print(cache.summary())  # interrupted before a game ended
    if len(scores) > 1:
        print(f"mean score {sum(scores) / len(scores):.1f} over {len(scores)} games")


if __name__ == "__main__":
    main()
