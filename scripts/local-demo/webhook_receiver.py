#!/usr/bin/env python3
"""Local webhook fixture with deterministic failures; Python standard library only."""

import argparse
import json
import re
import threading
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit


class Receiver(BaseHTTPRequestHandler):
    scenarios = {}
    lock = threading.Lock()
    max_body_bytes = 65536

    def respond(self, status, body):
        encoded = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def scenario_name(self, prefix):
        path = urlsplit(self.path).path
        match = re.fullmatch(prefix + r"/([A-Za-z0-9_-]{1,80})", path)
        return match.group(1) if match else None

    def read_json(self):
        size = int(self.headers.get("Content-Length", "0"))
        if size < 0 or size > self.max_body_bytes:
            raise ValueError("request body must be at most 64 KiB")
        return json.loads(self.rfile.read(size))

    def do_GET(self):
        if urlsplit(self.path).path == "/healthz":
            self.respond(200, {"status": "ok", "fixture": "nimbus-local-webhook"})
            return
        name = self.scenario_name("/scenarios")
        with self.lock:
            state = self.scenarios.get(name)
            snapshot = json.loads(json.dumps(state)) if state is not None else None
        self.respond(200 if snapshot is not None else 404,
                     snapshot if snapshot is not None else {"error": "scenario not found"})

    def do_PUT(self):
        name = self.scenario_name("/scenarios")
        if name is None:
            self.respond(404, {"error": "use PUT /scenarios/<name>"})
            return
        try:
            body = self.read_json()
            failures = body.get("failures") if isinstance(body, dict) else None
            if type(failures) is not int or failures < -1:
                raise ValueError("failures must be -1 (always fail) or a nonnegative integer")
        except ValueError as exc:
            self.respond(400, {"error": str(exc)})
            return
        with self.lock:
            state = self.scenarios.setdefault(name, {"deliveries": []})
            state["remaining_failures"] = failures
        self.respond(200, {"scenario": name, "remaining_failures": failures})

    def do_POST(self):
        name = self.scenario_name("/webhooks")
        if name is None:
            self.respond(404, {"error": "use POST /webhooks/<name>"})
            return
        try:
            body = self.read_json()
        except ValueError as exc:
            self.respond(400, {"error": str(exc)})
            return
        with self.lock:
            state = self.scenarios.setdefault(name, {"remaining_failures": 0, "deliveries": []})
            remaining = state["remaining_failures"]
            status = 503 if remaining != 0 else 200
            if remaining > 0:
                state["remaining_failures"] -= 1
            state["deliveries"].append({
                "notification_id": self.headers.get("X-Nimbus-Notification-ID"),
                "tenant_id": self.headers.get("X-Nimbus-Tenant-ID"),
                "status": status,
                "body": body,
                "received_at": datetime.now(timezone.utc).isoformat(),
            })
        self.respond(status, {"scenario": name, "status": "accepted" if status == 200 else "fixture failure"})


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=9080)
    args = parser.parse_args()
    print(f"Local webhook fixture listening on {args.host}:{args.port}", flush=True)
    ThreadingHTTPServer((args.host, args.port), Receiver).serve_forever()
