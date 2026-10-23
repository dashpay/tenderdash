---
order: 1
parent:
  title: Consensus
  order: 4
---

# Consensus

Specification of the Tenderdash consensus protocol, derived from the
implementation in `internal/consensus`.

## Contents

- [Consensus Protocol](./consensus.md) — overview, terminology, system model,
  round state, step state machine and invariants; the entry point.
    - [Primary flow of a height](./consensus-happy-path.md)
    - [Transitions and decision rules](./consensus-transitions.md)
    - [Alternative flows and edge cases](./consensus-alternative-flows.md)
    - [Commit handling](./consensus-commit.md)
    - [Recovery and synchronisation](./consensus-recovery.md)
    - [Known issues and discrepancies](./consensus-known-issues.md)
- [Proposer Selection](./proposer-selection.md) — which validator proposes at
  a given height and round.
- [Signing](./signing.md) — sign bytes, request IDs and double-signing rules
  for proposals and votes.
- [Write Ahead Log](./wal.md) — the log the consensus state machine uses to
  recover from crashes.
- [Evidence](./evidence.md) — detection and handling of misbehaviour.
- [Proposer-Based Timestamps](./proposer-based-timestamp/README.md) — the
  timestamp algorithm behind the timeliness rules.
- [BFT Time](./bft-time.md) — historical: the median-based timestamp rule
  Tenderdash no longer uses.
- [Consensus Paper](./consensus-paper/README.md) — the Tendermint consensus
  paper ([arxiv](https://arxiv.org/abs/1807.04938)) with proofs of safety and
  termination for the algorithm the implementation descends from.
- [Light Client Protocol](./light-client/README.md) — deprecated pointers to
  the light-client specification.

The gossip of consensus messages between peers is summarised in
[Commit handling](./consensus-commit.md#commit-gossip) and
[Non-validators](./consensus-alternative-flows.md#non-validators-and-signer-problems);
the wire format is in [Consensus messages](../p2p/messages/consensus.md).
