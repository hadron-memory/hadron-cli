#!/usr/bin/env python3
"""Compiled C2 smoke against a loopback fixture; no real Orca/server/host."""
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
        if op in ("HostRegistrations", "DeliveryBatches"):
            assert self.headers.get("Authorization") == "Bearer synthetic-host"
            assert not self.headers.get("X-Hadron-Session")
            if mode == "preflight-refused" or (mode == "after-refused" and "CreateTeamChatMessage" in calls):
                result = {"errors": [{"message": "PRIVATE-RAW-ERROR", "extensions": {"code": "APP_KEY_SCOPE_REQUIRED"}}]}
            elif op == "HostRegistrations":
                result = {"data": {"hostRegistrations": [{"hostId": "own", "live": True, "workers": []}]}}
            else:
                result = {"data": {"deliveryBatches": {"batches": [], "pageInfo": {"hasNextPage": False, "endCursor": None}}}}
        else:
            assert self.headers.get("Authorization") == "Bearer synthetic-post"
            if op == "TeamAppIdentity":
                result = {"data": {"app": {"id": "app", "urn": "hrn:app:test:team", "name": "Team"}}}
            elif op == "CreateTeamChatMessage":
                result = {"data": {"createTeamChatMessage": {"nodeId": "m", "seq": 17, "body": "new", "at": "2026-10-19T00:00:00Z", "mentions": []}}}
            else:
                raise AssertionError(op)
        data = json.dumps(result).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(data)

with tempfile.TemporaryDirectory() as scratch:
    root = pathlib.Path(scratch)
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    url = f"http://127.0.0.1:{server.server_port}"
    (root / "key").write_text("synthetic-host")
    (root / "key").chmod(0o600)
    (root / "state.json").write_text('{"hostId":"own"}')
    (root / "host.yaml").write_text(f"server: {url}\napp: hrn:app:test:team\nkey_file: key\nstate_file: state.json\nworktrees_root: {root}\nworkers:\n  worker:\n    family: claude\n    launch: [fake]\n    worktrees: [tree]\ndelivery:\n  enabled: true\n  orca_bin: /bin/false\n")
    env = dict(os.environ, HADRON_TEAM_GIT_DIR=scratch, XDG_CONFIG_HOME=scratch, HADRON_TOKEN="synthetic-post", HADRON_SERVER=url)
    try:
        for mode in ("absent", "empty", "success", "preflight-refused", "after-refused"):
            calls.clear()
            args = [binary, "team", "chat", "post", "new", "--as-me", "--app", "hrn:app:test:team", "--json", "--server", url]
            if mode != "absent":
                args += ["--orca-host-config", "" if mode == "empty" else str(root / "host.yaml")]
            run = subprocess.run(args, env=env, text=True, capture_output=True, timeout=15)
            assert "PRIVATE-RAW-ERROR" not in run.stderr
            assert "RegisterHost" not in calls
            if mode in ("empty", "preflight-refused"):
                assert run.returncode == (2 if mode == "empty" else 8), (mode, run.returncode, run.stderr)
                assert "CreateTeamChatMessage" not in calls
            else:
                assert run.returncode == 0, (mode, run.returncode, run.stderr)
                assert json.loads(run.stdout)["seq"] == 17
                assert calls.count("CreateTeamChatMessage") == 1
                if mode == "absent":
                    assert "HostRegistrations" not in calls and "DeliveryBatches" not in calls
                if mode == "after-refused":
                    assert "do not repost" in run.stderr
            print(f"PASS {mode}")
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
print("5/5 compiled C2 fixture cases PASS; no live-host evidence")
