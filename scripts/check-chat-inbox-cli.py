#!/usr/bin/env python3
"""Compiled C7 review regression matrix. Loopback fixtures only, no live host."""
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

binary = str(pathlib.Path(sys.argv[1]).resolve())
mode = ""
calls = []

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_POST(self):
        q = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        op = q["operationName"]
        calls.append(op)
        assert self.headers.get("X-Hadron-Session") == "session-self"
        assert self.headers.get("Authorization") == "Bearer synthetic-user"
        status = 200
        if op == "SessionInbox":
            assert q["variables"]["workerRefs"] == ["self"]
            if mode == "read-refused":
                result = {"errors": [{"message": "PRIVATE-RAW-SENTINEL", "extensions": {"code": "SESSION_NOT_LIVE"}}]}
            else:
                batch = {"id": "PRIVATE-PROOF", "worker": {"id": "self", "name": "Self"}, "text": "server text", "ranges": [], "overflow": 0}
                result = {"data": {"deliveryBatches": {"batches": [] if mode == "empty" else [batch], "pageInfo": {"hasNextPage": False, "endCursor": None}}}}
        elif op == "AcknowledgeDeliveryBatches":
            assert "through" not in q["variables"]
            assert q["variables"]["batchIds"] == ["PRIVATE-PROOF"]
            if "unknown" in mode:
                status = 503
                result = {"error": "PRIVATE-RAW-SENTINEL"}
            elif "refused" in mode:
                result = {"errors": [{"message": "PRIVATE-RAW-SENTINEL", "extensions": {"code": "WAKE_PROOF_INVALID"}}]}
            else:
                result = {"data": {"acknowledgeDeliveryBatches": {"acknowledged": 1, "unchanged": 0}}}
        else:
            raise AssertionError(op)
        data = json.dumps(result).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(data)

with tempfile.TemporaryDirectory() as scratch:
    root = pathlib.Path(scratch)
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    url = f"http://127.0.0.1:{server.server_port}"
    binding = root / "hadron-team-session.json"
    env = dict(os.environ, HADRON_TEAM_GIT_DIR=scratch, XDG_CONFIG_HOME=scratch, HADRON_TOKEN="synthetic-user", HADRON_SERVER=url)
    try:
        for mode in ("human-success", "json-success", "empty", "human-unknown", "json-unknown", "human-refused", "json-refused", "read-refused", "unbound", "server-mismatch", "ack-only", "duplicate-ack", "empty-after"):
            calls.clear()
            binding.write_text(json.dumps({"appId": "app", "workerId": "self", "sessionId": "session-self", "server": "https://bound.example" if mode == "server-mismatch" else url}))
            if mode == "unbound":
                binding.unlink()
            args = [binary, "team", "chat", "inbox", "--server", url]
            if mode.startswith("json"):
                args += ["--json"]
            inp = None
            if mode in ("ack-only", "duplicate-ack"):
                args += ["--ack-file", "-"]
                inp = json.dumps({"batchIds": ["PRIVATE-PROOF"] * (2 if mode == "duplicate-ack" else 1)})
            if mode == "empty-after":
                args += ["--after="]
            run = subprocess.run(args, env=env, input=inp, text=True, capture_output=True, timeout=15)
            assert "PRIVATE" not in run.stderr, (mode, run.stderr)
            if not mode.startswith("json"):
                assert "PRIVATE-PROOF" not in run.stdout
            expected = 7 if "unknown" in mode else 2 if mode in ("human-refused", "json-refused", "unbound", "duplicate-ack", "empty-after") else 5 if mode in ("server-mismatch", "read-refused") else 0
            assert run.returncode == expected, (mode, run.returncode, run.stderr)
            if mode in ("unbound", "server-mismatch", "duplicate-ack", "empty-after"):
                assert calls == [], (mode, calls)
            if mode == "unbound":
                assert "INBOX_UNBOUND" in run.stderr
            if mode == "server-mismatch":
                assert "INBOX_SERVER_MISMATCH" in run.stderr
            if mode == "ack-only":
                assert calls == ["AcknowledgeDeliveryBatches"]
                assert "already acknowledged" in run.stdout
            if mode == "empty":
                assert "No deliveries" in run.stdout and calls == ["SessionInbox"]
            if mode.startswith("json"):
                assert json.loads(run.stdout)["batches"][0]["id"] == "PRIVATE-PROOF"
            if mode.endswith("unknown"):
                assert "unconfirmed" in run.stderr.lower()
                assert ("--ack-file -" in run.stderr) if mode.startswith("json") else ("hadron team chat inbox again" in run.stderr)
            if mode in ("human-refused", "json-refused"):
                assert "WAKE_PROOF_INVALID" in run.stderr and "unconfirmed" not in run.stderr.lower()
            if mode == "read-refused":
                assert "SESSION_NOT_LIVE" in run.stderr
            print(f"PASS {mode}")
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
print("13/13 compiled C7 fixture cases PASS; no live CLI/MCP claim")
