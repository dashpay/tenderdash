---
order: 6
---

# Indexing Transactions

Tenderdash can index committed transaction results and block events for RPC
searches and subscriptions. Transaction events come from `ExecTxResult.Events`
in the proposal execution results. Block events come from
`ResponseProcessProposal.Events`. They are published after block finalization;
`ResponseFinalizeBlock` does not contain an events field.

Each event has a type and string-valued attributes. The search key combines the
event type and attribute key with a dot. An event of type `transfer` with an
attribute `sender=Bob` can therefore be queried as `transfer.sender='Bob'`.
Set an attribute's `Index` flag to `true` to include it in event indexes.

For the execution lifecycle, see [commit and apply](../../spec/consensus/consensus-happy-path.md#9-commit-and-apply).
The [event publisher](https://github.com/dashpay/tenderdash/blob/master/internal/state/events.go)
shows how committed proposal results become transaction and block events.

## Configuration

Operators can configure indexing via the `[tx-index]` section. The `indexer`
field takes a series of supported indexers. If `null` is included, indexing will
be turned off. All configured indexer names must still be supported.

```toml
[tx-index]

# The backend database list to back the indexer.
# If list contains null, meaning no indexer service will be used.
#
# The application will set which txs to index. In some cases a node operator will be able
# to decide which txs to index based on configuration set in the application.
#
# Options:
#   1) "null" (default) - no indexer services.
#   2) "kv" - an indexer backed by key-value storage (see DBBackend).
#     - When "kv" is chosen "tx.height" and "tx.hash" will always be indexed.
# indexer = []
```

### Supported Indexers

#### KV

The `kv` indexer type is an embedded key-value store supported by the main
underlying Tenderdash database. Using the `kv` indexer type allows you to query
for block and transaction events directly against Tenderdash's RPC. However, the
query syntax is limited to the supported event-query operators.

#### Removed PostgreSQL indexer

The `psql` event indexer is no longer supported. Selecting it, including alongside
`kv` or `null`, returns a configuration error. Choose `indexer = ["kv"]` for local
indexing and RPC searches, or `["null"]` to disable indexing. The obsolete
`psql-conn` key is ignored, so existing Dashmate configurations with an empty
value remain compatible.

Existing PostgreSQL data is not deleted or migrated, and SQL consumers no longer
receive new events. To populate KV indexes, explicitly run `tenderdash
reindex-event` against the retained local blocks and results after selecting `kv`.
Pruned history cannot be reconstructed by this command.

## Default Indexes

The Tenderdash tx and block event indexer indexes a few select reserved events
by default.

### Transactions

The following indexes are indexed by default:

- `tx.height`
- `tx.hash`

### Blocks

The following indexes are indexed by default:

- `block.height`

## Adding Events

Return transaction events in the corresponding `ExecTxResult` in
`ResponsePrepareProposal.TxResults` / `ResponseProcessProposal.TxResults`.
For example, an individual transaction result can contain:

```go
result := &abci.ExecTxResult{
    Code: 0,
    Events: []abci.Event{
        {
            Type: "transfer",
            Attributes: []abci.EventAttribute{
                {Key: "sender", Value: "Bob", Index: true},
                {Key: "recipient", Value: "Alice", Index: true},
                {Key: "note", Value: "payment", Index: false},
            },
        },
    },
}
```

Here `abci` refers to `github.com/dashpay/tenderdash/abci/types`; append `result`
to the response's transaction results in the same order as the executed
transactions. The `kv` indexer indexes `transfer.sender` and
`transfer.recipient`; `transfer.note` is not indexed. An empty event type or
attribute key is not indexed. Avoid reserved keys such as `tx.hash` and
`tx.height`.

## Querying Transactions Events

You can query for a paginated set of transaction by their events by calling the
`/tx_search` RPC endpoint:

```bash
curl "localhost:26657/tx_search?query=\"transfer.sender='Bob'\"&prove=true"
```

Check out [API docs](../tenderdash-core/rpc.md)
for more information on query syntax and other options.

## Subscribing to Transactions

Clients can subscribe to transactions with the given tags via WebSocket by providing
a query to `/subscribe` RPC endpoint.

```json
{
  "jsonrpc": "2.0",
  "method": "subscribe",
  "id": "0",
  "params": {
    "query": "transfer.sender='Bob'"
  }
}
```

Check out [API docs](../tenderdash-core/rpc.md) for more information
on query syntax and other options.

## Querying Blocks Events

You can query for a paginated set of blocks by their events by calling the
`/block_search` RPC endpoint:

```bash
curl "localhost:26657/block_search?query=\"block.height > 10\""
```

Check out [API docs](../tenderdash-core/rpc.md)
for more information on query syntax and other options.
