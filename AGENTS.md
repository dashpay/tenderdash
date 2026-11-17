# AGENTS

This file defines the minimum rules for AI agents working on Tenderdash.
It does not duplicate details from the style guide; instead, it references the source.

## Code Conventions and Style (Go, tests, comments)

Follow the rules in `STYLE_GUIDE.md`. Do not duplicate them here or in other
agent configuration files.

## Code quality and security

1. Ensure all changed code passes the linter.
2. Ensure the code is thread-safe. Check memory leaks, deadlocks and race conditions.
3. Review implementation to find any security issues. Identify and verify edge cases. Check known security issues.
4. If unsure, explicitly report any potential security issues, gaps, missing features and TODO items.


## Repo Structure and Key Directories

Key directories:

- `abci/` – Application BlockChain Interface (app ↔ consensus boundary)
- `cmd/` – CLI/binary entrypoints
- `config/` – configuration structs, defaults, and config tests
- `crypto/` – cryptographic primitives and key handling
- `dash/` – Dash-specific components (quorums, validators, etc.)
- `docs/` – documentation
- `internal/` – internal (unexported) packages
- `libs/` – shared utility libraries
- `node/` – node setup, lifecycle, and reactor wiring
- `proto/` – protobuf definitions (source of truth for wire types)
- `rpc/` – JSON-RPC server and client
- `spec/` – protocol specification
- `types/` – domain types (Block, Vote, Commit, etc.)
- `test/` – integration tests and test support
- `tools/`, `scripts/` – helper tools and automation scripts

**Do not edit generated files** (e.g. `*.pb.go`). Edit the `.proto` source
and regenerate with `make proto-gen`.

## Building, Testing, and Linting

```bash
# Build the BLS native library: inits the submodule, needs cmake, a C++
# compiler and libgmp-dev. Once per checkout or worktree.
make build-bls

# Build (includes build-bls)
make build

# Run all unit tests (-p 1, -tags deadlock)
make test

# Run all unit tests with race detector (-p 1)
make test_race

# Lint lines changed vs origin/dev (what CI enforces);
# stacked PRs: make lint LINT_BASE=origin/<base-branch>
make lint

# Format
make format
```

### Running targeted tests

Most packages link BLS through cgo. The Makefile passes the cgo flags only to
its own targets, so a bare `go test`, `go vet` or `go build` fails with:

```text
blschia.cpp:17:10: fatal error: dashbls/bls.hpp: No such file or directory
```

Export the Makefile's flags once per shell, then run `go` directly:

```bash
BLS_DIR="$(git rev-parse --show-toplevel)/third_party/bls-signatures"
export CGO_ENABLED=1
export CGO_CXXFLAGS="-I$BLS_DIR/build/depends/relic/include -I$BLS_DIR/src/depends/mimalloc/include -I$BLS_DIR/src/depends/relic/include -I$BLS_DIR/src/include"
export CGO_LDFLAGS="-L$BLS_DIR/build/depends/mimalloc -L$BLS_DIR/build/depends/relic/lib -L$BLS_DIR/build/src -ldashbls -lrelic_s -lmimalloc-secure -lgmp"

go test -race -count=1 -run 'TestName' ./internal/p2p/
```

- `third_party/bls-signatures/build/` exists only where `make build-bls` ran.
  In a fresh clone or a new `git worktree`, run it there, or set `BLS_DIR` to
  another checkout that already built the same BLS revision.
- macOS: append `-L$(brew --prefix gmp)/lib` to `CGO_LDFLAGS` and
  `-I$(brew --prefix gmp)/include` to `CGO_CXXFLAGS`, as the Makefile does.
- CI (`.github/workflows/tests.yml`) installs BLS into `/usr/local` and runs
  `go test -race -timeout=5m` over package groups (`make test-group-N
  NUM_SPLIT=6`).
- `cmd/abcidump` needs `libpcap-dev`.
- `-count=1` bypasses the Go test cache when re-running unchanged tests.

## Working with Dependencies and Tools (Go modules, Buf, etc.)

- Go dependencies are managed via Go Modules. Only update required modules.
- To review newer versions: `go list -u -m all`.
- If build issues arise, use `go mod tidy`.
- Protobufs: `buf` and `gogoproto` are required.
  - Lint: `make proto-lint`
  - Breaking changes: `make proto-check-breaking`
  - Generation: `make proto-gen`
  - Formatting: `make proto-format` (requires `clang-format`)

## Branching and PR Workflow

- `dev` is the integration branch; `master` is the latest stable Dash Platform release line.
- Do all development on `dev`: create feature branches from `origin/dev` and
  open PRs into `dev`. The only exception is a stable hotfix, which branches
  from `master` and is forward-ported to `dev`.
- Versioned `vX.Y-dev` branches (e.g. `v1.8-dev`, `v1.9-dev`) are retired. Never
  branch from, target, or compare against them, even if one still exists on
  the remote.
- Keep commits focused and well-described.
- Use conventional commit format for commit and PR titles.
- Do not manually edit or generate `CHANGELOG.md` for development changes.
  The project release skill prepares it only for a requested release.
- PR descriptions: read `.github/PULL_REQUEST_TEMPLATE.md`, fill in every
  section, base content on the full diff against the target branch.
- AI code review (`.github/workflows/claude-code-review.yml`) is opt-in:
  maintainers trigger it via the `claudius-review` label.
  See `CONTRIBUTING.md` for the review process.


## Security and Privacy (secrets, keys, logs)

- Private keys and configuration files containing keys are secret: do not log
  them or commit them to the repo.
- Particularly sensitive files: `config/priv_validator_key.json`,
  `config/node_key.json`, TLS keys (`tls-key-file` in config).

## Common Pitfalls

- BLS native dependency must be built before Go binaries (`make build` handles
  this); standalone `go` commands need the cgo flags from
  [Running targeted tests](#running-targeted-tests).
- `*.pb.go` files are generated — never edit them by hand.
- `gogoproto` extensions (e.g., `nullable`, `customtype`) can produce
  unexpected Go types; always check the generated code after proto changes.
- Some packages under `internal/` import `dash/` types; be mindful of import
  cycles when moving code between packages.

## Dash Platform releases

Tenderdash is supported as a component of Dash Platform development, not as an
independent consensus product with a fixed minor-version support window.
Use [tenderdash-release](.agents/skills/tenderdash-release/SKILL.md) for release
analysis or a requested release. Read the actual history and diffs before
selecting SemVer; a normal development task does not authorize publication.
See [RELEASES.md](RELEASES.md) for release preparation and coordinated rollout.
