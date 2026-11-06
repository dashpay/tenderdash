# Protocol Documentation
<a name="top"></a>

## Table of Contents

- [tendermint/abci/types.proto](#tendermint_abci_types-proto)
    - [CommitInfo](#tendermint-abci-CommitInfo)
    - [Event](#tendermint-abci-Event)
    - [EventAttribute](#tendermint-abci-EventAttribute)
    - [ExecTxResult](#tendermint-abci-ExecTxResult)
    - [ExtendVoteExtension](#tendermint-abci-ExtendVoteExtension)
    - [ExtendedVoteInfo](#tendermint-abci-ExtendedVoteInfo)
    - [Misbehavior](#tendermint-abci-Misbehavior)
    - [QuorumHashUpdate](#tendermint-abci-QuorumHashUpdate)
    - [Request](#tendermint-abci-Request)
    - [RequestApplyBlockRandomness](#tendermint-abci-RequestApplyBlockRandomness)
    - [RequestApplySnapshotChunk](#tendermint-abci-RequestApplySnapshotChunk)
    - [RequestCheckTx](#tendermint-abci-RequestCheckTx)
    - [RequestEcho](#tendermint-abci-RequestEcho)
    - [RequestExtendVote](#tendermint-abci-RequestExtendVote)
    - [RequestFinalizeBlock](#tendermint-abci-RequestFinalizeBlock)
    - [RequestFlush](#tendermint-abci-RequestFlush)
    - [RequestInfo](#tendermint-abci-RequestInfo)
    - [RequestInitChain](#tendermint-abci-RequestInitChain)
    - [RequestListSnapshots](#tendermint-abci-RequestListSnapshots)
    - [RequestLoadSnapshotChunk](#tendermint-abci-RequestLoadSnapshotChunk)
    - [RequestOfferSnapshot](#tendermint-abci-RequestOfferSnapshot)
    - [RequestPrepareProposal](#tendermint-abci-RequestPrepareProposal)
    - [RequestProcessProposal](#tendermint-abci-RequestProcessProposal)
    - [RequestQuery](#tendermint-abci-RequestQuery)
    - [RequestVerifyVoteExtension](#tendermint-abci-RequestVerifyVoteExtension)
    - [Response](#tendermint-abci-Response)
    - [ResponseApplyBlockRandomness](#tendermint-abci-ResponseApplyBlockRandomness)
    - [ResponseApplySnapshotChunk](#tendermint-abci-ResponseApplySnapshotChunk)
    - [ResponseCheckTx](#tendermint-abci-ResponseCheckTx)
    - [ResponseEcho](#tendermint-abci-ResponseEcho)
    - [ResponseException](#tendermint-abci-ResponseException)
    - [ResponseExtendVote](#tendermint-abci-ResponseExtendVote)
    - [ResponseFinalizeBlock](#tendermint-abci-ResponseFinalizeBlock)
    - [ResponseFlush](#tendermint-abci-ResponseFlush)
    - [ResponseInfo](#tendermint-abci-ResponseInfo)
    - [ResponseInitChain](#tendermint-abci-ResponseInitChain)
    - [ResponseListSnapshots](#tendermint-abci-ResponseListSnapshots)
    - [ResponseLoadSnapshotChunk](#tendermint-abci-ResponseLoadSnapshotChunk)
    - [ResponseOfferSnapshot](#tendermint-abci-ResponseOfferSnapshot)
    - [ResponsePrepareProposal](#tendermint-abci-ResponsePrepareProposal)
    - [ResponseProcessProposal](#tendermint-abci-ResponseProcessProposal)
    - [ResponseQuery](#tendermint-abci-ResponseQuery)
    - [ResponseVerifyVoteExtension](#tendermint-abci-ResponseVerifyVoteExtension)
    - [Snapshot](#tendermint-abci-Snapshot)
    - [ThresholdPublicKeyUpdate](#tendermint-abci-ThresholdPublicKeyUpdate)
    - [TxRecord](#tendermint-abci-TxRecord)
    - [TxResult](#tendermint-abci-TxResult)
    - [Validator](#tendermint-abci-Validator)
    - [ValidatorSetUpdate](#tendermint-abci-ValidatorSetUpdate)
    - [ValidatorUpdate](#tendermint-abci-ValidatorUpdate)
    - [VoteInfo](#tendermint-abci-VoteInfo)
  
    - [CheckTxType](#tendermint-abci-CheckTxType)
    - [MisbehaviorType](#tendermint-abci-MisbehaviorType)
    - [ResponseApplySnapshotChunk.Result](#tendermint-abci-ResponseApplySnapshotChunk-Result)
    - [ResponseOfferSnapshot.Result](#tendermint-abci-ResponseOfferSnapshot-Result)
    - [ResponseProcessProposal.ProposalStatus](#tendermint-abci-ResponseProcessProposal-ProposalStatus)
    - [ResponseVerifyVoteExtension.VerifyStatus](#tendermint-abci-ResponseVerifyVoteExtension-VerifyStatus)
    - [TxRecord.TxAction](#tendermint-abci-TxRecord-TxAction)
  
    - [ABCIApplication](#tendermint-abci-ABCIApplication)
  
- [Scalar Value Types](#scalar-value-types)



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
| beacon | [bytes](#bytes) |  | Random beacon of the committed block: the threshold signature recovered from the prevotes of the commit round. Set from consensus version 2; empty before. |
| app_hash | [bytes](#bytes) |  | App hash after `ApplyBlockRandomness` for the committed block and round. The commit signs it through the state ID of its block ID, which Tenderdash checks before passing it on. Set from consensus version 2; empty before. |






<a name="tendermint-abci-Event"></a>

### Event
Event carries application attributes in ResponseProcessProposal
and ExecTxResult.

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
| total_voting_power | [int64](#int64) |  | Total voting power of the validator set in case the ABCI application does not store historical validators. https://github.com/tendermint/tendermint/issues/4581 |






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
| apply_block_randomness | [RequestApplyBlockRandomness](#tendermint-abci-RequestApplyBlockRandomness) |  |  |






<a name="tendermint-abci-RequestApplyBlockRandomness"></a>

### RequestApplyBlockRandomness
Applies the block&#39;s randomness and returns the app hash the Precommit signs.

#### Usage

- Used from consensus version 2 (`VersionParams.consensus_version`). Before that, Tenderdash never calls it.
  This release defines consensus versions 0 and 1 only, so it never calls it.
- `RequestApplyBlockRandomness.beacon` is the random beacon of the block and round: the 96-byte BLS threshold
  signature recovered from the Prevotes of round `round` for the block `hash`. Tenderdash has verified it against
  the quorum&#39;s threshold public key before the call.
- The Application applies the randomness to the state that `ProcessProposal` (or `PrepareProposal`) left for
  that block and round, and returns the resulting Merkle root hash in `ResponseApplyBlockRandomness.app_hash`.
  When nothing in the block uses randomness, `app_hash` equals the app hash of the block header.
- The header keeps the app hash from `ProcessProposal`, which Prevotes sign. Precommits and the commit sign
  `ResponseApplyBlockRandomness.app_hash`, and the commit carries it in `Commit.app_hash`.
- The randomness may change only the application state behind `app_hash`. Transaction results, events,
  validator set updates, consensus parameter updates and the other outputs of `ProcessProposal` are fixed
  by the header the Prevotes signed: they MUST NOT depend on the beacon, and Tenderdash keeps the ones
  `ProcessProposal` returned.
- Vote extensions and their verification MUST NOT depend on the beacon or on the state this call produces;
  see `ExtendVote`.
- The implementation MUST be deterministic: every process that applies the same beacon to the same block
  and round MUST return the same `app_hash`.
- Tenderdash may call it more than once for the same block, round and beacon; the Application MUST return
  the same `app_hash` each time.
- It MUST NOT modify the committed state: Tenderdash may call it for a round that never commits, and
  `FinalizeBlock` commits the state for the commit round.
- An ABCI error is fatal, as for `ExtendVote`.

#### When does Tenderdash call it?

- On a validator, once the Prevotes of round _r_ for block _v_ reach the threshold and their signature is
  recovered, before `ExtendVote` and before signing its Precommit for _v_.
- On every path that finalizes a block from a commit (consensus, block sync, replay, a node that did not
  vote): after `ProcessProposal` of the committed block for the commit round, with the commit&#39;s beacon, and
  before `FinalizeBlock`. Tenderdash compares the returned `app_hash` with `Commit.app_hash` and stops on a
  mismatch.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| hash | [bytes](#bytes) |  | The header hash of the block the Prevotes are for. |
| height | [int64](#int64) |  | Height of the block. |
| round | [int32](#int32) |  | Round whose Prevotes produced the beacon. |
| beacon | [bytes](#bytes) |  | 96-byte BLS threshold signature recovered from those Prevotes. |






<a name="tendermint-abci-RequestApplySnapshotChunk"></a>

### RequestApplySnapshotChunk
Applies a snapshot chunk.

- The application can choose to refetch chunks and/or ban P2P peers as appropriate.
  Tenderdash will not do this unless instructed by the application.
- The application may want to verify each chunk, e.g. by attaching chunk hashes in `Snapshot.Metadata` and/or
  incrementally verifying contents against AppHash.
- On ACCEPT, Tenderdash enqueues `ResponseApplySnapshotChunk.next_chunks` and continues applying chunks.
  Restoration finishes only when the application returns COMPLETE_SNAPSHOT; an ACCEPT without
  `next_chunks` does not signal completion.
- After COMPLETE_SNAPSHOT, Tenderdash makes an ABCI Info call to verify that LastBlockAppHash,
  LastBlockHeight and AppVersion match the expected values.
  It then switches to block sync or consensus and joins the network.
- If Tenderdash is unable to retrieve the next chunk after some time (e.g. because no suitable peers are available),
  it will reject the snapshot and try a different one via OfferSnapshot. The application should be prepared to reset
  and accept it or abort as appropriate.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| chunk_id | [bytes](#bytes) |  | Application-defined chunk identifier: `Snapshot.hash` for the first chunk, then identifiers returned in `ResponseApplySnapshotChunk.next_chunks`. |
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
- Tenderdash attributes no other value to the response code


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
Return the vote_extensions vector for the block and round processed last.
Before a validator signs a non-nil precommit, Tenderdash requests the extensions
to attach to that vote. It may also call ExtendVote on any node after rejecting
a commit vector, to learn the expected vector without signing or broadcasting.
The call must be free of side effects. Threshold-recoverable extensions must be
deterministic and match the vector accepted at commit-level VerifyVoteExtension.
From consensus version 2, ApplyBlockRandomness runs first, but the extensions
must not depend on the beacon or on the state it produced.
See the [ExtendVote contract](methods.md#extendvote).


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| hash | [bytes](#bytes) |  | The header hash of the proposed block that the vote extensions is to refer to. |
| height | [int64](#int64) |  | Height of the proposed block (for sanity check). |
| round | [int32](#int32) |  | Round number for the block. |






<a name="tendermint-abci-RequestFinalizeBlock"></a>

### RequestFinalizeBlock
Persist the decided candidate for the block and the round processed last.
Tenderdash calls this after processing and accepting the commit extension vector.
The application must persist the candidate before returning; execution results
and events are provided through proposal processing, not ResponseFinalizeBlock.
The response contains retention and next-block scheduling hints only.
Use retain_height with care: pruning blocks can prevent replay and bootstrapping.
From consensus version 2, the state to persist is that candidate with commit.beacon
applied, as ApplyBlockRandomness applies it, and its root must equal commit.app_hash.
See the [FinalizeBlock contract](methods.md#finalizeblock) and
[commit and apply](../consensus/consensus-happy-path.md#9-commit-and-apply).


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
Return information about the application&#39;s last committed state.
Used during startup reconciliation and by RPC queries. Persist the reported
height and application hash in FinalizeBlock. See the
[startup handshake](../consensus/consensus-recovery.md#start-up-abci-handshake).


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
| chunk_id | [bytes](#bytes) |  | Application-defined chunk identifier: `Snapshot.hash` for the first chunk, then identifiers returned in `ResponseApplySnapshotChunk.next_chunks`. |






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
Prepare and execute a new block proposal, potentially altering its transactions.
Return same-block application/results hashes and next-state commitments.
ADDED transactions enter the proposal without being added to the mempool;
REMOVED transactions are removed from both. Changes to validators and consensus
parameters from height H take effect at H&#43;1. Preparation may be non-deterministic.
See the [PrepareProposal contract](methods.md#prepareproposal) for transaction
constraints, response validation and the exact conditions for preparing a fresh block.


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
Execute a proposed block deterministically against the last committed state.
Return its status, same-block application/results hashes and next-state commitments.
Keep execution speculative until FinalizeBlock. Processing can repeat for the same
block and round after another execution context has replaced it.
See the [ProcessProposal contract](methods.md#processproposal) and
[application requirements](app-requirements.md).


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

Tenderdash calls it for two kinds of request, told apart by `validator_pro_tx_hash`:

- **Precommit verification** (`validator_pro_tx_hash` set): the vote extensions of one validator&#39;s
  Precommit, exactly as its Application returned them from `ExtendVote`.
- **Commit verification** (`validator_pro_tx_hash` empty): the extension vector of a commit, after
  `ProcessProposal` of the committed block with the same `hash`, `height` and `round` (the commit round).
  The vector holds only the threshold-recoverable extensions (`THRESHOLD_RECOVER` and
  `THRESHOLD_RECOVER_RAW`), in the order `ExtendVote` returned them; extensions of any other type
  never appear in a commit. It can be empty.

#### Usage

- `RequestVerifyVoteExtension.vote_extensions` can be empty. For a Precommit, the Application&#39;s
  interpretation of it should be that the Application running at the process that sent the vote chose
  not to extend it. Tenderdash will always call `RequestVerifyVoteExtension`, even for 0 length vote
  extensions.
- For a Precommit, if `ResponseVerifyVoteExtension.status` is `REJECT`, Tenderdash will reject the
  whole received vote. See the [Requirements](app-requirements.md) section to understand the
  potential liveness implications of this.
- For a commit, the Application MUST compare the vector with the threshold-recoverable subset of the
  extensions it expects for that block, in order, and return `REJECT` if they differ: a commit whose
  block signature is valid can still carry a stripped, duplicated or replayed vector. Tenderdash
  requires `ACCEPT` before it saves the block and commit or calls `FinalizeBlock`. What `REJECT` does
  depends on the path:
  - consensus: the commit is discarded without blaming its sender. A commit whose block the node
    already holds is checked first and never changes the round. A commit for a later round whose
    block has not arrived moves the node to that round on its threshold signature alone; it is
    checked when the block arrives, then the commits other peers sent meanwhile. Copies of a commit
    already waiting are dropped. If the node&#39;s own commit is rejected, nothing is persisted and the
    node waits for a peer&#39;s commit. The block is processed again only for a commit of a round other
    than the one processed last. After a rejection, Tenderdash calls `ExtendVote` for the block and
    round, and refuses, without processing the block again or calling the Application, a later
    commit of that round that would need the block processed again, when its vector differs from
    the one returned or equals it and was rejected. Consensus WAL replay follows these rules;
  - block sync: the block is not applied; the peer that served it is dropped and the height is
    requested again;
  - handshake catch-up (application behind the block store): the commit is already in the block
    store, so the node fails to start; there is no automatic recovery, and the operator has to roll
    back or re-sync the node.
- Any status other than `ACCEPT`, including `UNKNOWN`, counts as `REJECT`. An error from the ABCI
  call itself, as opposed to a status, is fatal on every path.
- The implementation of `VerifyVoteExtension` MUST be deterministic. Moreover, the value of
  `ResponseVerifyVoteExtension.status` MUST **exclusively** depend on the parameters passed in
  the call to `RequestVerifyVoteExtension`, the block processed by `ProcessProposal`, and the last
  committed Application state (see [Requirements](app-requirements.md) section).
- For a Precommit, application implementers SHOULD always set `ResponseVerifyVoteExtension.status`
  to `ACCEPT`, unless they _really_ know what the potential liveness implications of returning
  `REJECT` are.

#### When does Tenderdash call it?

When a validator _p_ is in Tenderdash consensus height _h_ and receives, from another validator _q_,
a Precommit message for a block (not `nil`) at height _h_:

1. If the Precommit message does not contain a vote extensions with a valid signature, Tenderdash discards the message as invalid.
  - a 0-length vote extensions is valid as long as its accompanying signature is also valid.
2. Else, _p_&#39;s Tenderdash calls `RequestVerifyVoteExtension`.
3. The Application returns _accept_ or _reject_ via `ResponseVerifyVoteExtension.status`.
4. If the Application returns
  - _accept_, _p_&#39;s Tenderdash will keep the received vote, together with its corresponding
    vote extension in its internal data structures. Threshold-recoverable extensions
    are recovered into CommitInfo when the commit is assembled.
  - _reject_, _p_&#39;s Tenderdash will deem the Precommit message invalid and discard it.

Whenever a process is about to save a block and its commit, in consensus, block sync or replay, it
first calls `RequestVerifyVoteExtension` for the commit, once `ProcessProposal` has processed the
block. This includes commits the process assembled from its own &#43;2/3 precommits.


| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| hash | [bytes](#bytes) |  | The header hash of the propsed block that the vote extensions refers to. |
| validator_pro_tx_hash | [bytes](#bytes) |  | ProTxHash of the validator that signed the extensions; empty for commit verification. |
| height | [int64](#int64) |  | Height of the block (for sanity check). |
| round | [int32](#int32) |  | Round of the vote; for commit verification, the commit round. |
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
| apply_block_randomness | [ResponseApplyBlockRandomness](#tendermint-abci-ResponseApplyBlockRandomness) |  |  |






<a name="tendermint-abci-ResponseApplyBlockRandomness"></a>

### ResponseApplyBlockRandomness



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| app_hash | [bytes](#bytes) |  | The Merkle root hash of the application state after the randomness is applied. 32 bytes. Equals the header app hash when nothing in the block uses randomness. The randomness changes nothing but this state: the other results of the block are the ones ProcessProposal returned. |






<a name="tendermint-abci-ResponseApplySnapshotChunk"></a>

### ResponseApplySnapshotChunk



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| result | [ResponseApplySnapshotChunk.Result](#tendermint-abci-ResponseApplySnapshotChunk-Result) |  | The result of applying this chunk. |
| refetch_chunks | [bytes](#bytes) | repeated | Identifiers of chunks to discard, refetch and reapply, regardless of `result`. |
| reject_senders | [string](#string) | repeated | Reject the given P2P senders, regardless of `Result`. Any chunks already applied will not be refetched unless explicitly requested, but queued chunks from these senders will be discarded, and new chunks or other snapshots rejected. |
| next_chunks | [bytes](#bytes) | repeated | Chunk identifiers to enqueue after ACCEPT; ignored for other results. |






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
| propose_next_block_immediately | [bool](#bool) |  | When `true`, this node does not wait for transactions (`create-empty-blocks-interval`) before proposing round 0 of the next height: the application has block-driven work pending, for example withdrawal transactions to sign. A hint for the local node only, consumed once; a node that restarts in between falls back to the configured wait. Defaults to `false`. |






<a name="tendermint-abci-ResponseFlush"></a>

### ResponseFlush







<a name="tendermint-abci-ResponseInfo"></a>

### ResponseInfo



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| data | [string](#string) |  | Some arbitrary information. |
| version | [string](#string) |  | The application software semantic version. |
| app_version | [uint64](#uint64) |  | The application protocol version. |
| last_block_height | [int64](#int64) |  | Last height persisted by FinalizeBlock. |
| last_block_app_hash | [bytes](#bytes) |  | Application hash at the last persisted height. 32 bytes. |






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
| REJECT | 2 | Proposal is not valid |



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
| ApplyBlockRandomness | [RequestApplyBlockRandomness](#tendermint-abci-RequestApplyBlockRandomness) | [ResponseApplyBlockRandomness](#tendermint-abci-ResponseApplyBlockRandomness) |  |

 



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

