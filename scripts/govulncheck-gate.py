#!/usr/bin/env python3
"""Fail on govulncheck findings unless an allowlist justifies them.

govulncheck has no way to suppress a finding, so this runs it with
`-format json` (which always exits 0) and fails only for vulnerabilities
the code calls (symbol-level findings) that the allowlist does not list.

Usage: govulncheck-gate.py ALLOWLIST -- COMMAND [ARGS...]
"""

import json
from pathlib import Path
import re
import subprocess
import sys

OSV_ID = re.compile(r"GO-\d{4}-\d+")


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
        fields = line.split(maxsplit=1)
        osv_id = fields[0]
        if not OSV_ID.fullmatch(osv_id):
            sys.exit(f"{path}:{number}: {osv_id!r} is not an OSV ID like GO-2026-1234")
        if len(fields) < 2:
            sys.exit(f"{path}:{number}: {osv_id} has no justification")
        if osv_id in allowed:
            sys.exit(f"{path}:{number}: {osv_id} is listed twice")
        allowed[osv_id] = fields[1]
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
    configs = [m["config"] for m in messages if "config" in m]
    if not configs:
        print("error: govulncheck output has no config message")
        return 2
    # Only a symbol-level scan reports which vulnerabilities the code calls.
    if configs[0].get("scan_level") != "symbol":
        print(
            f"error: govulncheck scan_level is {configs[0].get('scan_level')!r}, need 'symbol'"
        )
        return 2

    summaries = {
        m["osv"]["id"]: m["osv"].get("summary", "") for m in messages if "osv" in m
    }
    called, uncalled = {}, set()
    for message in messages:
        finding = message.get("finding")
        if finding is None:
            continue
        if not finding.get("osv") or not finding.get("trace"):
            print(f"error: malformed govulncheck finding: {json.dumps(finding)}")
            return 2
        if finding["trace"][0].get("function"):
            called.setdefault(finding["osv"], finding)
        else:
            uncalled.add(finding["osv"])
    uncalled -= set(called)

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
    if uncalled:
        print(
            f"{len(uncalled)} vulnerabilities in imported or required code are not called: "
            + ", ".join(sorted(uncalled))
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
        return result.returncode if result.returncode > 0 else 1
    return gate(result.stdout, argv[1])


if __name__ == "__main__":
    sys.exit(main(sys.argv))
