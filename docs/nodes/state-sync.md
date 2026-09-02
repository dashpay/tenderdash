---
order: 5
---

# Configure State-Sync

State sync rapidly bootstraps a new node by discovering, fetching, and restoring
a state machine snapshot from peers instead of fetching and replaying historical
blocks. The node will have a truncated block history, starting from the height
of the snapshot.

> NOTE: Before trying to use state sync, see if the application you are
> operating a node for supports it.

State sync is only attempted on first start: if the node already has any local
state (`LastBlockHeight > 0`), it is skipped and the node falls back to block
sync.

Unlike upstream Tendermint, Tenderdash does **not** require trust anchors
(`trust-height`, `trust-hash`, `trust-period`). Light blocks are verified by
checking the quorum threshold signature against the active validator quorum via
Dash Core (`quorum verify`), so there is no need to obtain a trusted block hash
out of band. If you are migrating a configuration that still contains
`trust-height`, `trust-hash`, or `trust-period` under `[statesync]`, remove
them — they are no longer valid options.

Because light block verification goes through Dash Core, a full node **must**
be configured with a Dash Core RPC connection or it will refuse to start: set
`core-rpc-host` (and `core-rpc-username`/`core-rpc-password` as needed) in the
`[priv-validator]` section of `config.toml`. Validator nodes already have this
connection configured.

Under the `[statesync]` section in `config.toml` you will find the settings
that need to be configured in order for your node to use state sync.

Let's break down the settings:

- `enable`: Inform the node that you will be using state sync to bootstrap.
  This only controls *consuming* snapshots at first start; full and validator
  nodes serve snapshots and light blocks to peers regardless of this setting
  (seed nodes only run peer exchange and serve neither).
- `use-p2p`: State sync uses light client verification to verify state. This
  can be done either through the P2P layer or the RPC layer. Set this to `true`
  to use the P2P layer. If `false` (default), the RPC layer will be used.
- `rpc-servers`: Comma-separated list of RPC servers used for light client
  verification when `use-p2p = false`. In that mode at least **two** servers
  are required (more is always helpful). They should be compatible with
  `net.Dial`, for example: `host.example.com:2125`. Ignored when
  `use-p2p = true`.
- `discovery-time`: Time to spend discovering snapshots before initiating a
  restore (default: `15s`). Must be `0s` or at least `5s`. With `0s` the node
  gives up as soon as no suitable snapshot is available and falls back to
  block sync.
- `retries`: Number of times to retry state sync before giving up. When
  retries are exhausted, the node **falls back to regular block sync**. Set to
  `0` to retry indefinitely — the node keeps requesting snapshots forever and
  **never** falls back to block sync (default: `3`). Note that in the
  pessimistic case it will take at least `discovery-time * retries` before
  falling back to block sync.
- `temp-dir`: Temporary directory for snapshot chunks; defaults to the
  operating system temporary directory (e.g. `/tmp`). The synchronizer creates
  a new, randomly named directory within it and removes it when the sync is
  complete.
- `chunk-request-timeout`: The timeout before re-requesting a chunk, possibly
  from a different peer (default: `15s`). Must be at least `5s`.
- `fetchers`: The number of concurrent chunk and block fetchers to run
  (default: `4`).

Example configuration for a full node using RPC-based light client
verification (the `[priv-validator]` Dash Core connection is required on full
nodes regardless of state sync):

```toml
[priv-validator]
core-rpc-host = "127.0.0.1:9998"
core-rpc-username = "dashrpc"
core-rpc-password = "changeme"

[statesync]
enable = true
use-p2p = false
rpc-servers = "seed-1.example.com:26657,seed-2.example.com:26657"
```

Or, using the P2P layer for verification (no RPC servers needed):

```toml
[priv-validator]
core-rpc-host = "127.0.0.1:9998"
core-rpc-username = "dashrpc"
core-rpc-password = "changeme"

[statesync]
enable = true
use-p2p = true
```
