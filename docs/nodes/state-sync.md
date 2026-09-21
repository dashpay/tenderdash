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

Dash Core is also what authenticates quorum membership and public key shares
before validator state received from peers is installed. If genesis configures
`VotingPowerThreshold`, the threshold received during state sync must match the
locally stored genesis value; it cannot be learned safely from a remote
state-sync provider.

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

## Snapshot discovery resource limits

Snapshot advertisements retain the existing 4,000,000-byte network message limit;
there is no smaller limit on application metadata. The node retains at most
64 MiB of unique snapshot hash and metadata payload, including the snapshot being
restored, and charges at most 40,000,000 bytes of advertised payload to each peer.
Shared snapshots count once globally and once for each supplying peer. Removing
an association releases that peer's charge immediately, while an active snapshot
remains globally charged until restoration cleanup finishes. If the same snapshot
is admitted again while a removed active copy is still in use, both owned copies
consume the global budget until the old copy is released. The pool owns copies
of the payload and releases its charge when it releases the data.
These limits bound retained discovery payload, not process RSS, transport receive
queues, decoded messages in flight, chunk data, or application state.

The pool also bounds candidates to 1,024 and peer associations to 10,240. Each
rejection history holds at most 1,024 entries; an older rejection can be considered
again after it leaves that history. Empty indexes are removed. Under payload
pressure, advertisements from less represented peers can replace unselected
candidates from peers retaining more data. The active snapshot is never evicted
or uncharged before restoration releases it. Admission is an opportunity to try
a candidate, not a guarantee against malicious peers controlling many identities.

Discovery sends directed requests to batches of at most 16 peers. Each requested
peer may return ten advertisements, counting duplicates and rejected replies.
Responses without remaining allowance are ignored without penalizing the peer.
Late responses remain eligible while that batch's allowance is open; subsequent
batches replace the allowance. Newly connected peers can use unallocated request
slots, and otherwise wait for a later discovery sweep.

The `retries` setting counts completed discovery sweeps. A sweep freezes at most
1,024 connected peers, visits them in batches, and cannot be prolonged by later
peer arrivals. Successive sweeps rotate through larger connected-peer lists.
Already retained candidates are tried before discovery is declared exhausted.
With no usable responses, a sweep takes up to
`ceil(min(connected_peers, 1024) / 16) * discovery-time`, with at least one discovery
interval and a minimum interval of five seconds. Snapshot restoration adds its
own processing time. Context cancellation interrupts discovery; `retries = 0`
continues discovery indefinitely as before.
