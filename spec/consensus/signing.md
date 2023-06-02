# Validator Signing

Here we specify the rules for validating a proposal and vote before signing.
First we include some general notes on validating data structures common to both types.
We then provide specific validation rules for each. Finally, we include validation rules to prevent double-sigining.

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

Timestamp validation is subtle and there are currently no bounds placed on the
timestamp included in a proposal or vote. It is expected that validators will honestly
report their local clock time. The median of all timestamps
included in a commit is used as the timestamp for the next block height.

Timestamps are expected to be strictly monotonic for a given validator, though
this is not currently enforced.

## ChainID

ChainID is an unstructured string with a max length of 50-bytes.
In the future, the ChainID may become structured, and may take on longer lengths.
For now, it is recommended that signers be configured for a particular ChainID,
and to only sign votes and proposals corresponding to that ChainID.

## BlockID

BlockID is the structure used to represent the block:

```go
type BlockID struct {
	Hash          []byte
	PartSetHeader PartSetHeader
	StateID       []byte
}

type PartSetHeader struct {
 Hash  []byte
 Total int
}

type StateID struct {
	AppVersion            uint64
	Height                uint64 
	AppHash               []byte
	CoreChainLockedHeight uint32 
	Time                  int64
}
```

To be included in a valid vote or proposal, BlockID must either represent a `nil` block, or a complete one.
We introduce two methods, `BlockID.IsNil()` and `BlockID.IsComplete()` for these cases, respectively.

`BlockID.IsNil()` returns true for BlockID `b` if each of the following
are true:

```go
b.Hash == nil
b.PartsHeader.Total == 0
b.PartsHeader.Hash == nil
b.StateID == nil
```

`BlockID.IsComplete()` returns true for BlockID `b` if each of the following
are true:

```go

len(b.Hash) == 32
b.PartsHeader.Total > 0
len(b.PartsHeader.Hash) == 32
len(b.StateID) == 32
```

## Proposals

The structure of a proposal for signing looks like:

```go
type CanonicalProposal struct {
 Type      SignedMsgType // type alias for byte
 Height    int64         `binary:"fixed64"`
 Round     int64         `binary:"fixed64"`
 POLRound  int64         `binary:"fixed64"`
 BlockID   BlockID
 Timestamp time.Time
 ChainID   string
}
```

A proposal is valid if each of the following lines evaluates to true for proposal `p`:

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

`CanonicalVoteID` is a sha256 checksum of `CanonicalVote` encoded using **protobuf encoding**:

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

`block_id` is a sha256 checksum of Protobuf-encoded [`CanonicalBlockID` message:

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

Block signature is threshold-recovered signature of commit votes.

In a typical light client use case, light client wants to verify
app state using vote signature. Data needed to do the verification is:

* Chain ID
* Commit information:
    * Type (constant, always Precommit)
    * Height
    * Round
* Hash of CanonicalBlockID
* State ID data:
    * AppVersion
    * Height
    * AppHash
    * CoreChainLockedHeight
    * Time

Verification algorithm can be described as follows:

1. Build `StateID` message and encode it using Protobuf encoding.
2. Calculate checksum (SHA256) of encoded `StateID`.
3. Retrieve or calculate SHA256 checksum of `CanonicalBlockID`
4. Build `CanonicalVote` message and encode it using Protobuf.
5. Calculate SHA256 checksum of encoded `CanonicalVote`.
6. Verify that block signature matches calculated checksum.

## Invalid Votes and Proposals

Votes and proposals which do not satisfy the above rules are considered invalid.
Peers gossipping invalid votes and proposals may be disconnected from other peers on the network.
Note, however, that there is not currently any explicit mechanism to punish validators signing votes or proposals that fail
these basic validation rules.

## Double Signing

Signers must be careful not to sign conflicting messages, also known as "double signing" or "equivocating".
Tendermint has mechanisms to publish evidence of validators that signed conflicting votes, so they can be punished
by the application. Note Tendermint does not currently handle evidence of conflciting proposals, though it may in the future.

### State

To prevent such double signing, signers must track the height, round, and type of the last message signed.
Assume the signer keeps the following state, `s`:

```go
type LastSigned struct {
 Height int64
 Round int64
 Type SignedMsgType // byte
}
```

After signing a vote or proposal `m`, the signer sets:

```go
s.Height = m.Height
s.Round = m.Round
s.Type = m.Type
```

### Proposals

A signer should only sign a proposal `p` if any of the following lines are true:

```go
p.Height > s.Height
p.Height == s.Height && p.Round > s.Round
```

In other words, a proposal should only be signed if it's at a higher height, or a higher round for the same height.
Once a proposal or vote has been signed for a given height and round, a proposal should never be signed for the same height and round.

### Votes

A signer should only sign a vote `v` if any of the following lines are true:

```go
v.Height > s.Height
v.Height == s.Height && v.Round > s.Round
v.Height == s.Height && v.Round == s.Round && v.Step == 0x1 && s.Step == 0x20
v.Height == s.Height && v.Round == s.Round && v.Step == 0x2 && s.Step != 0x2
```

In other words, a vote should only be signed if it's:

* at a higher height
* at a higher round for the same height
* a prevote for the same height and round where we haven't signed a prevote or precommit (but have signed a proposal)
* a precommit for the same height and round where we haven't signed a precommit (but have signed a proposal and/or a prevote)

This means that once a validator signs a prevote for a given height and round, the only other message it can sign for that height and round is a precommit.
And once a validator signs a precommit for a given height and round, it must not sign any other message for that same height and round.

Note this includes votes for `nil`, ie. where `BlockID.IsNil()` is true. If a
signer has already signed a vote where `BlockID.IsNil()` is true, it cannot
sign another vote with the same type for the same height and round where
`BlockID.IsComplete()` is true. Thus only a single vote of a particular type
(ie. 0x01 or 0x02) can be signed for the same height and round.

### Other Rules

According to the rules of Tendermint consensus, once a validator precommits for
a block, they become "locked" on that block, which means they can't prevote for
another block unless they see sufficient justification (ie. a polka from a
higher round). For more details, see the [consensus
spec](https://arxiv.org/abs/1807.04938).

Violating this rule is known as "amnesia". In contrast to equivocation,
which is easy to detect, amnesia is difficult to detect without access to votes
from all the validators, as this is what constitutes the justification for
"unlocking". Hence, amnesia is not punished within the protocol, and cannot
easily be prevented by a signer. If enough validators simultaneously commit an
amnesia attack, they may cause a fork of the blockchain, at which point an
off-chain protocol must be engaged to collect votes from all the validators and
determine who misbehaved. For more details, see [fork
detection](https://github.com/tendermint/tendermint/pull/3978).
