---
order: 6
---

# Configure a Light Client

> **Unsupported.** The Tenderdash light client is currently unsupported.
> The implementation details below are provided for reference.

Tenderdash includes a `tenderdash light` command that runs an RPC proxy backed by
a verifying light client. Requests with supported proofs can be checked before
results are returned. Other RPC methods do not automatically gain proof verification.

The proxy requires a chain ID, a primary Tenderdash RPC provider and witness
providers. The current implementation uses Dash Core verification for quorum
signatures, so it also requires access to the appropriate Dash Core RPC service.
It does not use upstream trust-height and trust-hash flags.

Inspect the options for your installed release:

```sh
tenderdash light --help
```

Set `--primary`, `--witnesses` and the Dash Core connection settings for your
network. Use `--dir` to select an explicit directory for the light client's
local database and `--laddr` to control the proxy listen address.

Keep Core credentials private and restrict RPC network access. Ensure that the
application's query proof format is compatible with the proxy before relying
on `/abci_query` verification.

See the [light client overview](../tenderdash-core/light-client.md),
[command implementation](../../cmd/tenderdash/commands/light.go)
and [client implementation](../../light/client.go)
for the supported configuration and verification behavior.
