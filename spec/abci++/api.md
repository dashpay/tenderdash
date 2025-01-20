# Protocol Documentation

<a name="top"></a>

## Table of Contents

1. [Table of Contents](#table-of-contents)
2. [tendermint/abci/types.proto](#tendermintabcitypesproto)
   1. [CommitInfo](#commitinfo)
   2. [Event](#event)
   3. [EventAttribute](#eventattribute)
   4. [ExecTxResult](#exectxresult)
   5. [ExtendVoteExtension](#extendvoteextension)
   6. [ExtendedVoteInfo](#extendedvoteinfo)
   7. [Misbehavior](#misbehavior)
   8. [QuorumHashUpdate](#quorumhashupdate)
   9. [Request](#request)
   10. [RequestApplySnapshotChunk](#requestapplysnapshotchunk)
   11. [RequestCheckTx](#requestchecktx)
   12. [RequestEcho](#requestecho)
   13. [RequestExtendVote](#requestextendvote)
       1. [Usage](#usage)
       2. [When does Tenderdash call it?](#when-does-tenderdash-call-it)
   14. [RequestFinalizeBlock](#requestfinalizeblock)
       1. [Usage](#usage-1)
   15. [RequestFlush](#requestflush)
   16. [RequestInfo](#requestinfo)
   17. [RequestInitChain](#requestinitchain)
   18. [RequestListSnapshots](#requestlistsnapshots)
   19. [RequestLoadSnapshotChunk](#requestloadsnapshotchunk)
   20. [RequestOfferSnapshot](#requestoffersnapshot)
   21. [RequestPrepareProposal](#requestprepareproposal)
       1. [Usage](#usage-2)
       2. [When does Tenderdash call it?](#when-does-tenderdash-call-it-1)
   22. [RequestProcessProposal](#requestprocessproposal)
       1. [Usage](#usage-3)
       2. [When does Tenderdash call it?](#when-does-tenderdash-call-it-2)
   23. [RequestQuery](#requestquery)
   24. [RequestVerifyVoteExtension](#requestverifyvoteextension)
       1. [Usage](#usage-4)
       2. [When does Tenderdash call it?](#when-does-tenderdash-call-it-3)
   25. [Response](#response)
   26. [ResponseApplySnapshotChunk](#responseapplysnapshotchunk)
   27. [ResponseCheckTx](#responsechecktx)
   28. [ResponseEcho](#responseecho)
   29. [ResponseException](#responseexception)
   30. [ResponseExtendVote](#responseextendvote)
   31. [ResponseFinalizeBlock](#responsefinalizeblock)
   32. [ResponseFlush](#responseflush)
   33. [ResponseInfo](#responseinfo)
   34. [ResponseInitChain](#responseinitchain)
   35. [ResponseListSnapshots](#responselistsnapshots)
   36. [ResponseLoadSnapshotChunk](#responseloadsnapshotchunk)
   37. [ResponseOfferSnapshot](#responseoffersnapshot)
   38. [ResponsePrepareProposal](#responseprepareproposal)
   39. [ResponseProcessProposal](#responseprocessproposal)
   40. [ResponseQuery](#responsequery)
   41. [ResponseVerifyVoteExtension](#responseverifyvoteextension)
   42. [Snapshot](#snapshot)
   43. [ThresholdPublicKeyUpdate](#thresholdpublickeyupdate)
   44. [TxRecord](#txrecord)
   45. [TxResult](#txresult)
   46. [Validator](#validator)
   47. [ValidatorSetUpdate](#validatorsetupdate)
   48. [ValidatorUpdate](#validatorupdate)
   49. [VoteInfo](#voteinfo)
   50. [CheckTxType](#checktxtype)
   51. [MisbehaviorType](#misbehaviortype)
   52. [ResponseApplySnapshotChunk.Result](#responseapplysnapshotchunkresult)
   53. [ResponseOfferSnapshot.Result](#responseoffersnapshotresult)
   54. [ResponseProcessProposal.ProposalStatus](#responseprocessproposalproposalstatus)
   55. [ResponseVerifyVoteExtension.VerifyStatus](#responseverifyvoteextensionverifystatus)
   56. [TxRecord.TxAction](#txrecordtxaction)
   57. [ABCIApplication](#abciapplication)
3. [Scalar Value Types](#scalar-value-types)



<a name="tendermint_abci_types-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## tendermint/abci/types.proto



<a name="tendermint-abci-CommitInfo"></a>

### CommitInfo



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| round | [int32](#int32) |  |  |
| quorum_hash | [bytes](#bytes) |  |  |
| block_signature | [bytes](#bytes) |  |  |
| threshold_vote_extensions | [tendermint.types.VoteExtension](#tendermint-types-VoteExtension) | repeated |  |






<a name="tendermint-abci-Event"></a>

### Event

Event allows application developers to attach additional information to
ResponseCheckTx, ResponsePrepareProposal, ResponseProcessProposal
and ResponseFinalizeBlock.

Later, transactions may be queried using these events.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| type | [string](#string) |  |  |
| attributes | [EventAttribute](#tendermint-abci-EventAttribute) | repeated |  |






<a name="tendermint-abci-EventAttribute"></a>

### EventAttribute

EventAttribute is a single key-value pair, associated with an event.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| key | [string](#string) |  |  |
| value | [string](#string) |  |  |
| index | [bool](#bool) |  | nondeterministic |






<a name="tendermint-abci-ExecTxResult"></a>

### ExecTxResult

ExecTxResult contains results of executing one individual transaction.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| code | [uint32](#uint32) |  | Response code within codespace; by convention, 0 means success. |
| data | [bytes](#bytes) |  | Result bytes, if any (arbitrary data, not interpreted by Tenderdash). |
| log | [string](#string) |  | The output of the application&#39;s logger. May be non-deterministic. |
| info | [string](#string) |  | Additional information. May be non-deterministic. |
| gas_used | [int64](#int64) |  | Amount of gas consumed by transaction. |
| events | [Event](#tendermint-abci-Event) | repeated | Type &amp; Key-Value events for indexing transactions (e.g. by account). |
| codespace | [string](#string) |  | Namespace for the code. |






<a name="tendermint-abci-ExtendVoteExtension"></a>

### ExtendVoteExtension

Provides a vote extension for signing. `type` and `extension` fields are mandatory for filling


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| type | [tendermint.types.VoteExtensionType](#tendermint-types-VoteExtensionType) |  | Vote extension type can be either DEFAULT, THRESHOLD_RECOVER or THRESHOLD_RECOVER_RAW. The Tenderdash supports only THRESHOLD_RECOVER and THRESHOLD_RECOVER_RAW at this moment. |
| extension | [bytes](#bytes) |  | Deterministic or (Non-Deterministic) extension provided by the sending validator&#39;s Application.

For THRESHOLD_RECOVER_RAW, it MUST be 32 bytes.

Sign request ID that will be used to sign the vote extensions. Only applicable for THRESHOLD_RECOVER_RAW vote extension type.

Tenderdash will use SHA256 checksum of `sign_request_id` when generating quorum signatures of THRESHOLD_RECOVER_RAW vote extensions. It MUST NOT be set for any other vote extension types. |
| sign_request_id | [bytes](#bytes) | optional | If not set, Tenderdash will generate it based on height and round.

If set, it SHOULD be unique per voting round, and it MUST start with `dpevote` or `\x06plwdtx` prefix.

Use with caution - it can have severe security consequences. |






<a name="tendermint-abci-ExtendedVoteInfo"></a>

### ExtendedVoteInfo

ExtendedVoteInfo


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| validator | [Validator](#tendermint-abci-Validator) |  | The validator that sent the vote. |
| signed_last_block | [bool](#bool) |  | Indicates whether the validator signed the last block, allowing for rewards based on validator availability. |
| vote_extension | [bytes](#bytes) |  | Non-deterministic extension provided by the sending validator&#39;s application. |






<a name="tendermint-abci-Misbehavior"></a>

### Misbehavior



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| type | [MisbehaviorType](#tendermint-abci-MisbehaviorType) |  |  |
| validator | [Validator](#tendermint-abci-Validator) |  | The offending validator |
| height | [int64](#int64) |  | The height when the offense occurred |
| time | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  | The corresponding time where the offense occurred |
| total_voting_power | [int64](#int64) |  | Total voting power of the validator set in case the ABCI application does not store historical validators. <https://github.com/tendermint/tendermint/issues/4581> |






<a name="tendermint-abci-QuorumHashUpdate"></a>

### QuorumHashUpdate



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| quorum_hash | [bytes](#bytes) |  |  |






<a name="tendermint-abci-Request"></a>

### Request

Request types


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| echo | [RequestEcho](#tendermint-abci-RequestEcho) |  |  |
| flush | [RequestFlush](#tendermint-abci-RequestFlush) |  |  |
| info | [RequestInfo](#tendermint-abci-RequestInfo) |  |  |
| init_chain | [RequestInitChain](#tendermint-abci-RequestInitChain) |  |  |
| query | [RequestQuery](#tendermint-abci-RequestQuery) |  |  |
| check_tx | [RequestCheckTx](#tendermint-abci-RequestCheckTx) |  |  |
| list_snapshots | [RequestListSnapshots](#tendermint-abci-RequestListSnapshots) |  |  |
| offer_snapshot | [RequestOfferSnapshot](#tendermint-abci-RequestOfferSnapshot) |  |  |
| load_snapshot_chunk | [RequestLoadSnapshotChunk](#tendermint-abci-RequestLoadSnapshotChunk) |  |  |
| apply_snapshot_chunk | [RequestApplySnapshotChunk](#tendermint-abci-RequestApplySnapshotChunk) |  |  |
| prepare_proposal | [RequestPrepareProposal](#tendermint-abci-RequestPrepareProposal) |  |  |
| process_proposal | [RequestProcessProposal](#tendermint-abci-RequestProcessProposal) |  |  |
| extend_vote | [RequestExtendVote](#tendermint-abci-RequestExtendVote) |  |  |
| verify_vote_extension | [RequestVerifyVoteExtension](#tendermint-abci-RequestVerifyVoteExtension) |  |  |
| finalize_block | [RequestFinalizeBlock](#tendermint-abci-RequestFinalizeBlock) |  |  |






<a name="tendermint-abci-RequestApplySnapshotChunk"></a>

### RequestApplySnapshotChunk

Applies a snapshot chunk.

- The application can choose to refetch chunks and/or ban P2P peers as appropriate.
  Tenderdash will not do this unless instructed by the application.
- The application may want to verify each chunk, e.g. by attaching chunk hashes in `Snapshot.Metadata`` and/or
  incrementally verifying contents against AppHash.
- When all chunks have been accepted, Tenderdash will make an ABCI Info call to verify that LastBlockAppHash
  and LastBlockHeight matches the expected values, and record the AppVersion in the node state.
  It then switches to fast sync or consensus and joins the network.
- If Tenderdash is unable to retrieve the next chunk after some time (e.g. because no suitable peers are available),
  it will reject the snapshot and try a different one via OfferSnapshot. The application should be prepared to reset
  and accept it or abort as appropriate.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| chunk_id | [bytes](#bytes) |  | The chunk index, starting from 0. Tenderdash applies chunks sequentially. |
| chunk | [bytes](#bytes) |  | The binary chunk contents, as returned by LoadSnapshotChunk. |
| sender | [string](#string) |  | The P2P ID of the node who sent this chunk. |






<a name="tendermint-abci-RequestCheckTx"></a>

### RequestCheckTx

Check if transaction is valid.

- Technically optional - not involved in processing blocks.
- Guardian of the mempool: every node runs CheckTx before letting a transaction into its local mempool.
- The transaction may come from an external user or another node
- CheckTx validates the transaction against the current state of the application, for example, checking
  signatures and account balances, but does not apply any of the state changes described in the transaction.
- Transactions where ResponseCheckTx.Code != 0 will be rejected - they will not be broadcast to other nodes
  or included in a proposal block.
- Tendermint attributes no other value to the response code


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| tx | [bytes](#bytes) |  | The request transaction bytes. |
| type | [CheckTxType](#tendermint-abci-CheckTxType) |  | Type or transaction check to execute. |






<a name="tendermint-abci-RequestEcho"></a>

### RequestEcho

Echo a string to test an abci client/server implementation


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| message | [string](#string) |  | A string to echo back |






<a name="tendermint-abci-RequestExtendVote"></a>

### RequestExtendVote

Extends a vote with application-side injection

#### Usage

- `ResponseExtendVote.vote_extensions` is optional information that, if present, will be signed by Tenderdash and
  attached to the Precommit message.
- `RequestExtendVote.hash` corresponds to the hash of a proposed block that was made available to the application
  in a previous call to `ProcessProposal` or `PrepareProposal` for the current height.
- `ResponseExtendVote.vote_extensions` will only be attached to a non-`nil` Precommit message. If Tenderdash is to
  precommit `nil`, it will not call `RequestExtendVote`.
- The Application logic that creates the extensions can be non-deterministic.

#### When does Tenderdash call it?

When a validator _p_ is in Tenderdash consensus state _prevote_ of round _r_, height _h_, in which _q_ is the proposer; and _p_ has received

- the Proposal message _v_ for round _r_, height _h_, along with all the block parts, from _q_,
- `Prevote` messages from _2f &#43; 1_ validators&#39; voting power for round _r_, height _h_, prevoting for the same block _id(v)_,

then _p_&#39;s Tenderdash locks _v_  and sends a Precommit message in the following way

1. _p_&#39;s Tenderdash sets _lockedValue_ and _validValue_ to _v_, and sets _lockedRound_ and _validRound_ to _r_
2. _p_&#39;s Tenderdash calls `RequestExtendVote` with _id(v)_ (`RequestExtendVote.hash`). The call is synchronous.
3. The Application optionally returns an array of bytes, `ResponseExtendVote.extension`, which is not interpreted by Tenderdash.
4. _p_&#39;s Tenderdash includes `ResponseExtendVote.extension` in a field of type [CanonicalVoteExtension](#canonicalvoteextension),
   it then populates the other fields in [CanonicalVoteExtension](#canonicalvoteextension), and signs the populated
   data structure.
5. _p_&#39;s Tenderdash constructs and signs the [CanonicalVote](../core/data_structures.md#canonicalvote) structure.
6. _p_&#39;s Tenderdash constructs the Precommit message (i.e. [Vote](../core/data_structures.md#vote) structure)
   using [CanonicalVoteExtension](#canonicalvoteextension) and [CanonicalVote](../core/data_structures.md#canonicalvote).
7. _p_&#39;s Tenderdash broadcasts the Precommit message.

In the cases when _p_&#39;s Tenderdash is to broadcast `precommit nil` messages (either _2f&#43;1_ `prevote nil` messages received,
or _timeoutPrevote_ triggered), _p_&#39;s Tenderdash does **not** call `RequestExtendVote` and will not include
a [CanonicalVoteExtension](#canonicalvoteextension) field in the `precommit nil` message.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| hash | [bytes](#bytes) |  | The header hash of the proposed block that the vote extensions is to refer to. |
| height | [int64](#int64) |  | Height of the proposed block (for sanity check). |
| round | [int32](#int32) |  | Round number for the block. |






<a name="tendermint-abci-RequestFinalizeBlock"></a>

### RequestFinalizeBlock

Finalize newly decided block.

#### Usage

- Contains the fields of the newly decided block.
- The height and timestamp values match the values from the header of the proposed block.
- The Application can use `RequestFinalizeBlock.decided_last_commit` and `RequestFinalizeBlock.byzantine_validators`
  to determine rewards and punishments for the validators.
- The application must execute the transactions in full, in the order they appear in `RequestFinalizeBlock.txs`,
  before returning control to Tenderdash. Alternatively, it can commit the candidate state corresponding to the same block
  previously executed via `PrepareProposal` or `ProcessProposal`.
- If ProcessProposal for the same arguments have succeeded, FinalizeBlock MUST always succeed.
- Application is expected to persist its state at the end of this call, before returning `ResponseFinalizeBlock`.
- Later calls to `Query` can return proofs about the application state anchored
  in this Merkle root hash.
- Use `ResponseFinalizeBlock.retain_height` with caution! If all nodes in the network remove historical
  blocks then this data is permanently lost, and no new nodes will be able to join the network and
  bootstrap. Historical blocks may also be required for other purposes, e.g. auditing, replay of
  non-persisted heights, light client verification, and so on.
- Just as `ProcessProposal`, the implementation of `FinalizeBlock` MUST be deterministic, since it is
  making the Application&#39;s state evolve in the context of state machine replication.
- Currently, Tenderdash will fill up all fields in `RequestFinalizeBlock`, even if they were
  already passed on to the Application via `RequestPrepareProposal` or `RequestProcessProposal`.
  If the Application is in same-block execution mode, it applies the right candidate state here
  (rather than executing the whole block). In this case the Application disregards all parameters in
  `RequestFinalizeBlock` except `RequestFinalizeBlock.hash`.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| commit | [CommitInfo](#tendermint-abci-CommitInfo) |  | Info about the current commit |
| misbehavior | [Misbehavior](#tendermint-abci-Misbehavior) | repeated | List of information about validators that acted incorrectly. |
| hash | [bytes](#bytes) |  | The block header&#39;s hash. Present for convenience (can be derived from the block header). |
| height | [int64](#int64) |  | The height of the finalized block. |
| round | [int32](#int32) |  | Round number for the block |
| block | [tendermint.types.Block](#tendermint-types-Block) |  | The block that was finalized |
| block_id | [tendermint.types.BlockID](#tendermint-types-BlockID) |  | The block ID that was finalized |






<a name="tendermint-abci-RequestFlush"></a>

### RequestFlush

Signals that messages queued on the client should be flushed to the server.
It is called periodically by the client implementation to ensure asynchronous
requests are actually sent, and is called immediately to make a synchronous request,
which returns when the Flush response comes back.






<a name="tendermint-abci-RequestInfo"></a>

### RequestInfo

Return information about the application state.

Used to sync Tenderdash with the application during a handshake that happens on startup.
The returned app_version will be included in the Header of every block.
Tenderdash expects last_block_app_hash and last_block_height to be updated during Commit,
ensuring that Commit is never called twice for the same block height.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| version | [string](#string) |  | The Tenderdash software semantic version. |
| block_version | [uint64](#uint64) |  | The Tenderdash Block Protocol version. |
| p2p_version | [uint64](#uint64) |  | The Tenderdash P2P Protocol version. |
| abci_version | [string](#string) |  | The Tenderdash ABCI semantic version. |






<a name="tendermint-abci-RequestInitChain"></a>

### RequestInitChain

Called once upon genesis.

- If ResponseInitChain.Validators is empty, the initial validator set will be the RequestInitChain.Validators
- If ResponseInitChain.Validators is not empty, it will be the initial validator set (regardless of what is in
  RequestInitChain.Validators).
- This allows the app to decide if it wants to accept the initial validator set proposed by Tenderdash
  (ie. in the genesis file), or if it wants to use a different one (perhaps computed based on some application
  specific information in the genesis file).


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| time | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  | Genesis time |
| chain_id | [string](#string) |  | ID of the blockchain. |
| consensus_params | [tendermint.types.ConsensusParams](#tendermint-types-ConsensusParams) |  | Initial consensus-critical parameters. |
| validator_set | [ValidatorSetUpdate](#tendermint-abci-ValidatorSetUpdate) |  | Initial genesis validators, sorted by voting power. |
| app_state_bytes | [bytes](#bytes) |  | Serialized initial application state. JSON bytes. |
| initial_height | [int64](#int64) |  | Height of the initial block (typically `1`). |
| initial_core_height | [uint32](#uint32) |  | Initial core chain lock height. |






<a name="tendermint-abci-RequestListSnapshots"></a>

### RequestListSnapshots

Lists available snapshots

- Used during state sync to discover available snapshots on peers.
- See Snapshot data type for details.






<a name="tendermint-abci-RequestLoadSnapshotChunk"></a>

### RequestLoadSnapshotChunk

Used during state sync to retrieve snapshot chunks from peers.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| height | [uint64](#uint64) |  | The height of the snapshot the chunks belongs to. |
| version | [uint32](#uint32) |  | The application-specific format of the snapshot the chunk belongs to. |
| chunk_id | [bytes](#bytes) |  | The chunk id is a hash of the node of subtree of the snapshot. |






<a name="tendermint-abci-RequestOfferSnapshot"></a>

### RequestOfferSnapshot

Offers a snapshot to the application.

- OfferSnapshot is called when bootstrapping a node using state sync. The application may accept or reject snapshots
  as appropriate. Upon accepting, Tenderdash will retrieve and apply snapshot chunks via ApplySnapshotChunk.
  The application may also choose to reject a snapshot in the chunk response, in which case it should be prepared
  to accept further OfferSnapshot calls.
- Only AppHash can be trusted, as it has been verified by the light client. Any other data can be spoofed
  by adversaries, so applications should employ additional verification schemes to avoid denial-of-service attacks.
  The verified AppHash is automatically checked against the restored application at the end of snapshot restoration.
- For more information, see the Snapshot data type or the state sync section.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| snapshot | [Snapshot](#tendermint-abci-Snapshot) |  | The snapshot offered for restoration. |
| app_hash | [bytes](#bytes) |  | The light client-verified app hash for this height, from the blockchain. 32 bytes. |






<a name="tendermint-abci-RequestPrepareProposal"></a>

### RequestPrepareProposal

Prepare new block proposal, potentially altering list of transactions.

#### Usage

- The first six parameters of `RequestPrepareProposal` are the same as `RequestProcessProposal`
  and `RequestFinalizeBlock`.
- The height and time values match the values from the header of the proposed block.
- `RequestPrepareProposal` contains a preliminary set of transactions `txs` that Tenderdash considers to be a good block proposal, called _raw proposal_. The Application can modify this set via `ResponsePrepareProposal.tx_records` (see [TxRecord](#txrecord)).
    - The Application _can_ reorder, remove or add transactions to the raw proposal. Let `tx` be a transaction in `txs`:
        - If the Application considers that `tx` should not be proposed in this block, e.g., there are other transactions with higher priority, then it should not include it in `tx_records`. In this case, Tenderdash won&#39;t remove `tx` from the mempool. The Application should be extra-careful, as abusing this feature may cause transactions to stay forever in the mempool.
        - If the Application considers that a `tx` should not be included in the proposal and removed from the mempool, then the Application should include it in `tx_records` and _mark_ it as `REMOVED`. In this case, Tenderdash will remove `tx` from the mempool.
        - If the Application wants to add a new transaction, then the Application should include it in `tx_records` and _mark_ it as `ADD`. In this case, Tenderdash will add it to the mempool.
    - The Application should be aware that removing and adding transactions may compromise _traceability_.
      &gt; Consider the following example: the Application transforms a client-submitted transaction `t1` into a second transaction `t2`, i.e., the Application asks Tenderdash to remove `t1` and add `t2` to the mempool. If a client wants to eventually check what happened to `t1`, it will discover that `t_1` is not in the mempool or in a committed block, getting the wrong idea that `t_1` did not make it into a block. Note that `t_2` _will be_ in a committed block, but unless the Application tracks this information, no component will be aware of it. Thus, if the Application wants traceability, it is its responsability to support it. For instance, the Application could attach to a transformed transaction a list with the hashes of the transactions it derives from.
- Tenderdash MAY include a list of transactions in `RequestPrepareProposal.txs` whose total size in bytes exceeds `RequestPrepareProposal.max_tx_bytes`.
  Therefore, if the size of `RequestPrepareProposal.txs` is greater than `RequestPrepareProposal.max_tx_bytes`, the Application MUST make sure that the
  `RequestPrepareProposal.max_tx_bytes` limit is respected by those transaction records returned in `ResponsePrepareProposal.tx_records` that are marked as `UNMODIFIED` or `ADDED`.
- In same-block execution mode, the Application must provide values for `ResponsePrepareProposal.app_hash`,
  `ResponsePrepareProposal.tx_results`, `ResponsePrepareProposal.validator_updates`, `ResponsePrepareProposal.core_chain_lock_update` and
  `ResponsePrepareProposal.consensus_param_updates`, as a result of fully executing the block.
    - The values for `ResponsePrepareProposal.validator_updates`, `ResponsePrepareProposal.core_chain_lock_update` or
      `ResponsePrepareProposal.consensus_param_updates` may be empty. In this case, Tenderdash will keep
      the current values.
    - `ResponsePrepareProposal.validator_updates`, triggered by block `H`, affect validation
      for blocks `H&#43;1`, and `H&#43;2`. Heights following a validator update are affected in the following way:
        - `H`: `NextValidatorsHash` includes the new `validator_updates` value.
        - `H&#43;1`: The validator set change takes effect and `ValidatorsHash` is updated.
        - `H&#43;2`: `local_last_commit` now includes the altered validator set.
    - `ResponseFinalizeBlock.consensus_param_updates` returned for block `H` apply to the consensus
      params for block `H&#43;1` even if the change is agreed in block `H`.
      For more information on the consensus parameters,
      see the [application spec entry on consensus parameters](../abci/apps.md#consensus-parameters).
    - It is the responsibility of the Application to set the right value for _TimeoutPropose_ so that
      the (synchronous) execution of the block does not cause other processes to prevote `nil` because
      their propose timeout goes off.
- As a result of executing the prepared proposal, the Application may produce header events or transaction events.
  The Application must keep those events until a block is decided and then pass them on to Tenderdash via
  `ResponsePrepareProposal`.
- As a sanity check, Tenderdash will check the returned parameters for validity if the Application modified them.
  In particular, `ResponsePrepareProposal.tx_records` will be deemed invalid if
    - There is a duplicate transaction in the list.
    - A new or modified transaction is marked as `UNMODIFIED` or `REMOVED`.
    - An unmodified transaction is marked as `ADDED`.
    - A transaction is marked as `UNKNOWN`.
- `ResponsePrepareProposal.tx_results` contains only results of  `UNMODIFIED` and `ADDED` transactions.
`REMOVED` transactions are omitted. The length of `tx_results` can be different than the length of `tx_records`.
- If Tenderdash fails to validate the `ResponsePrepareProposal`, Tenderdash will assume the application is faulty and crash.
    - The implementation of `PrepareProposal` can be non-deterministic.

#### When does Tenderdash call it?

When a validator _p_ enters Tenderdash consensus round _r_, height _h_, in which _p_ is the proposer,
and _p_&#39;s _validValue_ is `nil`:

1. _p_&#39;s Tenderdash collects outstanding transactions from the mempool
    - The transactions will be collected in order of priority
    - Let $C$ the list of currently collected transactions
    - The collection stops when any of the following conditions are met
        - the mempool is empty
        - the total size of transactions $\in C$ is greater than or equal to `consensusParams.block.max_bytes`
        - the sum of `GasWanted` field of transactions $\in C$ is greater than or equal to
          `consensusParams.block.max_gas`
    - _p_&#39;s Tenderdash creates a block header.
2. _p_&#39;s Tenderdash calls `RequestPrepareProposal` with the newly generated block.
   The call is synchronous: Tenderdash&#39;s execution will block until the Application returns from the call.
3. The Application checks the block (hashes, transactions, commit info, misbehavior). Besides,
    - in same-block execution mode, the Application can (and should) provide `ResponsePrepareProposal.app_hash`,
      `ResponsePrepareProposal.validator_updates`, or
      `ResponsePrepareProposal.consensus_param_updates`.
    - the Application can manipulate transactions
        - leave transactions untouched - `TxAction = UNMODIFIED`
        - add new transactions directly to the proposal - `TxAction = ADDED`
        - remove transactions (invalid) from the proposal and from the mempool - `TxAction = REMOVED`
        - remove transactions from the proposal but not from the mempool (effectively _delaying_ them) - the
          Application removes the transaction from the list
        - modify transactions (e.g. aggregate them) - `TxAction = ADDED` followed by `TxAction = REMOVED`. As explained above, this compromises client traceability, unless it is implemented at the Application level.
        - reorder transactions - the Application reorders transactions in the list
4. If the block is modified, the Application includes the modified block in the return parameters (see the rules in section _Usage_).
   The Application returns from the call.
5. _p_&#39;s Tenderdash uses the (possibly) modified block as _p_&#39;s proposal in round _r_, height _h_.

Note that, if _p_ has a non-`nil` _validValue_, Tenderdash will use it as proposal and will not call `RequestPrepareProposal`.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| max_tx_bytes | [int64](#int64) |  | Currently configured maximum size in bytes taken by the modified transactions. The modified transactions cannot exceed this size. |
| txs | [bytes](#bytes) | repeated | Preliminary list of transactions that have been picked as part of the block to propose. Sent to the app for possible modifications. |
| local_last_commit | [CommitInfo](#tendermint-abci-CommitInfo) |  | Info about the last commit, obtained locally from Tenderdash&#39;s data structures. |
| misbehavior | [Misbehavior](#tendermint-abci-Misbehavior) | repeated | List of information about validators that acted incorrectly. |
| height | [int64](#int64) |  | The height of the block that will be proposed. |
| time | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  | Timestamp of the block that that will be proposed. |
| next_validators_hash | [bytes](#bytes) |  | Merkle root of the next validator set. |
| round | [int32](#int32) |  | Round number for the block. |
| core_chain_locked_height | [uint32](#uint32) |  | Core chain lock height to be used when signing this block. |
| proposer_pro_tx_hash | [bytes](#bytes) |  | ProTxHash of the original proposer of the block. |
| proposed_app_version | [uint64](#uint64) |  | Proposer&#39;s latest available app protocol version. |
| version | [tendermint.version.Consensus](#tendermint-version-Consensus) |  | App and block version used to generate the block. App version included in the block can be modified by setting ResponsePrepareProposal.app_version. |
| quorum_hash | [bytes](#bytes) |  | quorum_hash contains hash of validator quorum that will sign the block |






<a name="tendermint-abci-RequestProcessProposal"></a>

### RequestProcessProposal

Process prepared proposal.

#### Usage

- Contains fields from the proposed block.
    - The Application may fully execute the block as though it was handling `RequestFinalizeBlock`.
      However, any resulting state changes must be kept as _candidate state_,
      and the Application should be ready to backtrack/discard it in case the decided block is different.
- The height and timestamp values match the values from the header of the proposed block.
- If `ResponseProcessProposal.status` is `REJECT`, Tenderdash assumes the proposal received
  is not valid.
- In same-block execution mode, the Application is required to fully execute the block and provide values
  for parameters `ResponseProcessProposal.app_hash`, `ResponseProcessProposal.tx_results`,
  `ResponseProcessProposal.validator_updates`, and `ResponseProcessProposal.consensus_param_updates`,
  so that Tenderdash can then verify the hashes in the block&#39;s header are correct.
  If the hashes mismatch, Tenderdash will reject the block even if `ResponseProcessProposal.status`
  was set to `ACCEPT`.
- The implementation of `ProcessProposal` MUST be deterministic. Moreover, the value of
  `ResponseProcessProposal.status` MUST **exclusively** depend on the parameters passed in
  the call to `RequestProcessProposal`, and the last committed Application state
  (see [Requirements](abci&#43;&#43;_app_requirements.md) section).
- Moreover, application implementors SHOULD always set `ResponseProcessProposal.status` to `ACCEPT`,
  unless they _really_ know what the potential liveness implications of returning `REJECT` are.

#### When does Tenderdash call it?

When a validator _p_ enters Tenderdash consensus round _r_, height _h_, in which _q_ is the proposer (possibly _p_ = _q_):

1. _p_ sets up timer `ProposeTimeout`.
2. If _p_ is the proposer, _p_ executes steps 1-6 in [PrepareProposal](#prepareproposal).
3. Upon reception of Proposal message (which contains the header) for round _r_, height _h_ from _q_, _p_&#39;s Tenderdash verifies the block header.
4. Upon reception of Proposal message, along with all the block parts, for round _r_, height _h_ from _q_, _p_&#39;s Tenderdash follows its algorithm
   to check whether it should prevote for the block just received, or `nil`
5. If Tenderdash should prevote for the block just received
    1. Tenderdash calls `RequestProcessProposal` with the block. The call is synchronous.
    2. The Application checks/processes the proposed block, which is read-only, and returns true (_accept_) or false (_reject_) in `ResponseProcessProposal.accept`.
       - The Application, depending on its needs, may call `ResponseProcessProposal`
         - either after it has completely processed the block (the simpler case),
         - or immediately (after doing some basic checks), and process the block asynchronously. In this case the Application will
           not be able to reject the block, or force prevote/precommit `nil` afterwards.
    3. If the returned value is
         - _accept_, Tenderdash prevotes on this proposal for round _r_, height _h_.
         - _reject_, Tenderdash prevotes `nil`.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| txs | [bytes](#bytes) | repeated | List of transactions that have been picked as part of the proposed |
| proposed_last_commit | [CommitInfo](#tendermint-abci-CommitInfo) |  | Info about the last commit, obtained from the information in the proposed block. |
| misbehavior | [Misbehavior](#tendermint-abci-Misbehavior) | repeated | List of information about validators that acted incorrectly. |
| hash | [bytes](#bytes) |  | The block header&#39;s hash of the proposed block. It is computed as a Merkle tree from the header fields. |
| height | [int64](#int64) |  | The height of the proposed block. |
| round | [int32](#int32) |  | Round number for the block |
| time | [google.protobuf.Timestamp](#google-protobuf-Timestamp) |  | Timestamp included in the proposed block. |
| next_validators_hash | [bytes](#bytes) |  | Merkle root of the next validator set. |
| core_chain_locked_height | [uint32](#uint32) |  | Core chain lock height to be used when signing this block. |
| core_chain_lock_update | [tendermint.types.CoreChainLock](#tendermint-types-CoreChainLock) |  | Next core-chain-lock-update for validation in ABCI. |
| proposer_pro_tx_hash | [bytes](#bytes) |  | ProTxHash of the original proposer of the block. |
| proposed_app_version | [uint64](#uint64) |  | Proposer&#39;s latest available app protocol version. |
| version | [tendermint.version.Consensus](#tendermint-version-Consensus) |  | App and block version used to generate the block. App version MUST be verified by the app. |
| quorum_hash | [bytes](#bytes) |  | quorum_hash contains hash of validator quorum that will sign the block |






<a name="tendermint-abci-RequestQuery"></a>

### RequestQuery

Query for data from the application at current or past height.

- Optionally return Merkle proof.
- Merkle proof includes self-describing type field to support many types of Merkle trees and encoding formats.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| data | [bytes](#bytes) |  | Raw query bytes. Can be used with or in lieu of Path. |
| path | [string](#string) |  | Path field of the request URI. Can be used with or in lieu of data. Apps MUST interpret /store as a query by key on the underlying store. The key SHOULD be specified in the data field. Apps SHOULD allow queries over specific types like /accounts/... or /votes/... |
| height | [int64](#int64) |  | The block height for which you want the query (default=0 returns data for the latest committed block). Note that this is the height of the block containing the application&#39;s Merkle root hash, which represents the state as it was after committing the block at Height-1. |
| prove | [bool](#bool) |  | Return Merkle proof with response if possible. |






<a name="tendermint-abci-RequestVerifyVoteExtension"></a>

### RequestVerifyVoteExtension

Verify the vote extension

#### Usage

- `RequestVerifyVoteExtension.vote_extension` can be an empty byte array. The Application&#39;s interpretation of it should be
  that the Application running at the process that sent the vote chose not to extend it.
  Tenderdash will always call `RequestVerifyVoteExtension`, even for 0 length vote extensions.
- If `ResponseVerifyVoteExtension.status` is `REJECT`, Tenderdash will reject the whole received vote.
  See the [Requirements](abci&#43;&#43;_app_requirements.md) section to understand the potential
  liveness implications of this.
- The implementation of `VerifyVoteExtension` MUST be deterministic. Moreover, the value of
  `ResponseVerifyVoteExtension.status` MUST **exclusively** depend on the parameters passed in
  the call to `RequestVerifyVoteExtension`, and the last committed Application state
  (see [Requirements](abci&#43;&#43;_app_requirements.md) section).
- Moreover, application implementers SHOULD always set `ResponseVerifyVoteExtension.status` to `ACCEPT`,
  unless they _really_ know what the potential liveness implications of returning `REJECT` are.

#### When does Tenderdash call it?

When a validator _p_ is in Tenderdash consensus round _r_, height _h_, state _prevote_ (**TODO** discuss: I think I must remove the state
from this condition, but not sure), and _p_ receives a Precommit message for round _r_, height _h_ from _q_:

1. If the Precommit message does not contain a vote extensions with a valid signature, Tenderdash discards the message as invalid.

- a 0-length vote extensions is valid as long as its accompanying signature is also valid.

2. Else, _p_&#39;s Tenderdash calls `RequestVerifyVoteExtension`.
3. The Application returns _accept_ or _reject_ via `ResponseVerifyVoteExtension.status`.
4. If the Application returns

- _accept_, _p_&#39;s Tenderdash will keep the received vote, together with its corresponding
    vote extension in its internal data structures. It will be used to populate the [ExtendedCommitInfo](#extendedcommitinfo)
    structure in calls to `RequestPrepareProposal`, in rounds of height _h &#43; 1_ where _p_ is the proposer.
- _reject_, _p_&#39;s Tenderdash will deem the Precommit message invalid and discard it.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| hash | [bytes](#bytes) |  | The header hash of the propsed block that the vote extensions refers to. |
| validator_pro_tx_hash | [bytes](#bytes) |  | ProTxHash of the validator that signed the extensions. |
| height | [int64](#int64) |  | Height of the block (for sanity check). |
| round | [int32](#int32) |  | Round number for the block. |
| vote_extensions | [ExtendVoteExtension](#tendermint-abci-ExtendVoteExtension) | repeated | Application-specific information signed by Tenderdash. Can have 0 length. |






<a name="tendermint-abci-Response"></a>

### Response



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| exception | [ResponseException](#tendermint-abci-ResponseException) |  |  |
| echo | [ResponseEcho](#tendermint-abci-ResponseEcho) |  |  |
| flush | [ResponseFlush](#tendermint-abci-ResponseFlush) |  |  |
| info | [ResponseInfo](#tendermint-abci-ResponseInfo) |  |  |
| init_chain | [ResponseInitChain](#tendermint-abci-ResponseInitChain) |  |  |
| query | [ResponseQuery](#tendermint-abci-ResponseQuery) |  |  |
| check_tx | [ResponseCheckTx](#tendermint-abci-ResponseCheckTx) |  |  |
| list_snapshots | [ResponseListSnapshots](#tendermint-abci-ResponseListSnapshots) |  |  |
| offer_snapshot | [ResponseOfferSnapshot](#tendermint-abci-ResponseOfferSnapshot) |  |  |
| load_snapshot_chunk | [ResponseLoadSnapshotChunk](#tendermint-abci-ResponseLoadSnapshotChunk) |  |  |
| apply_snapshot_chunk | [ResponseApplySnapshotChunk](#tendermint-abci-ResponseApplySnapshotChunk) |  |  |
| prepare_proposal | [ResponsePrepareProposal](#tendermint-abci-ResponsePrepareProposal) |  |  |
| process_proposal | [ResponseProcessProposal](#tendermint-abci-ResponseProcessProposal) |  |  |
| extend_vote | [ResponseExtendVote](#tendermint-abci-ResponseExtendVote) |  |  |
| verify_vote_extension | [ResponseVerifyVoteExtension](#tendermint-abci-ResponseVerifyVoteExtension) |  |  |
| finalize_block | [ResponseFinalizeBlock](#tendermint-abci-ResponseFinalizeBlock) |  |  |






<a name="tendermint-abci-ResponseApplySnapshotChunk"></a>

### ResponseApplySnapshotChunk



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| result | [ResponseApplySnapshotChunk.Result](#tendermint-abci-ResponseApplySnapshotChunk-Result) |  | The result of applying this chunk. |
| refetch_chunks | [bytes](#bytes) | repeated | Refetch and reapply the given chunks, regardless of `result`. Only the listed chunks will be refetched, and reapplied in sequential order. |
| reject_senders | [string](#string) | repeated | Reject the given P2P senders, regardless of `Result`. Any chunks already applied will not be refetched unless explicitly requested, but queued chunks from these senders will be discarded, and new chunks or other snapshots rejected. |
| next_chunks | [bytes](#bytes) | repeated | Next chunks provides the list of chunks that should be requested next, if any. |






<a name="tendermint-abci-ResponseCheckTx"></a>

### ResponseCheckTx



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| code | [uint32](#uint32) |  | Response code. |
| data | [bytes](#bytes) |  | Result bytes, if any. |
| info | [string](#string) |  | Additional information. **May be non-deterministic.** |
| gas_wanted | [int64](#int64) |  | Amount of gas requested for transaction. |
| codespace | [string](#string) |  | Namespace for the `code`. |
| sender | [string](#string) |  | The transaction&#39;s sender (e.g. the signer). |
| priority | [int64](#int64) |  | The transaction&#39;s priority (for mempool ordering). |






<a name="tendermint-abci-ResponseEcho"></a>

### ResponseEcho



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| message | [string](#string) |  | The input string. |






<a name="tendermint-abci-ResponseException"></a>

### ResponseException

nondeterministic


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| error | [string](#string) |  |  |






<a name="tendermint-abci-ResponseExtendVote"></a>

### ResponseExtendVote



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| vote_extensions | [ExtendVoteExtension](#tendermint-abci-ExtendVoteExtension) | repeated | Optional information signed by Tenderdash. |






<a name="tendermint-abci-ResponseFinalizeBlock"></a>

### ResponseFinalizeBlock



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| retain_height | [int64](#int64) |  | Blocks below this height may be removed. Defaults to `0` (retain all). |






<a name="tendermint-abci-ResponseFlush"></a>

### ResponseFlush







<a name="tendermint-abci-ResponseInfo"></a>

### ResponseInfo



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| data | [string](#string) |  | Some arbitrary information. |
| version | [string](#string) |  | The application software semantic version. |
| app_version | [uint64](#uint64) |  | The application protocol version. |
| last_block_height | [int64](#int64) |  | Latest block for which the app has called Commit. |
| last_block_app_hash | [bytes](#bytes) |  | Latest result of Commit. 32 bytes. |






<a name="tendermint-abci-ResponseInitChain"></a>

### ResponseInitChain



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| consensus_params | [tendermint.types.ConsensusParams](#tendermint-types-ConsensusParams) |  | Initial consensus-critical parameters (optional). |
| app_hash | [bytes](#bytes) |  | Initial application hash. 32 bytes. |
| validator_set_update | [ValidatorSetUpdate](#tendermint-abci-ValidatorSetUpdate) |  | Initial validator set (optional). |
| next_core_chain_lock_update | [tendermint.types.CoreChainLock](#tendermint-types-CoreChainLock) |  | Initial core chain lock update. |
| initial_core_height | [uint32](#uint32) |  | Initial height of core lock. |
| genesis_time | [google.protobuf.Timestamp](#google-protobuf-Timestamp) | optional | Override genesis time with provided time. |






<a name="tendermint-abci-ResponseListSnapshots"></a>

### ResponseListSnapshots



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| snapshots | [Snapshot](#tendermint-abci-Snapshot) | repeated | List of local state snapshots. |






<a name="tendermint-abci-ResponseLoadSnapshotChunk"></a>

### ResponseLoadSnapshotChunk



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| chunk | [bytes](#bytes) |  | The binary chunk contents, in an arbitray format. Chunk messages cannot be larger than 16 MB _including metadata_, so 10 MB is a good starting point. |






<a name="tendermint-abci-ResponseOfferSnapshot"></a>

### ResponseOfferSnapshot



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| result | [ResponseOfferSnapshot.Result](#tendermint-abci-ResponseOfferSnapshot-Result) |  | The result of the snapshot offer. |






<a name="tendermint-abci-ResponsePrepareProposal"></a>

### ResponsePrepareProposal



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| tx_records | [TxRecord](#tendermint-abci-TxRecord) | repeated | Possibly modified list of transactions that have been picked as part of the proposed block. |
| app_hash | [bytes](#bytes) |  | The Merkle root hash of the application state. 32 bytes. |
| tx_results | [ExecTxResult](#tendermint-abci-ExecTxResult) | repeated | List of structures containing the data resulting from executing the transactions. |
| consensus_param_updates | [tendermint.types.ConsensusParams](#tendermint-types-ConsensusParams) |  | Changes to consensus-critical gas, size, and other parameters that will be applied at next height. |
| core_chain_lock_update | [tendermint.types.CoreChainLock](#tendermint-types-CoreChainLock) |  | Core chain lock that will be used for next block. |
| validator_set_update | [ValidatorSetUpdate](#tendermint-abci-ValidatorSetUpdate) |  | Changes to validator set that will be applied at next height. |
| app_version | [uint64](#uint64) |  | Application version that was used to create the current proposal. |






<a name="tendermint-abci-ResponseProcessProposal"></a>

### ResponseProcessProposal



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| status | [ResponseProcessProposal.ProposalStatus](#tendermint-abci-ResponseProcessProposal-ProposalStatus) |  | `enum` that signals if the application finds the proposal valid. |
| app_hash | [bytes](#bytes) |  | The Merkle root hash of the application state. 32 bytes. |
| tx_results | [ExecTxResult](#tendermint-abci-ExecTxResult) | repeated | List of structures containing the data resulting from executing the transactions. |
| consensus_param_updates | [tendermint.types.ConsensusParams](#tendermint-types-ConsensusParams) |  | Changes to consensus-critical gas, size, and other parameters. |
| validator_set_update | [ValidatorSetUpdate](#tendermint-abci-ValidatorSetUpdate) |  | Changes to validator set (set voting power to 0 to remove). |
| events | [Event](#tendermint-abci-Event) | repeated | Type &amp; Key-Value events for indexing |






<a name="tendermint-abci-ResponseQuery"></a>

### ResponseQuery



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| code | [uint32](#uint32) |  | Response code. |
| log | [string](#string) |  | The output of the application&#39;s logger. **May be non-deterministic.** |
| info | [string](#string) |  | Additional information. **May be non-deterministic.** |
| index | [int64](#int64) |  | The index of the key in the tree. |
| key | [bytes](#bytes) |  | The key of the matching data. |
| value | [bytes](#bytes) |  | The value of the matching data. |
| proof_ops | [tendermint.crypto.ProofOps](#tendermint-crypto-ProofOps) |  | Serialized proof for the value data, if requested, to be verified against the `app_hash` for the given Height. |
| height | [int64](#int64) |  | The block height from which data was derived. Note that this is the height of the block containing the application&#39;s Merkle root hash, which represents the state as it was after committing the block at Height-1. |
| codespace | [string](#string) |  | Namespace for the `code`. |






<a name="tendermint-abci-ResponseVerifyVoteExtension"></a>

### ResponseVerifyVoteExtension



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| status | [ResponseVerifyVoteExtension.VerifyStatus](#tendermint-abci-ResponseVerifyVoteExtension-VerifyStatus) |  | `enum` signaling if the application accepts the vote extension |






<a name="tendermint-abci-Snapshot"></a>

### Snapshot



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| height | [uint64](#uint64) |  | The height at which the snapshot was taken |
| version | [uint32](#uint32) |  | The application-specific snapshot version |
| hash | [bytes](#bytes) |  | Arbitrary snapshot hash, equal only if identical |
| metadata | [bytes](#bytes) |  | Arbitrary application metadata |






<a name="tendermint-abci-ThresholdPublicKeyUpdate"></a>

### ThresholdPublicKeyUpdate



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| threshold_public_key | [tendermint.crypto.PublicKey](#tendermint-crypto-PublicKey) |  |  |






<a name="tendermint-abci-TxRecord"></a>

### TxRecord



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| action | [TxRecord.TxAction](#tendermint-abci-TxRecord-TxAction) |  | What should Tenderdash do with this transaction? |
| tx | [bytes](#bytes) |  | Transaction contents. |






<a name="tendermint-abci-TxResult"></a>

### TxResult

TxResult contains results of executing the transaction.

One usage is indexing transaction results.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| height | [int64](#int64) |  |  |
| index | [uint32](#uint32) |  |  |
| tx | [bytes](#bytes) |  |  |
| result | [ExecTxResult](#tendermint-abci-ExecTxResult) |  |  |






<a name="tendermint-abci-Validator"></a>

### Validator

Validator


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| power | [int64](#int64) |  | The voting power |
| pro_tx_hash | [bytes](#bytes) |  |  |






<a name="tendermint-abci-ValidatorSetUpdate"></a>

### ValidatorSetUpdate

ValidatorSetUpdate represents a change in the validator set.
It can be used to add, remove, or update a validator.

Validator set update consists of multiple ValidatorUpdate records,
each of them can be used to add, remove, or update a validator, according to the
following rules:

1. If a validator with the same public key already exists in the validator set
and power is greater than 0, the existing validator will be updated with the new power.
2. If a validator with the same public key already exists in the validator set
and power is 0, the existing validator will be removed from the validator set.
3. If a validator with the same public key does not exist in the validator set and the power is greater than 0,
a new validator will be added to the validator set.
4. As a special case, if quorum hash has changed, all existing validators will be removed before applying
the new validator set update.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| validator_updates | [ValidatorUpdate](#tendermint-abci-ValidatorUpdate) | repeated |  |
| threshold_public_key | [tendermint.crypto.PublicKey](#tendermint-crypto-PublicKey) |  |  |
| quorum_hash | [bytes](#bytes) |  |  |






<a name="tendermint-abci-ValidatorUpdate"></a>

### ValidatorUpdate

ValidatorUpdate


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| pub_key | [tendermint.crypto.PublicKey](#tendermint-crypto-PublicKey) |  |  |
| power | [int64](#int64) |  |  |
| pro_tx_hash | [bytes](#bytes) |  |  |
| node_address | [string](#string) |  | node_address is an URI containing address of validator (`proto://node_id@ip_address:port`), for example: `tcp://f2dbd9b0a1f541a7c44d34a58674d0262f5feca5@12.34.5.6:1234` |






<a name="tendermint-abci-VoteInfo"></a>

### VoteInfo

VoteInfo


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| validator | [Validator](#tendermint-abci-Validator) |  |  |
| signed_last_block | [bool](#bool) |  |  |








<a name="tendermint-abci-CheckTxType"></a>

### CheckTxType

Type of transaction check

| Name | Number | Description |
| ---- | ------ | ----------- |
| NEW | 0 | NEW is the default and means that a full check of the tranasaction is required. |
| RECHECK | 1 | RECHECK is used when the mempool is initiating a normal recheck of a transaction. |



<a name="tendermint-abci-MisbehaviorType"></a>

### MisbehaviorType


| Name | Number | Description |
| ---- | ------ | ----------- |
| UNKNOWN | 0 |  |
| DUPLICATE_VOTE | 1 |  |
| LIGHT_CLIENT_ATTACK | 2 |  |



<a name="tendermint-abci-ResponseApplySnapshotChunk-Result"></a>

### ResponseApplySnapshotChunk.Result


| Name | Number | Description |
| ---- | ------ | ----------- |
| UNKNOWN | 0 | Unknown result, abort all snapshot restoration |
| ACCEPT | 1 | Chunk successfully accepted |
| ABORT | 2 | Abort all snapshot restoration |
| RETRY | 3 | Retry chunk (combine with refetch and reject) |
| RETRY_SNAPSHOT | 4 | Retry snapshot (combine with refetch and reject) |
| REJECT_SNAPSHOT | 5 | Reject this snapshot, try others |
| COMPLETE_SNAPSHOT | 6 | Complete this snapshot, no more chunks |



<a name="tendermint-abci-ResponseOfferSnapshot-Result"></a>

### ResponseOfferSnapshot.Result


| Name | Number | Description |
| ---- | ------ | ----------- |
| UNKNOWN | 0 | Unknown result, abort all snapshot restoration |
| ACCEPT | 1 | Snapshot accepted, apply chunks |
| ABORT | 2 | Abort all snapshot restoration |
| REJECT | 3 | Reject this specific snapshot, try others |
| REJECT_FORMAT | 4 | Reject all snapshots of this format, try others |
| REJECT_SENDER | 5 | Reject all snapshots from the sender(s), try others |



<a name="tendermint-abci-ResponseProcessProposal-ProposalStatus"></a>

### ResponseProcessProposal.ProposalStatus


| Name | Number | Description |
| ---- | ------ | ----------- |
| UNKNOWN | 0 | Unspecified error occurred |
| ACCEPT | 1 | Proposal accepted |
| REJECT | 2 | Proposal is not valid; prevoting `nil` |



<a name="tendermint-abci-ResponseVerifyVoteExtension-VerifyStatus"></a>

### ResponseVerifyVoteExtension.VerifyStatus


| Name | Number | Description |
| ---- | ------ | ----------- |
| UNKNOWN | 0 |  |
| ACCEPT | 1 |  |
| REJECT | 2 |  |



<a name="tendermint-abci-TxRecord-TxAction"></a>

### TxRecord.TxAction

TxAction contains App-provided information on what to do with a transaction that is part of a raw proposal

| Name | Number | Description |
| ---- | ------ | ----------- |
| UNKNOWN | 0 | Unknown action |
| UNMODIFIED | 1 | The Application did not modify this transaction. |
| ADDED | 2 | The Application added this transaction. |
| REMOVED | 3 | The Application wants this transaction removed from the proposal and the mempool. |
| DELAYED | 4 | The Application wants this transaction removed from the proposal but not the mempool. |







<a name="tendermint-abci-ABCIApplication"></a>

### ABCIApplication


| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| Echo | [RequestEcho](#tendermint-abci-RequestEcho) | [ResponseEcho](#tendermint-abci-ResponseEcho) | Echo a string to test an abci client/server implementation |
| Flush | [RequestFlush](#tendermint-abci-RequestFlush) | [ResponseFlush](#tendermint-abci-ResponseFlush) |  |
| Info | [RequestInfo](#tendermint-abci-RequestInfo) | [ResponseInfo](#tendermint-abci-ResponseInfo) |  |
| CheckTx | [RequestCheckTx](#tendermint-abci-RequestCheckTx) | [ResponseCheckTx](#tendermint-abci-ResponseCheckTx) |  |
| Query | [RequestQuery](#tendermint-abci-RequestQuery) | [ResponseQuery](#tendermint-abci-ResponseQuery) |  |
| InitChain | [RequestInitChain](#tendermint-abci-RequestInitChain) | [ResponseInitChain](#tendermint-abci-ResponseInitChain) |  |
| ListSnapshots | [RequestListSnapshots](#tendermint-abci-RequestListSnapshots) | [ResponseListSnapshots](#tendermint-abci-ResponseListSnapshots) |  |
| OfferSnapshot | [RequestOfferSnapshot](#tendermint-abci-RequestOfferSnapshot) | [ResponseOfferSnapshot](#tendermint-abci-ResponseOfferSnapshot) |  |
| LoadSnapshotChunk | [RequestLoadSnapshotChunk](#tendermint-abci-RequestLoadSnapshotChunk) | [ResponseLoadSnapshotChunk](#tendermint-abci-ResponseLoadSnapshotChunk) |  |
| ApplySnapshotChunk | [RequestApplySnapshotChunk](#tendermint-abci-RequestApplySnapshotChunk) | [ResponseApplySnapshotChunk](#tendermint-abci-ResponseApplySnapshotChunk) |  |
| PrepareProposal | [RequestPrepareProposal](#tendermint-abci-RequestPrepareProposal) | [ResponsePrepareProposal](#tendermint-abci-ResponsePrepareProposal) |  |
| ProcessProposal | [RequestProcessProposal](#tendermint-abci-RequestProcessProposal) | [ResponseProcessProposal](#tendermint-abci-ResponseProcessProposal) |  |
| ExtendVote | [RequestExtendVote](#tendermint-abci-RequestExtendVote) | [ResponseExtendVote](#tendermint-abci-ResponseExtendVote) |  |
| VerifyVoteExtension | [RequestVerifyVoteExtension](#tendermint-abci-RequestVerifyVoteExtension) | [ResponseVerifyVoteExtension](#tendermint-abci-ResponseVerifyVoteExtension) |  |
| FinalizeBlock | [RequestFinalizeBlock](#tendermint-abci-RequestFinalizeBlock) | [ResponseFinalizeBlock](#tendermint-abci-ResponseFinalizeBlock) |  |





## Scalar Value Types

| .proto Type | Notes | C++ | Java | Python | Go | C# | PHP | Ruby |
| ----------- | ----- | --- | ---- | ------ | -- | -- | --- | ---- |
| <a name="double" /> double |  | double | double | float | float64 | double | float | Float |
| <a name="float" /> float |  | float | float | float | float32 | float | float | Float |
| <a name="int32" /> int32 | Uses variable-length encoding. Inefficient for encoding negative numbers – if your field is likely to have negative values, use sint32 instead. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="int64" /> int64 | Uses variable-length encoding. Inefficient for encoding negative numbers – if your field is likely to have negative values, use sint64 instead. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="uint32" /> uint32 | Uses variable-length encoding. | uint32 | int | int/long | uint32 | uint | integer | Bignum or Fixnum (as required) |
| <a name="uint64" /> uint64 | Uses variable-length encoding. | uint64 | long | int/long | uint64 | ulong | integer/string | Bignum or Fixnum (as required) |
| <a name="sint32" /> sint32 | Uses variable-length encoding. Signed int value. These more efficiently encode negative numbers than regular int32s. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="sint64" /> sint64 | Uses variable-length encoding. Signed int value. These more efficiently encode negative numbers than regular int64s. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="fixed32" /> fixed32 | Always four bytes. More efficient than uint32 if values are often greater than 2^28. | uint32 | int | int | uint32 | uint | integer | Bignum or Fixnum (as required) |
| <a name="fixed64" /> fixed64 | Always eight bytes. More efficient than uint64 if values are often greater than 2^56. | uint64 | long | int/long | uint64 | ulong | integer/string | Bignum |
| <a name="sfixed32" /> sfixed32 | Always four bytes. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="sfixed64" /> sfixed64 | Always eight bytes. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="bool" /> bool |  | bool | boolean | boolean | bool | bool | boolean | TrueClass/FalseClass |
| <a name="string" /> string | A string must always contain UTF-8 encoded or 7-bit ASCII text. | string | String | str/unicode | string | string | string | String (UTF-8) |
| <a name="bytes" /> bytes | May contain any arbitrary sequence of bytes. | string | ByteString | str | []byte | ByteString | string | String (ASCII-8BIT) |

