#!/usr/bin/env python3
"""Tests for govulncheck_gate.py: python3 -m unittest scripts/test_govulncheck_gate.py"""

import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
from contextlib import redirect_stdout

sys.path.insert(0, str(Path(__file__).resolve().parent))

import govulncheck_gate  # noqa: E402

CONFIG = {"config": {"protocol_version": "v1.0.0", "scanner_name": "govulncheck"}}


def osv(osv_id):
    return {"osv": {"id": osv_id, "summary": f"summary of {osv_id}"}}


def called(osv_id):
    return {
        "finding": {
            "osv": osv_id,
            "fixed_version": "v1.2.4",
            "trace": [
                {
                    "module": "example.com/dep",
                    "version": "v1.2.3",
                    "package": "example.com/dep",
                    "function": "Vulnerable",
                },
                {
                    "module": "github.com/dashpay/tenderdash",
                    "package": "github.com/dashpay/tenderdash/rpc",
                    "function": "Caller",
                    "position": {"filename": "rpc/caller.go", "line": 7},
                },
            ],
        }
    }


def imported_only(osv_id):
    return {
        "finding": {
            "osv": osv_id,
            "trace": [
                {
                    "module": "example.com/dep",
                    "version": "v1.2.3",
                    "package": "example.com/dep",
                }
            ],
        }
    }


def stream(*messages):
    # govulncheck emits indented JSON objects back to back.
    return "\n".join(json.dumps(m, indent=2) for m in messages)


class GateTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.allowlist = Path(self.dir.name) / "allowlist.txt"
        self.allowlist.write_text(
            "# comment\n\nGO-2026-0001 justified because reasons\n"
        )

    def tearDown(self):
        self.dir.cleanup()

    def gate(self, text):
        out = io.StringIO()
        with redirect_stdout(out):
            code = govulncheck_gate.gate(text, self.allowlist)
        return code, out.getvalue()

    def test_called_vulnerability_not_allowed_fails(self):
        code, out = self.gate(
            stream(CONFIG, osv("GO-2026-0002"), called("GO-2026-0002"))
        )
        self.assertEqual(code, 1)
        self.assertIn("GO-2026-0002", out)
        self.assertIn("rpc/caller.go:7", out)

    def test_called_vulnerability_allowed_passes_with_warning(self):
        code, out = self.gate(
            stream(CONFIG, osv("GO-2026-0001"), called("GO-2026-0001"))
        )
        self.assertEqual(code, 0)
        self.assertIn("::warning::GO-2026-0001", out)

    def test_imported_but_not_called_passes(self):
        code, _ = self.gate(
            stream(CONFIG, osv("GO-2026-0003"), imported_only("GO-2026-0003"))
        )
        self.assertEqual(code, 0)

    def test_stale_allowlist_entry_is_reported(self):
        code, out = self.gate(stream(CONFIG))
        self.assertEqual(code, 0)
        self.assertIn("GO-2026-0001", out)
        self.assertIn("no longer reported", out)

    def test_missing_config_fails(self):
        code, _ = self.gate(stream(osv("GO-2026-0002")))
        self.assertEqual(code, 2)

    def test_entry_without_justification_is_rejected(self):
        self.allowlist.write_text("GO-2026-0001\n")
        with self.assertRaises(SystemExit):
            self.gate(stream(CONFIG))


if __name__ == "__main__":
    unittest.main()
