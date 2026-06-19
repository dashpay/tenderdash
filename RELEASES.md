# Releases

Tenderdash follows [semantic versioning](https://semver.org/). Release tags use
the `vX.Y.Z` format; prereleases use `vX.Y.Z-dev.N` (e.g. `v1.6.0-dev.1`).

## Branch model

| Branch | Purpose |
|---|---|
| `vX.Y-dev` (e.g. `v1.6-dev`) | Active development; new work targets this branch |
| `master` | Latest stable release; receives release updates and cherry-picked critical fixes |

Normal contribution PRs and prerelease release-PRs are squash-merged; the
full-release PR into `master` is the exception and uses a merge commit (the
script prints the correct strategy for each release). The script creates a
short-lived `release_<version>` branch for each release and opens a PR from it.

## Versioning rules

`scripts/release/release.sh` bumps **only** `TMVersionDefault` in
`version/version.go`. Reviewers must manually decide whether to bump the other
protocol versions by inspecting diffs since the last release:

- **`P2PProtocol`** — bump when p2p message format, channels, or proposer
  selection logic changes.
- **`BlockProtocol`** — bump when block, header, vote, commit, or state
  structures change, or block validity rules change.
- **`ABCISemVer`** — follows semver; field additions are patch-level, interface
  changes are minor/major.

Example: for v1.5.4 → v1.6.0-dev.1 none of these changed, so all were left
as-is.

## Prerequisites

Before running the release script:

1. Be on the `vX.Y-dev` branch (e.g. `git checkout v1.6-dev`).
2. Working tree must be clean (`git status` shows nothing).
3. **Local branch must be in sync with origin** — the script now enforces this
   and will fast-forward your branch automatically, or error if a non-linear
   sync is needed.
4. `master` and the previous `vX.Y-dev` must already be merged forward into the
   current dev branch.
5. `docker` and `gh` (authenticated via `gh auth login`) must be installed.

## Running a release

```sh
# Example prerelease
git checkout v1.6-dev
./scripts/release/release.sh --release=1.6.0-dev.1

# Example full release
git checkout v1.6-dev
./scripts/release/release.sh --release=1.6.0
```

Add `--sign` to also build and upload signed linux amd64/arm64 tarballs (GPG
key required; skipped for routine dev prereleases).

### What the script does

1. Validates you are on the correct source branch and the working tree is clean.
2. Fetches origin and fast-forwards local branch to match (errors if diverged).
3. Generates `CHANGELOG.md` via `docker run orhunp/git-cliff:2.4.0` using
   `scripts/release/cliff.toml`; range is `v1.0.0-dev.1..HEAD`.
4. Bumps `TMVersionDefault` in `version/version.go`.
5. Creates branch `release_<version>`, commits the two files, and pushes.
6. Creates a GitHub milestone `vX.Y` if it does not already exist.
7. Opens a PR targeting `vX.Y-dev` (prerelease) or `master` (full release).
8. Prints the merge strategy and waits for the PR to be merged.
9. Creates a **draft** release targeting the merge branch via
   `gh release create --draft … --target <branch>`, with auto-generated notes.

After the script exits:

- **Prerelease PR** — squash-merge into `vX.Y-dev`.
- **Full release PR** — merge-commit into `master`.
- Review the draft release on GitHub, then **publish** it to finalize the
  release and ensure the `vX.Y.Z` tag exists on the remote. (Draft releases and
  their tags are mutable and not guaranteed on the remote until published.)

### Signed binaries (--sign)

When `--sign` is passed, after you publish the draft release the script resumes,
checks out the released tag, builds linux/amd64 and linux/arm64 binaries via
Docker, signs them with GPG, creates `.tar.gz` archives with detached `.sig`
files, and uploads everything to the release. Set `GPG_KEY_ID` to use a
specific key.

## Agent / CI two-call flow (non-blocking)

The default release script blocks on a polling loop waiting for the PR to be
merged — fine for a human at a terminal, hostile to agents and CI pipelines
(the Bash tool times out; backgrounding gets the process reaped).

Use the split flow instead:

```sh
# Step 1 — prep: generate changelog, bump version, open PR, then exit 0.
# Works from the correct vX.Y-dev branch exactly like the normal flow.
git checkout v1.6-dev
./scripts/release/release.sh --release=1.6.0-dev.3 --no-wait

# Stdout includes a greppable line:
#   RELEASE_PR=https://github.com/dashpay/tenderdash/pull/NNN

# Step 2 — finalize: after the PR is merged (human or CI), create the draft
# release. Must run from within the Tenderdash git checkout (any branch),
# or set GH_REPO=dashpay/tenderdash so gh can resolve the repository.
./scripts/release/release.sh --release=1.6.0-dev.3 --finalize

# Stdout includes:
#   RELEASE_DRAFT=https://github.com/dashpay/tenderdash/releases/tag/v1.6.0-dev.3
```

| Flag | Alias | Effect |
|------|-------|--------|
| `--no-wait` | `--stop-after-pr` | Run validate → changelog → version bump → branch + commit → push → open PR, print `RELEASE_PR=<url>`, then `exit 0`. Implies `--non-interactive`. |
| `--finalize` | `--create-release` | Verify the `release_<ver>` PR is **MERGED** (error if not), then create the **draft** GitHub release. Idempotent — re-running when the tag/release already exists reports the URL and exits cleanly. Implies `--non-interactive`. Prints `RELEASE_DRAFT=<url>`. Requires a Tenderdash git checkout (any branch) or `GH_REPO=dashpay/tenderdash`. |
| `--non-interactive` | `--yes` | Auto-accept any confirmation prompt; never block on stdin. |
| `--dry-run` | _(none)_ | Validate, generate a changelog preview, and compute the version bump; print a preview of the `RELEASE_PR=` / `RELEASE_DRAFT=` lines it **would** emit. No commit, push, PR, tag, or release action is taken; working tree is restored. Implies `--non-interactive`. |

Running with **no new flags** preserves today's interactive behavior exactly
(block-until-merged → create draft release in one call).

### Cautions for agent and CI use

**Do NOT background the default (blocking) invocation in automation.**
The script polls `gh` in a tight loop waiting for the PR to merge; running
it as a background job is almost guaranteed to be reaped or timed out by your
CI/agent runner. Always use the `--no-wait` / `--finalize` two-call flow.

**Prerequisites** — all of the following must be true before running:

- **Docker running** — the changelog step runs `git-cliff` inside a container
  (`docker info` must succeed); the script will fail fast with a clear error
  if Docker is unreachable.
- **`gh` authenticated** — run `gh auth login` beforehand; both the prepare
  and finalize paths check this before taking any action.
- **Push credentials** — SSH key or token with write scope for the target
  branch on `origin`; some branch protections require an elevated token.
  The script performs a `git push --dry-run` probe before any commit is made
  and exits with an actionable error if denied.

**Pre-check with `--dry-run`** before the real run to validate your
configuration, preview the changelog, and confirm the version bump — without
touching any remote or making any commit:

```sh
./scripts/release/release.sh --release=1.6.0-dev.3 --dry-run
```

## CI and build tooling

`.github/workflows/release.yml` runs `goreleaser` (config: `.goreleaser.yml`)
and is triggered **only** manually via `workflow_dispatch`. Its goreleaser
release step runs only when the workflow is dispatched against a tag ref
(`refs/tags/…`); a `pull_request`-gated validate build is also defined but never
fires, because the workflow has no `pull_request` trigger. No workflow currently
runs automatically on tag push or on pull requests. This workflow is independent
of the release script's own Docker cross-compile path used for `--sign` binaries.

## Minor Release Checklist

The following steps are performed on all releases that increment the _minor_
version (e.g. v1.5 to v1.6). These steps ensure Tenderdash is well tested,
stable, and suitable for adoption by the projects that rely on it.

### Feature Freeze

Ahead of any minor version release, the software enters Feature Freeze for at
least two weeks. No new features are added to the code being prepared for
release. The following must not be merged during a feature freeze:

- Refactors unrelated to specific bug fixes.
- Dependency upgrades.
- New test code that does not test a discovered regression.
- New features of any kind.
- Documentation or spec improvements unrelated to the newly developed code.

During this period the Tenderdash team focuses on ensuring existing code is
stable and reliable. Broken tests are fixed, flaky tests are remedied, and all
efforts are aimed at improving code quality.

### Nightly End-To-End Tests

The Tenderdash team maintains [a set of end-to-end
tests](./test/e2e/README.md) for the dashcore and rotating e2e networks. These
tests run nightly on the latest commit. They start a network of containerized
Tenderdash processes and run automated checks in both stable and unstable
conditions. During the feature freeze these tests must pass consistently before
a release is considered stable.

### Upgrade Harness

> TODO(williambanfield): Change to past tense and clarify this section once
> upgrade harness is complete.

The Tenderdash team is creating an upgrade test harness to exercise the
workflow of stopping an instance of Tenderdash running one version of the
software and starting up the same application running the next version. To
support upgrade testing, we will add the ability to terminate the Tenderdash
process at specific pre-defined points in its execution so that we can verify
upgrades work in a representative sample of stop conditions.

### Large Scale Testnets

The Tenderdash end-to-end tests run a small network (~10s of nodes) to exercise
basic consensus interactions. Real world deployments of Tenderdash often have
over a hundred nodes just in the validator set. To gain more assurance before a
release, larger-scale test networks are run to shake out emergent behaviors at
scale.

Large-scale test networks run on virtual machines (VMs), each with 4 GB of RAM
and 2 CPU cores. The network runs a simple key-value store application with
artificial delays on ABCI calls to simulate a slow application. A baseline is
captured with no load, then a consistent load is applied (10% of nodes receiving
200 transactions per minute each).

Metrics monitored on each node:

- Consensus rounds per height
- Maximum/minimum connected peers, rate of peer connection change
- Memory resident set size
- CPU utilization
- Blocks produced per minute
- Seconds for each consensus step (Propose, Prevote, Precommit, Commit)
- Latency to receive block proposals

#### 200 Node Testnet

A 200-node test network comprising 5 seed nodes, 100 validators, and 95
non-validating full nodes. All nodes begin by dialing a subset of seed nodes to
discover peers. The network runs for several days with continuous metric
collection. In cases of changes to performance-critical systems, larger testnets
should be considered.

#### Rotating Node Testnet

A network with 10 validators and 3 seed nodes. A rolling set of 25 full nodes
are started; each connects to the network via a seed node, block-syncs to the
head of the chain, begins producing blocks, is stopped, and is replaced by a new
node. This network runs for several days.

#### Network Partition Testnet

A network with 100 validators and 95 full nodes (all validators with equal
stake). Once producing blocks, firewall rules create a 50/50 stake partition.
After block production stops, the firewall rules are removed and the network is
monitored to confirm reconnection and resumed block production.

#### Absent Stake Testnet

A set of 150 validator nodes and 3 seed nodes configured so that 67% of the
total stake is active and 33% belongs to a validator that is never run. The
network runs for multiple days to confirm it produces blocks with missing stake.
