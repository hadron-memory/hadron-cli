#!/usr/bin/env python3
"""Compare the onboard fixture with an explicitly selected server git revision.

Reads git objects, never a sibling's working files. No network access.
"""
import argparse
import json
from pathlib import Path
import re
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--server-repo", required=True)
parser.add_argument("--server-ref", required=True)
args = parser.parse_args()
revision = subprocess.check_output(
    ["git", "-C", args.server_repo, "rev-parse", "--verify", args.server_ref + "^{commit}"], text=True
).strip()
print(f"onboard host contract: {args.server_repo} @ {revision}")
source = subprocess.check_output(
    ["git", "-C", args.server_repo, "show", revision + ":src/lib/hostOperations/types.ts"], text=True
)
fixture = json.loads((Path(__file__).resolve().parents[1] / "internal/cmd/team/testdata/onboard-host-contract.json").read_text())
for operation, expected in fixture["operations"].items():
    match = re.search(r"'" + re.escape(operation) + r"':\s*\{\s*required:\s*\{([^}]+)\}\s*(?:,\s*optional:\s*\{([^}]+)\}\s*)?\}", source)
    if match is None:
        raise SystemExit(f"Cannot parse live {operation}; inspect its contract before updating the fixture")
    def fields(value):
        return dict(re.findall(r"(\w+):\s*'(\w+)'", value or ""))
    actual = {"required": fields(match[1]), "optional": fields(match[2])}
    if actual != expected:
        raise SystemExit(f"DRIFT: {operation}: live={actual}, fixture={expected}")
    print(f"OK {operation}: {actual}")
