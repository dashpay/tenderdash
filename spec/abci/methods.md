---
order: 2
title: Methods
---

# Methods

## General, mempool and state-sync methods

### Echo

* **Request**:
    * `Message (string)`: A string to echo back
* **Response**:
    * `Message (string)`: The input string
* **Usage**:
* Echo a string to test an abci client/server implementation

### Flush

* **Usage**:
* Signals that messages queued on the client should be flushed to
the server. It is called periodically by the client
implementation to ensure asynchronous requests are actually
sent, and is called immediately to make a synchronous request,
which returns when the Flush response comes back.

### Info

* **Request**:

    | Name          | Type   | Description                              | Field Number |
    |---------------|--------|------------------------------------------|--------------|
    | version       | string | The Tenderdash software semantic version | 1            |
    | block_version | uint64 | The Tenderdash Block Protocol version    | 2            |
    | p2p_version   | uint64 | The Tenderdash P2P Protocol version      | 3            |
    | abci_version  | string | The Tenderdash ABCI semantic version     | 4            |

* **Response**:

    | Name                | Type   | Description                                             | Field Number |
    | ------------------- | ------ | ------------------------------------------------------- | ------------ |
    | data                | string | Some arbitrary information                              | 1            |
    | version             | string | The application software semantic version               | 2            |
    | app_version         | uint64 | The application protocol version                        | 3            |
    | last_block_height   | int64  | Latest block finalized and persisted by the application | 4            |
    | last_block_app_hash | bytes  | Application hash of that finalized block                | 5            |

* **Usage**:
* Return information about the application state.
* Used to sync Tenderdash with the application during a handshake
that happens on startup.
* During the startup handshake, `app_version` initializes the local state's
  application version only if no block has been committed yet. It also supplies
  this node's advertised version, used as `Header.ProposedAppVersion` when the
  node proposes a block. Another proposer may advertise a different version.
* `Header.Version.App` is the version used for the proposed block's execution.
  A nonzero `ResponsePrepareProposal.app_version` overrides its inherited value.
  Changing `Info.app_version` on an existing chain does not by itself change
  the committed execution version.
