---
order: 4
title: Tenderdash's expected behavior
---

# Tenderdash's expected behavior

## Valid method call sequences

Tenderdash initiates ABCI requests and the Application responds. The
[consensus protocol](../consensus/consensus.md) specifies the implemented order,
including guards, quorum/recovery thresholds and failure paths. Its
[system model](../consensus/consensus.md#system-model-as-enforced-by-the-code)
states the safety assumptions; a configurable threshold must not be assumed to
satisfy the strict BFT floor in every supported configuration.

The [primary flow](../consensus/consensus-happy-path.md) describes a height that
succeeds in round zero. Its ordinary application calls are:

| Role or purpose | Calls |
| --- | --- |
| Proposer creating a fresh block | `PrepareProposal` supplies transactions, same-block application/results hashes and next-state changes. A reused valid block does not require another `PrepareProposal`. |
| Processing before voting or commit application | `ProcessProposal` for the block and the round being checked, unless that exact result is already current. |
| Validator signing a non-nil precommit, from consensus version 2 | `ApplyBlockRandomness` with the beacon recovered from the round's Prevotes, before `ExtendVote`. Before version 2 it is never called. |
| Validator signing a non-nil precommit | `ExtendVote` after processing for that round. Nil precommits and prevotes carry no extensions. |
| Peer non-nil precommit | `VerifyVoteExtension` with the validator's pro-tx-hash, after signature verification. |
| Commit, on any full node | `VerifyVoteExtension` with an empty validator pro-tx-hash, after processing for the commit round. |
| Commit, on any full node, from consensus version 2 | `ApplyBlockRandomness` with the commit's beacon, before verifying the commit and again before `FinalizeBlock`. |
| Accepted decided block | `FinalizeBlock` persists the candidate corresponding to that block and round. |

These are not exact per-height callback counts. A node can finish on a commit
without receiving every individual precommit. `ProcessProposal` can run again for
the same block/round after a different round has replaced the Application's
execution context. Commit checks can process a foreign round and then restore the
node's own round before checking an individual precommit. See
[Commit handling](../consensus/consensus-commit.md) and
[ABCI calls per height](../consensus/consensus-happy-path.md#abci-calls-per-height-on-the-happy-path).

After commit-level `VerifyVoteExtension` rejects a vector, Tenderdash can call
`ExtendVote` for that processed block and commit round to learn the expected
vector. This call can occur on an observer and does not sign or broadcast a vote.
A replacement commit can require further verification and processing. Rejection of
a held-block commit does not itself advance the round; a future-round commit whose
block is absent can already advance the round before application validation.
The full branch rules are in
[Commits received from peers](../consensus/consensus-commit.md#commits-received-from-peers)
and the [VerifyVoteExtension contract](methods.md#verifyvoteextension).

An Application must handle these repeated calls deterministically for the block,
round, and last committed state. It must not treat an abbreviated normal-round
sequence as an at-most-once guarantee. A successfully completed live height calls
`FinalizeBlock` only after accepting the commit vector. Rejection while replaying
an already-stored commit during startup instead terminates replay before
`FinalizeBlock`; see [Recovery and synchronisation](../consensus/consensus-recovery.md).

### Startup and methods outside block execution

Startup uses `Info` to reconcile application and Tenderdash state and `InitChain`
when the Application is at height zero; the exact cases, replay calls and crash
windows are in [ABCI handshake](../consensus/consensus-recovery.md#start-up-abci-handshake).
Snapshot restoration can attempt `OfferSnapshot`/`ApplySnapshotChunk` before a
successful restore and `Info`; these are synchronization boundaries rather than
ordinary voting rounds.

`CheckTx` controls mempool admission separately from the block execution sequence.
`Query`, `ListSnapshots` and `LoadSnapshotChunk` read application state or existing
snapshots. `Echo` and `Flush` serve client/debugging purposes; from consensus version 2 a
waiting proposer also uses `Echo` as a readiness probe
([Echo](./methods.md#echo)). `Info` can also serve
RPC queries independently of startup. These methods are not bounded by the normal
height's consensus callback count.

## Application execution requirements

Tenderdash requires same-block execution: `ResponsePrepareProposal` and
`ResponseProcessProposal` supply the current block's application hash and
transaction results before it is decided. Returning only a boolean acceptance or
waiting until `FinalizeBlock` to compute those commitments is insufficient.
Use the current [method schemas and requirements](methods.md).

Keep speculative execution as candidate state; publish and persist it only when
`FinalizeBlock` selects that block and the round processed last. There is no ABCI
`Commit` method; persistence belongs in `FinalizeBlock`.
`ResponseFinalizeBlock` has only `retain_height` and
`propose_next_block_immediately`; it returns neither `AppHash`, transaction results,
nor events. Block/transaction events belong to `ProcessProposal` results; see
[Events](basic-concepts.md#events).
From consensus version 2, the state `FinalizeBlock` persists also has the block's
randomness applied, and its root is the app hash `ApplyBlockRandomness` returned,
which the commit carries. An Application that uses no randomness returns the app hash
`ProcessProposal` returned for the block and round, without changing any state.

An Application returning no vote extensions must accept an empty expected commit
vector and reject a nonempty one at commit-level verification. Other Applications
must compare the ordered threshold-recoverable subset they expect for that block
and round; independently valid signatures do not establish the vector's identity,
count and order. See [VerifyVoteExtension](methods.md#verifyvoteextension).
