---
order: 7
---

# RPC

Tenderdash exposes HTTP JSON-RPC methods for node status, blocks, transactions,
application queries and events. With RPC enabled, opening the configured RPC
listen address in a browser lists the endpoints available on that node.

See the [RPC API reference](../../api/rpc/index.html) for method parameters
and responses. The [RPC routes](../../internal/rpc/core/routes.go)
and [implementation](../../internal/rpc/core)
define the methods supported by this source version.

For clients written in Go, use the
[RPC client package](https://pkg.go.dev/github.com/dashpay/tenderdash/rpc/client).
For event queries and subscriptions, see [event subscriptions](./subscription.md).

Restrict RPC exposure according to your deployment. Administrative and debugging
methods must not be accessible to untrusted clients. See
[configuration](../nodes/configuration.md) and
[running in production](../nodes/running-in-production.md).
