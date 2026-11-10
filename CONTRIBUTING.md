# Contributing to Tenderdash

Tenderdash is the consensus component of Dash Platform. Contributions should
support Platform development, deployed networks and coordinated upgrades.
For security-sensitive reports, use the private process in [SECURITY.md](SECURITY.md).

## Understand the workflow

Tenderdash follows a [Gitflow-style release workflow](https://nvie.com/posts/a-successful-git-branching-model/),
adapted to squash-merged feature PRs and coordinated Dash Platform releases.
The useful distinction is each branch's role:

| Role | Purpose and target |
| --- | --- |
| Integration (`dev`) | Development for the next Platform release. |
| Stable (`master`) | Latest stable Platform release line and the base for an urgent stable hotfix. |
| Feature branch | A focused change proposed back to its integration target. |
| Release candidate | A reviewed, frozen integration commit prepared for promotion to stable. |
| Hotfix or backport | An isolated fix for an affected deployed release, with a corresponding integration fix. |

Target `dev` for ordinary integration PRs. Select another target explicitly for
a release, hotfix or dependent change, following [RELEASES.md](RELEASES.md).
Versioned `vX.Y-dev` branches (e.g. `v1.9-dev`) are retired; never target them.

A PR that depends on another open PR can target its parent feature branch.
Describe the dependency and keep its diff focused; after the parent lands,
update the branch and retarget the PR to integration. The related
[#1582](https://github.com/dashpay/tenderdash/pull/1582) and
[#1583](https://github.com/dashpay/tenderdash/pull/1583) illustrate stacked work.

Ordinary feature PRs are squash-merged. Full-release promotions and forward-merges
of stable fixes preserve ancestry, subject to repository protections and the
release procedure.

Repository history illustrates these roles: the stable security fixes in
[#1574](https://github.com/dashpay/tenderdash/pull/1574), release preparation in
[#1576](https://github.com/dashpay/tenderdash/pull/1576), forward-merge in
[#1577](https://github.com/dashpay/tenderdash/pull/1577), and explicit backport in
[#1566](https://github.com/dashpay/tenderdash/pull/1566). These examples describe
release and synchronization work; they do not prescribe a fixed release cadence
or feature-branch lifetime.

Develop a fix on integration first when practical, then backport the compatible
change needed by a stable release. An urgent stable hotfix can start from
`master` and be forward-ported to integration. Coordinate the affected Platform
release with maintainers and keep unrelated unreleased features out of the fix.

## Choose a change and prepare a branch

Search existing issues and pull requests for related work. Explain the problem,
expected behavior and relevant deployment context in the issue or PR. Discuss
substantial protocol, application-interface or architectural changes with
maintainers before committing to an approach; existing [RFCs](docs/rfc/)
and [ADRs](docs/architecture/) provide design context.

Use a focused feature branch. If you do not have repository push access,
submit the PR from your fork. For a fresh checkout of the current integration
line:

```sh
git clone --recurse-submodules https://github.com/dashpay/tenderdash.git
cd tenderdash
git switch -c my-change origin/dev
make build
```

Use the Go version declared in [go.mod](go.mod). Building also requires the native
BLS toolchain: Git, C/C++ build tools, CMake and GMP development headers. Some
packages additionally require libpcap development headers. Follow the
[installation guide](docs/introduction/install.md#from-source) and
[Makefile](Makefile) for platform-specific setup; `make build` builds BLS before
Tenderdash.

Keep the branch focused and current with its PR target. Coordinate with another
contributor before changing their branch, and preserve shared branch history.

## Make and check the change

Follow [STYLE_GUIDE.md](STYLE_GUIDE.md) for code, comments, tests and errors, and
[AGENTS.md](AGENTS.md) when using an AI coding agent. Add tests for changed
behavior, including relevant failures and edge cases. Review concurrency,
resource cleanup and untrusted-input handling where the change affects them.

Run checks appropriate to the affected area. The Make targets supply the native
BLS include and linker settings; using them avoids an incomplete standalone Go
command. These examples use `./types`; replace it with the affected package or
space-separated package list:

```sh
make test PACKAGES=./types
make test_race PACKAGES=./types
make lint LINT_BASE=origin/dev
```

`make test` enables the deadlock build tag; `make test_race` enables the race
detector. Both targets serialize package testing. They are separate checks.
Use the actual PR target for `LINT_BASE`, such as `origin/master` for a stable
hotfix. `make vet` checks all packages. The authoritative recipes are in
[Makefile](Makefile) and [test/Makefile](test/Makefile).

Format changed Go files according to the style guide. The repository-wide
`make format` target also uses `golines` and `goimports`; inspect its diff before
including formatting changes outside your work.

CI also checks builds, tests, lint, generated files and vulnerabilities, with
path-based conditions for some jobs. Read the failed check and its
[workflow](.github/workflows) before choosing a local reproduction. For network
or application integration work, use the
[end-to-end test guide](test/e2e/README.md) and
[actual E2E workflow](.github/workflows/e2e.yml) to select the relevant scenario.
Record what you ran and any checks you could not perform in the PR.

### Protobuf and generated files

Edit definitions in [proto/](proto/), then regenerate their Go bindings; edit
source interfaces and regenerate their mocks. Generated `*.pb.go` files and mocks
must agree with their source definitions.

For protobuf work, install the `protoc-gen-gogofaster` plugin required by the
Makefile and `clang-format` for formatting. Buf is run at the version pinned by
the repository's Go modules. The relevant targets are:

```sh
make proto-format
make proto-gen
make proto-lint
```

`make proto-check-breaking` compares uncommitted schema changes with the local
Git revision; use an explicit reviewed baseline for committed changes. See the
[protobuf build configuration](buf.gen.yaml) and
[generated-code CI](.github/workflows/check-generated.yml) for tool versions and
verification. For mock changes, run `make mockery` with the version used by that
workflow and review the generated diff.

### Documentation and RPC changes

Edit canonical guides and specifications in `docs/` and `spec/`. Add new pages to
[book/SUMMARY.md](book/SUMMARY.md), then build the book:

```sh
make build-docs
```

The [documentation guide](docs/DOCS_README.md) covers Python/mdBook setup, previews,
local HTTP serving and generated output. For ABCI API documentation, update the
protobuf comments and run `make proto-doc`. RPC endpoint changes should update
[rpc/openapi/openapi.yaml](rpc/openapi/openapi.yaml) and the corresponding tests.

### Dependency changes

Update only the dependencies required by the change, using an explicit reviewed
version. Inspect the `go.mod` and `go.sum` diff and test affected callers.
`go list -u -m all` can show available updates; it does not decide which updates
belong in your PR. Use `go mod tidy` when the module graph needs reconciliation,
and review its changes rather than bundling unrelated upgrades.

## Open a pull request and address review

Open a draft PR when early feedback would help. Fill the applicable sections of
[the PR template](.github/PULL_REQUEST_TEMPLATE.md): describe the user-visible
problem and result, implementation, validation, compatibility effects and related
work. Mark it ready for review when the change and its explanation are ready.

Use [Conventional Commits](https://www.conventionalcommits.org/) for commit and PR
titles, for example `fix(consensus): validate the proposal before voting`.
Use `!` and explain migration requirements for an incompatible change. These
summaries help readers; the actual compatibility review determines release scope
(see the [versioning policy](RELEASES.md#versioning-policy)).
A normal development PR does not update the release version or `CHANGELOG.md`.

Read the final diff, address verified findings and update the tests and PR
explanation when the change evolves. After pushing the final changes, the author
or assigned developer records self-review with a **new, unedited PR comment**:

```text
/self-reviewed FULL_HEAD_SHA
```

Replace `FULL_HEAD_SHA` with the complete PR head commit, available from
`git rev-parse HEAD` when your checkout matches the PR. Renew self-review after
changing the reviewed diff; follow the live checklist for base-only updates.
The current
[PR Hygiene policy](https://github.com/dashpay/stale_prs_are_bad/blob/master/policies/tenderdash.json)
and [operations guide](https://github.com/dashpay/stale_prs_are_bad/blob/master/guides/PR_REVIEW_OPERATIONS.md)
define bot review and self-review handling. Follow the live checklist, checks and
review requests; required human routing depends on the changed areas and policy.

Resolve a bot thread after verifying that its finding is fully fixed, when your
permissions allow it. Leave a human reviewer's objection for that reviewer to
resolve. Discuss disagreements with evidence from the code or tests.

An additional Claudius review is opt-in: ask a maintainer to apply
`claudius-review`. The current [workflow](.github/workflows/claude-code-review.yml)
waits for the PR's Test workflow to succeed and can review drafts too. Follow its
reported result, address verified findings and follow the maintainer review policy.

Keep the PR target current and satisfy the required checks and review policy.
A maintainer with merge permission completes the merge under branch protections.
Release promotions and stable synchronization follow their separate
ancestry-preserving procedure.

## Releases and support

Tenderdash support and rollout follow Dash Platform's needs. Release preparation
uses [RELEASES.md](RELEASES.md) and the
[project release skill](.agents/skills/tenderdash-release/SKILL.md), which review
actual history, compatibility and a selected candidate SHA before choosing a
version. Review the version/changelog preparation and any merge-time changes too;
the release tag identifies the reviewed **final release commit**, which may differ
from that initial candidate. Record both SHAs in the release preparation.
Coordinate release publication and deployment with the Platform release owners
under that release process.

<sub>🤖 Co-authored by [Claudius the Magnificent](https://github.com/lklimek/claudius) AI Agent</sub>
