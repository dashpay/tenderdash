#!/usr/bin/env python3
"""Bind Docker publication metadata to a validated source commit."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess


RELEASE_TAG = re.compile(
    r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
)
STAGING_IMAGE = "ghcr.io/dashpay/tenderdash"


def release_version(tag):
    match = RELEASE_TAG.fullmatch(tag)
    if not match or len(tag) > 128:
        raise ValueError("expected an existing vX.Y.Z[-prerelease] release tag")
    suffix = match[4]
    if suffix and any(
        part.isdigit() and len(part) > 1 and part[0] == "0"
        for part in suffix.split(".")
    ):
        raise ValueError("numeric prerelease identifiers cannot have leading zeroes")
    return tuple(int(match[i]) for i in (1, 2, 3)), suffix


def plan(event_name, event, ref, latest_release, repo=Path(".")):
    def git(*args):
        return subprocess.check_output(
            ["git", "-C", str(repo), *args], text=True
        ).strip()

    if event_name not in ("release", "workflow_dispatch", "pull_request"):
        raise ValueError("Docker publication requires a release or manual event")
    inputs = event.get("inputs") or {}
    staging_ref = inputs.get("source_ref") or ""
    if staging_ref and inputs.get("tag"):
        raise ValueError(
            "source_ref stages an image; tag requests a manual publication"
        )
    if event_name == "pull_request":
        pr = event["pull_request"]
        if (
            pr["base"]["ref"] != "master"
            or not pr["head"]["ref"].startswith("release_")
            or pr["head"]["repo"]["full_name"] != event["repository"]["full_name"]
        ):
            raise ValueError(
                "staging requires a same-repository release_* PR into master"
            )
        staging_ref = pr["head"]["sha"]
    release = event.get("release", {})
    tag = (
        release.get("tag_name", "")
        if event_name == "release"
        else inputs.get("tag") or ""
    )
    if not staging_ref and not tag and ref.startswith("refs/tags/"):
        tag = ref.removeprefix("refs/tags/")
    head = git("rev-parse", "HEAD")
    if staging_ref:
        if not re.fullmatch(r"[0-9a-f]{40}", staging_ref):
            if not staging_ref.startswith(("refs/heads/", "refs/tags/")):
                raise ValueError(
                    "source_ref must be a full commit SHA or refs/heads/ or refs/tags/ ref"
                )
            try:
                git("check-ref-format", staging_ref)
            except subprocess.CalledProcessError as error:
                raise ValueError("invalid source_ref") from error
        if staging_ref.startswith("refs/heads/"):
            remote_ref = staging_ref.replace("refs/heads/", "refs/remotes/origin/", 1)
            if (
                remote_ref
                in git("for-each-ref", "--format=%(refname)", remote_ref).splitlines()
            ):
                staging_ref = remote_ref
        source = git(
            "rev-parse", "--verify", "--end-of-options", f"{staging_ref}^{{commit}}"
        )
        if staging_ref.startswith("refs/tags/"):
            version = staging_ref.removeprefix("refs/tags/v")
            _, suffix = release_version(staging_ref.removeprefix("refs/tags/"))
        else:
            version_file = git("show", f"{source}:version/version.go")
            match = re.search(r'\bTMVersionDefault\s*=\s*"([^"\n]+)"', version_file)
            if not match:
                raise ValueError("staging source lacks TMVersionDefault")
            version = match[1]
            _, suffix = release_version(f"v{version}")
        tags = [f"sha-{source}"]
        options = "tenderdash,dev" if suffix else "tenderdash,stable"
    elif tag:
        numbers, suffix = release_version(tag)
        source = git("rev-parse", "--verify", f"refs/tags/{tag}^{{commit}}")
        if event_name == "release" and source != head:
            raise ValueError("release checkout does not match its tag")
        version = tag[1:]
        tags = [version]
        prerelease = bool(suffix) or release.get("prerelease", False)
        stable_versions = []
        for candidate in git("tag", "--list", "v*").splitlines():
            try:
                candidate_version, candidate_suffix = release_version(candidate)
            except ValueError:
                continue
            if not candidate_suffix:
                stable_versions.append(candidate_version)
        # The API must affirmatively identify this release as latest (an empty
        # result fails closed); an older line must also never overwrite aliases
        # for a higher version already tagged locally.
        if (
            event_name == "release"
            and not prerelease
            and latest_release == tag
            and numbers >= max(stable_versions, default=numbers)
        ):
            tags += [f"{numbers[0]}.{numbers[1]}", str(numbers[0]), "latest"]
        options = "tenderdash,dev" if prerelease else "tenderdash,stable"
    else:
        if not ref.startswith("refs/heads/"):
            raise ValueError("manual build requires a branch or release tag")
        branch = ref.removeprefix("refs/heads/")
        safe_branch = re.sub(r"[^a-zA-Z0-9_.-]", "-", branch)
        source = head
        version = f"unreleased-{safe_branch}-{source}"
        branch_tag = "dev" if branch == "dev" else f"branch-{safe_branch.lower()}"[:128]
        tags = [branch_tag, f"sha-{source}"]
        options = "tenderdash,dev"
    dockerfile = git("show", f"{source}:DOCKER/Dockerfile")
    if not re.search(r"^ARG VERSION(?:=.*)?$", dockerfile, re.MULTILINE):
        raise ValueError(
            "source lacks explicit Docker VERSION support; use a reviewed legacy publishing path"
        )
    return {
        "source_sha": source,
        "version": version,
        "tags": tags,
        "build_options": options,
    }


def staged_digest(source, version, options, digest=""):
    """Resolve once, then verify both platform configs at the immutable digest."""

    def inspect(image, template):
        return subprocess.check_output(
            ["docker", "buildx", "imagetools", "inspect", image, "--format", template],
            text=True,
            timeout=120,
        ).strip()

    try:
        if not digest:
            digest = inspect(f"{STAGING_IMAGE}:sha-{source}", "{{.Manifest.Digest}}")
        if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
            raise ValueError("invalid staging digest")
        image = f"{STAGING_IMAGE}@{digest}"
        index = json.loads(inspect(image, "{{json .Manifest}}"))
        platforms = []
        for descriptor in index["manifests"]:
            if descriptor["mediaType"] not in (
                "application/vnd.oci.image.manifest.v1+json",
                "application/vnd.docker.distribution.manifest.v2+json",
            ):
                raise ValueError("staged index must reference image manifests directly")
            platform = descriptor["platform"]
            name = f"{platform['os']}/{platform['architecture']}"
            if (
                name == "unknown/unknown"
                and descriptor.get("annotations", {}).get("vnd.docker.reference.type")
                == "attestation-manifest"
            ):
                continue
            platforms.append(name)
        # Buildx's .Image map collapses duplicate platforms; check the index first.
        if sorted(platforms) != ["linux/amd64", "linux/arm64"]:
            raise ValueError(
                "staged index must contain exactly one image per target platform"
            )
        images = json.loads(inspect(image, "{{json .Image}}"))
        if not isinstance(images, dict) or set(images) != {
            "linux/amd64",
            "linux/arm64",
        }:
            raise ValueError("staging requires linux/amd64 and linux/arm64")
        expected = {
            "org.opencontainers.image.revision": source,
            "org.opencontainers.image.version": version,
            "org.dashpay.tenderdash.build-options": options,
        }
        for platform, config in images.items():
            if f"{config['os']}/{config['architecture']}" != platform:
                raise ValueError("staged platform does not match its config")
            labels = config["config"]["Labels"]
            if any(labels.get(key) != value for key, value in expected.items()):
                raise ValueError(f"staged {platform} labels do not match the release")
    except (
        subprocess.SubprocessError,
        ValueError,
        KeyError,
        TypeError,
        AttributeError,
    ) as error:
        print(f"Staged image unavailable or incompatible; rebuild required: {error}")
        return ""
    return digest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--resolve-staged", action="store_true")
    parser.add_argument("--require-staged", action="store_true")
    parser.add_argument("--digest", default="")
    args = parser.parse_args()
    if args.resolve_staged:
        digest = staged_digest(
            os.environ["SOURCE_SHA"],
            os.environ["VERSION"],
            os.environ["BUILD_OPTIONS"],
            args.digest,
        )
        with Path(os.environ["GITHUB_OUTPUT"]).open("a") as output:
            output.write(f"digest={digest}\n")
        if args.require_staged and not digest:
            raise SystemExit("staged image verification failed")
        return
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
    result = plan(
        os.environ["GITHUB_EVENT_NAME"],
        event,
        os.environ["GITHUB_REF"],
        os.environ["LATEST_RELEASE_TAG"],
    )
    with Path(os.environ["GITHUB_OUTPUT"]).open("a") as output:
        for key in ("source_sha", "version", "build_options"):
            output.write(f"{key}={result[key]}\n")
        output.write("tags<<DOCKER_TAGS\n")
        for tag in result["tags"]:
            output.write(f"type=raw,value={tag}\n")
        output.write("DOCKER_TAGS\n")
        output.write("aliases<<DOCKER_ALIASES\n")
        if os.environ["GITHUB_EVENT_NAME"] == "release":
            for alias in result["tags"][1:]:
                output.write(f"{alias}\n")
        output.write("DOCKER_ALIASES\n")


if __name__ == "__main__":
    main()
