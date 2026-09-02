---
order: 5
---

# State Sync

## Channels

State sync has four distinct channels. The channel identifiers are listed below.

| Name              | Number |
|-------------------|--------|
| SnapshotChannel   | 96     |
| ChunkChannel      | 97     |
| LightBlockChannel | 98     |
| ParamsChannel     | 99     |

## Snapshot and chunk model

Tenderdash uses a content-addressed chunk model that differs from upstream
Tendermint. A snapshot is identified by its `height`, an application-specific
`version`, and a `hash`; there is no `format` field and no up-front chunk
count. Chunks are identified by an opaque, application-defined `chunk_id`
(in practice the hash of the corresponding node or subtree of the snapshot)
rather than by a sequential index.

Chunk discovery is driven by the ABCI application during restoration:

1. The syncer requests the first chunk using the snapshot `hash` as the
   initial `chunk_id`.
2. Each applied chunk is handed to the application via `ApplySnapshotChunk`;
   the response's `next_chunks` field lists the chunk IDs to fetch next, and
   `refetch_chunks` can ask for chunks to be re-fetched.
3. Restoration terminates when the application returns the
   `COMPLETE_SNAPSHOT` result — not when a predefined number of chunks has
   been applied.

## Message Types

### SnapshotsRequest

When a new node begins state syncing, it will ask all peers it encounters if
they have any available snapshots. The message has no fields:

| Name     | Type   | Description | Field Number |
|----------|--------|-------------|--------------|

### SnapshotsResponse

The receiver will query the local ABCI application via `ListSnapshots`, and
send a message containing snapshot metadata (limited to 4 MB) for each of the
10 most recent snapshots:

| Name     | Type   | Description                                               | Field Number |
|----------|--------|-----------------------------------------------------------|--------------|
| height   | uint64 | Height at which the snapshot was taken                    | 1            |
| version  | uint32 | Application-specific snapshot version                     | 2            |
| hash     | bytes  | Snapshot hash; also the ID of the first chunk to request  | 3            |
| metadata | bytes  | Arbitrary application data. **May be non-deterministic.** | 4            |

### ChunkRequest

The node running state sync will offer these snapshots to the local ABCI
application via `OfferSnapshot` ABCI calls, and keep track of which peers
contain which snapshots. Once a snapshot is accepted, the state syncer will
request snapshot chunks from appropriate peers:

| Name     | Type   | Description                                 | Field Number |
|----------|--------|---------------------------------------------|--------------|
| height   | uint64 | Height at which the snapshot was taken      | 1            |
| version  | uint32 | Application-specific snapshot version       | 2            |
| chunk_id | bytes  | Content-addressed ID of the requested chunk | 3            |

### ChunkResponse

The receiver will load the requested chunk from its local application via
`LoadSnapshotChunk`, and respond with it (limited to 16 MB):

| Name     | Type   | Description                                 | Field Number |
|----------|--------|---------------------------------------------|--------------|
| height   | uint64 | Height at which the snapshot was taken      | 1            |
| version  | uint32 | Application-specific snapshot version       | 2            |
| chunk_id | bytes  | Content-addressed ID of the chunk           | 3            |
| chunk    | bytes  | Binary chunk contents                       | 4            |
| missing  | bool   | True if the chunk was not found on the peer | 5            |

Here, `missing` is used to signify that the chunk was not found on the peer,
since an empty chunk is a valid (although unlikely) response.

The returned chunk is given to the ABCI application via `ApplySnapshotChunk`.
The application response determines what happens next: `next_chunks` lists
further chunk IDs to fetch, `refetch_chunks` requests re-fetching of chunks,
`reject_senders` requests peer bans, and the `COMPLETE_SNAPSHOT` result ends
the restoration. If a chunk response is not returned within some time, it will
be re-requested, possibly from a different peer.

### LightBlockRequest

To verify state and to provide state relevant information for consensus, the
node will ask peers for light blocks at specified heights.

| Name     | Type   | Description                | Field Number |
|----------|--------|----------------------------|--------------|
| height   | uint64 | Height of the light block  | 1            |

### LightBlockResponse

The receiver will retrieve and construct the light block from both the block
and state stores. The receiver will verify the data by comparing the hashes
and store the header, commit and validator set if necessary. The light block
at the height of the snapshot will be used to verify the `AppHash`.

| Name          | Type                                                    | Description                          | Field Number |
|---------------|---------------------------------------------------------|--------------------------------------|--------------|
| light_block   | [LightBlock](../../core/data_structures.md#lightblock)  | Light block at the height requested  | 1            |

Unlike upstream Tendermint, light blocks are not verified against a trusted
header obtained out of band. Tenderdash's light client verifies the quorum
threshold signature on the block commit via Dash Core (`quorum verify`), so no
trust anchors (trusted height, hash, or period) are required.

If no state sync is in progress (i.e. during normal operation), any
unsolicited response messages are discarded.

### ParamsRequest

In order to build tendermint state, the state provider will request the params
at the height of the snapshot and use the header to verify it.

| Name     | Type   | Description                     | Field Number |
|----------|--------|---------------------------------|--------------|
| height   | uint64 | Height of the consensus params  | 1            |

### ParamsResponse

A receiver to the request will use the state store to fetch the consensus
params at that height and return it to the sender.

| Name             | Type                                                             | Description                              | Field Number |
|------------------|------------------------------------------------------------------|------------------------------------------|--------------|
| height           | uint64                                                           | Height of the consensus params           | 1            |
| consensus_params | [ConsensusParams](../../core/data_structures.md#ConsensusParams) | Consensus params at the height requested | 2            |
