"""Contracts of the govulncheck allowlist gate used by `make vulncheck`."""

import importlib.util
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
from contextlib import redirect_stdout

SCRIPT = Path(__file__).resolve().parents[1] / "govulncheck-gate.py"

CONFIG = {"config": {"protocol_version": "v1.0.0", "scan_level": "symbol"}}


def load_gate():
    spec = importlib.util.spec_from_file_location("govulncheck_gate", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


gate_module = load_gate()


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


class GateTests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.allowlist = Path(self.dir.name) / "allowlist.txt"
        self.allowlist.write_text(
            "# comment\n\nGO-2026-0001\tjustified because reasons\n"
        )

    def tearDown(self):
        self.dir.cleanup()

    def gate(self, text):
        out = io.StringIO()
        with redirect_stdout(out):
            code = gate_module.gate(text, self.allowlist)
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

    def test_imported_but_not_called_passes_and_is_counted(self):
        code, out = self.gate(
            stream(CONFIG, osv("GO-2026-0003"), imported_only("GO-2026-0003"))
        )
        self.assertEqual(code, 0)
        self.assertIn("1 vulnerabilities in imported or required code", out)

    def test_stale_allowlist_entry_is_reported(self):
        code, out = self.gate(stream(CONFIG))
        self.assertEqual(code, 0)
        self.assertIn("GO-2026-0001", out)
        self.assertIn("no longer reported", out)

    def test_missing_config_fails(self):
        code, _ = self.gate(stream(osv("GO-2026-0002")))
        self.assertEqual(code, 2)

    def test_non_symbol_scan_level_fails(self):
        config = {"config": {"protocol_version": "v1.0.0", "scan_level": "package"}}
        code, out = self.gate(stream(config))
        self.assertEqual(code, 2)
        self.assertIn("scan_level", out)

    def test_finding_without_trace_fails(self):
        finding = {"finding": {"osv": "GO-2026-0004"}}
        code, _ = self.gate(stream(CONFIG, finding))
        self.assertEqual(code, 2)

    def test_malformed_json_fails(self):
        with self.assertRaises(ValueError):
            self.gate(stream(CONFIG) + "\n{not json")

    def test_entry_without_justification_is_rejected(self):
        self.allowlist.write_text("GO-2026-0001\n")
        with self.assertRaises(SystemExit):
            self.gate(stream(CONFIG))

    def test_duplicate_entry_is_rejected(self):
        self.allowlist.write_text("GO-2026-0001 one\nGO-2026-0001 two\n")
        with self.assertRaises(SystemExit):
            self.gate(stream(CONFIG))

    def test_malformed_id_is_rejected(self):
        self.allowlist.write_text("GO-2026-0001,reason\n")
        with self.assertRaises(SystemExit):
            self.gate(stream(CONFIG))


class MainTests(unittest.TestCase):
    def run_main(self, *command):
        out = io.StringIO()
        with tempfile.NamedTemporaryFile("w", suffix=".txt") as allowlist:
            allowlist.write("# empty\n")
            allowlist.flush()
            with redirect_stdout(out):
                code = gate_module.main(["gate", allowlist.name, "--", *command])
        return code, out.getvalue()

    def test_scanner_failure_propagates(self):
        code, out = self.run_main(sys.executable, "-c", "raise SystemExit(3)")
        self.assertEqual(code, 3)
        self.assertIn("exited with 3", out)

    def test_scanner_killed_by_signal_fails(self):
        code, _ = self.run_main(
            sys.executable, "-c", "import os, signal; os.kill(os.getpid(), 9)"
        )
        self.assertNotEqual(code, 0)

    def test_clean_scan_passes(self):
        script = f"print({json.dumps(json.dumps(CONFIG))})"
        code, out = self.run_main(sys.executable, "-c", script)
        self.assertEqual(code, 0)
        self.assertIn("No called vulnerabilities", out)

    def test_usage_without_separator_exits(self):
        with self.assertRaises(SystemExit):
            gate_module.main(["gate", "allowlist.txt", "go"])


if __name__ == "__main__":
    unittest.main()
