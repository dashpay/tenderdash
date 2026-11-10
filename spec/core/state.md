# State

The committed `State` in [`internal/state/state.go`](../../internal/state/state.go)
contains the information needed to validate the next block. It is local state,
not a network message or a hash of the whole consensus state. Header commitments
and the signed `StateID` have their own definitions in
[Data Structures](./data_structures.md).

| Fields | Meaning |
| --- | --- |
| `ChainID`, `InitialHeight` | Chain identity and first block height; the initial height is normally 1 and must be positive. |
| `LastBlockHeight`, `LastBlockRound`, `LastBlockID`, `LastBlockTime` | Last committed block and its commit round. |
| `LastCoreChainLockedBlockHeight` | Last committed core chain lock height; unchanged when a block carries no new lock. |
| `Validators`, `LastValidators` | Active validators for the next block, and the previous set used to verify its `LastCommit`. |
| `ConsensusParams` | Parameters used to validate the next block. |
| `LastAppHash`, `LastResultsHash` | Application state and transaction-result commitments of the last committed block. |
| `LastHeightValidatorsChanged`, `LastHeightConsensusParamsChanged` | Heights at which changes become active. |
| `Version` | Consensus block/application versions and software version. |

The ephemeral [round state](../consensus/consensus.md#round-state) is separate
from this committed state. Pending next validators and parameters are held in
`CurrentRoundState`, not a committed `State.NextValidators` field.

## Execution

Tenderdash uses same-block execution: `PrepareProposal` and `ProcessProposal`
return the application hash and transaction results for the block being proposed.
`FinalizeBlock` persists the accepted candidate state. There is no ABCI `Commit`
call, and `ResponseFinalizeBlock` does not return an application hash.

The authoritative ordering of validation, block/WAL writes, `FinalizeBlock`,
mempool/evidence updates and state persistence is
[Commit and apply](../consensus/consensus-happy-path.md#9-commit-and-apply).
Validator-set and parameter changes from block `h` take effect at `h + 1`; see
[Validator-set and parameter changes](../consensus/consensus-alternative-flows.md#validator-set-and-parameter-changes).
For startup reconciliation of application, block-store and state-store heights,
see [Recovery and synchronisation](../consensus/consensus-recovery.md).

## Version

[Version](./data_structures.md#version) distinguishes block protocol version from
application version. `ConsensusParams.Version.ConsensusVersion` additionally
selects the [proposer-selection strategy](../consensus/proposer-selection.md)
and, from version 2, enables
[proposer liveness](../consensus/consensus-proposer-liveness.md).

## Block

`ConsensusParams.Block.MaxBytes` limits block size. Transaction selection uses
`Block.MaxGas`, but applications must validate gas requirements when processing
blocks; see [BlockParams](./data_structures.md#blockparams).
The complete block/header/application checks are in the
[proposal-validity summary](../consensus/consensus-transitions.md#proposal-validity-summary).

## Evidence

`ConsensusParams.Evidence` bounds evidence age and bytes. Pending evidence is
included during [proposal creation](../consensus/consensus-happy-path.md#3a-creating-the-proposal-proposer-only).
[Conflicting-vote handling](../consensus/consensus-alternative-flows.md#equivocation-duplicates-and-conflicting-votes)
reports evidence to the pool; reporting is distinct from its inclusion in a block
and application-level punishment.

## Validator

Validators are identified by `ProTxHash`, ordered by ascending `ProTxHash`, and
have equal `DefaultDashVotingPower = 100`. The active quorum includes its type,
hash, threshold public key and voting-power threshold; see
[Terminology](../consensus/consensus.md#terminology) and
[ValidatorSet](./data_structures.md#validatorset).
