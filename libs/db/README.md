# Tenderdash database adapters

This package provides Tenderdash's GoLevelDB, MemDB and PrefixDB adapters and
the DB, Batch and Iterator interfaces. GoLevelDB uses `<name>.db` directories;
MemDB is transient storage. The engine versions are pinned in the root `go.mod`.

The implementation and its original tests derive from
[`cometbft-db v1.0.4`](https://github.com/cometbft/cometbft-db/tree/v1.0.4),
commit `f5ed70fe0e068393bf5aa4fe84162b6ad57ce19e`,
under the Apache License 2.0 included in [LICENSE](LICENSE).
CometBFT DB is a fork of [tm-db](https://github.com/tendermint/tm-db).
The retained source files are `db.go`, `types.go`, `util.go`, `goleveldb*.go`,
`memdb*.go`, and `prefixdb*.go`. Only GoLevelDB and MemDB are registered.
The upstream test helper `test_helpers.go` is named `test_helpers_test.go` here.
Local changes narrow backend registration, close the debugging Print iterator,
and adapt tests and documentation to the retained implementations.

GoLevelDB returns nil from Get and false from Has for a stored empty value;
MemDB returns a non-nil empty slice and true. This inherited difference is
characterized by tests and tracked in [#1554](https://github.com/dashpay/tenderdash/issues/1554).
Use non-empty values when callers must distinguish existing from absent keys.

Callers must close databases, batches and iterators. PrefixDB borrows its prefix
slice, so callers must not mutate it. Closing a PrefixDB closes its underlying
database; owners sharing a database across prefixes must coordinate that lifetime.
PrefixDB compaction bounds are passed directly to the underlying database.

Custom DB providers must import `github.com/dashpay/tenderdash/libs/db` for their
DB, Batch and Iterator types. The interfaces are separate Go types from those in
`cometbft-db`; an implementation returning that module's Batch or Iterator
interfaces requires an import update or an adapter.
