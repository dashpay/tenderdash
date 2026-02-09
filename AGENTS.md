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
# Build (includes BLS native dependency)
make build

# Run all unit tests with race detector
go test -race -timeout=5m ./...

# Run a single package's tests
go test -race -timeout=5m ./dash/quorum/...

# Lint
make lint

# Format
make format
```

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

- Main development branch is the most recent one with name in format `vMAJOR.MINOR-dev`, for example `v0.10-dev`.
- Create feature branches from the development branch; open PRs back into it. 
- Keep commits focused and well-described.
- PR descriptions should follow `.github/PULL_REQUEST_TEMPLATE.md`
- Use conventional commit format for commit and PR titles.


## Security and Privacy (secrets, keys, logs)

- Private keys and configuration files containing keys are secret: do not log
  them or commit them to the repo.
- Particularly sensitive files: `config/priv_validator_key.json`,
  `config/node_key.json`, TLS keys (`tls-key-file` in config).

## Common Pitfalls

- BLS native dependency must be built before Go binaries (`make build` handles
  this; standalone `go build` may fail).
- `*.pb.go` files are generated — never edit them by hand.
- `gogoproto` extensions (e.g., `nullable`, `customtype`) can produce
  unexpected Go types; always check the generated code after proto changes.
- Some packages under `internal/` import `dash/` types; be mindful of import
  cycles when moving code between packages.
