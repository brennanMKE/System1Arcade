#!/usr/bin/env python3
"""A minimal custom agent for System 1 Arcade. Copy it and replace decide().

Run it, then in the app open Settings, choose "Custom agent" and set the URL to
http://127.0.0.1:8000/predict. It needs only the Python standard library.

    python3 agents/custom_agent_example.py --port 8000

Contract: the app POSTs {"state", "questions"} (or {"batch": {key: {state,
questions}}}) and expects {"answers": {name: answer}} (batch names are
"key.question"). An answer is {"type": "choice", "choice", "probabilities"},
{"type": "noul", "noul": P(yes)} or {"type": "score", "score"}.
"""
import argparse
import json
import random
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def decide(state, question):
    """Answer one question about one state. This example guesses at random;
    a real agent would call its model here."""
    if question["type"] == "choice":
        options = list(question["criteria"])
        pick = random.choice(options)
        return {"type": "choice", "choice": pick, "probabilities": {o: float(o == pick) for o in options}}
    if question["type"] == "noul":
        return {"type": "noul", "noul": random.random()}
    return {"type": "score", "score": random.random() * (len(question["criteria"]) - 1)}


def answer(body):
    if "batch" in body:
        return {f"{key}.{name}": decide(p["state"], q)
                for key, p in body["batch"].items() for name, q in p["questions"].items()}
    return {name: decide(body["state"], q) for name, q in body["questions"].items()}


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
        data = json.dumps({"answers": answer(body)}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *a):
        pass


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--port", type=int, default=8000)
    port = p.parse_args().port
    print(f"custom agent listening on http://127.0.0.1:{port}/predict", flush=True)
    ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
