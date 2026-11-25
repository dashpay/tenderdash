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
MODULE_VERSION = re.compile(r"(\S+)@(v\S+)")


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
    """Return {(OSV ID, module, version): justification}.

    An entry accepts one advisory for one module version, so a dependency
    change to another affected version is not covered by an old assessment.
    """
    allowed = {}
    for number, line in enumerate(Path(path).read_text().splitlines(), 1):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        fields = line.split(maxsplit=2)
        osv_id = fields[0]
        if not OSV_ID.fullmatch(osv_id):
            sys.exit(f"{path}:{number}: {osv_id!r} is not an OSV ID like GO-2026-1234")
        target = MODULE_VERSION.fullmatch(fields[1]) if len(fields) > 1 else None
        if target is None:
            sys.exit(f"{path}:{number}: {osv_id} needs a module@version after the ID")
        if len(fields) < 3:
            sys.exit(f"{path}:{number}: {osv_id} has no justification")
        key = (osv_id, target[1], target[2])
        if key in allowed:
            sys.exit(f"{path}:{number}: {fields[0]} {fields[1]} is listed twice")
        allowed[key] = fields[2]
    return allowed


def entry_name(key):
    return f"{key[0]} {key[1]}@{key[2]}"


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
    # Only a symbol-level source scan reports which vulnerabilities the code
    # calls; query mode, for one, emits advisories without scanning any code.
    for field, want in (("scan_mode", "source"), ("scan_level", "symbol")):
        if configs[0].get(field) != want:
            print(
                f"error: govulncheck {field} is {configs[0].get(field)!r}, need {want!r}"
            )
            return 2
    if not any(m.get("SBOM", {}).get("roots") for m in messages):
        print("error: govulncheck output lists no scanned packages (SBOM roots)")
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
        frame = finding["trace"][0]
        if frame.get("function"):
            # A frame from a local replace has no version; it cannot be
            # allowlisted, so the finding always blocks.
            key = (
                finding["osv"],
                frame.get("module") or "(unknown)",
                frame.get("version") or "(unknown)",
            )
            called.setdefault(key, finding)
        else:
            uncalled.add(finding["osv"])
    uncalled -= {key[0] for key in called}

    allowed = load_allowlist(allowlist_path)
    blocked = sorted(set(called) - set(allowed))
    for key in sorted(set(called) & set(allowed)):
        print(
            f"::warning::{entry_name(key)} is called but allowed by {allowlist_path}: {allowed[key]}"
        )
    for key in sorted(set(allowed) - set(called)):
        moved = sorted(f"{c[1]}@{c[2]}" for c in called if c[0] == key[0])
        if moved:
            print(
                f"::notice::{entry_name(key)} is not called, but {key[0]} is now called at "
                + ", ".join(moved)
                + f"; re-assess and update the entry in {allowlist_path}"
            )
        else:
            print(
                f"::notice::{entry_name(key)} is no longer called; remove it from {allowlist_path}"
            )
    if uncalled:
        print(
            f"{len(uncalled)} vulnerabilities in imported or required code are not called: "
            + ", ".join(sorted(uncalled))
        )
    for key in blocked:
        finding = called[key]
        print(f"Vulnerability {entry_name(key)}: {summaries.get(key[0], '')}")
        print(f"  Fixed in: {finding.get('fixed_version') or 'N/A'}")
        print(
            f"  Example trace: {' -> '.join(frame_name(f) for f in reversed(finding['trace']))}"
        )
        print(f"  More info: https://pkg.go.dev/vuln/{key[0]}")
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
