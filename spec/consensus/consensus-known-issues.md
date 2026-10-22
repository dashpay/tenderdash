---
order: 8
---

# Known issues and discrepancies

Behaviour found while deriving this specification that looks like a bug,
diverges from the Tendermint algorithm, or is otherwise surprising. Each
entry describes and cites; none is fixed here. "Inferred" marks conclusions
drawn from reading the code without a reproducing test.

## An untimely `ValidBlock` is never re-proposed

`Proposaler.Create` (`internal/consensus/state_proposaler.go`) re-proposes
`ValidBlock` only when `checkValidBlock` finds it timely for `ValidRound`
(`ValidBlock.IsTimely(ValidBlockRecvTime, SynchronyParams, ValidRound)`);
otherwise it builds a fresh block and proposes it with `POLRound = -1`. The
PBTS algorithm checks timeliness only for proposals without a POL round, so a
block that already gathered a polka would be re-proposed regardless of its
timestamp.

A proposer sees its `ValidBlock` as untimely when it learned the block late.
`updateValidBlock` dates a block with `tmtime.Now()` when the node learned it
without a matching proposal, which puts the receive time well after
`block.Time`. The proposer logs `proposal block is not timely` at ERROR level
when this happens.

Effect: validators locked on `ValidBlock` prevote nil for the fresh block,
since a lock is cleared only at a new height (see
[No unlock within a height](#no-unlock-within-a-height)). If the voting power
locked on that block exceeds `TotalVotingPower - QuorumVotingThresholdPower`
and every subsequent proposer sees it as untimely, no block can gather a
polka and the height stalls (inferred; no reproducing test).

History: before
[#1564](https://github.com/dashpay/tenderdash/pull/1564) the fresh block was
proposed with `POLRound = ValidRound`, so every validator prevoted nil and
the round was always lost.

## `PrecommitWait` step is never entered

`RoundStepPrecommitWait` exists in `RoundStepType`
(`internal/consensus/types/round_state.go`) and is used as the step of the
precommit-wait timeout and in peer-state comparisons
(`pickVoteForGossip`), but `EnterPrecommitWaitAction.Execute`
(`internal/consensus/state_enter_wait.go`) only sets
`TriggeredTimeoutPrecommit` and never calls `updateRoundStep(round,
RoundStepPrecommitWait)`. A node waiting for the precommit timeout reports
step `Precommit`. Consequences are cosmetic (events, metrics, peer state
comparisons), and `handleTimeout` accepts the `PrecommitWait` tock because
its step is greater than `Precommit`.

## Proposer clock-wait can be swallowed by the ticker

`EnterProposeAction.Execute` reschedules a `(h, r, NewRound)` tock when the
proposer's clock is behind `state.LastBlockTime`. The ticker
(`timeoutTicker.timeoutRoutine`) ignores a tick whose step is `≤` the step of
the last scheduled tick for the same height and round, and it does not forget
the last tick after it fires. When `EnterPropose` itself was triggered by a
`(h, r, NewRound)` tock — the empty-block interval
(`EnterNewRoundAction.Execute`) or the transactions-available path
(`State.handleTxsAvailable`) — the rescheduled tick is dropped and nothing
else moves the node out of `NewRound` for that round. The node recovers only
when the other validators' nil precommits reach +2/3-any
(`addVoteDispatchPrecommitMw` → `EnterPrecommitWait` → `EnterNewRound(r + 1)`),
costing the network one round. Preconditions: `WaitForTxs()` true, round 0,
the node is the proposer, and its clock is behind the previous block time by
more than the time since that block was produced. (Inferred; not reproduced.)

## Precommit requires this round's proposal even when locked

`EnterPrecommitAction.Execute`, rule C-LockedNotProposed: a validator locked
on `B` that sees a polka for `B` in round `r' > LockedRound` precommits nil
unless `B` is also round `r'`'s proposal block. The Tendermint paper (line
36) also requires a proposal for `v` in the same round, so this is faithful
to the algorithm, but it is stricter than the pre-vote-extension Tendermint
implementation, which relocked on the polka alone. The code's rationale is the
application contract: `ExtendVote` is answered for the round the application
processed last, and Dash Drive refuses any other. Liveness cost: a locked
validator that missed the round's proposal cannot help commit `B` in that
round.

## No unlock within a height

`LockedRound/LockedBlock` are only replaced by a new lock at a later round
or reset at the next height (`StateData.updateLockedBlock`,
`StateData.updateToState`). The paper's unlock-on-nil-polka rule from the
original Tendermint implementation is absent by design
(`TestStateLock_POLDoesNotUnlock`), relying on P-Lock-POL to move the lock
forward instead.

## Commit threshold is configurable below the BFT bound

`ValidatorSet.QuorumVotingThresholdPower` honours `VotingPowerThreshold`,
which may be lower than `⌊2n/3⌋ + 1` validators; several devnet LLMQ types
have a 50 % threshold. The code only warns
(`ValidatorSet.BelowStrictThreshold`, logged by `StateData.updateToState`).
Safety of the protocol is not guaranteed below the strict floor.

## Proposer selection v0 skips the first validator after quorum rotation

`heightProposerSelector.proposerFromStore`
(`internal/consensus/versioned/selectproposer/height_proposer.go`) deliberately
sets the proposer to **index 1** after a validator-set change ("bug preserved
to avoid breaking consensus"); the v1 selector uses index 0. See
[Proposer selection](./proposer-selection.md).

## A `DEFAULT` vote extension from `ExtendVote` is not rejected before signing

`VoteExtension.Validate` (`proto/tendermint/types/dash.go`) rejects the
`DEFAULT` type, and `MakeQuorumSigns` (`types/quorum_sign_data.go`) builds
sign items only for threshold-recoverable extensions. The signing path does
not run `Validate`: `BlockExecutor.ExtendVote` converts the application's
response with `NewVoteExtensionsFromABCIExtended`, which accepts `DEFAULT`
(`VoteExtensionFromProto`), and hands the vote to the private validator.
`FilePV.signVote` then signs the filtered sign items and copies them back
with `fillProtoVoteExtensionSigns(vote.VoteExtensions, extSigns)`, which
indexes the shorter signature list by the position in the unfiltered
extension list — an index-out-of-range panic once the `DEFAULT` extension
is reached; `DashCoreSignerClient.signVoteExtensions` instead writes
signature `i` of the filtered list into extension `i` of the unfiltered one,
misplacing signatures. A vote that did get out would fail `Vote.ValidateBasic`
at every peer. Effect: an application returning a `DEFAULT` extension takes
the validator down (file signer) or produces precommits nobody accepts (Core
signer), rather than being told the type is unsupported. (Inferred; not
reproduced.)

## Code/comment drift and dead code

- `BlockExecutor.ProcessProposal` (`internal/state/execution.go`) checks
  `len(resp.AppHash) != appHashSize` after `NewStateChangeset`, but
  `ResponseProcessProposal.Validate` already returned for any size other
  than `crypto.DefaultAppHashSize`; the later check is unreachable unless
  `SetAppHashSize` changes the size, which no non-test caller does.

- `EnterNewRoundAction`'s doc comment mentions `timeoutNewHeight`,
  `timeoutCommit` and `SkipTimeoutCommit`, none of which exist; round 0
  starts at `CommitTime` (`StateData.updateToState`,
  `roundScheduler.ScheduleRound0`).
- `EnterPrevoteEvent.AllowOldBlocks` is set by `ProposalCompletedAction` and
  never read (`internal/consensus/state_enter_prevote.go`).
- `ErrInvalidProposalNotSet` and `ErrAddingVote` (`internal/consensus/state.go`)
  have no non-comment users.
- `ApplyCommitAction.Execute` handles `commit == nil`, but every caller
  passes a commit.
- `config.ConsensusConfig.DoubleSignCheckHeight` is exposed as a CLI flag
  (`cmd/tenderdash/commands/run_node.go`) but no consumer was found under
  `internal/consensus` (inferred from grep).
- `blockExecutor.create` returns `(nil, nil)` when `LastCommit == nil` above
  `InitialHeight` ("shouldn't happen"); `Proposaler.Create` would then
  dereference a nil block in `makeProposal`. Unreachable in normal operation
  because `updateToState` panics on a missing `LastCommit`.
- `EnterPrecommitAction.Execute` logs routine nil precommits (no polka, nil
  polka) at **error** level, including the ordinary timeout path.

## Non-fatal optimistic-concurrency errors

`State.receiveRoutine` persists `StateData` after each handler with a version
check (`StateDataStore.update`); a mismatch is only logged, after the
handler's side effects (signed votes, WAL records) already happened. No
concurrent writer exists today, so the check is a latent guard rather than a
protection.
