#!/usr/bin/env python3
"""Collect local release evidence; version decisions and publication belong to the skill."""

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

STABLE_TAG = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z")


def git(repo, *args):
    return subprocess.check_output(
        ["git", "-C", str(repo), *args], text=True, stderr=subprocess.PIPE
    ).strip()


def ancestor(repo, older, newer):
    return (
        subprocess.run(
            ["git", "-C", str(repo), "merge-base", "--is-ancestor", older, newer],
            check=False,
            capture_output=True,
        ).returncode
        == 0
    )


def exists(repo, ref):
    return (
        subprocess.run(
            ["git", "-C", str(repo), "rev-parse", "--verify", ref],
            check=False,
            capture_output=True,
        ).returncode
        == 0
    )


def release_plan(repo, mode, source, baseline):
    match = STABLE_TAG.fullmatch(baseline)
    if not match:
        raise ValueError("baseline must be a stable release tag vX.Y.Z")
    if source.startswith("-"):
        raise ValueError("source must be a Git ref or commit")
    source_sha = git(repo, "rev-parse", "--verify", f"{source}^{{commit}}")
    baseline_sha = git(
        repo, "rev-parse", "--verify", f"refs/tags/{baseline}^{{commit}}"
    )
    if not ancestor(repo, baseline_sha, source_sha):
        raise ValueError("baseline must be an ancestor of source")
    baseline_version = tuple(map(int, match.groups()))
    for tag in git(repo, "tag", "--merged", source_sha).splitlines():
        version = STABLE_TAG.fullmatch(tag)
        if version and tuple(map(int, version.groups())) > baseline_version:
            raise ValueError(f"newer reachable stable tag {tag}; review the baseline")

    integration = (
        "origin/dev" if exists(repo, "refs/remotes/origin/dev") else "origin/v1.8-dev"
    )
    integration_sha = git(repo, "rev-parse", "--verify", f"{integration}^{{commit}}")
    stable_sha = git(repo, "rev-parse", "--verify", "origin/master^{commit}")
    if mode == "next":
        if not ancestor(repo, source_sha, integration_sha):
            raise ValueError(
                "next release source must be on the fetched integration lineage"
            )
        target = "dev" if integration == "origin/dev" else "v1.8-dev"
    else:
        if not ancestor(repo, stable_sha, source_sha):
            raise ValueError("hotfix source must descend from origin/master")
        candidate = set(
            git(repo, "rev-list", source_sha, f"^{stable_sha}").splitlines()
        )
        development = set(
            git(repo, "rev-list", integration_sha, f"^{stable_sha}").splitlines()
        )
        if candidate & development:
            raise ValueError(
                "hotfix includes unreleased development history; use a stable fix branch"
            )
        target = "master"

    return {
        "mode": mode,
        "source_sha": source_sha,
        "baseline": baseline,
        "baseline_sha": baseline_sha,
        "integration": integration,
        "integration_sha": integration_sha,
        "stable_sha": stable_sha,
        "preparation_base": target,
        "commits": git(
            repo,
            "log",
            "--reverse",
            "--format=%H %s%n%b",
            f"{baseline_sha}..{source_sha}",
        ),
        "diff": git(
            repo,
            "diff",
            "--no-ext-diff",
            "--no-textconv",
            baseline_sha,
            source_sha,
            "--",
        ),
        "stable_patch_equivalence": git(repo, "cherry", stable_sha, source_sha),
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=Path.cwd())
    parser.add_argument("--mode", choices=("next", "hotfix"), required=True)
    parser.add_argument("--source", required=True)
    parser.add_argument("--baseline", required=True)
    args = parser.parse_args()
    try:
        print(
            json.dumps(
                release_plan(args.repo, args.mode, args.source, args.baseline), indent=2
            )
        )
    except (ValueError, subprocess.CalledProcessError) as error:
        print(f"release plan: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
