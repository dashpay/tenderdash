"""Check publication coverage and links without modifying documentation sources."""

import importlib.util
from pathlib import Path
import tempfile
import unittest

MODULE = Path(__file__).resolve().parents[1] / "prepare-docs.py"


class PrepareDocsTests(unittest.TestCase):
    def setUp(self):
        spec = importlib.util.spec_from_file_location("prepare_docs", MODULE)
        self.module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.module)
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for directory in ("book", "docs", "spec", "internal", "rpc/openapi"):
            (self.root / directory).mkdir(parents=True)
        self.write(
            "book/SUMMARY.md",
            "# Summary\n\n[Welcome](README.md)\n\n- [Guides](docs/README.md)\n- [Protocol](spec/consensus.md)\n",
        )
        self.write("book/README.md", "# Welcome\n\n[Guides](docs/README.md)\n")
        self.write("book/mermaid-LICENSE", "Fixture license\n")
        self.write(
            "docs/README.md",
            "---\ntitle: Old metadata\n---\n\n# Welcome\n\n[Protocol](../spec/consensus.md#rounds)\n[Code](../internal/state.go)\n[RPC](../api/rpc/index.html)\n",
        )
        self.write(
            "spec/consensus.md",
            "# Protocol\n\n[Home](../docs/)\n\n```mermaid\nflowchart LR\n  A --> B\n```\n",
        )
        self.write("internal/state.go", "package internal\n")
        self.write("rpc/openapi/index.html", "<title>Tenderdash RPC</title>")

    def write(self, path, content):
        (self.root / path).write_text(content)

    def test_stages_sources_assets_and_rpc_without_changing_originals(self):
        original = (self.root / "docs/README.md").read_text()
        self.write("docs/image.svg", "<svg/>")
        self.module.prepare(self.root, "v1.8-dev")
        staged = self.root / "book/src"
        welcome = (staged / "docs/README.md").read_text()
        self.assertNotIn("Old metadata", welcome)
        self.assertIn("[Guides](docs/index.md)", (staged / "README.md").read_text())
        self.assertIn("[Protocol](../spec/consensus.md#rounds)", welcome)
        self.assertIn(
            "https://github.com/dashpay/tenderdash/blob/v1.8-dev/internal/state.go",
            welcome,
        )
        self.assertIn(
            "[Home](../docs/index.md)", (staged / "spec/consensus.md").read_text()
        )
        self.assertIn("```mermaid", (staged / "spec/consensus.md").read_text())
        self.assertTrue((staged / "docs/image.svg").is_file())
        self.assertTrue((staged / "api/rpc/index.html").is_file())
        self.assertEqual((staged / "mermaid-LICENSE").read_text(), "Fixture license\n")
        self.assertEqual(original, (self.root / "docs/README.md").read_text())

    def test_multiline_source_links_and_external_urls(self):
        self.write(
            "docs/README.md",
            "# Guide\n\n[Source\ncode](../internal/state.go#example)\n"
            "[Outside](../../outside.md)\n[External](https://example.com/a.md#part)\n",
        )
        self.module.prepare(self.root, "a/b")
        content = (self.root / "book/src/docs/README.md").read_text()
        self.assertIn(
            "https://github.com/dashpay/tenderdash/blob/a%2Fb/internal/state.go#example",
            content,
        )
        self.assertIn("[Outside](../../outside.md)", content)
        self.assertIn("[External](https://example.com/a.md#part)", content)

    def test_ignores_old_vuepress_dependencies_and_output(self):
        for directory in ("node_modules", ".vuepress", "dist", "_build", "spec"):
            (self.root / "docs" / directory).mkdir()
            self.write(f"docs/{directory}/stale.md", "# Old build output\n")
        self.module.prepare(self.root, "v1.8-dev")
        for directory in ("node_modules", ".vuepress", "dist", "_build", "spec"):
            self.assertFalse((self.root / "book/src/docs" / directory).exists())

    def test_strips_metadata_without_final_newline(self):
        self.write("docs/README.md", "---\norder: false\n---")
        self.module.prepare(self.root, "v1.8-dev")
        self.assertEqual((self.root / "book/src/docs/README.md").read_text(), "")

    def test_rejects_unlisted_chapter(self):
        self.write("spec/new-protocol.md", "# New protocol\n")
        with self.assertRaisesRegex(ValueError, "new-protocol.md"):
            self.module.prepare(self.root, "v1.8-dev")

    def test_rejects_missing_chapter(self):
        (self.root / "spec/consensus.md").unlink()
        with self.assertRaisesRegex(ValueError, "consensus.md"):
            self.module.prepare(self.root, "v1.8-dev")

    def test_removes_stale_staged_files(self):
        self.module.prepare(self.root, "v1.8-dev")
        self.write("book/src/stale.md", "# Removed\n")
        self.module.prepare(self.root, "v1.8-dev")
        self.assertFalse((self.root / "book/src/stale.md").exists())


if __name__ == "__main__":
    unittest.main()
