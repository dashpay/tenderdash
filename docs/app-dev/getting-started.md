---
order: 1
---

# Getting Started with Application Development

Tenderdash handles P2P communication, transaction gossip and consensus. Your
ABCI application defines transaction validation, execution and application state.
Start with the [application architecture guide](./app-architecture.md) and the
[ABCI overview](../introduction/what-is-tenderdash.md#application-boundary).

## Run an example

[Install Tenderdash](../introduction/install.md) and initialize a local validator
in a new directory:

```sh
tenderdash init validator --home ./example-node
tenderdash start --home ./example-node --proxy-app=kvstore
```

The built-in `kvstore` is an in-memory demonstration application. Do not use it
as persistent production storage.

Submit and query a key-value transaction:

```sh
curl -s 'localhost:26657/broadcast_tx_commit?tx="example=rocks"'
curl -s 'localhost:26657/abci_query?data="example"'
```

## Build your application

- [External Go application](../tutorials/go.md): run an ABCI server separately
  from the node.
- [Built-in Go application](../tutorials/go-built-in.md): use a local ABCI client
  in the same process.
- [ABCI CLI](./abci-cli.md): send requests directly to an application during
  development and debugging.

Applications in other languages must implement the protobuf messages and ABCI
lifecycle for the Tenderdash release they target. Check compatibility before
using a third-party ABCI library.

Use the [application interface](../../abci/types/application.go),
[protobuf definitions](../../proto/tendermint/abci/types.proto)
and [ABCI specification](../../spec/abci/README.md) as the contract. Test
transaction rejection, proposal processing, block finalization, deterministic
state hashes and restart recovery before connecting to a live network.
