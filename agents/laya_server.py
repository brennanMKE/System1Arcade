#!/usr/bin/env python3
"""Serve Laya as a decision endpoint for System 1 Arcade.

This is the built-in agent: the app starts it and POSTs each prompt to it.

    POST /predict  {"state": ..., "questions": {...}}             -> {"answers": {name: answer}}
    POST /predict  {"batch": {key: {"state": ..., "questions": ...}}} -> {"answers": {"key.name": answer}}
    GET  /health   -> {"ok": true}

A custom agent implements the same POST contract with its own logic.
"""
import argparse
import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, __file__.rsplit("/", 1)[0])


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--host", default="127.0.0.1", help="address to listen on (0.0.0.0 to serve other machines)")
    p.add_argument("--port", type=int, default=0, help="port to listen on (0 = any free port)")
    p.add_argument("--model", default="convaiinnovations/laya")
    p.add_argument("--device", help="mps, cuda or cpu (default: auto)")
    args = p.parse_args()

    import laya
    from laya_batch import predict_many

    model = laya.load(args.model, device=args.device)
    lock = threading.Lock()

    class Handler(BaseHTTPRequestHandler):
        def reply(self, code, body):
            data = json.dumps(body).encode()
            self.send_response(code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_GET(self):
            self.reply(200, {"ok": True}) if self.path == "/health" else self.reply(404, {"error": "not found"})

        def do_POST(self):
            if self.path != "/predict":
                return self.reply(404, {"error": "POST /predict"})
            try:
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
                with lock:
                    if "batch" in body:
                        grouped = predict_many(model, body["batch"])
                        answers = {f"{k}.{q}": a for k, qs in grouped.items() for q, a in qs.items()}
                    else:
                        answers = model.predict(body["state"], body["questions"])["answers"]
                self.reply(200, {"answers": answers})
            except Exception as e:  # report bad requests instead of dropping the connection
                self.reply(400, {"error": str(e)})

        def log_message(self, *a):
            pass

    server = ThreadingHTTPServer((args.host, args.port), Handler)
    print(f"agent ready http://{args.host}:{server.server_port}/predict", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
