# Contributing

Thank you for your interest in contributing to Tenderdash! Before
contributing, it may be helpful to understand the goal of the project. The goal
of Tenderdash is to develop a BFT consensus engine robust enough to
support permissionless value-carrying networks. While all contributions are
welcome, contributors should bear this goal in mind in deciding if they should
target Tenderdash or a potential fork. When targeting Tenderdash, the following
process leads to the best chance of landing changes in the development branch.

All work on the code base should be motivated by a [Github
Issue](https://github.com/dashpay/tenderdash/issues).
[Search](https://github.com/dashpay/tenderdash/issues?q=is%3Aopen+is%3Aissue+label%3A%22help+wanted%22)
is a good place start when looking for places to contribute. If you
would like to work on an issue which already exists, please indicate so
by leaving a comment.

All new contributions should start with a [Github
Issue](https://github.com/dashpay/tenderdash/issues/new/choose). The
issue helps capture the problem you're trying to solve and allows for
early feedback. Once the issue is created the process can proceed in different
directions depending on how well defined the problem and potential
solution are. If the change is simple and well understood, maintainers
will indicate their support with a heartfelt emoji.

If the issue would benefit from thorough discussion, maintainers may
request that you create a [Request For
Comment](./docs/rfc/)
in the Tenderdash repo. Discussion
at the RFC stage will build collective understanding of the dimensions
of the problems and help structure conversations around trade-offs.

When the problem is well understood but the solution leads to large structural
changes to the code base, these changes should be proposed in the form of an
[Architectural Decision Record (ADR)](./docs/architecture/). The ADR will help
build consensus on an overall strategy to ensure the code base maintains
coherence in the larger context. If you are not comfortable with writing an
ADR, you can open a less-formal issue and the maintainers will help you turn it
into an ADR.

> How to pick a number for the ADR?

Find the largest existing ADR number and bump it by 1.

When the problem as well as proposed solution are well understood,
changes should start with a [draft
pull request](https://github.blog/2019-02-14-introducing-draft-pull-requests/)
against the development branch (latest `vMAJOR.MINOR-dev`). The draft signals
that work is underway. When the work
is ready for feedback, hitting "Ready for Review" will signal to the
maintainers to take a look.

![Contributing flow](./docs/imgs/contributing.png)

Each stage of the process is aimed at creating feedback cycles which align contributors and maintainers to make sure:

- Contributors don’t waste their time implementing/proposing features which won’t land in the development branch.
- Maintainers have the necessary context in order to support and review contributions.

## Code Style

Follow the conventions in [STYLE_GUIDE.md](./STYLE_GUIDE.md) for Go code
structure, comments, tests, and errors.

## Dependencies

We use [go modules](https://github.com/golang/go/wiki/Modules) to manage dependencies.

The development branch (latest `vMAJOR.MINOR-dev`) should build cleanly with
`go get`, which means dependencies should be kept up-to-date so we can get away
with telling people they can just `go get` our software. Since some
dependencies are not under our control, a third party may break our build, in
which case we can fall back on `go mod tidy`.

Run `go list -u -m all` to get a list of dependencies that may not be
up-to-date.

When updating dependencies, please only update the particular dependencies you
need. Instead of running `go get -u=patch`, which will update anything,
specify exactly the dependency you want to update, eg.
`go get -u github.com/tendermint/go-amino@<version>`.

## Protobuf

We use [Protocol Buffers](https://developers.google.com/protocol-buffers) along
with [`gogoproto`](https://github.com/cosmos/gogoproto) to generate code for use
across Tenderdash.

To generate proto stubs, lint, and check protos for breaking changes, you will
need to install [buf](https://buf.build/) and `gogoproto`. Then, from the root
of the repository, run:

```bash
# Lint all of the .proto files in proto/tendermint
make proto-lint

# Check if any of your local changes (prior to committing to the Git repository)
# are breaking
make proto-check-breaking

# Generate Go code from the .proto files in proto/tendermint
make proto-gen
```

To automatically format `.proto` files, you will need
[`clang-format`](https://clang.llvm.org/docs/ClangFormat.html) installed. Once
installed, you can run:

```bash
make proto-format
```

### Visual Studio Code

If you are a VS Code user, you may want to add the following to your `.vscode/settings.json`:

```json
{
  "protoc": {
    "options": [
      "--proto_path=${workspaceRoot}/proto",
      "--proto_path=${workspaceRoot}/third_party/proto"
    ]
  }
}
```

## Release Notes

Release notes are generated from commit messages by `scripts/release.sh`. Use
clear conventional commit summaries so the release tooling can assemble accurate
notes; `CHANGELOG_PENDING.md` is no longer maintained.

## Branching Model and Release

Tenderdash maintains two primary branches:

- `master` for the latest stable release.
- The highest-versioned `vMAJOR.MINOR-dev` branch for active development.

Find the current development branch with:

```sh
git branch -r --list 'origin/v[0-9]*-dev' --sort=-version:refname | head -1
```

Release tags are cut from `master`. We only maintain `master` and the current
development branch.

Note all pull requests should be squash merged. This keeps the commit history
clean and makes it easy to reference the pull request where a change was
introduced.

### Development Procedure

The latest state of development is on the development branch (latest
`vMAJOR.MINOR-dev`), which must keep CI green (build, lint, and tests). _Never_
force push the development branch, unless fixing broken git history (which we
rarely do anyways).

To begin contributing, create a development branch either on
`github.com/dashpay/tenderdash`, or your fork (using `git remote add origin`).

Make changes and keep your branch updated with the latest development branch.
Ensure CI is green; run `make build`, `make lint`, and
`go test -race -timeout=5m ./...` locally as needed. (Since pull requests are
squash-merged, either `git rebase` or `git merge` is fine.) When opening a PR,
fill in every section of `.github/PULL_REQUEST_TEMPLATE.md`.

If a change needs to land in the latest stable release, coordinate with the
maintainers and open a follow-up PR against `master` after the development
branch merge.

Once you have submitted a pull request label the pull request with either `R:minor`, if the change should be included in the next minor release, or `R:major`, if the change is meant for a major release.

Sometimes (often!) pull requests get out-of-date with the development branch, as
other people merge different pull requests to the development branch. It is our
convention that pull request authors are responsible for updating their
branches with the development branch. (This also means that you shouldn't
update someone else's branch for them; even if it seems like you're doing them
a favor, you may be interfering with their git flow in some way!)

#### Merging Pull Requests

It is also our convention that authors merge their own pull requests, when possible. External contributors may not have the necessary permissions to do this, in which case, a member of the core team will merge the pull request once it's been approved.

Before merging a pull request:

- Ensure pull branch is up-to-date with the development branch (GitHub won't let
  you merge without this!)
- Ensure CI is green
- [Squash](https://stackoverflow.com/questions/5189560/squash-my-last-x-commits-together-using-git) merge pull request

### Git Commit Style

We use conventional commit format for commit and PR titles. Follow the
[Conventional Commits](https://www.conventionalcommits.org/) specification and
use common types like `feat`, `fix`, `docs`, `refactor`, `test`, and `chore`.
Scopes should describe the affected package or subsystem (e.g., `cmd/debug`).
Write concise commits that follow `type(scope): summary` on the first line;
optional body and footer sections may include context or references. For example,

```sh
fix(cmd/debug): execute p.Signal only when p is not nil

[potentially longer description in the body]

Fixes #nnnn
```

Each PR should have one commit once it lands on the development branch; this
can be accomplished by using the "squash and merge" button on Github. Be sure
to edit your commit message, though!

## Testing

### Unit tests

Unit tests are located in `_test.go` files as directed by [the Go testing
package](https://golang.org/pkg/testing/). If you're adding or removing a
function, please check there's a `TestType_Method` test for it.

Run: `go test -race -timeout=5m ./...`

To run a single package's tests, use `go test -race -timeout=5m ./dash/quorum/...`.

### Integration tests

Integration tests are also located in `_test.go` files. What differentiates
them is a more complicated setup, which usually involves setting up two or more
components.

Run: `make test_integrations`

### End-to-end tests

End-to-end tests are used to verify a fully integrated Tenderdash network.

See [README](./test/e2e/README.md) for details.

Run:

```sh
cd test/e2e && \
  make && \
  ./build/runner -f networks/ci.toml
```

### Model-based tests (ADVANCED)

_NOTE: if you're just submitting your first PR, you won't need to touch these
most probably (99.9%)_.

For components, that have been [formally
verified](https://en.wikipedia.org/wiki/Formal_verification) using
[TLA+](https://en.wikipedia.org/wiki/TLA%2B), it may be possible to generate
tests using a combination of the [Apalache Model
Checker](https://apalache.informal.systems/) and [tendermint-rs testgen
util](https://github.com/informalsystems/tendermint-rs/tree/master/testgen).

Now, I know there's a lot to take in. If you want to learn more, check out [
this video](https://www.youtube.com/watch?v=aveoIMphzW8) by Andrey Kupriyanov
& Igor Konnov.

At the moment, we have model-based tests for the light client, located in the
`./light/mbt` directory.

Run: `cd light/mbt && go test`

### Fuzz tests (ADVANCED)

_NOTE: if you're just submitting your first PR, you won't need to touch these
most probably (99.9%)_.

[Fuzz tests](https://en.wikipedia.org/wiki/Fuzzing) can be found inside the
`./test/fuzz` directory. See [README.md](./test/fuzz/README.md) for details.

Run: `cd test/fuzz && make fuzz-{PACKAGE-COMPONENT}`

### Jepsen tests (ADVANCED)

_NOTE: if you're just submitting your first PR, you won't need to touch these
most probably (99.9%)_.

[Jepsen](http://jepsen.io/) tests are used to verify the
[linearizability](https://jepsen.io/consistency/models/linearizable) property
of the Tendermint consensus. They are located in a separate repository
-> <https://github.com/tendermint/jepsen>. Please refer to its README for more
information.

### RPC Testing

**If you contribute to the RPC endpoints it's important to document your
changes in the [Openapi file](./rpc/openapi/openapi.yaml)**.

To test your changes you must install `nodejs` and run:

```bash
npm i -g dredd
make build-linux build-contract-tests-hooks
make contract-tests
```

**WARNING: these are currently broken due to <https://github.com/apiaryio/dredd>
not supporting complete OpenAPI 3**.

This command will popup a network and check every endpoint against what has
been documented.
