#!/usr/bin/env python3
"""Serve Laya as a decision endpoint for System 1 Arcade.

This is the built-in agent: the app starts it and POSTs each prompt to it.

    POST /predict  {"state": ..., "questions": {...}}             -> {"answers": {name: answer}}
    POST /predict  {"batch": {key: {"state": ..., "questions": ...}}} -> {"answers": {"key.name": answer}}
    GET  /health   -> {"ok": true}
    GET  /stats    -> answer cache hits, misses and hit rate

Answers are cached by (state, question), since Laya gives the same answer to
the same prompt; only misses reach the model. --cache-size 0 (or
SYSTEM1_LAYA_CACHE=0) turns the cache off.

--weights (or SYSTEM1_LAYA_WEIGHTS) applies a head tuned by agents/finetune.py
on top of the base model; without it the base model answers.

A custom agent implements the same POST contract with its own logic.
"""
import argparse
import json
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, __file__.rsplit("/", 1)[0])


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--host", default="127.0.0.1", help="address to listen on (0.0.0.0 to serve other machines)")
    p.add_argument("--port", type=int, default=0, help="port to listen on (0 = any free port)")
    p.add_argument("--model", help="base checkpoint (default: $SYSTEM1_LAYA_MODEL or convaiinnovations/laya)")
    p.add_argument("--weights", help="tuned weights from agents/finetune.py (default: $SYSTEM1_LAYA_WEIGHTS, else none)")
    p.add_argument("--device", help="mps, cuda or cpu (default: auto)")
    p.add_argument("--cache-size", type=int, help="answers to cache, 0 = off (default: $SYSTEM1_LAYA_CACHE or 10000)")
    p.add_argument("--stats-every", type=float, default=0, help="print cache stats every N seconds (0 = never)")
    args = p.parse_args()

    from laya_batch import AnswerCache, answer_cached, cache_size_from_env, load_model, predict_many

    model = load_model(args.model, args.weights, device=args.device)
    cache = AnswerCache(cache_size_from_env() if args.cache_size is None else args.cache_size)
    lock = threading.Lock()

    def single(prompts):
        return {k: model.predict(p["state"], p["questions"])["answers"] for k, p in prompts.items()}

    class Handler(BaseHTTPRequestHandler):
        def reply(self, code, body):
            data = json.dumps(body).encode()
            self.send_response(code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_GET(self):
            if self.path == "/health":
                return self.reply(200, {"ok": True})
            if self.path == "/stats":
                with lock:
                    return self.reply(200, cache.stats())
            self.reply(404, {"error": "not found"})

        def do_POST(self):
            if self.path != "/predict":
                return self.reply(404, {"error": "POST /predict"})
            try:
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
                with lock:
                    if "batch" in body:
                        grouped = answer_cached(body["batch"], lambda ps: predict_many(model, ps), cache)
                        answers = {f"{k}.{q}": a for k, qs in grouped.items() for q, a in qs.items()}
                    else:
                        answers = answer_cached({"": body}, single, cache)[""]
                self.reply(200, {"answers": answers})
            except Exception as e:  # report bad requests instead of dropping the connection
                self.reply(400, {"error": str(e)})

        def log_message(self, *a):
            pass

    server = ThreadingHTTPServer((args.host, args.port), Handler)
    if args.stats_every > 0:
        def report():
            while True:
                time.sleep(args.stats_every)
                with lock:
                    print(cache.summary(), flush=True)
        threading.Thread(target=report, daemon=True).start()
    print(f"agent ready http://{args.host}:{server.server_port}/predict", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
