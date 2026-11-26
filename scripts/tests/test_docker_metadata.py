"""Offline source, version and alias contracts for Docker publication."""

import importlib.util
import json
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
        (self.repo / "version").mkdir()
        (self.repo / "version/version.go").write_text(
            'package version\nconst TMVersionDefault = "2.0.0"\n'
        )
        self.git("add", "DOCKER/Dockerfile", "version/version.go")
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
        self.assertIn("latest", self.plan(latest="v2.0.0")["tags"])

    def test_missing_latest_release_never_promotes_aliases(self):
        self.git("tag", "v2.0.0")
        self.assertEqual(self.plan(latest="")["tags"], ["2.0.0"])

    def test_build_job_pending_latest_never_promotes_aliases(self):
        # docker.yml's build job passes LATEST_RELEASE_TAG=pending; only the
        # promote-aliases job may resolve aliases.
        self.git("tag", "v2.0.0")
        self.assertEqual(self.plan(latest="pending")["tags"], ["2.0.0"])

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

    def staging_plan(self, source_ref):
        return self.module.plan(
            "workflow_dispatch",
            {"inputs": {"source_ref": source_ref}},
            "refs/heads/dev",
            "pending",
            self.repo,
        )

    def test_staging_dispatch_accepts_sha_branch_and_annotated_tag(self):
        self.git(
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.invalid",
            "tag",
            "-am",
            "release",
            "v2.0.0",
        )
        for ref in (self.sha, "refs/heads/dev", "refs/tags/v2.0.0"):
            with self.subTest(ref=ref):
                result = self.staging_plan(ref)
                self.assertEqual(result["source_sha"], self.sha)
                self.assertEqual(result["version"], "2.0.0")
                self.assertEqual(result["tags"], [f"sha-{self.sha}"])
                self.assertEqual(result["build_options"], "tenderdash,stable")

    def test_staging_tag_version_overrides_source_default(self):
        self.git("tag", "v2.1.0-dev.1")
        result = self.staging_plan("refs/tags/v2.1.0-dev.1")
        self.assertEqual(result["version"], "2.1.0-dev.1")
        self.assertEqual(result["build_options"], "tenderdash,dev")
        self.assertEqual(result["tags"], [f"sha-{self.sha}"])

    def test_staging_branch_resolves_fetched_remote_branch(self):
        self.git("update-ref", "refs/remotes/origin/release_2.0.0", self.sha)
        result = self.staging_plan("refs/heads/release_2.0.0")
        self.assertEqual(result["source_sha"], self.sha)

    def test_required_staging_cli_fails_without_verified_digest(self):
        output = self.repo / "output"
        with (
            mock.patch(
                "sys.argv", [str(SCRIPT), "--resolve-staged", "--require-staged"]
            ),
            mock.patch.dict(
                os.environ,
                {
                    "SOURCE_SHA": self.sha,
                    "VERSION": "2.0.0",
                    "BUILD_OPTIONS": "tenderdash,stable",
                    "GITHUB_OUTPUT": str(output),
                },
            ),
            mock.patch.object(
                self.module.subprocess,
                "check_output",
                side_effect=subprocess.CalledProcessError(1, "inspect"),
            ),
            self.assertRaises(SystemExit),
        ):
            self.module.main()
        self.assertEqual(output.read_text(), "digest=\n")

    def test_staging_cli_verifies_build_output_without_resolving_mutable_tag(self):
        digest = "sha256:" + "a" * 64
        output = self.repo / "output"
        with (
            mock.patch(
                "sys.argv",
                [
                    str(SCRIPT),
                    "--resolve-staged",
                    "--require-staged",
                    "--digest",
                    digest,
                ],
            ),
            mock.patch.dict(
                os.environ,
                {
                    "SOURCE_SHA": self.sha,
                    "VERSION": "2.0.0",
                    "BUILD_OPTIONS": "tenderdash,stable",
                    "GITHUB_OUTPUT": str(output),
                },
            ),
            mock.patch.object(
                self.module.subprocess,
                "check_output",
                side_effect=[
                    json.dumps(self.staged_index()),
                    json.dumps(self.staged_images()),
                ],
            ) as inspect,
        ):
            self.module.main()
        self.assertEqual(output.read_text(), f"digest={digest}\n")
        self.assertEqual(inspect.call_count, 2)
        self.assertIn(f"ghcr.io/dashpay/tenderdash@{digest}", inspect.call_args.args[0])

    def test_staging_rejects_ambiguous_or_unsafe_inputs(self):
        for ref in ("dev", "HEAD~1", "--all", "refs/heads/dev\nlatest"):
            with self.subTest(ref=ref), self.assertRaises(ValueError):
                self.staging_plan(ref)
        with self.assertRaises(ValueError):
            self.module.plan(
                "workflow_dispatch",
                {
                    "inputs": {
                        "source_ref": self.sha,
                        "tag": "v2.0.0",
                    }
                },
                "refs/heads/dev",
                "pending",
                self.repo,
            )

    def release_pr(self):
        return {
            "repository": {"full_name": "dashpay/tenderdash"},
            "pull_request": {
                "base": {"ref": "master"},
                "head": {
                    "ref": "release_2.0.0",
                    "sha": self.sha,
                    "repo": {"full_name": "dashpay/tenderdash"},
                },
            },
        }

    def test_release_pr_stages_head_not_synthetic_merge(self):
        self.git(
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.invalid",
            "commit",
            "-qm",
            "synthetic merge",
            "--allow-empty",
        )
        result = self.module.plan(
            "pull_request",
            self.release_pr(),
            "refs/pull/12/merge",
            "pending",
            self.repo,
        )
        self.assertEqual(result["source_sha"], self.sha)
        self.assertEqual(result["version"], "2.0.0")
        self.assertEqual(result["tags"], [f"sha-{self.sha}"])

    def test_pr_gate_rejects_forks_other_bases_and_nonrelease_branches(self):
        for field, value in (
            ("repo", {"full_name": "fork/tenderdash"}),
            ("ref", "feature/test"),
        ):
            event = self.release_pr()
            event["pull_request"]["head"][field] = value
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.module.plan(
                    "pull_request", event, "refs/pull/12/merge", "", self.repo
                )
        event = self.release_pr()
        event["pull_request"]["base"]["ref"] = "dev"
        with self.assertRaises(ValueError):
            self.module.plan("pull_request", event, "refs/pull/12/merge", "", self.repo)

    def staged_images(self):
        return {
            f"linux/{arch}": {
                "os": "linux",
                "architecture": arch,
                "config": {
                    "Labels": {
                        "org.opencontainers.image.revision": self.sha,
                        "org.opencontainers.image.version": "2.0.0",
                        "org.dashpay.tenderdash.build-options": "tenderdash,stable",
                    }
                },
            }
            for arch in ("amd64", "arm64")
        }

    def staged_index(self):
        return {
            "manifests": [
                {
                    "digest": "sha256:" + digit * 64,
                    "mediaType": "application/vnd.oci.image.manifest.v1+json",
                    "platform": {"os": "linux", "architecture": arch},
                }
                for arch, digit in (("amd64", "b"), ("arm64", "c"))
            ]
        }

    def test_staging_rejects_duplicate_or_extra_runnable_descriptors(self):
        for platform in (
            {"os": "linux", "architecture": "amd64"},
            {"os": "windows", "architecture": "amd64"},
            {"os": "unknown", "architecture": "unknown"},
        ):
            index = self.staged_index()
            index["manifests"].append(
                {
                    "digest": "sha256:" + "d" * 64,
                    "platform": platform,
                    "mediaType": "application/vnd.oci.image.manifest.v1+json",
                }
            )
            responses = {
                "{{.Manifest.Digest}}": "sha256:" + "a" * 64,
                "{{json .Manifest}}": json.dumps(index),
                "{{json .Image}}": json.dumps(self.staged_images()),
            }
            with (
                self.subTest(platform=platform),
                mock.patch.object(
                    self.module.subprocess,
                    "check_output",
                    side_effect=lambda command, **kwargs: responses[command[-1]],
                ),
            ):
                self.assertEqual(
                    self.module.staged_digest(self.sha, "2.0.0", "tenderdash,stable"),
                    "",
                )

    def test_staging_accepts_standard_buildkit_attestations(self):
        index = self.staged_index()
        index["manifests"].append(
            {
                "digest": "sha256:" + "d" * 64,
                "mediaType": "application/vnd.oci.image.manifest.v1+json",
                "platform": {"os": "unknown", "architecture": "unknown"},
                "annotations": {
                    "vnd.docker.reference.type": "attestation-manifest",
                    "vnd.docker.reference.digest": "sha256:" + "b" * 64,
                },
            }
        )
        digest = "sha256:" + "a" * 64
        with mock.patch.object(
            self.module.subprocess,
            "check_output",
            side_effect=[
                digest,
                json.dumps(index),
                json.dumps(self.staged_images()),
            ],
        ):
            self.assertEqual(
                self.module.staged_digest(self.sha, "2.0.0", "tenderdash,stable"),
                digest,
            )

    def test_staging_rejects_nested_indexes_including_attestations(self):
        for media_type in (
            "application/vnd.oci.image.index.v1+json",
            "application/vnd.docker.distribution.manifest.list.v2+json",
        ):
            for attestation in (False, True):
                index = self.staged_index()
                nested = {
                    "mediaType": media_type,
                    "digest": "sha256:" + "d" * 64,
                    "platform": {"os": "linux", "architecture": "amd64"},
                }
                if attestation:
                    nested["platform"] = {"os": "unknown", "architecture": "unknown"}
                    nested["annotations"] = {
                        "vnd.docker.reference.type": "attestation-manifest"
                    }
                    index["manifests"].append(nested)
                else:
                    index["manifests"][0] = nested
                with (
                    self.subTest(media_type=media_type, attestation=attestation),
                    mock.patch.object(
                        self.module.subprocess,
                        "check_output",
                        side_effect=[
                            "sha256:" + "a" * 64,
                            json.dumps(index),
                            json.dumps(self.staged_images()),
                        ],
                    ),
                ):
                    self.assertEqual(
                        self.module.staged_digest(
                            self.sha, "2.0.0", "tenderdash,stable"
                        ),
                        "",
                    )

    def test_malformed_platform_configs_request_rebuild(self):
        for response in ("invalid json", "null", "[]", "{}"):
            with (
                self.subTest(response=response),
                mock.patch.object(
                    self.module.subprocess,
                    "check_output",
                    side_effect=[
                        "sha256:" + "a" * 64,
                        json.dumps(self.staged_index()),
                        response,
                    ],
                ),
            ):
                self.assertEqual(
                    self.module.staged_digest(self.sha, "2.0.0", "tenderdash,stable"),
                    "",
                )

    def test_staged_digest_is_pinned_before_revision_and_version_verification(self):
        digest = "sha256:" + "a" * 64
        with mock.patch.object(
            self.module.subprocess,
            "check_output",
            side_effect=[
                digest,
                json.dumps(self.staged_index()),
                json.dumps(self.staged_images()),
            ],
        ) as inspect:
            self.assertEqual(
                self.module.staged_digest(self.sha, "2.0.0", "tenderdash,stable"),
                digest,
            )
        calls = inspect.call_args_list
        self.assertIn(f"ghcr.io/dashpay/tenderdash:sha-{self.sha}", calls[0].args[0])
        self.assertIn(f"ghcr.io/dashpay/tenderdash@{digest}", calls[1].args[0])
        self.assertIn(f"ghcr.io/dashpay/tenderdash@{digest}", calls[2].args[0])
        self.assertNotIn(f"ghcr.io/dashpay/tenderdash:sha-{self.sha}", calls[1].args[0])
        self.assertNotIn(f"ghcr.io/dashpay/tenderdash:sha-{self.sha}", calls[2].args[0])

    def test_missing_malformed_or_unreadable_stage_requests_rebuild(self):
        for response in (subprocess.CalledProcessError(1, "inspect"), "", "bad-digest"):
            with (
                self.subTest(response=response),
                mock.patch.object(
                    self.module.subprocess,
                    "check_output",
                    side_effect=[response],
                ),
            ):
                self.assertEqual(
                    self.module.staged_digest(self.sha, "2.0.0", "tenderdash,stable"),
                    "",
                )
        for response in ("invalid json", "null", "[]", "{}"):
            with (
                self.subTest(response=response),
                mock.patch.object(
                    self.module.subprocess,
                    "check_output",
                    side_effect=["sha256:" + "a" * 64, response],
                ),
            ):
                self.assertEqual(
                    self.module.staged_digest(self.sha, "2.0.0", "tenderdash,stable"),
                    "",
                )

    def test_either_platform_with_wrong_or_missing_labels_requests_rebuild(self):
        for arch in ("amd64", "arm64"):
            for label in (
                "org.opencontainers.image.revision",
                "org.opencontainers.image.version",
                "org.dashpay.tenderdash.build-options",
            ):
                for value in (None, "wrong"):
                    images = self.staged_images()
                    labels = images[f"linux/{arch}"]["config"]["Labels"]
                    if value is None:
                        del labels[label]
                    else:
                        labels[label] = value
                    with (
                        self.subTest(arch=arch, label=label, value=value),
                        mock.patch.object(
                            self.module.subprocess,
                            "check_output",
                            side_effect=[
                                "sha256:" + "a" * 64,
                                json.dumps(self.staged_index()),
                                json.dumps(images),
                            ],
                        ),
                    ):
                        self.assertEqual(
                            self.module.staged_digest(
                                self.sha, "2.0.0", "tenderdash,stable"
                            ),
                            "",
                        )

    def test_single_platform_stage_requests_rebuild(self):
        images = self.staged_images()
        del images["linux/arm64"]
        with mock.patch.object(
            self.module.subprocess,
            "check_output",
            side_effect=[
                "sha256:" + "a" * 64,
                json.dumps(self.staged_index()),
                json.dumps(images),
            ],
        ):
            self.assertEqual(
                self.module.staged_digest(self.sha, "2.0.0", "tenderdash,stable"), ""
            )


if __name__ == "__main__":
    unittest.main()
