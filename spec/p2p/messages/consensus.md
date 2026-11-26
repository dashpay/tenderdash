---
order: 7
---

# Consensus

Wire format of the messages exchanged by the consensus reactor. How a node
acts on them is specified in the [consensus protocol](../../consensus/consensus.md).

## Channel

Consensus has four separate channels. The channel identifiers are listed below.

| Name               | Number |
|--------------------|--------|
| StateChannel       | 32     |
| DataChannel        | 33     |
| VoteChannel        | 34     |
| VoteSetBitsChannel | 35     |

## Message Types

### Proposal

Proposal is sent when a new block is proposed. It is a suggestion of what the
next block in the blockchain should be.

| Name     | Type                                               | Description                            | Field Number |
|----------|----------------------------------------------------|----------------------------------------|--------------|
| proposal | [Proposal](../../core/data_structures.md#proposal) | Proposed Block to come to consensus on | 1            |

### Vote

Vote is sent to vote for some block (or to inform others that a process does not vote in the
current round). [Vote](../../core/data_structures.md#vote) contains the validator's
information (`ProTxHash` and index), height and round for which the vote is sent, vote type,
blockID if process vote for some block (`nil` otherwise), the block signature and, on a non-nil
precommit, the vote extensions. The message is signed by the validator private key.

| Name | Type                                       | Description               | Field Number |
|------|--------------------------------------------|---------------------------|--------------|
| vote | [Vote](../../core/data_structures.md#vote) | Vote for a proposed Block | 1            |

### BlockPart

BlockPart is sent when gossiping a piece of the proposed block. It contains height, round
and the block part.

| Name   | Type                                       | Description                            | Field Number |
|--------|--------------------------------------------|----------------------------------------|--------------|
| height | int64                                      | Height of corresponding block.         | 1            |
| round  | int32                                      | Round of voting to finalize the block. | 2            |
| part   | [Part](../../core/data_structures.md#part) | A part of the block.                   | 3            |

### NewRoundStep

NewRoundStep is sent for every step transition during the core consensus algorithm execution.
It is used in the gossip part of the Tenderdash protocol to inform peers about a current
height/round/step a process is in. It is also sent to each peer when the peer connects and
then periodically, at the `peer-query-maj23-sleep-duration` cadence, so that a peer whose
copy was lost in transit learns the current step without waiting for the next transition.
A receiver ignores a copy that does not advance the height, round or step it already holds
for the sender.

| Name                     | Type   | Description                            | Field Number |
|--------------------------|--------|----------------------------------------|--------------|
| height                   | int64  | Height of corresponding block          | 1            |
| round                    | int32  | Round of voting to finalize the block. | 2            |
| step                     | uint32 |                                        | 3            |
| seconds_since_start_time | int64  |                                        | 4            |
| last_commit_round        | int32  |                                        | 5            |

### NewValidBlock

NewValidBlock advertises a block-part download target: its height, round,
part-set header, and the parts the node already holds. `is_commit` identifies a
commit target. Consensus emits this event for a current-round polka or a verified
commit even when the proposal or complete block is not held. It is a gossip
announcement, not proof that the sender has processed a valid proposal. While the
node holds block parts for its round, the periodic per-peer NewRoundStep described
above is followed by a NewValidBlock for the same height and round.
See [valid-block bookkeeping](../../consensus/consensus-alternative-flows.md#valid-block-bookkeeping)
and [commit handling](../../consensus/consensus-commit.md).

| Name                  | Type                                                         | Description                            | Field Number |
|-----------------------|--------------------------------------------------------------|----------------------------------------|--------------|
| height                | int64                                                        | Height of corresponding block          | 1            |
| round                 | int32                                                        | Round of voting to finalize the block. | 2            |
| block_part_set_header | [PartSetHeader](../../core/data_structures.md#partsetheader) |                                        | 3            |
| block_parts           | BitArray                                                     |                                        | 4            |
| is_commit             | bool                                                         |                                        | 5            |

### ProposalPOL

ProposalPOL is sent when a previous block is re-proposed.
It is used to inform peers in what round the process learned for this block (ProposalPOLRound),
and what prevotes for the re-proposed block the process has.

| Name               | Type     | Description                   | Field Number |
|--------------------|----------|-------------------------------|--------------|
| height             | int64    | Height of corresponding block | 1            |
| proposal_pol_round | int32    |                               | 2            |
| proposal_pol       | bitarray |                               | 3            |

### HasVote

HasVote is sent to indicate that a particular vote has been received. It contains height,
round, vote type and the index of the validator that is the originator of the corresponding vote.

| Name   | Type                                                             | Description                            | Field Number |
|--------|------------------------------------------------------------------|----------------------------------------|--------------|
| height | int64                                                            | Height of corresponding block          | 1            |
| round  | int32                                                            | Round of voting to finalize the block. | 2            |
| type   | [SignedMsgType](../../core/data_structures.md#signedmsgtype)     |                                        | 3            |
| index  | int32                                                            |                                        | 4            |

### Commit

Commit is sent on the VoteChannel to deliver a threshold-signed commit for a block.

| Name   | Type                                       | Description                | Field Number |
|--------|--------------------------------------------|----------------------------|--------------|
| commit | [Commit](../../core/data_structures.md#commit) | Commit for a decided block | 1            |

### HasCommit

HasCommit is sent to indicate that a process holds the commit for a given height and round.

| Name   | Type  | Description                 | Field Number |
|--------|-------|-----------------------------|--------------|
| height | int64 | Height of the commit        | 1            |
| round  | int32 | Round the commit belongs to | 2            |

### VoteSetMaj23

VoteSetMaj23 is sent to indicate that a process has seen +2/3 votes for some BlockID.
It contains height, round, vote type and the BlockID.

| Name   | Type                                                             | Description                            | Field Number |
|--------|------------------------------------------------------------------|----------------------------------------|--------------|
| height | int64                                                            | Height of corresponding block          | 1            |
| round  | int32                                                            | Round of voting to finalize the block. | 2            |
| type   | [SignedMsgType](../../core/data_structures.md#signedmsgtype)     |                                        | 3            |
| block_id | [BlockID](../../core/data_structures.md#blockid)               |                                        | 4            |

### VoteSetBits

VoteSetBits is sent to communicate the bit-array of votes a process has seen for a given
BlockID. It contains height, round, vote type, BlockID and a bit array of
the votes a process has.

| Name     | Type                                                             | Description                            | Field Number |
|----------|------------------------------------------------------------------|----------------------------------------|--------------|
| height   | int64                                                            | Height of corresponding block          | 1            |
| round    | int32                                                            | Round of voting to finalize the block. | 2            |
| type     | [SignedMsgType](../../core/data_structures.md#signedmsgtype)     |                                        | 3            |
| block_id | [BlockID](../../core/data_structures.md#blockid)                 |                                        | 4            |
| votes    | BitArray                                                         | Votes the process has for the BlockID. | 5            |

### ProposerHeartbeat

ProposerHeartbeat is sent on the StateChannel by the proposer of a round that
waits for transactions, and relayed once by validators that verify it
(`CONSENSUS_VERSION_2`; see
[Proposer liveness](../../consensus/consensus-proposer-liveness.md#heartbeats)).

| Name      | Type                                             | Description           | Field Number |
|-----------|--------------------------------------------------|-----------------------|--------------|
| heartbeat | [ProposerHeartbeat](#proposerheartbeat-contents) | The signed heartbeat. | 1            |

#### ProposerHeartbeat contents

`tendermint.types.ProposerHeartbeat`:

| Name                 | Type                      | Description                                                                                                                      | Field Number |
|----------------------|---------------------------|----------------------------------------------------------------------------------------------------------------------------------|--------------|
| height               | int64                     | Height of the round.                                                                                                             | 1            |
| round                | int32                     | Round the sender proposes in.                                                                                                    | 2            |
| sequence             | uint64                    | Grows with every heartbeat of the round, counting from the signing time in milliseconds.                                         | 3            |
| proposer_pro_tx_hash | bytes                     | ProTxHash of the proposer.                                                                                                       | 4            |
| timestamp            | google.protobuf.Timestamp | Signing time.                                                                                                                    | 5            |
| signature            | bytes                     | Signature with the proposer's quorum key share ([Signing](../../consensus/signing.md#proposer-heartbeats-and-round-skip-votes)). | 6            |

### RoundSkipVote

RoundSkipVote is sent on the StateChannel by a validator that heard no
heartbeat from the proposer of its waiting round for the silence timeout, and
relayed by validators when they first count it and again on a later copy until
the round is certified (`CONSENSUS_VERSION_2`; see
[Collecting votes](../../consensus/consensus-proposer-liveness.md#collecting-votes-and-recovering-the-certificate)).

| Name | Type                                     | Description           | Field Number |
|------|------------------------------------------|-----------------------|--------------|
| vote | [RoundSkipVote](#roundskipvote-contents) | The signed skip vote. | 1            |

#### RoundSkipVote contents

`tendermint.types.RoundSkipVote`:

| Name                  | Type  | Description                                                                     | Field Number |
|-----------------------|-------|---------------------------------------------------------------------------------|--------------|
| height                | int64 | Height of the round to skip.                                                    | 1            |
| round                 | int32 | Round to skip.                                                                  | 2            |
| validator_pro_tx_hash | bytes | ProTxHash of the voting validator.                                              | 3            |
| validator_index       | int32 | Index of the voting validator in the validator set.                             | 4            |
| signature             | bytes | Signature share over the round's skip sign bytes, the same for every validator. | 5            |

### RoundSkipCertificate

RoundSkipCertificate is sent on the StateChannel by a node that recovered or
received a valid certificate, and resent by gossip to peers at its height that
have not sent one for that round or a later one (`CONSENSUS_VERSION_2`).

| Name        | Type                                                   | Description                 | Field Number |
|-------------|--------------------------------------------------------|-----------------------------|--------------|
| certificate | [RoundSkipCertificate](#roundskipcertificate-contents) | The round skip certificate. | 1            |

#### RoundSkipCertificate contents

`tendermint.types.RoundSkipCertificate`:

| Name                | Type  | Description                                                                                                                                                         | Field Number |
|---------------------|-------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------|--------------|
| height              | int64 | Height of the skipped round.                                                                                                                                        | 1            |
| round               | int32 | Skipped round.                                                                                                                                                      | 2            |
| quorum_hash         | bytes | Quorum that signed.                                                                                                                                                 | 3            |
| threshold_signature | bytes | Quorum threshold signature recovered from the skip votes of +2/3 of the validator set (the mainnet default; the quorum type and `VotingPowerThreshold` can change it). | 4            |

### Message

Message is a [`oneof` protobuf type](https://developers.google.com/protocol-buffers/docs/proto#oneof).

| Name | Type | Description | Field Number |
| --- | --- | --- | --- |
| new_round_step | [NewRoundStep](#newroundstep) | | 1 |
| new_valid_block | [NewValidBlock](#newvalidblock) | | 2 |
| proposal | [Proposal](#proposal) | | 3 |
| proposal_pol | [ProposalPOL](#proposalpol) | | 4 |
| block_part | [BlockPart](#blockpart) | | 5 |
| vote | [Vote](#vote) | | 6 |
| has_vote | [HasVote](#hasvote) | | 7 |
| vote_set_maj23 | [VoteSetMaj23](#votesetmaj23) | | 8 |
| vote_set_bits | [VoteSetBits](#votesetbits) | | 9 |
| commit | [Commit](#commit) | | 10 |
| has_commit | [HasCommit](#hascommit) | | 11 |
| proposer_heartbeat | [ProposerHeartbeat](#proposerheartbeat) | | 12 |
| round_skip_vote | [RoundSkipVote](#roundskipvote) | | 13 |
| round_skip_certificate | [RoundSkipCertificate](#roundskipcertificate) | | 14 |
