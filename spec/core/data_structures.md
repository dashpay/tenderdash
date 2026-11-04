# Data Structures

Here we describe the data structures in the Tenderdash blockchain and the rules for validating them.

The Tenderdash blockchains consists of a short list of data types:

- [Data Structures](#data-structures)
    - [ProTxHash](#protxhash)
    - [Block](#block)
    - [Execution](#execution)
    - [Header](#header)
    - [Version](#version)
    - [BlockID](#blockid)
    - [PartSetHeader](#partsetheader)
    - [Part](#part)
    - [Time](#time)
    - [Data](#data)
    - [Commit](#commit)
    - [BlockIDFlag](#blockidflag)
    - [Vote](#vote)
    - [VoteExtension](#voteextension)
    - [CanonicalVote](#canonicalvote)
    - [Proposal](#proposal)
    - [SignedMsgType](#signedmsgtype)
    - [Signature](#signature)
    - [EvidenceList](#evidencelist)
    - [Evidence](#evidence)
        - [DuplicateVoteEvidence](#duplicatevoteevidence)
    - [LightBlock](#lightblock)
    - [SignedHeader](#signedheader)
    - [ValidatorSet](#validatorset)
    - [Validator](#validator)
    - [Address](#address)
    - [ConsensusParams](#consensusparams)
        - [BlockParams](#blockparams)
        - [EvidenceParams](#evidenceparams)
        - [ValidatorParams](#validatorparams)
        - [VersionParams](#versionparams)
        - [SynchronyParams](#synchronyparams)
        - [TimeoutParams](#timeoutparams)
        - [ABCIParams](#abciparams)
    - [Proof](#proof)

## ProTxHash

ProTxHash is 32 bytes array. Validator provides this parameter via a config.

## Block

A block consists of a header, transactions, the commit of the previous block, an optional core chain lock,
and a list of evidence of malfeasance (ie. signing conflicting votes).

| Name | Type | Description | Validation |
| --- | --- | --- | --- |
| Header | [Header](#header) | Header corresponding to the block. This field contains information used throughout consensus and other areas of the protocol. To find out what it contains, visit [header] (#header) | Must adhere to the validation rules of [header](#header) |
| Data | [Data](#data) | Data contains a list of transactions. The contents of the transaction is unknown to Tenderdash. | This field can be empty or populated, but no validation is performed. Applications can perform validation on individual transactions prior to block creation using [checkTx](../abci/abci.md#checktx). |
| Evidence | [EvidenceList](#evidencelist) | Evidence contains a list of infractions committed by validators. | Can be empty, but when populated the validations rules from [evidenceList](#evidencelist) apply |
| LastCommit | [Commit](#commit) | The threshold-signed commit of the previous block. | Must not be nil, must carry no threshold block signature at the initial height, and must adhere to the validation rules of [commit](#commit). |
| CoreChainLock | `CoreChainLock` (optional) | Core chain lock carried by the block. | When present, must pass `CoreChainLock.ValidateBasic`. |

## Execution

Once a block is validated, it can be executed against the state.

The state follows this recursive equation:

```go
state(initialHeight) = InitialState
state(h+1) <- Execute(state(h), ABCIApp, block(h))
```

where `InitialState` includes the initial consensus parameters and validator set,
and `ABCIApp` is an ABCI application that can return results and changes to the validator
set (TODO). Execute is defined as:

```go
func Execute(s State, app ABCIApp, block Block) State {
 // Fuction ApplyBlock executes block of transactions against the app and returns the new root hash of the app state,
 // modifications to the validator set and the changes of the consensus parameters.
 AppHash, ValidatorChanges, ConsensusParamChanges := app.ApplyBlock(block)

 nextConsensusParams := UpdateConsensusParams(state.ConsensusParams, ConsensusParamChanges)
 return State{
  ChainID:         state.ChainID,
  InitialHeight:   state.InitialHeight,
  LastResults:     abciResponses.DeliverTxResults,
  AppHash:         AppHash,
  InitialHeight:   state.InitialHeight,
  LastValidators:  state.Validators,
  Validators:      state.NextValidators,
  NextValidators:  UpdateValidators(state.NextValidators, ValidatorChanges),
  ConsensusParams: nextConsensusParams,
  Version: {
   Consensus: {
    AppVersion: nextConsensusParams.Version.AppVersion,
   },
  },
 }
}
```

Validating a new block is first done prior to the `prevote`, `precommit` & `finalizeCommit` stages.

The steps to validate a new block are:

- Check the validity rules of the block and its fields.
- Check the versions (Block & App) are the same as in local state.
- Check the chainID's match.
- Check the height is correct.
- Check the `LastBlockID` corresponds to BlockID currently in state.
- Check the hashes in the header match those in state.
- Verify the LastCommit against state, this step is skipped for the initial height.
    - This is where checking the signatures correspond to the correct block will be made.
- Make sure the proposer is part of the validator set.
- Validate bock time.
    - Above the initial height, make sure the block's time is after the previous block's time.
    - At the initial height, make sure the block's time is not before the genesis time.
- Validate the evidence in the block. Note: Evidence can be empty

## Header

A block header contains metadata about the block and about the consensus, as well as commitments to
the data in the current block, the previous block, and the results returned by the application:

| Name | Type | Description | Validation |
| --- | --- | --- | --- |
| Version | [Version](#version) | Version defines the application and protocol version being used. | Must adhere to the validation rules of [Version](#version) |
| ChainID | String | ChainID is the ID of the chain. This must be unique to your chain. | ChainID must be at most 50 bytes. |
| Height | int64 | Height is the height for this header. | Must be > 0, >= initialHeight, and == previous Height+1 |
| CoreChainLockedHeight | uint32 | Core chain height used to verify this block. | Must match the block's core chain lock, or the state's last locked height without one (`ValidateBlockChainLock`). |
| Time | [Time](#time) | The timestamp is chosen by the proposer from its local clock (genesis time for the initial block) and accepted by validators only if timely; see [Proposal timestamps](../consensus/consensus-alternative-flows.md#proposal-timestamps-and-time-rules). The historical median-based rule is described in the [BFT-time section](../consensus/bft-time.md). | Above the initial height, Time must be greater than the previous block's time. At the initial height, Time must be >= genesis time; a normal prevote additionally requires it to equal genesis time (rule [P-Timely](../consensus/consensus-transitions.md#prevote-decision-prevoterdo)). |
| LastBlockID | [BlockID](#blockid) | BlockID of the previous block. | Must adhere to the validation rules of [blockID](#blockid). The first block has `block.Header.LastBlockID == BlockID{}`. |
| LastCommitHash | slice of bytes (`[]byte`) | Checksum of the last commit's threshold block signature (`Commit.Hash`). | Empty or of length 32; must equal `LastCommit.Hash()` |
| DataHash | slice of bytes (`[]byte`) | MerkleRoot of the hash of transactions. **Note**: The transactions are hashed before being included in the merkle tree, the leaves of the Merkle tree are the hashes, not the transactions themselves. | Empty or of length 32; must equal `Data.Hash()` |
| ValidatorsHash | slice of bytes (`[]byte`) | Merkle root of the current validator set's threshold public key and quorum hash (`ValidatorSet.Hash`). The validator list is not hashed, so its order, ascending `ProTxHash` (`ValidatorsByProTxHashes`), does not affect this value. | Empty or of length 32 |
| NextValidatorsHash | slice of bytes (`[]byte`) | Merkle root of the next validator set's threshold public key and quorum hash (`ValidatorSet.Hash`). The validator list is not hashed, so its order, ascending `ProTxHash` (`ValidatorsByProTxHashes`), does not affect this value. | Empty or of length 32 |
| ConsensusHash | slice of bytes (`[]byte`) | SHA-256 of the protobuf-encoded `HashedParams` subset of the current consensus parameters (`HashConsensusParams`). | Empty or of length 32 |
| NextConsensusHash | slice of bytes (`[]byte`) | The same hash over the consensus parameters of the next block. | Empty or of length 32 |
| AppHash | slice of bytes (`[]byte`) | Application state hash after executing this block's transactions, returned by the application from `PrepareProposal` and `ProcessProposal`. | The application's response must be 32 bytes; must equal the application's answer (`ValidateBlockWithRoundState`). |
| ResultsHash | slice of bytes (`[]byte`) | Merkle root of this block's `ExecTxResult` values, keeping only `Code`, `Data` and `GasUsed` (`TxResultsHash`). | Empty or of length 32; must equal the application's answer |
| EvidenceHash | slice of bytes (`[]byte`) | MerkleRoot of the evidence of Byzantine behaviour included in this block. | Empty or of length 32; must equal `Evidence.Hash()` |
| ProposedAppVersion | uint64 | Proposer's latest available application protocol version. | |
| ProposerProTxHash | [ProTxHash](#protxhash) | `ProTxHash` of the original proposer of the block. Validator must be in the current validatorSet. | Must be of length 32 |

## Version

NOTE: that this is more specifically the consensus version and doesn't include information like the
P2P Version. (TODO: we should write a comprehensive document about
versioning that this can refer to)

| Name  | type   | Description                                                                                                     | Validation                                                                                                         |
|-------|--------|-----------------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------|
| Block | uint64 | This number represents the version of the block protocol and must be the same throughout an operational network | Must be equal to protocol version being used in a network (`block.Version.Block == state.Version.Consensus.Block`) |
| App   | uint64 | App version is decided on by the application. Read [here](../abci/abci.md#info)                                 | `block.Version.App == state.Version.Consensus.App`                                                                 |

## BlockID

The `BlockID` contains two distinct Merkle roots of the block, the number of parts (ie. `len(MakeParts(block))`) and a state ID hash.

| Name          | Type                            | Description                                                                                                                                                      | Validation                                                             |
|---------------|---------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------|
| Hash | slice of bytes (`[]byte`) | MerkleRoot of all the fields in the header (ie. `MerkleRoot(header)`. | Empty (nil vote) or of length 32 |
| PartSetHeader | [PartSetHeader](#partsetheader) | Used for secure gossiping of the block during consensus, is the MerkleRoot of the complete serialized block cut into parts (ie. `MerkleRoot(MakeParts(block))`). | Must adhere to the validation rules of [PartSetHeader](#partsetheader) |
| StateID | slice of bytes (`[]byte`) | Hash of the `StateID` fields a light client needs to verify state; see [Signing](../consensus/signing.md). | Must be of length 32 when `Hash` is not empty |

See [MerkleRoot](./encoding.md#MerkleRoot) for details.

## PartSetHeader

| Name  | Type                      | Description                       | Validation           |
|-------|---------------------------|-----------------------------------|----------------------|
| Total | uint32                    | Total amount of parts for a block | Must be <= `MaxBlockPartsCount`; > 0 in a complete `BlockID` |
| Hash  | slice of bytes (`[]byte`) | MerkleRoot of a serialized block  | Empty or of length 32 |

## Part

Part defines a part of a block. In Tenderdash blocks are broken into `parts` for gossip.

| Name  | Type            | Description                  | Validation                          |
|-------|-----------------|------------------------------|-------------------------------------|
| index | uint32          | Index of this part           | Must equal `Proof.Index`            |
| bytes | bytes           | Content of this part         | Must be <= `BlockPartSizeBytes` long |
| proof | [Proof](#proof) | Merkle proof of this part    | Must pass `Proof.ValidateBasic`     |

## Time

Tenderdash uses the [Google.Protobuf.Timestamp](https://developers.google.com/protocol-buffers/docs/reference/google.protobuf#google.protobuf.Timestamp)
format, which uses two integers, one 64 bit integer for Seconds and a 32 bit integer for Nanoseconds.

## Data

Data is just a wrapper for a list of transactions, where transactions are arbitrary byte arrays:

| Name | Type                       | Description            | Validation                                                                  |
|------|----------------------------|------------------------|-----------------------------------------------------------------------------|
| Txs  | Matrix of bytes ([][]byte) | Slice of transactions. | Validation does not occur on this field, this data is unknown to Tenderdash |

## Commit

Commit proves that a quorum decided a block: it carries the threshold-recovered signatures rather than one signature per validator.

| Name | Type | Description | Validation |
| --- | --- | --- | --- |
| Height | int64 | Height at which this commit was created. | Must be >= 0 |
| Round | int32 | Round that the commit corresponds to. | Must be >= 0 |
| BlockID | [BlockID](#blockid) | The blockID of the corresponding block. | At height >= 1, must not be nil and must adhere to the validation rules of [BlockID](#blockid). |
| QuorumHash | slice of bytes (`[]byte`) | Hash of the quorum that signed the commit. | |
| ThresholdBlockSignature | [Signature](#signature) | Threshold-recovered block signature. | At height >= 1, must be of length 96 |
| ThresholdVoteExtensions | Array of [VoteExtension](#voteextension) | Threshold-recovered vote extensions. | No nil entries; every type must be a defined enum value |

## BlockIDFlag

BlockIDFlag is defined in `types.proto`, but [Commit](#commit) has no per-validator signatures and does not use it.

```go
enum BlockIDFlag {
  BLOCK_ID_FLAG_UNKNOWN = 0;
  BLOCK_ID_FLAG_ABSENT  = 1; // signatures for other blocks are also considered absent
  BLOCK_ID_FLAG_COMMIT  = 2;
  BLOCK_ID_FLAG_NIL     = 3;
}
```

## Vote

A vote is a signed message from a validator for a particular block.
The vote includes information about the validator signing it. When stored in the blockchain or propagated over the network, votes are encoded in Protobuf.
The vote extension is not part of the [`CanonicalVote`](#canonicalvote).

| Name | Type | Description | Validation |
| --- | --- | --- | --- |
| Type | [SignedMsgType](#signedmsgtype) | Either prevote or precommit. [SignedMsgType](#signedmsgtype) | A Vote is valid if its corresponding fields are included in the enum [signedMsgType](#signedmsgtype) |
| Height | int64 | Height for which this vote was created. | Must be >= 0 |
| Round | int32 | Round that the vote corresponds to. | Must be >= 0 |
| BlockID | [BlockID](#blockid) | The blockID of the corresponding block; zero for a nil vote. | Must adhere to [BlockID](#blockid) validation and be either nil or complete |
| ValidatorProTxHash | [ProTxHash](#protxhash) | `ProTxHash` of the validator. | Length must be equal to 32 |
| ValidatorIndex | int32 | Index of the validator in the validator set at this height. | Must be >= 0 |
| BlockSignature | [Signature](#signature) | Validator's signature of the [`CanonicalVote`](#canonicalvote) sign bytes. | Length must be > 0 and <= 96 |
| VoteExtensions | Array of [VoteExtension](#voteextension) | Vote extensions provided by the Application. | Must be empty unless this is a non-nil precommit, which must pass [VoteExtension](#voteextension) validation |

## VoteExtension

| Name | Type | Description | Validation |
| --- | --- | --- | --- |
| Type | `VoteExtensionType` | `THRESHOLD_RECOVER` or `THRESHOLD_RECOVER_RAW`. `DEFAULT` exists in the enum but is not supported. | Must not be `DEFAULT` |
| Extension | slice of bytes (`[]byte`) | Extension data provided by the Application. | Must be of length 32 for `THRESHOLD_RECOVER_RAW` |
| Signature | [Signature](#signature) | Validator's signature of the extension. | Must be present when `Extension` is not empty; length must be <= 96 |
| SignRequestId | slice of bytes (`[]byte`, optional) | Application-supplied sign request ID. | |

## CanonicalVote

CanonicalVote is for validator signing. This type will not be present in a block. For signing, votes are represented via `CanonicalVote` and encoded using protobuf via `Vote.SignBytes`, which includes the `ChainID` and omits `ValidatorProTxHash` and `ValidatorIndex`. `block_id` is a checksum of the `CanonicalBlockID`, and `state_id` is the `BlockID`'s `StateID`.

```proto
message CanonicalVote {
  SignedMsgType type     = 1;
  sfixed64      height   = 2;
  sfixed64      round    = 3;
  bytes         block_id = 4;
  bytes         state_id = 5;
  string        chain_id = 99;
}
```

`Vote.Verify(chainID, quorumType, quorumHash, pubKey, proTxHash)` checks that `proTxHash` matches `ValidatorProTxHash`, then verifies the block signature, and any extension signatures, against the 48-byte BLS public key. See [Signing](../consensus/signing.md).

## Proposal

Proposal contains height and round for which this proposal is made, BlockID as a unique identifier
of proposed block, timestamp, and POLRound (a so-called Proof-of-Lock (POL) round) that is needed for
termination of the consensus. If POLRound >= 0, then BlockID corresponds to the block that
is locked in POLRound. The message is signed by the validator private key.

| Name | Type | Description | Validation |
| --- | --- | --- | --- |
| Type | [SignedMsgType](#signedmsgtype) | Represents a Proposal [SignedMsgType](#signedmsgtype) | Must be `ProposalType`  [signedMsgType](#signedmsgtype) |
| Height | int64 | Height for which this proposal was created | Must be >= 0 |
| Round | int32 | Round that the proposal corresponds to. | Must be >= 0 |
| POLRound | int32 | Proof of lock | Must be >= -1 |
| BlockID | [BlockID](#blockid) | The blockID of the corresponding block. | Must be complete and adhere to [BlockID](#blockid) validation |
| Timestamp | [Time](#time) | Timestamp chosen by the proposer; equal to the block time. | [Time](#time) |
| Signature | [Signature](#signature) | Signature by the proposer. | Length of signature must be > 0 and <= 96 |
| CoreChainLockedHeight | uint32 | Core chain locked height used to sign and verify the block. | Must not be `MaxUint32` |
| CoreChainLockUpdate | `CoreChainLock` (optional) | Core chain lock used to sign and verify the next block. | When present, must pass `CoreChainLock.ValidateBasic` |

## SignedMsgType

Signed message type represents a signed messages in consensus.

```proto
enum SignedMsgType {

  SIGNED_MSG_TYPE_UNKNOWN = 0;
  // Votes
  SIGNED_MSG_TYPE_PREVOTE   = 1;
  SIGNED_MSG_TYPE_PRECOMMIT = 2;

  // Proposal
  SIGNED_MSG_TYPE_PROPOSAL = 32;
}
```

## Signature

Signatures in Tenderdash are raw bytes representing the underlying signature. Consensus signatures are BLS12-381 signatures of 96 bytes (`types.SignatureSize`).

See the [signature spec](./encoding.md#key-types) for more.

## EvidenceList

EvidenceList is a simple wrapper for a list of evidence:

| Name     | Type                           | Description                            | Validation                                                      |
|----------|--------------------------------|----------------------------------------|-----------------------------------------------------------------|
| Evidence | Array of [Evidence](#evidence) | List of verified [evidence](#evidence) | Validation adheres to individual types of [Evidence](#evidence) |

## Evidence

Evidence in Tenderdash is used to indicate breaches in the consensus by a validator.

More information on how evidence works in Tenderdash can be found [here](../consensus/evidence.md)

### DuplicateVoteEvidence

`DuplicateVoteEvidence` represents a validator that has voted for two different blocks
in the same round of the same height. Votes are lexicographically sorted on `BlockID`.

| Name             | Type          | Description                                                        | Validation                                          |
|------------------|---------------|--------------------------------------------------------------------|-----------------------------------------------------|
| VoteA            | [Vote](#vote) | One of the votes submitted by a validator when they equivocated    | VoteA must adhere to [Vote](#vote) validation rules |
| VoteB            | [Vote](#vote) | The second vote submitted by a validator when they equivocated     | VoteB must adhere to [Vote](#vote) validation rules |
| TotalVotingPower | int64         | The total power of the validator set at the height of equivocation | Must be equal to nodes own copy of the data         |
| ValidatorPower   | int64         | Power of the equivocating validator at the height                  | Must be equal to the nodes own copy of the data     |
| Timestamp        | [Time](#time) | Time of the block where the equivocation occurred                  | Must be equal to the nodes own copy of the data     |

## LightBlock

LightBlock is the core data structure of the [light client](../light-client/README.md). It combines two data structures needed for verification ([signedHeader](#signedheader) & [validatorSet](#validatorset)).

| Name         | Type                          | Description                                                                                                                            | Validation                                                                          |
|--------------|-------------------------------|----------------------------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------|
| SignedHeader | [SignedHeader](#signedheader) | The header and commit, these are used for verification purposes. To find out more visit [light client docs](../light-client/README.md) | Must not be nil and adhere to the validation rules of [signedHeader](#signedheader) |
| ValidatorSet | [ValidatorSet](#validatorset) | The validatorSet is used to help with verify that the validators in that committed the infraction were truly in the validator set.     | Must not be nil and adhere to the validation rules of [validatorSet](#validatorset) |

The `SignedHeader` and `ValidatorSet` are linked by the hash of the validator set(`SignedHeader.ValidatorsHash == ValidatorSet.Hash()`.

## SignedHeader

The SignedhHeader is the [header](#header) accompanied by the commit to prove it.

| Name   | Type              | Description       | Validation                                                                        |
|--------|-------------------|-------------------|-----------------------------------------------------------------------------------|
| Header | [Header](#header) | [Header](#header) | Header cannot be nil and must adhere to the [Header](#header) validation criteria |
| Commit | [Commit](#commit) | [Commit](#commit) | Commit cannot be nil and must adhere to the [Commit](#commit) criteria            |

## ValidatorSet

| Name | Type | Description | Validation |
| --- | --- | --- | --- |
| Validators | Array of [validator](#validator) | List of the active validators at a specific height, sorted by ascending `ProTxHash` | The list of validators can not be empty or nil and must adhere to the validation rules of [validator](#validator) |
| Proposer | [validator](#validator) | Validator at the set's proposer index | The proposer index must be in range |
| ThresholdPublicKey | public key | Threshold public key of the quorum | Must pass `ThresholdPublicKeyValid` |
| QuorumType | int32 | LLMQ type of the quorum | |
| QuorumHash | slice of bytes (`[]byte`) | Hash of the quorum | Must pass `QuorumHashValid` |
| HasPublicKeys | bool | Whether validators carry public keys, which holds when the local node is in the set | When true, each public key must be valid |
| VotingPowerThreshold | uint64 | Minimum voting power required to commit; equals `ValidatorParams.voting_power_threshold` | |

## Validator

| Name | Type | Description | Validation |
| --- | --- | --- | --- |
| ProTxHash | [ProTxHash](#protxhash) | Validator's identity | Length must be of size 32 |
| PubKey | slice of bytes (`[]byte`) | Validator's BLS public key; absent when the local node is not in the set | Length must be 48 when validated |
| VotingPower | int64 | Validator's voting power | cannot be < 0 |
| NodeAddress | `ValidatorAddress` | URI of the validator's node | Must be valid when set |

## Address

Address is a type alias of a slice of bytes, calculated by hashing the public key using sha256 and truncating it to the first 20 bytes (`crypto.AddressHash`). Validators are identified by [ProTxHash](#protxhash), not by address.

## ConsensusParams

| Name      | Type                                | Description                                                                  | Field Number |
|-----------|-------------------------------------|------------------------------------------------------------------------------|--------------|
| block     | [BlockParams](#blockparams)         | Parameters limiting the size and gas of a block.                             | 1            |
| evidence  | [EvidenceParams](#evidenceparams)   | Parameters limiting the validity of evidence of byzantine behaviour.         | 2            |
| validator | [ValidatorParams](#validatorparams) | Parameters limiting the types of public keys validators can use.             | 3            |
| version   | [VersionParams](#versionparams)     | The ABCI application and consensus versions.                                 | 4            |
| synchrony | [SynchronyParams](#synchronyparams) | Bounds under which a proposal timestamp is considered timely.                | 5            |
| timeout   | [TimeoutParams](#timeoutparams)     | Timeouts of the consensus steps.                                             | 6            |
| abci      | [ABCIParams](#abciparams)           | Application Blockchain Interface options.                                    | 7            |

### BlockParams

| Name | Type | Description | Field Number |
| --- | --- | --- | --- |
| max_bytes | int64 | Max size of a block, in bytes. | 1 |
| max_gas | int64 | Max sum of `GasWanted` in a proposed block. NOTE: blocks that violate this may be committed if there are Byzantine proposers. It's the application's responsibility to handle this when processing a block! | 2 |

### EvidenceParams

| Name | Type | Description | Field Number |
| --- | --- | --- | --- |
| max_age_num_blocks | int64 | Max age of evidence, in blocks. | 1 |
| max_age_duration | [google.protobuf.Duration](https://developers.google.com/protocol-buffers/docs/reference/google.protobuf#google.protobuf.Duration) | Max age of evidence, in time. It should correspond with an app's "unbonding period" or other similar mechanism for handling [Nothing-At-Stake attacks](https://ethereum.org/en/developers/docs/consensus-mechanisms/pos/faqs/). | 2 |
| max_bytes | int64 | maximum size in bytes of total evidence allowed to be entered into a block | 3 |

### ValidatorParams

| Name | Type | Description | Field Number |
| --- | --- | --- | --- |
| pub_key_types | repeated string | List of accepted public key types. Uses same naming as `PubKey.Type`. | 1 |
| voting_power_threshold | uint64 (optional) | Minimum voting power required to commit a block. See [voting_power_threshold](#voting_power_threshold). | 2 |

#### voting_power_threshold

`voting_power_threshold` is the live commit gate: a block is finalized when accumulated precommit
power crosses this value, recovering the BLS threshold signature. With `DefaultDashVotingPower = 100`
per validator, a quorum of N members has total power `N * 100`.

Validation is type-aware and operates as follows:

1. **Recognized LLMQ type, threshold unset (0):** The threshold must equal the canonical value for
   the quorum type as defined by Dash Core (e.g. 8 for `DEVNET_PLATFORM`, a 12-member quorum with
   threshold 8). This is the normal production path and the fix for issue #1314, where the
   canonical 8-of-12 threshold was previously rejected by an overly strict >2/3 check.

2. **Unknown or custom quorum type, threshold unset (0):** The strict size-based floor applies —
   threshold must equal `totalPower` for 1–2 validators, `>= 2/3 * totalPower` for 3 validators,
   or `> 2/3 * totalPower` for 4+ validators.

3. **Threshold explicitly set (> 0):** Accepted as-is, provided `0 < threshold <= totalPower`.
   This is an operator override intended for single-node development or testing environments
   (introduced in #1052). The value is recorded on-chain for accountability.

**Operator footgun:** When the override is set, the only enforced bounds are `0 < threshold <=
totalPower` — there is no mandatory 2/3 floor. Setting a value below 2/3 of total power is
accepted but reduces Byzantine fault tolerance: for example, setting `voting_power_threshold = 1`
on a 12-member quorum means a single node can commit a block alone, which is unsafe for
production. Use this override only in controlled development or testing environments.

This parameter validates locally (`ValidateBasic`) and is not hashed into the consensus state, so
changes do not require an ABCI protocol bump and cannot fork a running chain.

### VersionParams

| Name        | Type   | Description                   | Field Number |
|-------------|--------|-------------------------------|--------------|
| app_version | uint64 | The ABCI application version. | 1            |
| consensus_version | `ConsensusVersion` | Version of the consensus protocol, selecting the [proposer strategy](../consensus/proposer-selection.md). | 2 |

### SynchronyParams

| Name | Type | Description | Field Number |
| --- | --- | --- | --- |
| message_delay | [google.protobuf.Duration](https://developers.google.com/protocol-buffers/docs/reference/google.protobuf#google.protobuf.Duration) | Bound for how long a proposal message may take to reach all validators on a newtork and still be considered valid. | 1 |
| precision | [google.protobuf.Duration](https://developers.google.com/protocol-buffers/docs/reference/google.protobuf#google.protobuf.Duration) | Bound for how skewed a proposer's clock may be from any validator on the network while still producing valid proposals. | 2 |

### TimeoutParams

| Name | Type | Description | Field Number |
| --------------- | -------- | ------------------------------- | -------------- |
| propose | [google.protobuf.Duration](https://developers.google.com/protocol-buffers/docs/reference/google.protobuf#google.protobuf.Duration) | Parameter that, along with propose_delta, configures the timeout for the propose step of the consensus algorithm. | 1 |
| propose_delta | [google.protobuf.Duration](https://developers.google.com/protocol-buffers/docs/reference/google.protobuf#google.protobuf.Duration) | Parameter that, along with propose, configures the timeout for the propose step of the consensus algorithm. | 2 |
| vote | [google.protobuf.Duration](https://developers.google.com/protocol-buffers/docs/reference/google.protobuf#google.protobuf.Duration) | Parameter that, along with vote_delta, configures the timeout for the prevote and precommit step of the consensus algorithm. | 3 |
| vote_delta | [google.protobuf.Duration](https://developers.google.com/protocol-buffers/docs/reference/google.protobuf#google.protobuf.Duration) | Parameter that, along with vote, configures the timeout for the prevote and precommit step of the consensus algorithm. | 4 |

### ABCIParams

| Name | Type | Description | Field Number |
| --- | --- | --- | --- |
| recheck_tx | bool | Indicates whether to run `CheckTx` on all remaining transactions *after* every execution of a block | 1 |

## Proof

| Name      | Type           | Description                                   | Field Number |
|-----------|----------------|-----------------------------------------------|--------------|
| total     | int64          | Total number of items.                        | 1            |
| index     | int64          | Index item to prove.                          | 2            |
| leaf_hash | bytes          | Hash of item value.                           | 3            |
| aunts     | repeated bytes | Hashes from leaf's sibling to a root's child. | 4            |
