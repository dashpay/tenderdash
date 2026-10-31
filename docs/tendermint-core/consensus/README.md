---
order: 1
parent:
  title: Consensus
  order: 6
---

# Consensus

Tenderdash consensus is a distributed protocol executed by validator processes
to agree on the next block to be added to the blockchain. It proceeds in
rounds; in each round a proposer suggests a block with a `Proposal`, and
validators vote on it in two phases (`Prevote`, `Precommit`). When precommits
for a block reach the quorum threshold, their BLS signature shares are
recovered into a single threshold signature that forms the block's `Commit`.

The protocol is specified, from the implementation, in the
[consensus specification](../../../spec/consensus/consensus.md):

- [Primary flow of a height](../../../spec/consensus/consensus-happy-path.md)
- [Transitions and decision rules](../../../spec/consensus/consensus-transitions.md)
- [Alternative flows and edge cases](../../../spec/consensus/consensus-alternative-flows.md)
  (including [proposal timestamps](../../../spec/consensus/consensus-alternative-flows.md#proposal-timestamps-and-time-rules)
  and [message gossip to non-validators](../../../spec/consensus/consensus-alternative-flows.md#non-validators-and-signer-problems))
- [Commit handling and gossip](../../../spec/consensus/consensus-commit.md)
- [Recovery and synchronisation](../../../spec/consensus/consensus-recovery.md)
- [Known issues and discrepancies](../../../spec/consensus/consensus-known-issues.md)

The wire format of consensus messages is in
[Consensus messages](../../../spec/p2p/messages/consensus.md); the timestamp
algorithm in [Proposer-Based Timestamps](../../../spec/consensus/proposer-based-timestamp/README.md).
Operational guidance for timestamp problems is in the
[PBTS runbook](../../tools/debugging/proposer-based-timestamps-runbook.md).
