---
order: 7
---

# Recovery and synchronisation

## Write-ahead log

The consensus WAL (`internal/consensus/wal.go`; file layout in
[WAL](./wal.md)) records, per height:

| Record | Written by | Durability |
| -------- | ------------ | ------------ |
| `msgInfo` — proposal, block part, vote or commit, with the peer id and receive time | `walMiddleware` (`internal/consensus/msg_handlers.go`) | peer messages `Write` (buffered); the node's own messages `WriteSync` (fsync), **panic** on failure |
| `timeoutInfo` — every tock | `State.receiveRoutine` | `Write` |
| `EventDataRoundState` — every step change | `EventPublisher.PublishNewRoundStepEvent` | `Write` |
| `EndHeightMessage{h}` | `ApplyCommitAction.Execute`, after `SaveBlock` and before `FinalizeBlock` | `WriteSync`, **panic** on failure |

`verificationBudgetMiddleware` wraps `walMiddleware`, so a message dropped
for budget reasons leaves no WAL record. Before signing a proposal or a vote
the WAL is flushed and fsynced (`EnterProposeAction.Execute`,
`voteSigner.signVote`) so that replay reproduces the same message for the
private validator's double-sign protection. A failed sync stops the vote
(`signVote` returns the error, nothing is signed) but not the proposal:
`EnterProposeAction.Execute` logs it and proposes anyway. An empty WAL starts with
`EndHeightMessage{0}` (`BaseWAL.OnStart`).

## Start-up: ABCI handshake

`Handshaker.Handshake` (`internal/consensus/replay.go`) calls ABCI `Info` and
`BlockReplayer.Replay` (`internal/consensus/replayer.go`) with the
application's `(LastBlockHeight, LastBlockAppHash)`:

1. If the application height is 0: `InitChain` with the genesis validator
   set, parameters, `InitialCoreHeight`; when Tenderdash itself is at height
   0 the returned validator set, parameters, app hash and optional genesis
   time become the initial state (`execInitChain`).
2. `replayState.validate`: the application may not be ahead of the block
   store; the state may not be ahead of the store; the store may be at most
   one block ahead of the state; a truncated store must not have pruned the
   blocks the application needs.
3. Cases (`syncStateIfItIsEqualStore`, `syncStateIfItIsOneAheadOfStore`):

| Store vs state | App vs state | Action |
| ---------------- | -------------- | -------- |
| equal | behind | replay stored blocks into the application: per block `ProcessProposal(round = commit.Round)`, `VerifyCommitExtensions`, `FinalizeBlock` (`replayBlock`); the resulting app hash must equal the block's; a rejected commit vector stops replay (`commitExtensionsReplayError`) |
| equal | equal | app hash must equal `state.LastAppHash` |
| store = state + 1 | behind state | replay all but the last block as above, then apply the last one through `BlockExecutor.ApplyBlock` with the real application |
| store = state + 1 | equal state | apply the last block with the real application (`syncStateAt`) |
| store = state + 1 | equal store | the application already finalized it: apply the last block against a **mock** application that replays the saved `ABCIResponses` (`newMockProxyApp`), so that state and store catch up without re-executing |

## Start-up: WAL catch-up

`State.OnStart` (`internal/consensus/state.go`) rebuilds `LastCommit` from
the stored seen commit (`updateStateFromStore`), opens the WAL, starts the
ticker and, unless `doWALCatchup` was disabled by block sync, runs
`State.catchupReplay`:

- the WAL must **not** contain `#ENDHEIGHT h` for the current height;
- it must contain `#ENDHEIGHT h − 1` (`0` at `InitialHeight`);
- every record after it is replayed through `readReplayMessage`: step
  events are only logged, `msgInfo`s are dispatched with `fromReplay` (no
  peer budgets, no evictions), tocks go through `handleTimeout`. With
  `WalSkipRoundsToLast` a replayed proposal for a later round advances the
  round directly;
- replay mode: the node does not propose (`EnterProposeAction.Execute`), skips
  timeliness checks (`isValidForPrevote`), and re-signs nothing new — its own
  votes come back from the WAL as internal messages, and the private
  validator refuses to double-sign in the meantime (see [WAL](./wal.md));
- a corrupted WAL is backed up to `<wal>.CORRUPTED`, repaired by truncating
  at the first undecodable record (`repairWalFile`), and replay is retried
  once; other errors are logged and the node starts anyway.

Then `receiveRoutine` starts and `ScheduleRound0` arms the first tock.

## Crash windows

| Crash point | Recovery |
| ------------- | ---------- |
| Before `SaveBlock` | WAL replay re-delivers the height's messages and timeouts; the node re-reaches the same step |
| After `SaveBlock`, before `FinalizeBlock` completed (with or without `EndHeightMessage{h}`) | handshake case "store = state + 1, app = state": `ApplyBlock` with the real application. Without the marker, `catchupReplay(h + 1)` then fails with "WAL does not contain #ENDHEIGHT for h"; the error is not a corruption error, so `State.OnStart` logs it and starts without replay |
| After `FinalizeBlock`, before `stateStore.Save` | handshake case "app = store": mock replay from saved `ABCIResponses` (`BlockExecutor.FinalizeBlock` saves them **before** calling the application) |

## Block-sync handover

`Reactor.SwitchToConsensus(state, skipWAL, targetHeight)`
(`internal/consensus/reactor.go`):

1. If `targetHeight > state.LastBlockHeight` (peers reported more blocks than
   were applied), arm `catchupTracker` so that `EnterPropose` refuses to
   propose until the node passes `targetHeight` or `maxCatchupSuppression`
   (10 min) elapses; a block built from present-day application state must
   not be proposed into a height the network committed long ago.
2. Rebuild `LastCommit` from the stored seen commit; `updateToState`; start
   `State`; publish the step; mark syncing complete; with `skipWAL`, disable
   WAL catch-up.

While the reactor is still in sync mode (`WaitSync`) it ignores data, vote
and vote-set-bits messages.

A node that restarts mid-height and re-joins through consensus (no block
sync) relies on peers' commit gossip for the heights it missed
([Commit gossip](./consensus-commit.md#commit-gossip)): the proposer-selector
reconstruction reads proposers from the block store to resynchronise the
round-robin (`proposerFromStore`).
