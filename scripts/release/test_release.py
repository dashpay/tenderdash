"""Exercise release evidence and artifact boundaries without network or publication."""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import tarfile
import unittest

SCRIPTS = Path(__file__).resolve().parent


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.git("init", "-q")
        self.git("config", "user.name", "Release Test")
        self.git("config", "user.email", "test@example.invalid")
        self.git("commit", "-qm", "initial", "--allow-empty")
        self.git("tag", "v1.8.1")
        self.git("update-ref", "refs/remotes/origin/master", "HEAD")
        self.commit("breaking contract despite fix prefix", "fix: API")
        self.dev = self.git("rev-parse", "HEAD").strip()
        self.git("update-ref", "refs/remotes/origin/dev", self.dev)

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.repo), *args], text=True)

    def commit(self, content, message):
        (self.repo / "contract.txt").write_text(content)
        self.git("add", "contract.txt")
        self.git("commit", "-qm", message)

    def plan(self, *args, success=True):
        result = subprocess.run(
            [
                "python3",
                str(SCRIPTS / "plan.py"),
                "--repo",
                str(self.repo),
                "--mode",
                "next",
                "--source",
                self.dev,
                "--baseline",
                "v1.8.1",
                *args,
            ],
            text=True,
            capture_output=True,
        )
        self.assertEqual(result.returncode == 0, success, result.stderr)
        return json.loads(result.stdout) if success else result.stderr

    def test_plan_is_read_only_and_does_not_infer_semver_from_subject(self):
        before = self.git("status", "--porcelain")
        plan = self.plan()
        self.assertEqual(plan["source_sha"], self.dev)
        self.assertEqual(plan["baseline"], "v1.8.1")
        self.assertIn("fix: API", plan["commits"])
        self.assertIn("breaking contract", plan["diff"])
        self.assertNotIn("recommended_version", plan)
        self.assertEqual(before, self.git("status", "--porcelain"))
        self.assertEqual(self.dev, self.git("rev-parse", "HEAD").strip())

    def test_frozen_next_candidate_does_not_follow_moving_dev(self):
        self.commit("unreviewed next-cycle feature", "feat: experimental")
        self.git("update-ref", "refs/remotes/origin/dev", "HEAD")
        plan = self.plan()
        self.assertEqual(plan["source_sha"], self.dev)
        self.assertNotIn("experimental", plan["commits"])
        self.assertNotIn("unreviewed", plan["diff"])

    def test_hotfix_rejects_development_history(self):
        self.assertIn("development", self.plan("--mode", "hotfix", success=False))

    def test_hotfix_accepts_separate_stable_fix(self):
        self.git("checkout", "-q", "-b", "hotfix", "origin/master")
        self.commit("stable fix", "fix: repair")
        plan = self.plan("--mode", "hotfix", "--source", "HEAD")
        self.assertEqual(plan["preparation_base"], "master")
        self.assertIn("stable fix", plan["diff"])
        self.assertNotIn("breaking contract", plan["diff"])

    def test_newer_reachable_release_cannot_be_silently_ignored(self):
        self.git("tag", "v1.9.0")
        self.assertIn("newer", self.plan(success=False))

    def test_unreachable_baseline_is_rejected(self):
        self.git("checkout", "-q", "--orphan", "other")
        self.commit("unrelated", "release elsewhere")
        self.git("tag", "v9.0.0")
        self.assertIn("ancestor", self.plan("--baseline", "v9.0.0", success=False))

    def test_transition_uses_v18_not_highest_version(self):
        self.git("update-ref", "-d", "refs/remotes/origin/dev")
        self.git("update-ref", "refs/remotes/origin/v1.8-dev", self.dev)
        self.git("update-ref", "refs/remotes/origin/v9.0-dev", "origin/master")
        self.assertEqual(self.plan()["integration"], "origin/v1.8-dev")

    def test_backport_evidence_identifies_patch_already_on_stable(self):
        self.git("checkout", "-q", "-b", "stable", "origin/master")
        self.git("cherry-pick", "-x", self.dev)
        self.git("update-ref", "refs/remotes/origin/master", "HEAD")
        self.assertIn("- ", self.plan()["stable_patch_equivalence"])

    def test_revert_is_visible_in_net_diff_and_history(self):
        self.git("revert", "--no-edit", self.dev)
        self.git("update-ref", "refs/remotes/origin/dev", "HEAD")
        plan = self.plan("--source", "HEAD")
        self.assertEqual(plan["diff"], "")
        self.assertIn("Revert", plan["commits"])

    def test_artifacts_use_only_requested_tag_and_preserve_checkout(self):
        self.assert_artifacts_preserve_checkout(component_notices=False)

    def test_artifacts_include_component_license_and_provenance_when_present(self):
        self.assert_artifacts_preserve_checkout(component_notices=True)

    def assert_artifacts_preserve_checkout(self, component_notices):
        licenses = {"LICENSE": "reviewed root license"}
        if component_notices:
            licenses.update(
                {
                    "NOTICE": "reviewed root attribution",
                    "libs/db/LICENSE": "reviewed component license",
                    "libs/db/README.md": "reviewed component provenance",
                    "libs/db/NOTICE": "reviewed component attribution",
                }
            )
        for name, text in licenses.items():
            path = self.repo / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text)
        self.git("add", *licenses)
        self.git("commit", "-qm", "license fixtures")
        self.dev = self.git("rev-parse", "HEAD").strip()
        self.git("tag", "v2.0.0", self.dev)
        self.git("tag", "v99.0.0", self.dev)
        tools = self.root / "bin"
        tools.mkdir()
        (tools / "docker").write_text("""#!/usr/bin/env python3
import pathlib, subprocess, sys
args = sys.argv[1:]
if args[0] == 'buildx':
    context = args[-1]
    tag = subprocess.check_output(['git', '-C', context, 'describe', '--tags', '--exact-match'], text=True).strip()
    assert tag == 'v2.0.0', tag
elif args[0] == 'create':
    print('test-container')
elif args[0] == 'cp':
    pathlib.Path(args[-1]).write_text('binary fixture')
""")
        (tools / "gpg").write_text("""#!/usr/bin/env python3
import pathlib, sys
args = sys.argv[1:]
if '--detach-sign' in args:
    pathlib.Path(args[args.index('--output') + 1]).write_text('signature fixture')
""")
        for tool in tools.iterdir():
            tool.chmod(0o755)
        output = self.root / "artifacts"
        (self.repo / "LICENSE").write_text("unreviewed working-copy license")
        before = self.git("status", "--porcelain")
        result = subprocess.run(
            [
                "bash",
                str(SCRIPTS / "build-artifacts.sh"),
                str(self.repo),
                "v2.0.0",
                self.dev,
                str(output),
            ],
            env={**os.environ, "PATH": f"{tools}:{os.environ['PATH']}"},
            text=True,
            capture_output=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((output / "SOURCE_COMMIT").read_text().strip(), self.dev)
        self.assertEqual(len(list(output.glob("*.tar.gz"))), 2)
        for archive in output.glob("*.tar.gz"):
            with tarfile.open(archive) as package:
                for name, text in licenses.items():
                    self.assertIn(name, package.getnames())
                    self.assertEqual(package.extractfile(name).read().decode(), text)
                if not component_notices:
                    self.assertNotIn("libs/db/LICENSE", package.getnames())
        subprocess.run(
            ["sha256sum", "-c", "SHA256SUMS"],
            cwd=output,
            check=True,
            capture_output=True,
        )
        self.assertEqual(before, self.git("status", "--porcelain"))
        self.assertEqual(self.dev, self.git("rev-parse", "HEAD").strip())

    def test_artifacts_refuse_wrong_tag_before_build(self):
        result = subprocess.run(
            [
                "bash",
                str(SCRIPTS / "build-artifacts.sh"),
                str(self.repo),
                "v1.8.1",
                self.dev,
                str(self.root / "artifacts"),
            ],
            text=True,
            capture_output=True,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("tag does not match", result.stderr)
        self.assertFalse((self.root / "artifacts").exists())


if __name__ == "__main__":
    unittest.main()
