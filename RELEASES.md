# Dash Platform component releases

Tenderdash releases are coordinated with Dash Platform development and deployed
networks. A version number is not permission to roll out a consensus change
independently. Support is defined by Platform needs, not by a fixed count of
Tenderdash minor versions; see [README.md](README.md) and [SECURITY.md](SECURITY.md).

## Project release skill

Use [$tenderdash-release](.agents/skills/tenderdash-release/SKILL.md) to analyze
or prepare a release. Codex discovers it under `.agents/skills/`. Claude Code
uses `/tenderdash-release` through the `.claude/skills/tenderdash-release` symlink
to the same canonical `SKILL.md`. It reviews actual commits and net diffs,
selects the baseline and SemVer with a compatibility rationale, and prepares
reviewable release metadata. Analysis alone never creates a tag or release.

The skill replaces the branch-inference release script and the legacy manual
GoReleaser workflow. The local helpers collect evidence and build signed Linux
amd64/arm64 archives; publication remains an explicit release operation. Docker
images still publish through `.github/workflows/docker.yml` when a GitHub release
is published. No image or tag is published by the planning helpers.

## Branches and release sources

| Branch/source | Purpose |
| --- | --- |
| `dev` | Integration for the next coordinated Platform release |
| `master` | Latest stable release line |
| Temporary feature/release/hotfix branch | Isolated work, release freeze, or stable fix |
| Previously released tag | Exceptional fix for another affected Platform deployment |

Next releases start from a reviewed integration commit. Current-stable hotfixes
start from master, excluding unreleased development work. Forward-port stable
fixes to integration; do not merge next-version development into a patch release.
A fix for another deployed line starts from its exact released tag and requires
an explicit Platform support decision rather than automatic maintenance promises.

Release tags remain `vX.Y.Z`; prereleases use `vX.Y.Z-dev.N`. The skill evaluates
SemVer from compatibility, not branch names or `feat`/`fix` labels. It separately
reviews `P2PProtocol`, `BlockProtocol`, and `ABCISemVer`; a software version bump
does not update those contracts automatically.

Full-release promotion and stable-to-dev synchronization should preserve
ancestry. Master currently has protection rules that must be reconciled with
that merge strategy by maintainers before a release. The skill must report any
block and must not bypass or modify branch protections.

## Staged move to dev

This preparation keeps push CI on `master`, `dev`, `v1.8-dev`, and `v1.9-dev`.
Local lint and the release evidence helper prefer `origin/dev`, falling back
only to `origin/v1.8-dev` before the rename. An explicit PR target still overrides
the lint base. Historical branch names never determine the next version.

A separate administrator operation will rename `v1.8-dev` to `dev`, preserve
protections, and verify the default and open PR bases. PRs based on v1.9-dev need
an explicit retarget; stacked feature PRs retain their parent branch until that
parent lands. No branch is renamed or deleted by these helpers. Remove the
transition filters/fallback after consumers have migrated.

## Validation and artifacts

Record baseline tag/SHA, source SHA, release version, protocol decisions,
Platform compatibility, tests, and final reviewed merge SHA in the release PR.
Use the repository PR template and generated/reviewed changelog. Full releases
must pass appropriate consensus, state/storage, and integration/upgrade tests
for their actual diff; a documentation-only preparation does not prove a
network upgrade safe.

Publish tags only at the reviewed release commit. Existing tags are immutable.
A draft GitHub release is reviewable; publishing it triggers Docker image work.
The signed-artifact helper verifies the exact tag/SHA and creates Linux amd64
and arm64 binaries with detached GPG signatures, tarballs, `SHA256SUMS`, and
`SOURCE_COMMIT`. It does not upload, overwrite assets, or select a release.

When preparing an older deployment-line fix, check Docker alias behavior before
publishing: the current workflow promotes every non-dev release to `latest`
and the major alias. An older-line release needs a separate approved publishing
path that preserves newer aliases. Do not publish and repair the aliases later.

Local checks for this workflow:

```sh
sh scripts/test-local-gates.sh
python3 -m unittest discover -s scripts/release -p test_release.py
shellcheck scripts/release/build-artifacts.sh
```
