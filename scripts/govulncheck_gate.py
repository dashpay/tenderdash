#!/usr/bin/env python3
"""Fail on govulncheck findings unless an allowlist justifies them.

govulncheck has no way to suppress a finding, so this runs it with
`-format json` (which always exits 0) and fails only for vulnerabilities
the code calls (symbol-level findings) that the allowlist does not list.

Usage: govulncheck_gate.py ALLOWLIST -- COMMAND [ARGS...]
"""

import json
from pathlib import Path
import subprocess
import sys


def parse_stream(text):
    decoder = json.JSONDecoder()
    messages, pos = [], 0
    while True:
        while pos < len(text) and text[pos].isspace():
            pos += 1
        if pos == len(text):
            return messages
        message, pos = decoder.raw_decode(text, pos)
        messages.append(message)


def load_allowlist(path):
    """Return {OSV ID: justification}; every entry needs a justification."""
    allowed = {}
    for number, line in enumerate(Path(path).read_text().splitlines(), 1):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        osv_id, _, reason = line.partition(" ")
        if not reason.strip():
            sys.exit(f"{path}:{number}: {osv_id} has no justification")
        allowed[osv_id] = reason.strip()
    return allowed


def frame_name(frame):
    name = frame.get("function", "")
    if frame.get("receiver"):
        name = f"{frame['receiver']}.{name}"
    name = f"{frame.get('package', frame.get('module', ''))}.{name}"
    position = frame.get("position")
    if position:
        name += f" ({position.get('filename')}:{position.get('line')})"
    return name


def gate(text, allowlist_path):
    messages = parse_stream(text)
    if not any("config" in m for m in messages):
        print("error: govulncheck output has no config message")
        return 2

    summaries = {
        m["osv"]["id"]: m["osv"].get("summary", "") for m in messages if "osv" in m
    }
    called = {}
    for message in messages:
        finding = message.get("finding")
        if finding and finding.get("trace") and finding["trace"][0].get("function"):
            called.setdefault(finding["osv"], finding)

    allowed = load_allowlist(allowlist_path)
    blocked = sorted(set(called) - set(allowed))
    for osv_id in sorted(set(called) & set(allowed)):
        print(
            f"::warning::{osv_id} is called but allowed by {allowlist_path}: {allowed[osv_id]}"
        )
    for osv_id in sorted(set(allowed) - set(called)):
        print(
            f"::notice::{osv_id} is no longer reported; remove it from {allowlist_path}"
        )
    for osv_id in blocked:
        finding = called[osv_id]
        print(f"Vulnerability {osv_id}: {summaries.get(osv_id, '')}")
        print(f"  Fixed in: {finding.get('fixed_version') or 'N/A'}")
        print(
            f"  Example trace: {' -> '.join(frame_name(f) for f in reversed(finding['trace']))}"
        )
        print(f"  More info: https://pkg.go.dev/vuln/{osv_id}")
    if blocked:
        print(
            f"::error::{len(blocked)} called vulnerabilities are not in {allowlist_path}"
        )
        return 1
    print(f"No called vulnerabilities outside {allowlist_path}.")
    return 0


def main(argv):
    if len(argv) < 4 or argv[2] != "--":
        sys.exit(__doc__.strip().splitlines()[-1])
    result = subprocess.run(argv[3:], stdout=subprocess.PIPE, text=True)
    if result.returncode != 0:
        print(f"error: {' '.join(argv[3:])} exited with {result.returncode}")
        return result.returncode
    return gate(result.stdout, argv[1])


if __name__ == "__main__":
    sys.exit(main(sys.argv))
