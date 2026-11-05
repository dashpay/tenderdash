"""Offline source, version and alias contracts for Docker publication."""

import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

SCRIPT = Path(__file__).resolve().parents[1] / "docker-metadata.py"


class DockerMetadataTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec = importlib.util.spec_from_file_location("docker_metadata", SCRIPT)
        cls.module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cls.module)

    def setUp(self):
        self.scratch = tempfile.TemporaryDirectory()
        self.addCleanup(self.scratch.cleanup)
        self.repo = Path(self.scratch.name)
        self.git("init", "-q", "-b", "dev")
        (self.repo / "DOCKER").mkdir()
        (self.repo / "DOCKER/Dockerfile").write_text(
            'ARG VERSION\nRUN make build-binary VERSION="$VERSION"\n'
        )
        self.git("add", "DOCKER/Dockerfile")
        self.git(
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.invalid",
            "commit",
            "-qm",
            "source",
        )
        self.sha = self.git("rev-parse", "HEAD")

    def git(self, *args):
        return subprocess.check_output(
            ["git", "-C", str(self.repo), *args], text=True
        ).strip()

    def plan(
        self,
        name="release",
        tag="v2.0.0",
        prerelease=False,
        latest="v2.0.0",
        ref="refs/heads/dev",
    ):
        event = (
            {"release": {"tag_name": tag, "prerelease": prerelease}}
            if name == "release"
            else {"inputs": {"tag": tag}}
        )
        return self.module.plan(name, event, ref, latest, self.repo)

    def test_stable_release_binds_source_version_and_aliases(self):
        self.git("tag", "v2.0.0")
        result = self.plan()
        self.assertEqual(result["source_sha"], self.sha)
        self.assertEqual(result["version"], "2.0.0")
        self.assertEqual(result["tags"], ["2.0.0", "2.0", "2", "latest"])

    def test_any_prerelease_suffix_cannot_promote_stable_aliases(self):
        for tag in ["v2.0.0-dev.1", "v2.0.0-rc.1", "v2.0.0-beta"]:
            with self.subTest(tag=tag):
                self.git("tag", tag)
                result = self.plan(tag=tag, latest=tag)
                self.assertEqual(result["tags"], [tag[1:]])
                self.assertEqual(result["build_options"], "tenderdash,dev")

    def test_prerelease_flag_prevents_stable_aliases_for_plain_tag(self):
        self.git("tag", "v2.0.0")
        self.assertEqual(self.plan(prerelease=True)["tags"], ["2.0.0"])

    def test_older_line_cannot_overwrite_newer_aliases_even_if_marked_latest(self):
        self.git("tag", "v1.8.3")
        self.git("tag", "v2.0.0")
        self.assertEqual(self.plan(tag="v1.8.3", latest="v1.8.3")["tags"], ["1.8.3"])

    def test_nonlatest_release_and_manual_release_never_promote_aliases(self):
        self.git("tag", "v2.0.0")
        self.assertEqual(self.plan(latest="v1.8.2")["tags"], ["2.0.0"])
        self.assertEqual(self.plan(name="workflow_dispatch")["tags"], ["2.0.0"])

    def test_first_stable_release_can_promote_aliases(self):
        self.git("tag", "v2.0.0")
        self.assertIn("latest", self.plan(latest="")["tags"])

    def test_manual_tag_resolves_tag_not_selected_workflow_branch(self):
        self.git("tag", "v2.0.0")
        self.git(
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.invalid",
            "commit",
            "-qm",
            "later",
            "--allow-empty",
        )
        self.assertEqual(self.plan(name="workflow_dispatch")["source_sha"], self.sha)
        with self.assertRaises(ValueError):
            self.plan()

    def test_manual_branch_is_unreleased_and_has_sha_tag(self):
        result = self.plan(name="workflow_dispatch", tag="", ref="refs/heads/dev")
        self.assertEqual(result["source_sha"], self.sha)
        self.assertEqual(result["version"], f"unreleased-dev-{self.sha}")
        self.assertEqual(result["tags"], ["dev", f"sha-{self.sha}"])

    def test_manual_tag_selected_in_ui_uses_release_version_without_aliases(self):
        self.git("tag", "v2.0.0")
        result = self.plan(name="workflow_dispatch", tag="", ref="refs/tags/v2.0.0")
        self.assertEqual(result["version"], "2.0.0")
        self.assertEqual(result["tags"], ["2.0.0"])

    def test_invalid_missing_tag_and_unexpected_event_fail_closed(self):
        for tag in [
            "latest",
            "v2.0",
            "v02.0.0",
            "v2.0.0\nlatest",
            "v2.0.0-01",
            "v2.0.0+build.1",
        ]:
            with (
                self.subTest(tag=tag),
                self.assertRaises((ValueError, subprocess.CalledProcessError)),
            ):
                self.plan(name="workflow_dispatch", tag=tag)
        with self.assertRaises(ValueError):
            self.plan(name="push")

    def test_tagged_source_without_version_override_fails_closed(self):
        (self.repo / "DOCKER/Dockerfile").write_text("RUN make build-binary\n")
        self.git("add", "DOCKER/Dockerfile")
        self.git(
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.invalid",
            "commit",
            "-qm",
            "legacy",
        )
        self.git("tag", "v2.0.0")
        with self.assertRaises(ValueError):
            self.plan()

    def test_manual_branch_without_version_override_fails_closed(self):
        (self.repo / "DOCKER/Dockerfile").write_text("RUN make build-binary\n")
        self.git("add", "DOCKER/Dockerfile")
        self.git(
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.invalid",
            "commit",
            "-qm",
            "legacy",
        )
        with self.assertRaises(ValueError):
            self.plan(name="workflow_dispatch", tag="")

    def test_cli_emits_bound_version_tags_and_aliases(self):
        self.git("tag", "v2.0.0")
        event_path = self.repo / "event.json"
        event_path.write_text('{"release":{"tag_name":"v2.0.0","prerelease":false}}')
        output = self.repo / "output"
        with mock.patch.dict(
            os.environ,
            {
                "GITHUB_EVENT_NAME": "release",
                "GITHUB_REF": "refs/tags/v2.0.0",
                "GITHUB_EVENT_PATH": str(event_path),
                "GITHUB_OUTPUT": str(output),
                "LATEST_RELEASE_TAG": "v2.0.0",
            },
        ):
            subprocess.run(["python3", "-B", str(SCRIPT)], cwd=self.repo, check=True)
        content = output.read_text()
        self.assertIn(f"source_sha={self.sha}\nversion=2.0.0\n", content)
        self.assertIn("type=raw,value=2.0.0\n", content)
        self.assertIn(
            "aliases<<DOCKER_ALIASES\n2.0\n2\nlatest\nDOCKER_ALIASES", content
        )

    def test_manual_latest_named_branch_does_not_publish_latest(self):
        result = self.plan(name="workflow_dispatch", tag="", ref="refs/heads/latest")
        self.assertNotIn("latest", result["tags"])
        self.assertIn("branch-latest", result["tags"])


if __name__ == "__main__":
    unittest.main()