* `last_block_app_hash` and `last_block_height` describe the state persisted by
  `FinalizeBlock`; there is no ABCI `Commit` method. See
  [ABCI handshake](../consensus/consensus-recovery.md#start-up-abci-handshake)
  for replay and crash-recovery behavior.

> Note: Semantic version is a reference to [semantic versioning](https://semver.org/). Semantic versions in info will be displayed as X.X.x.

### InitChain

* **Request**:

    | Name                | Type                                                                                                                                 | Description                                            | Field Number |
    | ------------------- | ------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------ | ------------ |
    | time                | [google.protobuf.Timestamp](https://developers.google.com/protocol-buffers/docs/reference/google.protobuf#google.protobuf.Timestamp) | Genesis time                                           | 1            |
    | chain_id            | string                                                                                                                               | ID of the blockchain.                                  | 2            |
    | consensus_params    | [ConsensusParams](#consensusparams)                                                                                                  | Initial consensus-critical parameters.                 | 3            |
    | validator_set       | [ValidatorSetUpdate](#validatorsetupdate)                                                                                            | Initial genesis validator set, sorted by voting power. | 4            |
    | app_state_bytes     | bytes                                                                                                                                | Serialized initial application state. JSON bytes.      | 5            |
    | initial_height      | int64                                                                                                                                | Height of the initial block (typically `1`).           | 6            |
    | initial_core_height | uint32                                                                                                                               | Height of the initial core chainlock.                  | 7            |

* **Response**:

    | Name                        | Type                                                                                                                                 | Description                                       | Field Number |
    | --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------- | ------------ |
    | consensus_params            | [ConsensusParams](#consensusparams)                                                                                                  | Initial consensus-critical parameters (optional). | 1            |
    | app_hash                    | bytes                                                                                                                                | Initial application hash, 32 bytes.               | 2            |
    | validator_set_update        | [ValidatorSetUpdate](#validatorsetupdate)                                                                                            | Initial validator set update.                     | 3            |
    | next_core_chain_lock_update | [CoreChainLock](../../proto/tendermint/types/dash.proto)                                                                             | Initial core chain lock update (optional).        | 4            |
    | initial_core_height         | uint32                                                                                                                               | Initial core chain lock height.                   | 5            |
    | genesis_time                | [google.protobuf.Timestamp](https://developers.google.com/protocol-buffers/docs/reference/google.protobuf#google.protobuf.Timestamp) | Genesis time override (optional).                 | 6            |

* **Usage**:
* Called once upon genesis.
* If `ResponseInitChain.validator_set_update.validator_updates` is empty,
  genesis validator membership is retained; quorum-key and quorum-hash updates
  can still be supplied in `validator_set_update`.
* If it is not empty, those validators replace the initial membership supplied
  through `RequestInitChain.validator_set`.
* This allows the app to decide if it wants to accept the initial validator
set proposed by Tenderdash (ie. in the genesis file), or if it wants to use
a different one (perhaps computed based on some application specific
information in the genesis file).

### Query

* **Request**:

    | Name   | Type   | Description                                                                                                                                                                                                                                                                            | Field Number |
    |--------|--------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|--------------|
    | data   | bytes  | Raw query bytes. Can be used with or in lieu of Path.                                                                                                                                                                                                                                  | 1            |
    | path   | string | Path field of the request URI. Can be used with or in lieu of `data`. Apps MUST interpret `/store` as a query by key on the underlying store. The key SHOULD be specified in the `data` field. Apps SHOULD allow queries over specific types like `/accounts/...` or `/votes/...` | 2            |
    | height | int64  | The block height for which you want the query (default=0 returns data for the latest committed block). Note that this is the height of the block containing the application's Merkle root hash, which represents the state as it was after committing the block at Height            | 3            |
    | prove  | bool   | Return Merkle proof with response if possible                                                                                                                                                                                                                                          | 4            |

* **Response**:

    | Name      | Type                  | Description                                                                                                                                                                                                      | Field Number |
    | --------- | --------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------ |
    | code      | uint32                | Response code.                                                                                                                                                                                                   | 1            |
    | log       | string                | The output of the application's logger. **May be non-deterministic.**                                                                                                                                            | 2            |
    | info      | string                | Additional information. **May be non-deterministic.**                                                                                                                                                            | 3            |
    | index     | int64                 | The index of the key in the tree.                                                                                                                                                                                | 4            |
    | key       | bytes                 | The key of the matching data.                                                                                                                                                                                    | 5            |
    | value     | bytes                 | The value of the matching data.                                                                                                                                                                                  | 6            |
    | proof_ops | [ProofOps](#proofops) | Serialized proof for the value data, if requested, to be verified against the `app_hash` for the given Height.                                                                                                   | 7            |
    | height    | int64                 | The block height from which data was derived. Note that this is the height of the block containing the application's Merkle root hash, which represents the state as it was after committing the block at Height | 8            |
    | codespace | string                | Namespace for the `code`.                                                                                                                                                                                        | 9            |

* **Usage**:
* Query for data from the application at current or past height.
* Optionally return Merkle proof.
* Merkle proof includes self-describing `type` field to support many types
of Merkle trees and encoding formats.

### CheckTx

* **Request**:

    | Name | Type        | Description                                                                                                                                                                                                                                         | Field Number |
    |------|-------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|--------------|
    | tx   | bytes       | The request transaction bytes                                                                                                                                                                                                                       | 1            |
    | type | CheckTxType | One of `CheckTx_New` or `CheckTx_Recheck`. `CheckTx_New` is the default and means that a full check of the tranasaction is required. `CheckTx_Recheck` types are used when the mempool is initiating a normal recheck of a transaction.             | 2            |

* **Response**:

    | Name       | Type   | Description                                           | Field Number |
    |------------|--------|-------------------------------------------------------|--------------|
    | code       | uint32 | Response code.                                        | 1            |
    | data       | bytes  | Result bytes, if any.                                 | 2            |
    | info       | string | Additional information. **May be non-deterministic.** | 3            |
    | gas_wanted | int64  | Amount of gas requested for transaction.              | 4            |
    | codespace  | string | Namespace for the `code`.                             | 5            |
    | sender     | string | The transaction's sender (e.g. the signer)            | 6            |
    | priority   | int64  | The transaction's priority (for mempool ordering)     | 7            |

* **Usage**:

* Technically optional - not involved in processing blocks.
* Guardian of the mempool: every node runs `CheckTx` before letting a
transaction into its local mempool.
* The transaction may come from an external user or another node
* `CheckTx` validates the transaction against the current state of the application,
for example, checking signatures and account balances, but does not apply any
of the state changes described in the transaction.
* Transactions where `ResponseCheckTx.Code != 0` will be rejected - they will not be broadcast to
other nodes or included in a proposal block.
* Tenderdash attributes no other value to the response code

### ListSnapshots

* **Request**:

    | Name   | Type  | Description                        | Field Number |
    |--------|-------|------------------------------------|--------------|

    Empty request asking the application for a list of snapshots.

* **Response**:

    | Name      | Type                           | Description                    | Field Number |
    |-----------|--------------------------------|--------------------------------|--------------|
    | snapshots | repeated [Snapshot](#snapshot) | List of local state snapshots. | 1            |

* **Usage**:
* Used during state sync to discover available snapshots on peers.
* See `Snapshot` data type for details.

### LoadSnapshotChunk

* **Request**:

    | Name     | Type   | Description                                            | Field Number |
    | -------- | ------ | ------------------------------------------------------ | ------------ |
    | height   | uint64 | The height of the snapshot the chunk belongs to.       | 1            |
    | version  | uint32 | The application-specific snapshot version.             | 2            |
    | chunk_id | bytes  | Application-defined identifier of the requested chunk. | 3            |

* **Response**:

    | Name  | Type  | Description                                                                                                                                           | Field Number |
    |-------|-------|-------------------------------------------------------------------------------------------------------------------------------------------------------|--------------|
    | chunk | bytes | The binary chunk contents, in an arbitray format. Chunk messages cannot be larger than 16 MB _including metadata_, so 10 MB is a good starting point. | 1            |

* **Usage**:
* Used during state sync to retrieve snapshot chunks from peers.
* Chunk identifiers are bytes, not numeric indexes. The initial identifier is
  `Snapshot.hash`; the application supplies further identifiers through
  `ResponseApplySnapshotChunk.next_chunks`.

### OfferSnapshot

* **Request**:

    | Name     | Type                  | Description                                                              | Field Number |
    |----------|-----------------------|--------------------------------------------------------------------------|--------------|
    | snapshot | [Snapshot](#snapshot) | The snapshot offered for restoration.                                    | 1            |
    | app_hash | bytes                 | The light client-verified app hash for this height, from the blockchain. | 2            |

* **Response**:

    | Name   | Type              | Description                       | Field Number |
    |--------|-------------------|-----------------------------------|--------------|
    | result | [Result](#result) | The result of the snapshot offer. | 1            |

#### Result

```protobuf
  enum Result {
    UNKNOWN       = 0;  // Unknown result, abort all snapshot restoration
    ACCEPT        = 1;  // Snapshot is accepted, start applying chunks.
    ABORT         = 2;  // Abort snapshot restoration, and don't try any other snapshots.
    REJECT        = 3;  // Reject this specific snapshot, try others.
    REJECT_FORMAT = 4;  // Reject all snapshots with this version, try others.
    REJECT_SENDER = 5;  // Reject all snapshots from all senders of this snapshot, try others.
  }
```

* **Usage**:
* `OfferSnapshot` is called when bootstrapping a node using state sync. The application may
accept or reject snapshots as appropriate. Upon accepting, Tenderdash will retrieve and
apply snapshot chunks via `ApplySnapshotChunk`. The application may also choose to reject a
snapshot in the chunk response, in which case it should be prepared to accept further
`OfferSnapshot` calls.
* Only `AppHash` can be trusted, as it has been verified by the light client. Any other data
can be spoofed by adversaries, so applications should employ additional verification schemes
to avoid denial-of-service attacks. The verified `AppHash` is automatically checked against
the restored application at the end of snapshot restoration.
* For more information, see the `Snapshot` data type or the [state sync section](../p2p/messages/state-sync.md).

### ApplySnapshotChunk

* **Request**:

    | Name     | Type   | Description                                                    | Field Number |
    | -------- | ------ | -------------------------------------------------------------- | ------------ |
    | chunk_id | bytes  | Application-defined identifier of this chunk.                  | 1            |
    | chunk    | bytes  | The binary chunk contents, as returned by `LoadSnapshotChunk`. | 2            |
    | sender   | string | The P2P ID of the node who sent this chunk.                    | 3            |

* **Response**:

    | Name           | Type                | Description                                                                                                                                                                                                                             | Field Number |
    | -------------- | ------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------ |
    | result         | Result  (see below) | The result of applying this chunk.                                                                                                                                                                                                      | 1            |
    | refetch_chunks | repeated bytes      | Identifiers of chunks to discard, refetch and reapply, regardless of `result`.                                                                                                                                                          | 2            |
    | reject_senders | repeated string     | Reject the given P2P senders, regardless of `Result`. Any chunks already applied will not be refetched unless explicitly requested, but queued chunks from these senders will be discarded, and new chunks or other snapshots rejected. | 3            |
    | next_chunks    | repeated bytes      | Chunk identifiers to enqueue after `ACCEPT`; ignored for other results.                                                                                                                                                                 | 4            |

```proto
  enum Result {
    UNKNOWN         = 0;  // Unknown result, abort all snapshot restoration
    ACCEPT          = 1;  // The chunk was accepted.
    ABORT           = 2;  // Abort snapshot restoration, and don't try any other snapshots.
    RETRY           = 3;  // Reapply this chunk, combine with `RefetchChunks` and `RejectSenders` as appropriate.
    RETRY_SNAPSHOT  = 4;  // Restart this snapshot from `OfferSnapshot`, reusing chunks unless instructed otherwise.
    REJECT_SNAPSHOT = 5;  // Reject this snapshot, try a different one.
    COMPLETE_SNAPSHOT = 6;  // Accept this chunk and finish restoring the snapshot.
  }
```

* **Usage**:
* The application can choose to refetch chunks and/or ban P2P peers as appropriate. Tenderdash
will not do this unless instructed by the application.
* The application may want to verify each chunk, e.g. by attaching chunk hashes in
`Snapshot.Metadata` and/or incrementally verifying contents against `AppHash`.
* On `ACCEPT`, Tenderdash enqueues `next_chunks` and continues applying chunks.
  Fetching can run in parallel; identifiers do not impose a numeric application
  order. The application must discover and validate the required chunks.
* Return `COMPLETE_SNAPSHOT` only when restoration is complete. An `ACCEPT` with
  no `next_chunks`, or an empty request queue, does not signal completion.
* After `COMPLETE_SNAPSHOT`, Tenderdash calls `Info` and checks
  `last_block_app_hash` against the light-client-verified hash,
  `last_block_height` against the snapshot height, and `app_version` against the
  version in the reconstructed consensus state. Failed verification prevents
  successful state sync. The node can then catch up through block sync or enter
  consensus.
* If Tenderdash is unable to retrieve the next chunk after some time (e.g. because no suitable
peers are available), it will reject the snapshot and try a different one via `OfferSnapshot`.
The application should be prepared to reset and accept it or abort as appropriate.

## Consensus methods

### PrepareProposal

#### Parameters and Types

| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| PrepareProposal | [RequestPrepareProposal](api.md#tendermint-abci-RequestPrepareProposal) | [ResponsePrepareProposal](api.md#tendermint-abci-ResponsePrepareProposal) |  |

#### Usage

* Use the linked request/response schemas for field names and numbers;
  proposal preparation, processing and finalization have different request shapes.
* The height and time values match the values from the header of the proposed block.
* `RequestPrepareProposal` contains a preliminary set of transactions `txs` that Tenderdash considers to be a good block proposal, called _raw proposal_. The Application can modify this set via `ResponsePrepareProposal.tx_records` (see [TxRecord](#txrecord)).
    * The Application _can_ reorder, remove or add transactions to the raw proposal. Let `tx` be a transaction in `txs`:
        * If the Application considers that `tx` should not be proposed in this block, e.g., there are other transactions with higher priority, then it should not include it in `tx_records`. In this case, Tenderdash won't remove `tx` from the mempool. The Application should be extra-careful, as abusing this feature may cause transactions to stay forever in the mempool.
        * If the Application considers that a `tx` should not be included in the proposal and removed from the mempool, then the Application should include it in `tx_records` and _mark_ it as `REMOVED`. In this case, Tenderdash will remove `tx` from the mempool.
        * If the Application wants to add a new transaction, then the Application should include it in `tx_records` and _mark_ it as `ADDED`. Tenderdash includes it in the proposal without adding it to the mempool.
    * The Application should be aware that removing and adding transactions may compromise _traceability_.
      > Consider the following example: the Application transforms a client-submitted transaction `t1` into a second transaction `t2`, i.e., the Application asks Tenderdash to remove `t1` from the mempool and include `t2` in the proposal. If a client wants to eventually check what happened to `t1`, it will discover that `t_1` is not in the mempool or in a committed block, getting the wrong idea that `t_1` did not make it into a block. Note that `t_2` _will be_ in a committed block, but unless the Application tracks this information, no component will be aware of it. Thus, if the Application wants traceability, it is its responsability to support it. For instance, the Application could attach to a transformed transaction a list with the hashes of the transactions it derives from.
* Tenderdash MAY include a list of transactions in `RequestPrepareProposal.txs` whose total size in bytes exceeds `RequestPrepareProposal.max_tx_bytes`.
  Therefore, if the size of `RequestPrepareProposal.txs` is greater than `RequestPrepareProposal.max_tx_bytes`, the Application MUST make sure that the
  `RequestPrepareProposal.max_tx_bytes` limit is respected by those transaction records returned in `ResponsePrepareProposal.tx_records` that are marked as `UNMODIFIED` or `ADDED`.
* Tenderdash uses same-block execution. The Application must provide values for `ResponsePrepareProposal.app_hash`,
  `ResponsePrepareProposal.tx_results`, `ResponsePrepareProposal.validator_set_update`, `ResponsePrepareProposal.core_chain_lock_update` and
  `ResponsePrepareProposal.consensus_param_updates`, as a result of fully executing the block.
    * The values for `ResponsePrepareProposal.validator_set_update`, `ResponsePrepareProposal.core_chain_lock_update` or
      `ResponsePrepareProposal.consensus_param_updates` may be empty. In this case, Tenderdash will keep
      the current values.
    * Validator changes from block `H` are committed by `NextValidatorsHash`
      and become active at `H+1`. See
      [Validator-set and parameter changes](../consensus/consensus-alternative-flows.md#validator-set-and-parameter-changes).
    * Consensus parameter changes from `PrepareProposal`/`ProcessProposal` for block
      `H` take effect at `H+1`; `ResponseFinalizeBlock` has no parameter-update field.
      See [Validator-set and parameter changes](../consensus/consensus-alternative-flows.md#validator-set-and-parameter-changes).
    * It is the responsibility of the Application to set the right value for _TimeoutPropose_ so that
      the (synchronous) execution of the block does not cause other processes to prevote `nil` because
      their propose timeout goes off.
* Block and transaction events are returned with `ProcessProposal`, not
  `ResponseFinalizeBlock`; see [Events](basic-concepts.md#events).
* As a sanity check, Tenderdash will check the returned parameters for validity if the Application modified them.
  In particular, `ResponsePrepareProposal.tx_records` will be deemed invalid if
    * There is a duplicate transaction in the list.
    * A new or modified transaction is marked as `UNMODIFIED` or `REMOVED`.
    * An unmodified transaction is marked as `ADDED`.
    * A transaction is marked as `UNKNOWN`.
* `ResponsePrepareProposal.tx_results` contains only results of  `UNMODIFIED` and `ADDED` transactions.
`REMOVED` transactions are omitted. The length of `tx_results` can be different than the length of `tx_records`.
* If Tenderdash fails to validate the `ResponsePrepareProposal`, Tenderdash will assume the application is faulty and crash.
* The implementation of `PrepareProposal` can be non-deterministic.

#### When does Tenderdash call it?

Tenderdash calls `PrepareProposal` when creating a fresh proposal, after selecting
transactions within the block's byte/gas budget. A valid block is reused only when
`checkValidBlock` permits it; otherwise a fresh block is prepared even if a
`ValidBlock` is recorded. The exact transaction-space calculation, response
validation and reuse rules are in
[Creating the proposal](../consensus/consensus-happy-path.md#3a-creating-the-proposal-proposer-only).

### ProcessProposal

#### Parameters and Types

| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| ProcessProposal | [RequestProcessProposal](api.md#tendermint-abci-RequestProcessProposal) | [ResponseProcessProposal](api.md#tendermint-abci-ResponseProcessProposal) |  |

#### Usage

* Contains fields from the proposed block.
    * The Application may fully execute the block as though it was handling `RequestFinalizeBlock`.
      However, any resulting state changes must be kept as _candidate state_,
      and the Application should be ready to backtrack/discard it in case the decided block is different.
* The height and timestamp values match the values from the header of the proposed block.
* If `ResponseProcessProposal.status` is `REJECT`, Tenderdash assumes the proposal received
  is not valid.
* Tenderdash requires the current block's execution commitments. The Application must provide values
  for parameters `ResponseProcessProposal.app_hash`, `ResponseProcessProposal.tx_results`,
  `ResponseProcessProposal.validator_set_update`, and `ResponseProcessProposal.consensus_param_updates`,
  so that Tenderdash can then verify the hashes in the block's header are correct.
  If the hashes mismatch, Tenderdash will reject the block even if `ResponseProcessProposal.status`
  was set to `ACCEPT`.
* The implementation of `ProcessProposal` MUST be deterministic. Moreover, the value of
  `ResponseProcessProposal.status` MUST **exclusively** depend on the parameters passed in
  the call to `RequestProcessProposal`, and the last committed Application state
  (see [Requirements](app-requirements.md) section).
* Tenderdash may call `ProcessProposal` more than once for the same height and round with the
  same block, for example after the application rejected a commit's vote extensions or when block
  sync hands over to consensus, and again for a round after processing another round, for example
  to verify a commit of that round. The Application MUST treat such a call as re-execution that
  replaces its execution context for the height; `FinalizeBlock` refers to the round processed last.
* Moreover, application implementors SHOULD always set `ResponseProcessProposal.status` to `ACCEPT`,
  unless they _really_ know what the potential liveness implications of returning `REJECT` are.

#### When does Tenderdash call it?

`ensureProcess` processes the held block for the round being checked unless the
same block/round result is already current. It runs before the prevote lock rules,
so a locked validator can process another block and still prevote nil. A
well-formed application rejection or block/header mismatch produces a nil prevote;
unknown status, malformed results and transport errors are fatal on this path.
See the exact [prevote decision](../consensus/consensus-transitions.md#prevote-decision-prevoterdo).

Processing also occurs for commit application and can repeat after another round
changed the execution context; see
[Commit handling](../consensus/consensus-commit.md) and
[expected call sequences](expected-behavior.md#valid-method-call-sequences).

### ExtendVote

#### Parameters and Types

* **Request**:

    | Name   | Type  | Description                                                                    | Field Number |
    | ------ | ----- | ------------------------------------------------------------------------------ | ------------ |
    | hash   | bytes | The header hash of the proposed block that the vote extensions is to refer to. | 1            |
    | height | int64 | Height of the proposed block (for sanity check).                               | 2            |
    | round  | int32 | Round for the block and its application execution context.                     | 3            |

* **Response**:

    | Name            | Type  | Description                                   | Field Number |
    |-------------------|-------|-----------------------------------------------|--------------|
    | vote_extensions | [ExtendVoteExtension](#extendvoteextension) | Optional information signed by Tenderdash. | 1            |

* **Usage**:
* `ResponseExtendVote.vote_extensions` is optional information that, if present, will be signed by Tenderdash and
  attached to the Precommit message.
* `RequestExtendVote.hash` corresponds to the hash of a proposed block that was made available to the application
  in a previous call to `ProcessProposal` or `PrepareProposal` for the current height.
* `ResponseExtendVote.vote_extensions` will only be attached to a non-`nil` Precommit message. If Tenderdash is to
  precommit `nil`, it will not call `RequestExtendVote`.
* The Application logic that creates extensions that are not threshold-recoverable can be
  non-deterministic. Threshold-recoverable extensions (`THRESHOLD_RECOVER` and
  `THRESHOLD_RECOVER_RAW`) are recovered into the commit, so they MUST be deterministic.
* `ExtendVote` MUST be free of side effects, and the threshold-recoverable subset of its response
  MUST be exactly the vector the Application accepts in a commit-level `VerifyVoteExtension` for
  the same block, height and round.
* Tenderdash may also call `ExtendVote` on any node, a non-validator included, after a
  commit-level `VerifyVoteExtension` rejection that follows `ProcessProposal` of that block and
  round, to learn the vector a commit of that block and round must carry; see
  [VerifyVoteExtension](#verifyvoteextension). An ABCI error or an invalid response from this
  call is fatal, as on the Precommit path.

#### When does Tenderdash call it?

The normal call is inside signing a non-nil precommit, after the block was
processed for that round. The proposal, lock and polka requirements are in the
[precommit decision](../consensus/consensus-transitions.md#precommit-decision-enterprecommitactionexecute).
Nil precommits do not call `ExtendVote` for signing.

After rejecting a commit's extensions, Tenderdash can also call `ExtendVote` on
any node to learn the expected recoverable vector for the processed block and
commit round. That response is neither signed nor broadcast; see
[Parked commits and replacement candidates](../consensus/consensus-commit.md#parked-commit-and-replacement-candidates).

### VerifyVoteExtension

Tenderdash calls `VerifyVoteExtension` for two kinds of request, told apart by
`validator_pro_tx_hash`:

* **Precommit verification** (`validator_pro_tx_hash` set): the vote extensions
  of one validator's Precommit, exactly as its Application returned them from
  `ExtendVote`.
* **Commit verification** (`validator_pro_tx_hash` empty): the extension vector
  of a commit, sent after `ProcessProposal` of the committed block with the same
  `hash`, `height` and `round` (the commit round). The vector holds only the
  threshold-recoverable extensions (`THRESHOLD_RECOVER` and
  `THRESHOLD_RECOVER_RAW`), in the order `ExtendVote` returned them; extensions
  of any other type never appear in a commit. It can be empty.

#### Parameters and Types

* **Request**:

    | Name                  | Type                                                 | Description                                                                                                                  | Field Number |
    |-----------------------|------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------|--------------|
    | hash                  | bytes                                                | The header hash of the propsed block that the vote extensions refers to.                                                     | 1            |
    | validator_pro_tx_hash | bytes                                                | [ProTxHash](../core/data_structures.md#protxhash) of the validator that signed the extensions; empty for commit verification | 2            |
    | height                | int64                                                | Height of the block  (for sanity check).                                                                                     | 3            |
    | round                 | int32                                                | Round of the vote; for commit verification, the commit round.                                                                | 4            |
    | vote_extensions       | repeated [ExtendVoteExtension](#extendvoteextension) | Application-specific information signed by Tenderdash. Can have 0 length                                                     | 5            |

* **Response**:

    | Name   | Type                          | Description                                                    | Field Number |
    |--------|-------------------------------|----------------------------------------------------------------|--------------|
    | status | [VerifyStatus](#verifystatus) | `enum` signaling if the application accepts the vote extension | 1            |

* **Usage**:
* `RequestVerifyVoteExtension.vote_extensions` can be empty. For a Precommit, the Application's
  interpretation of it should be that the Application running at the process that sent the vote
  chose not to extend it. Tenderdash will always call `RequestVerifyVoteExtension`, even for 0
  length vote extensions.
* For a Precommit, if `ResponseVerifyVoteExtension.status` is `REJECT`, Tenderdash will reject
  the whole received vote. See the [Requirements](app-requirements.md) section to
  understand the potential liveness implications of this.
* For a commit, the Application MUST compare the vector with the threshold-recoverable subset of
  the extensions it expects for that block, in order, and return `REJECT` if they differ: a
  commit whose block signature is valid can still carry a stripped, duplicated or replayed
  vector. Tenderdash requires `ACCEPT` before it saves the block and commit or calls
  `FinalizeBlock`. What `REJECT` does depends on the path:
    * consensus: the commit is discarded without blaming its sender. A commit whose block the node
      already holds is checked first and never changes the round. A commit for a later round whose
      block has not arrived moves the node to that round on its threshold signature alone; it is
      checked when the block arrives, then the commits other peers sent meanwhile. Copies of a
      commit already waiting are dropped. If the node's own commit is rejected, nothing is
      persisted and the node waits for a peer's commit. The block is processed again only for a
      commit of a round other than the one processed last. After a rejection, Tenderdash calls
      `ExtendVote` for the block and round, and refuses, without processing the block again or
      calling the Application, a later commit of that round that would need the block processed
      again, when its vector differs from the one returned or equals it and was rejected.
      Consensus WAL replay follows these rules;
    * block sync: the block is not applied; the peer that served it is dropped and the height is
      requested again;
    * handshake catch-up (application behind the block store): the commit is already in the block
      store, so the node fails to start. There is no automatic recovery; the operator has to roll
      back or re-sync the node.
* Any status other than `ACCEPT`, including `UNKNOWN`, counts as `REJECT`. An error from the ABCI
  call itself, as opposed to a status, is fatal on every path.
* The implementation of `VerifyVoteExtension` MUST be deterministic. Moreover, the value of
  `ResponseVerifyVoteExtension.status` MUST **exclusively** depend on the parameters passed in
  the call to `RequestVerifyVoteExtension`, the block processed by `ProcessProposal`, and the
  last committed Application state (see [Requirements](app-requirements.md) section).
* For a Precommit, application implementers SHOULD always set
  `ResponseVerifyVoteExtension.status` to `ACCEPT`, unless they _really_ know what the potential
  liveness implications of returning `REJECT` are.

#### When does Tenderdash call it?

For a peer's non-nil precommit, Tenderdash verifies its block and extension
signatures, restores its own processed round if needed, then calls the Application
with the validator's pro-tx-hash. A rejected vote is not stored and may be
redelivered. Own votes skip this Application check; see
[Vote admission](../consensus/consensus-transitions.md#admission-addvotevalidatevotemw-heightvotesetaddvote-votesetaddvote).

For a commit, Tenderdash processes the block for `commit.Round` and verifies the
recovered extension vector with an empty validator pro-tx-hash. Live consensus
requires acceptance before saving/finalizing the block. Replacement candidates and
stored-block replay have different retry behavior, as described in the usage
contract above and [Commit handling](../consensus/consensus-commit.md).

### FinalizeBlock

#### Parameters and Types


| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| FinalizeBlock | [RequestFinalizeBlock](api.md#tendermint-abci-RequestFinalizeBlock) | [ResponseFinalizeBlock](api.md#tendermint-abci-ResponseFinalizeBlock) |  |

#### Usage

* Contains the fields of the newly decided block.
* The height and timestamp values match the values from the header of the proposed block.
* `RequestFinalizeBlock.commit` and `RequestFinalizeBlock.misbehavior` carry
  commit and misbehavior information; see the linked request schema.
* The Application persists the state corresponding to `RequestFinalizeBlock.block`
  and the round processed last, whose execution commitments were already supplied
  through `PrepareProposal`/`ProcessProposal`. It may reuse that candidate rather
  than execute the block again.
* Transaction results and events are returned during `ProcessProposal`;
  `ResponseFinalizeBlock` returns only `retain_height` and
  `propose_next_block_immediately`, with no application hash or events.
* Application is expected to persist its state at the end of this call, before calling `ResponseFinalizeBlock`.
* Later calls to `Query` can return proofs about the application state anchored
  in this Merkle root hash.
* Use `ResponseFinalizeBlock.retain_height` with caution! If all nodes in the network remove historical
  blocks then this data is permanently lost, and no new nodes will be able to join the network and
  bootstrap. Historical blocks may also be required for other purposes, e.g. auditing, replay of
  non-persisted heights, light client verification, and so on.
* Set `ResponseFinalizeBlock.propose_next_block_immediately` when the Application has block-driven
  work pending that the next block would carry out, for example withdrawal transactions waiting to be
  signed. The node then does not wait for transactions (`create-empty-blocks-interval`) before
  proposing round 0 of the next height. This is a hint to the local node, not part of consensus: it is
  not compared between validators, it only matters on the node that proposes the next height, and it
  is consumed once; a node that restarts in between falls back to the configured wait.
* Just as `ProcessProposal`, the implementation of `FinalizeBlock` MUST be deterministic, since it is
  making the Application's state evolve in the context of state machine replication.
* Currently, Tenderdash will fill up all fields in `RequestFinalizeBlock`, even if they were
  already passed on to the Application via `RequestPrepareProposal` or `RequestProcessProposal`.
  The Application must select the candidate matching the finalized block and
  the round processed last; a block hash alone does not identify a round's
  execution context. See [Commit validity depends on the application](../consensus/consensus-commit.md#commit-validity-depends-on-the-application).

#### When does Tenderdash call it?

Tenderdash finalizes a held block after validating its complete block ID,
processing it for the commit round, and accepting the recovered commit's
extensions. The commit can come from local precommits or a peer; it does not
require receipt of every original vote or proposal. See
[Commit handling](../consensus/consensus-commit.md) and the exact persistence order
in [Commit and apply](../consensus/consensus-happy-path.md#9-commit-and-apply).

## Shared data types

Most of the data structures used in ABCI are shared [common data structures](../core/data_structures.md). In certain cases, ABCI uses different data structures which are documented here:

### Validator

See the generated [Validator schema](api.md#tendermint-abci-Validator).

* **Usage**:
* Validator identified by ProTxHash
* Used by `Misbehavior.validator` to identify an offending validator.
* Public keys are carried separately by validator-set update messages.

### ValidatorUpdate

See the generated [ValidatorUpdate schema](api.md#tendermint-abci-ValidatorUpdate).

* **Usage**:
* Validator identified by ProTxHash
* Used to tell Tenderdash to update the validator set

### ValidatorSetUpdate

See the generated [ValidatorSetUpdate schema](api.md#tendermint-abci-ValidatorSetUpdate).

* **Usage**:
* Used to tell Tenderdash to update the validator set update

### Misbehavior

See the generated [Misbehavior schema](api.md#tendermint-abci-Misbehavior).

### ConsensusParams

* **Fields**:

    | Name      | Type                                                          | Description                                                                  | Field Number |
    |-----------|---------------------------------------------------------------|------------------------------------------------------------------------------|--------------|
    | block     | [BlockParams](../core/data_structures.md#blockparams)         | Parameters limiting the size of a block and time between consecutive blocks. | 1            |
    | evidence  | [EvidenceParams](../core/data_structures.md#evidenceparams)   | Parameters limiting the validity of evidence of byzantine behaviour.         | 2            |
    | validator | [ValidatorParams](../core/data_structures.md#validatorparams) | Parameters limiting the types of public keys validators can use.             | 3            |
    | version   | [VersionsParams](../core/data_structures.md#versionparams)    | The ABCI application version.                                                | 4            |

### ProofOps

* **Fields**:

    | Name | Type                         | Description                                                                                                                                                                                                                  | Field Number |
    |------|------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|--------------|
    | ops  | repeated [ProofOp](#proofop) | List of chained Merkle proofs, of possibly different types. The Merkle root of one op is the value being proven in the next op. The Merkle root of the final op should equal the ultimate root hash being verified against.. | 1            |

### ProofOp

* **Fields**:

    | Name | Type   | Description                                    | Field Number |
    |------|--------|------------------------------------------------|--------------|
    | type | string | Type of Merkle proof and how it's encoded.     | 1            |
    | key  | bytes  | Key in the Merkle tree that this proof is for. | 2            |
    | data | bytes  | Encoded Merkle proof for the key.              | 3            |

### Snapshot

See the generated [Snapshot schema](api.md#tendermint-abci-Snapshot).

* **Usage**:
* Used for state sync snapshots, see the [state sync section](../p2p/messages/state-sync.md) for details.
* A snapshot is considered identical across nodes only if _all_ fields are equal (including
`Metadata`). Chunks may be retrieved from all nodes that have the same snapshot.
* When sent across the network, a snapshot message can be at most 4 MB.

## Execution and consensus data types

### VoteInfo

See the generated [VoteInfo schema](api.md#tendermint-abci-VoteInfo).

* **Usage**:
* Indicates whether a validator signed the last block, allowing for rewards based on validator availability.
* This information is typically extracted from a proposed or decided block.

### ExtendVoteExtension

See the generated [ExtendVoteExtension schema](api.md#tendermint-abci-ExtendVoteExtension).

* **Usage**:
* Provides a vote extension for signing
* Each field is mandatory for filling
* Type can be `THRESHOLD_RECOVER`, `THRESHOLD_RECOVER_RAW` or `DEFAULT`
* `DEFAULT` is not supported so far
* `THRESHOLD_RECOVER` and `THRESHOLD_RECOVER_RAW` are deterministic, that means, each validator in a quorum must provide the same vote extension data

### VoteExtensionType

```proto
enum VoteExtensionType {
  DEFAULT               = 0;
  THRESHOLD_RECOVER     = 1;
  THRESHOLD_RECOVER_RAW = 2;  // signs the raw extension; allows overriding the sign request ID
}
```

### CommitInfo

See the generated [CommitInfo schema](api.md#tendermint-abci-CommitInfo).


### ExecTxResult

See the generated [ExecTxResult schema](api.md#tendermint-abci-ExecTxResult).

### TxAction

```proto
enum TxAction {
  UNKNOWN    = 0;  // Unknown action
  UNMODIFIED = 1;  // The Application did not modify this transaction.
  ADDED      = 2;  // The Application added this transaction.
  REMOVED    = 3;  // The Application wants this transaction removed from the proposal and the mempool.
  DELAYED    = 4;  // Exclude from this proposal, but keep in the mempool.
}
```

* **Usage**:
* If `Action` is `UNKNOWN`, a problem happened in the Application. Tenderdash will assume the application is faulty and crash.
* If `Action` is `UNMODIFIED`, Tenderdash includes the transaction in the proposal. Nothing to do on the mempool.
* If `Action` is `ADDED`, Tenderdash includes the transaction in the proposal. The transaction is _not_ added to the mempool.
* If `Action` is `REMOVED`, Tenderdash excludes the transaction from the proposal. The transaction is also removed from the mempool if it exists,
  similar to `CheckTx` returning _false_.

* If `Action` is `DELAYED`, Tenderdash excludes the transaction from the proposal and keeps it in the mempool.

### TxRecord

See the generated [TxRecord schema](api.md#tendermint-abci-TxRecord).

### ProposalStatus

```proto
enum ProposalStatus {
  UNKNOWN = 0; // Unknown status. Returning this from the application is always an error.
  ACCEPT  = 1; // Status that signals that the application finds the proposal valid.
  REJECT  = 2; // Status that signals that the application finds the proposal invalid.
}
```

* **Usage**:
* Used within the [ProcessProposal](#processproposal) response.
    * If `Status` is `UNKNOWN`, a problem happened in the Application. Tenderdash will assume the application is faulty and crash.
    * If `Status` is `ACCEPT`, the application finds the proposal valid. Tenderdash still validates
      the returned execution results, which can reject the block. On the prevote path, the lock and
      POL rules then decide between a prevote for the block and a nil prevote.
    * If `Status` is `REJECT`, the application finds the proposal invalid. On the prevote path,
      Tenderdash prevotes nil; during commit processing or replay, the call fails and the block
      is not applied. See [ProcessProposal](#processproposal) for each call path.

### VerifyStatus

```proto
enum VerifyStatus {
  UNKNOWN = 0; // Unknown status. Returning this from the application is always an error.
  ACCEPT  = 1; // Status that signals that the application finds the vote extension valid.
  REJECT  = 2; // Status that signals that the application finds the vote extension invalid.
}
```

* **Usage**:
* Used within the [VerifyVoteExtension](#verifyvoteextension) response, for a Precommit or a commit.
    * If `Status` is `ACCEPT`, Tenderdash will accept the vote, or the commit, as valid.
    * If `Status` is `REJECT` or `UNKNOWN`, Tenderdash will reject the vote, or the commit, as invalid.
      Only an error from the ABCI call itself is fatal.


### CanonicalVoteExtension

> This signing structure belongs to the consensus vote-extension protocol,
> rather than the ABCI application interface. See [vote-extension signing](../consensus/signing.md#block-signature-verification-on-light-client).

* **Fields**:

    | Name      | Type              | Description                                                             | Field Number |
    |-----------|-------------------|-------------------------------------------------------------------------|--------------|
    | extension | bytes             | Vote extension provided by the Application.                             | 1            |
    | height    | sfixed64          | Height in which the extension was provided.                             | 2            |
    | round     | sfixed64          | Round in which the extension was provided.                              | 3            |
    | chain_id  | string            | ID of the blockchain running consensus.                                 | 4            |
    | type      | VoteExtensionType | [VoteExtensionType](#voteextensiontype)                                 | 5            |

* **Usage**:
* Tenderdash is to sign the whole data structure and attach it to a Precommit message
* Upon reception, Tenderdash validates the sender's signature and sanity-checks the values of `height`, `round`, and `chain_id`.
  Then it sends `extension` to the Application via `RequestVerifyVoteExtension` for verification.
