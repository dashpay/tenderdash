#!/usr/bin/env python3
"""Bind Docker publication metadata to a validated source commit."""

import json
import os
from pathlib import Path
import re
import subprocess


RELEASE_TAG = re.compile(
    r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
)


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

    if event_name not in ("release", "workflow_dispatch"):
        raise ValueError("Docker publication requires a release or manual event")
    release = event.get("release", {})
    tag = (
        release.get("tag_name", "")
        if event_name == "release"
        else event.get("inputs", {}).get("tag", "")
    )
    if not tag and ref.startswith("refs/tags/"):
        tag = ref.removeprefix("refs/tags/")
    head = git("rev-parse", "HEAD")
    if tag:
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
        # The API must identify this release as latest; an older line must also
        # never overwrite aliases for a higher version already tagged locally.
        if (
            event_name == "release"
            and not prerelease
            and latest_release in ("", tag)
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


def main():
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
