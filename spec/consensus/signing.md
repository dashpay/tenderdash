# Validator Signing

Here we specify the rules for validating a proposal and vote before signing.
When a validator signs each message, and what it signs, is specified by the
[consensus protocol](./consensus.md).
First we include some general notes on validating data structures common to both types.
We then provide specific validation rules for each. Finally, we include validation rules to prevent double-signing.

## SignedMsgType

The `SignedMsgType` is a single byte that refers to the type of the message
being signed. It is defined in Go as follows:

```go
// SignedMsgType is a type of signed message in the consensus.
type SignedMsgType byte

const (
 // Votes
 PrevoteType   SignedMsgType = 0x01
 PrecommitType SignedMsgType = 0x02

 // Proposals
 ProposalType SignedMsgType = 0x20
)
```

All signed messages must correspond to one of these types.

## Timestamp

Votes carry no timestamp. A proposal's `Timestamp` must equal the proposed
block's `Header.Time`, which the proposer takes from its local clock (the
genesis time for the initial block); validators prevote the block only if the
proposal is *timely* with respect to its receive time and the chain's
`SynchronyParams`. The rules are specified in
[Proposal timestamps and time rules](./consensus-alternative-flows.md#proposal-timestamps-and-time-rules)
and the prevote decision in
[Transitions and decision rules](./consensus-transitions.md#prevote-decision-prevoterdo).

## ChainID

ChainID is an unstructured string with a max length of 50-bytes.
In the future, the ChainID may become structured, and may take on longer lengths.
For now, it is recommended that signers be configured for a particular ChainID,
and to only sign votes and proposals corresponding to that ChainID.

## BlockID

`BlockID` contains the block-header hash, `PartSetHeader` (part count and Merkle
root of serialized block parts), and the state-ID hash. A nil vote has all three
empty. A complete ID has 32-byte block, part-set and state hashes and a positive
part count. Proposals require a complete ID; votes allow nil or complete IDs.
See [BlockID](../core/data_structures.md#blockid) and the `StateID` schema below.

## Proposals

The structure of a proposal for signing looks like:

```go
type CanonicalProposal struct {
 Type      SignedMsgType // type alias for byte
 Height    int64         `binary:"fixed64"`
 Round     int64         `binary:"fixed64"`
 POLRound  int64         `binary:"fixed64"`
 BlockID   CanonicalBlockID
 Timestamp time.Time
 ChainID   string
}
```

`CanonicalBlockID` contains the block hash and canonical part-set header; it
excludes `BlockID.StateID`. The proposal signature also excludes
`CoreChainLockedHeight`. The exact signature-covered fields and replacement of
unsigned fields from assembled block bytes are specified in
[Creating the proposal](./consensus-happy-path.md#3a-creating-the-proposal-proposer-only)
and [Receiving the proposal and block](./consensus-happy-path.md#4-receiving-the-proposal-and-block).

The basic structural checks on the proposal before canonicalization include:

```go
p.Type == 0x20
p.Height > 0
p.Round >= 0
p.POLRound >= -1
p.BlockID.IsComplete()
```

In other words, a proposal is valid for signing if it contains the type of a Proposal
(0x20), has a positive, non-zero height, a
non-negative round, a POLRound not less than -1, and a complete BlockID.

## Votes

Sign bytes for votes are defined as `CanonicalVote` struct.

The canonical message is encoded using Protobuf. Its checksum is one input to
the quorum signing digest, not the complete digest verified by BLS; see
[Block signature verification on light client](#block-signature-verification-on-light-client).
The schema is:

```go
message CanonicalVote {
  SignedMsgType type   = 1;  // type alias for byte
  sfixed64      height = 2;
  sfixed64      round  = 3;
  // block_id is a checksum (sha256) of CanonicalBlockID for the block being voted on
  bytes block_id = 4 [(gogoproto.customname) = "BlockID"];
  // state_id is a checksum  (sha256) of StateID for the block being voted on
  bytes  state_id = 5 [(gogoproto.customname) = "StateID"];
  string chain_id = 99 [(gogoproto.customname) = "ChainID"];
}
```

`block_id` is a sha256 checksum of Protobuf-encoded `CanonicalBlockID` message:

```go
message CanonicalBlockID {
  bytes                  hash            = 1;
  CanonicalPartSetHeader part_set_header = 2 [(gogoproto.nullable) = false];
}

message CanonicalPartSetHeader {
  uint32 total = 1;
  bytes  hash  = 2;
}
```

`state_id` is a sha256 checksum of Protobuf-encoded [`StateID` message](../../proto/tendermint/types/types.proto#L70-L80):

```go
message StateID {
  // AppVersion used when generating the block, equals to Header.Version.App.
  fixed64 app_version = 1 [(gogoproto.customname) = "AppVersion"];
  // Height of block containing this state ID.
  fixed64 height = 2;
  // AppHash used in current block, equal to Header.AppHash. 32 bytes.
  bytes app_hash = 3 [(gogoproto.customname) = "AppHash"];
  // CoreChainLockedHeight for the block, equal to Header.CoreChainLockedHeight.
  fixed32 core_chain_locked_height = 4 [(gogoproto.customname) = "CoreChainLockedHeight"];
  // Time of the block.
  google.protobuf.Timestamp time = 5 [(gogoproto.nullable) = false];
}
```

A vote is valid if each of the following lines evaluates to true for vote `v`:

```go
v.Type == 0x1 || v.Type == 0x2
v.Height > 0
v.Round >= 0
v.BlockID.IsNil() || v.BlockID.IsComplete()
```

In other words, a vote is valid for signing if it contains the type of a Prevote
or Precommit (0x1 or 0x2, respectively), has a positive, non-zero height, a
non-negative round, and an empty or valid BlockID.

### Block signature verification on light client

The block signature in a commit is threshold-recovered from precommit shares
and verified with the quorum's `ThresholdPublicKey`. Its signing context includes
`ChainID`, the commit height and round, full `BlockID`, `QuorumType`, and
`QuorumHash`.

Use `MakeBlockSignItem` or `VoteBlockSignID` in
[`types/quorum_sign_data.go`](../../types/quorum_sign_data.go) and
[`types/vote.go`](../../types/vote.go) to construct the digest:

1. Build the precommit's canonical vote, including the canonical-block-ID checksum
   and state-ID hash.
2. `MakeBlockSignItem` hashes the canonical vote sign bytes and derives
   `BlockRequestID(height, round)` using the `dpbvote` domain.
3. `SignItem.UpdateSignHash` uses the quorum type, quorum hash, request ID and
   message hash to construct the Dash BLS signing hash, with the helper's byte-order
   conversions. Verify the signature against that digest and the threshold key.

Proposal signing uses `ProposalBlockSignID` and its own `dpproposal` request-ID
domain, likewise bound to height, round and quorum context
([`types/proposal.go`](../../types/proposal.go)). Vote extensions have separate
sign items; application-supplied raw-extension request IDs need not bind height
and round. See [Vote extensions](./consensus-alternative-flows.md#vote-extensions).

This describes digest construction, not a complete light-client trust algorithm.
Full-node commit acceptance also verifies every extension threshold signature,
checks the held block's complete ID, and requires the application's verdict on
the ordered commit extension vector; see
[Commit verification](./consensus-commit.md#commit-verification) and
[Commit validity depends on the application](./consensus-commit.md#commit-validity-depends-on-the-application).
A valid block signature does not authenticate that vector's count and order.

## Invalid Votes and Proposals

Basic structure, signature verification and consensus admissibility are separate
checks. Proposal verification failures do not automatically evict their sender;
received commits evict only on `ErrInvalidCommitSignature`, and replay never
evicts. Budget exhaustion is a local processing limit, not proof of peer fault.
The exact policies are in [Proposal acceptance](./consensus-transitions.md#proposal-acceptance-proposalerset),
[Vote admission](./consensus-transitions.md#admission-addvotevalidatevotemw-heightvotesetaddvote-votesetaddvote),
and [Commit verification](./consensus-commit.md#commit-verification).

## Double Signing

Signers must be careful not to sign conflicting messages, also known as "double signing" or "equivocating".
Tenderdash has mechanisms to publish evidence of validators that signed conflicting votes, so they can be punished
by the application. Note Tenderdash does not currently handle evidence of conflicting proposals, though it may in the future.

### State

`FilePV` persists height, round, internal signing step, block sign bytes and
block signature (`FilePVLastSignState` in [`privval/file.go`](../../privval/file.go)).
Its ordered steps are proposal, prevote and precommit; these are not the numeric
`SignedMsgType` values. `checkHRS` rejects regressions and permits a matching
height/round/step only for comparison with the persisted sign bytes.

### Proposals

A new proposal signing request must pass the height/round/step checks. Conflicting
canonical bytes at the same height/round/proposal step are refused. The current
repeated-proposal return path has a documented
[signature-field discrepancy](./consensus-known-issues.md#filepv-repeated-proposal-returns-sign-bytes-as-the-signature);
it must not be assumed to return a valid reused signature.

### Votes

For a new height or round, or a later internal step in the same height/round,
`FilePV` can sign and persist the vote. For identical block sign bytes at an
already-signed height/round/step, it reuses the stored block signature; different
bytes are refused. Precommit extension signatures are regenerated, since the
extensions are not part of the canonical block vote.

Nil and non-nil block IDs are different sign bytes: a signer cannot replace its
nil vote with a block vote of the same type at the same height and round.
The block-signature reuse rule permits recovery of an identical vote, not a second
conflicting vote. See [WAL catch-up](./consensus-recovery.md#start-up-wal-catch-up).
`DashCoreSignerClient` does not maintain FilePV's persisted state inside
Tenderdash; signer-side protection must not be inferred from the presence of the
client alone.

### Other Rules

Signer height/round/step protection does not establish lock eligibility. The
consensus state machine decides whether a block may be prevoted or precommitted;
see [Locking](./consensus-alternative-flows.md#locking) and the
[precommit decision](./consensus-transitions.md#precommit-decision-enterprecommitactionexecute).
A nil polka does not unlock within a height, and relocking a block requires this
round's proposal and application processing context.

Historical Tendermint discussions call unjustified lock changes “amnesia”; see
the original [fork-detection discussion](https://github.com/tendermint/tendermint/pull/3978).
That history does not introduce an unlock transition or additional signer
protection into the current implementation.
