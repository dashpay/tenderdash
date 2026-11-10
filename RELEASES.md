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
SemVer from compatibility, not branch names or `feat`/`fix` labels.

Full-release promotion and stable-to-dev synchronization should preserve
ancestry. Master currently has protection rules that must be reconciled with
that merge strategy by maintainers before a release. The skill must report any
block and must not bypass or modify branch protections.

## Versioning policy

The software version (`TMVersionDefault`) is decided only by these contracts:

| Contract | Covers |
| --- | --- |
| RPC | Methods, parameters, responses and their semantics |
| ABCI | Application methods, messages, return handling and app coordination |
| P2P and consensus | Message/channel formats, consensus rules and versions, proposer selection, peer compatibility |
| Block format | Serialization, hashes, signing and validity of blocks, votes and commits |

- **Major:** a backward-incompatible change to any of them. A new consensus
  version (for example `CONSENSUS_VERSION_3`) is a major even when a chain
  parameter activates it, as are changes older nodes cannot follow, removed or
  semantically changed RPC, and an `ABCISemVer` major.
- **Minor:** a backward-compatible extension, such as new optional fields, RPC
  methods or parameters that leave existing networks unchanged.
- **Patch:** fixes that change none of these contracts. A security or consensus
  fix is not automatically patch-safe; classify it by its contract effect.

Exported Go APIs, configuration and CLI (including removed settings and tightened
validation), tooling, CI and documentation do not affect the version; the
changelog describes them and any operator action. Persisted data is not versioned:
storage changes must migrate automatically and invisibly to operators, since most
data can be rebuilt by resync and rollback is an edge case. A change that needs a
manual migration blocks the release.

Protocol counters are reviewed separately; a software version bump does not
update them automatically:

- `ABCISemVer` is the ABCI contract's own SemVer. Drive (rs-tenderdash-abci)
  accepts `^major.minor` of its linked version.
- `P2PProtocol` changes with P2P messages or behavior.
- `BlockProtocol` changes only with the block format. Nodes enforce it strictly,
  so its bump also requires a major. It is a full hard fork and a red flag: it
  needs explicit confirmation from the release owner for that specific change,
  even within an otherwise authorized release.

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
`SOURCE_COMMIT`. Archives include the reviewed root license and any root NOTICE,
plus the local database license, provenance and NOTICE when present in that
source revision. It does not upload, overwrite assets, or select a release.

Docker publication binds the release tag to its exact source SHA and passes the
version explicitly to the build. Prereleases publish their complete version only.
A separate serialized promotion step updates `latest`, major and minor aliases
only for the current GitHub latest stable release, and never below a higher stable
version already tagged. API failures stop promotion. An older-line release keeps
its exact version image without replacing those aliases.

Manual Docker runs do not promote stable aliases. Enter an existing release tag
to build that exact source, or leave it empty to build the selected branch/tag.
Branch builds carry an `unreleased-<branch>-<SHA>` binary version and a SHA image
tag; integration also publishes the `dev` image tag. The workflow does not publish
images automatically on dev pushes. Tagged source predating explicit Docker
`VERSION` support requires a separately reviewed legacy publishing path.

Local checks for this workflow:

```sh
sh scripts/test-local-gates.sh
python3 -m unittest discover -s scripts/release -p test_release.py
shellcheck scripts/release/build-artifacts.sh
```
