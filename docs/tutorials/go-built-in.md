---
order: 2
---

# Creating a built-in application in Go

An application written in Go can run in the same process as Tenderdash. A local
ABCI client calls the application directly, avoiding a network connection between
the node and application. The application still follows the same consensus and
persistence rules as an [external application](./go.md).

## Use the current module and interface

Build against `github.com/dashpay/tenderdash` at the release used by your network.
Tenderdash includes a native BLS dependency; follow the
[build instructions](../introduction/install.md) when preparing your toolchain.

Implement the [ABCI application interface](../../abci/types/application.go).
Use the [key-value application](../../abci/example/kvstore/kvstore.go)
as a reference for the full lifecycle, including `PrepareProposal`,
`ProcessProposal`, `FinalizeBlock`, queries and snapshots.

## Construct a local client and node

The relevant imports are:

```go
import (
    abciclient "github.com/dashpay/tenderdash/abci/client"
    "github.com/dashpay/tenderdash/config"
    "github.com/dashpay/tenderdash/node"
)
```

Given your application `app`, a configured logger `logger`, and the application
context `ctx`, the integration is:

```go
client := abciclient.NewLocalClient(logger, app)
service, err := node.New(ctx, conf, logger, client, nil)
if err != nil {
    return err
}
if err := service.Start(ctx); err != nil {
    return err
}
defer func() {
    service.Stop()
    service.Wait()
}()
```

Stop the service before waiting for it. Keep the process running until cancellation or a shutdown
signal, then cancel the context and shut down the node before closing application
storage.

The `conf` value is a validated `*config.Config` loaded for the intended home
directory. The default home is `$HOME/.tenderdash`; set it explicitly for tests.
Initialize the node's configuration and genesis with `tenderdash init validator`
before starting it. Passing `nil` as the final `node.New` argument loads genesis
from the configured file.

The [node constructor](../../node/public.go)
and [local client](../../abci/client/local_client.go)
are the source of truth for the API signatures in your release.

## Concurrency and recovery

The application can receive different classes of ABCI requests while the node is
running. Protect shared state, keep queries consistent with committed state, and
separate mempool checks and tentative proposal execution from finalization.
Persistence must record the height and application hash with the state they
identify so that restarting the process can recover consistently.

Test the application independently through the ABCI interface, then exercise the
embedded node's startup, shutdown and restart paths. The
[ABCI specification](../../spec/abci/README.md) describes the required behavior.
