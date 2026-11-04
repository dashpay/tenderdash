---
order: 1
---

# Creating an external application in Go

An external ABCI application runs separately from the Tenderdash node. The node
connects to its socket or gRPC server and sends requests using the
[ABCI application interface](https://github.com/dashpay/tenderdash/blob/master/abci/types/application.go).
Use the application interface and protobuf definitions from your Tenderdash release.

## Start with the example application

[Build Tenderdash](../introduction/install.md) and the example ABCI CLI from the
same checkout:

```sh
make install_abci
```

Start the supplied key-value application in one terminal:

```sh
abci-cli kvstore --address tcp://127.0.0.1:26658
```

Initialize a local validator and connect it to that application in another
terminal. Use a new home directory for this experiment:

```sh
tenderdash init validator --home ./example-node
tenderdash start --home ./example-node --proxy-app=tcp://127.0.0.1:26658
```

Keep the ABCI server bound to loopback for local development. A publicly exposed
ABCI endpoint lets callers bypass the node's consensus lifecycle.

## Implement your application

Study the [key-value application source](https://github.com/dashpay/tenderdash/blob/master/abci/example/kvstore/kvstore.go)
and its tests. It provides working examples of transaction checking, proposal
handling, block finalization, queries and snapshots.

Your Go implementation uses these package paths:

```go
import (
    "github.com/dashpay/tenderdash/abci/server"
    abci "github.com/dashpay/tenderdash/abci/types"
)
```

The `abci.Application` interface includes:

- `Info` and `InitChain` for startup and chain initialization.
- `CheckTx` for mempool admission; this must not commit application state.
- `PrepareProposal` and `ProcessProposal` for block construction and validation.
- `ExtendVote` and `VerifyVoteExtension` for application vote extensions.
- `FinalizeBlock` for applying a decided block and persisting state.
- `Query` for application queries, plus the snapshot methods for state sync.

`abci.BaseApplication` supplies default implementations for scaffolding. Those
defaults are not a complete persistent application. Implement the state, height,
application hash, validation and recovery behavior your application requires.

Create the server with
`server.NewServer(logger, address, transport, app)`, then start the returned
service with a context. Stop and wait for the service during shutdown. See the
[server package](https://github.com/dashpay/tenderdash/tree/master/abci/server)
and [ABCI CLI implementation](https://github.com/dashpay/tenderdash/blob/master/abci/cmd/abci-cli/abci-cli.go)
for lifecycle handling.

## Submit and query transactions

With the example key-value application running, submit a transaction:

```sh
curl -s 'localhost:26657/broadcast_tx_commit?tx="example=rocks"'
```

Query the committed value:

```sh
curl -s 'localhost:26657/abci_query?data="example"'
```

Inspect the transaction result as well as transport-level success. Your own
application defines the transaction encoding and query format.

## Validate application behavior

Test invalid transactions, proposal rejection, repeated requests, crash recovery
and snapshot restore. All correct replicas must derive the same application hash
from the same committed input. Keep tentative proposal execution isolated from
committed state, because proposals can be rejected or superseded.

For the complete lifecycle, read the [ABCI specification](../../spec/abci/README.md).
For embedding in the same process, follow the [built-in application guide](./go-built-in.md).
