---
order: 9
---

# Proposer Selection Procedure

This document specifies how Tenderdash chooses the proposer of a round, as
implemented in `internal/consensus/versioned/selectproposer`. Tenderdash does
**not** use Tendermint's priority-queue (weighted round-robin) algorithm: all
validators of a Dash quorum have equal voting power, and the proposer is chosen
by rotating an index over the validator set.

## Requirements

- **R1 Determinism.** For a validator set `V`, every honest validator computes
  the same `proposer(h, r)`.
- **R2 Fairness.** Within a stable validator set every validator proposes
  equally often over `|V|` consecutive elections (equal power).

Both follow from the construction below.

## Validator order

`ValidatorSet.Validators` is sorted by `ProTxHash` ascending
(`ValidatorsByProTxHashes`, applied in `NewValidatorSet` and
`updateWithChangeSet`, `types/validator_set.go`). The set keeps a
`proposerIndex`; `ValidatorSet.Proposer()` returns `Validators[proposerIndex]`,
`SetProposer(proTxHash)` sets the index to that validator, and
`IncProposerIndex(n)` adds `n` (possibly negative) modulo `|V|`.

## Two versions

`selectproposer.NewProposerSelector` (`proposer_selector.go`) chooses the
strategy by `ConsensusParams.Version.ConsensusVersion`:

| `ConsensusVersion` | Strategy | File |
|--------------------|----------|------|
| 0 (default) | **Height-based**: the index advances once per height; the round offset is derived without being persisted | `height_proposer.go` (`heightProposerSelector`) |
| 1 | **Height-and-round-based**: the index advances once per height and once per round | `height_round_proposer.go` (`heightRoundProposerSelector`) |

Changing the version is a consensus-parameter update and takes effect at the
next height ([Validator-set and parameter changes](./consensus-alternative-flows.md#validator-set-and-parameter-changes)).

## When the index is computed

`StateData.updateToState` creates a **new** selector at every height with
`NewProposerSelector(params, Validators, h, 0, blockStore)`. Whenever the
block store is non-empty and contains `h − 1` or `h`, the constructor derives
the index from the store (`proposerFromStore`, below); it trusts the
validator set's own `proposerIndex` only when there is no usable store (e.g.
genesis). During the height, `StateData.updateRoundStep` calls
`ProposerSelector.UpdateHeightRound(h, r)` on every step change and
`GetProposer(h, r)` answers from that state.

Because every node derives the index from the same stored block at the start
of every height, determinism (R1) does not depend on nodes having tracked the
same rounds.

### `proposerFromStore(h, r)`

Let `V` be the validator set for height `h`, sorted by `ProTxHash`, and
`meta(x)` the stored block metadata for height `x` (`Header.ProposerProTxHash`
is the proposer of the round in which the block was **first** proposed — a
re-proposed block keeps its header — and `meta.Round` is the round in which
it was committed).

| Case | v0 (`height_proposer.go`) | v1 (`height_round_proposer.go`) |
|------|---------------------------|---------------------------------|
| `h ∈ {0, 1}` | `V[0]` | `V[0]` |
| `meta(h)` stored, `meta(h).ValidatorsHash == hash(V)` | `idx(meta(h).Proposer) − meta(h).Round` | `idx(meta(h).Proposer) + r − meta(h).Round` |
| `meta(h)` stored, hash differs | error | error |
| only `meta(h − 1)` stored, `meta(h − 1).ValidatorsHash == hash(V)` | `idx(meta(h − 1).Proposer) + 1 − meta(h − 1).Round` | `idx(meta(h − 1).Proposer) + 1 + r` |
| only `meta(h − 1)` stored, hash differs (**quorum rotation**) | `1` ("index 1 due to a bug in the original code that causes the first validator to never propose; preserved to avoid breaking consensus") | `0 + r` |
| neither stored | error | error |

All indices are modulo `|V|`. For the height in progress only the
`meta(h − 1)` rows apply.

### Within a height

- **v0** (`heightProposerSelector.GetProposer`): `proposer(h, r) = V[(idx₀ + r) mod |V|]`
  where `idx₀` is the stored round-0 index; the per-round advance is applied
  to a copy and never persisted. `UpdateHeightRound` ignores the round.
- **v1** (`heightRoundProposerSelector.updateScores`): at the same height
  `IncProposerIndex(r' − r)`; so `proposer(h, r) = V[(idx₀ + r) mod |V|]` as
  well, with the difference that the round offset is persisted in the
  selector and, in the `meta(h)` row above, the commit round is subtracted.

Both versions therefore rotate one validator per round. They differ in how
the round-0 index of height `h` relates to height `h − 1`:

- **v0:** `idx₀(h) = idx(meta(h − 1).Proposer) − meta(h − 1).Round + 1` —
  the first proposer of the previous block, rewound by the previous commit
  round, plus one. When the previous block was first proposed at round `p`
  and committed at round `c > p` (a re-proposal), this is `p − c + 1`
  positions after the previous round-0 proposer rather than one.
- **v1:** `idx₀(h) = idx(meta(h − 1).Proposer) + 1` — the validator after
  the first proposer of the previous block, independent of the commit round.

## Validator set changes

A new validator set (new `QuorumHash`) replaces the old one at the next
height; `meta(h − 1).ValidatorsHash` then differs from the new set's hash and
the **quorum rotation** row applies. Within one quorum, additions and
removals are applied as a change set and the set is re-sorted by `ProTxHash`
(`ValidatorSet.updateWithChangeSet`); since the index is recomputed from the
stored proposer's position in the *new* set at the next height, the rotation
continues from that validator.

## Use by consensus

- `EnterProposeAction.Execute` decides whether to propose by comparing the
  node's pro-tx-hash with `GetProposer(h, r)`.
- `Proposaler.verifyProposal` verifies a proposal's signature against
  `GetProposer(h, r)`'s key only.
- `StateData.isValidForPrevote` requires `Header.ProposerProTxHash` to equal
  `GetProposer(h, r)` for proposals with `POLRound = -1`.
