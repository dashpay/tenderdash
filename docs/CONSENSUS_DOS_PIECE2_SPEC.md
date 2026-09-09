# Consensus DoS — Piece 2 spec: verification fairness

Status: **v2, post-review.** Reviewed by four independent lenses (scope/simplicity,
liveness/failure-modes, security/Sybil, Codex). Implements plan item §2.3 and the **required**
fairness invariant of `docs/CONSENSUS_DOS_PLAN.md` §1. Phase 1 only.

## 1. Problem

Piece 1 (`c7b0a2a1e`) bounds *aggregate* peer Vote/Commit BLS work with a node-wide token
bucket (`VerificationRateLimit`, default 300 work/s, burst 66). It has no notion of *whose*
work it is: every peer shares one arrival-ordered FIFO (`msgQueueSize = 20000`,
`internal/consensus/msg_queue.go:40`) drained by a single reader. Two consequences:

1. **Starvation.** Attacker peers staying within their own per-peer limit can hold the global
   bucket at zero, so honest peer-relayed votes are denied and the node never reaches 2/3. CPU
   is bounded (piece 1's win) but liveness is not.
2. **Partial-message failure.** A remote precommit is verified twice (`state_add_vote.go:283`,
   then `types/vote_set.go:265`), each staged `1 + n`. Piece 1 charges each stage
   independently, so a message can pass stage 1 — paying full BLS cost *and* the ABCI
   `VerifyVoteExtension` call — then be denied later, wasting the work and dropping a valid vote.

## 2. Goal and non-goals

**Goal.** Guarantee *minimum honest service*: an admitted peer's message cannot be delayed
indefinitely by other peers' traffic, while total verification work stays inside piece 1's
rate/burst envelope.

**Non-goals** (plan §6): no weighted fair queueing (no weights, priorities, or classes); no
concurrent BLS or worker pool (Phase 2); no fairness claim for the p2p router queue *upstream*
of admission; no end-to-end consensus deadline.

**Scope note (revised).** The peer queue this piece reschedules carries **four** message types —
Proposal (`reactor.go:660`), BlockPart (`:668`), Commit (`:705`), Vote (`:741`). Piece 2 therefore
**prices all four** (§5a). It does *not* implement piece 5's BlockPart byte/proof budget or
piece 4's State/VoteSetBits ceilings; it only assigns each scheduled message a turn cost, which
its own invariants require.

**Known limitations (do not overclaim).**
- **Honest share is bounded by connection slots, not by this scheduler.** `MaxOutgoingConnections
  = 12` (`config/config.go:803-806`, refused at `peermanager.go:610` before the upgrade path), so
  we only ever *dial* 12 of `P = 68`. The other 56 inbound slots are fillable from **one host,
  one IP, 56 keypairs** (`conn_tracker.go:87` caps concurrent conns per IPv4 /32 at 100).
  Identity is free; slots are scarce; per-identity round-robin is therefore exactly per-slot
  round-robin and can do no better. Raising honest slot count is **piece 3b's** job (expanded:
  not merely protecting the 12 from eviction, but ensuring enough honest slots exist — plus
  per-/24 rather than per-/32 inbound diversity).
- **Upstream unfairness.** The router's per-channel inbound queue is a single FIFO shared by all
  peers (`newFIFOQueue` default, `router.go:223`; `channelQueues` = "inbound messages from all
  peers to a single channel", `router.go:171`; vote channel `RecvBufferCapacity = 4096`). Honest
  messages can be dropped there *before* reaching a lane. I1 holds only for admitted messages;
  end-to-end honest service also depends on piece 6 and piece 3b.
- **Rejected alternative.** Implementing fairness as a per-peer `queue` impl plugged into the
  router's existing pluggable `queue` interface (`internal/p2p/queue.go:11-29`) is the natural
  shape, but rejected: p2p would need consensus verification-cost and budget knowledge (layering
  inversion), with a large blast radius and collision with PRs #19/#22.

## 3. Design decision: fairness form (b)

Plan §1 offers two forms; §9 defers the choice to the load test. This spec fixes **(b)**
(bounded per-peer queues, round-robin) **provisionally**, to be confirmed by the §5 load test:

- Form (a) — static caps with `P × cap ≤ safe_rate` — yields `cap ≈ 4.4` work/s at `P = 68`,
  `R = 300`, while one Dash-realistic 4-extension precommit costs 10 and a maximum one costs 66.
- The decisive argument is not throughput alone but **decoupling**: round-robin separates the
  *latency* bound from the *rate* cap, delivering bounded delay **and** full-rate catch-up
  simultaneously. Form (a) structurally cannot do both — its latency bound *is* its rate cap.

**Why per-identity fairness at all:** the node cannot distinguish honest from malicious senders
(a verification failure is not proof of guilt — plan §3), so equal service per authenticated
identity is the only defensible policy. Per the limitation above, this is a *slot*-fairness
statement, not a claim that the scheduler creates honest capacity.

## 4. Invariants

`P` = 68 hard ceiling (`MaxConnected 64 + MaxConnectedUpgrade 4`, `peermanager.go:811-813`).
`C_max` = worst-case cost of one message (66 precommit, 33 commit). `R` =
`VerificationRateLimit`. `B` = global burst. `Q` = DRR quantum.

- **I1 — bounded honest turn, stated over ADMITTED VERIFICATION.** With DRR over active lanes, a
  continuously-connected peer's head-of-line message **completes verification** (or fails on its
  own merits) within one rotation of the other lanes' *settled* work plus at most one quantum.
  It is deliberately **not** stated over *dispatch*: a dispatch-worded invariant stays true while
  the message is thrown away for lack of budget, which is exactly how the rejected peek-and-drop
  design (§5b) passed I1 while the chain halted. The §7 I1 test must therefore assert honest
  *completion* latency.

  **The bound is `(P−1)·W/R`, and it is work-dependent — an earlier draft of this spec was
  wrong.** That draft quoted `(P−1)·1/R ≈ 0.22 s`, which is only the `W = 1` case, and stated it
  as the acceptance criterion. The real bound scales with the head message's own cost: a
  Dash-realistic `W = 10` precommit measures **~2.2 s**, which exceeds `timeout_vote = 1 s`.
  **No work-fair scheduler can do better** — this is the fair-share bound, not a scheduler
  defect, so the criterion was unachievable as written. The test grades honest completion against
  `timeout_propose` (3 s) and records the measured value. Closing the gap to `timeout_vote`
  requires changing an input, not the scheduler:
  raise `VerificationRateLimit`, reduce attacker-holdable lanes (piece 3b — now ≥18/68 honest),
  or remove the double verification (which alone would halve `W` for precommits; see §9 and plan
  §9's open item). Stated in **settled** cost, because an adversary that cannot forge
  a block signature settles at **1** (§5b), the realistic rotation is `(P−1)·1/R ≈ 0.22 s`, not
  the vacuous worst case `(P−1)·C_max/R = 14.7 s` — which exceeds `timeout_propose = 3 s` and
  `timeout_vote = 1 s` (`types/params.go:183-190`) and would let the test pass while the chain
  makes no progress. **Therefore the I1 test asserts wall-clock latency against the round
  timeouts, not the formula.** The bound must be accompanied by a *tested enumeration* of every
  way an adversary turn settles above 1 (§7), else I1 is unfalsifiable. Honest service is
  `honest_share ≥ honest_slots / P`, with `honest_slots` an explicit, load-tested parameter.
- **I2 — aggregate bound preserved, narrowly stated.** *Direct staged peer Vote/Commit
  verification covered by this cost model* in any interval `T` is `≤ R·T + B`. It explicitly
  does **not** cover threshold recovery/interpolation BLS work triggered when a vote crosses
  quorum (`types/vote_set.go:384,455`), nor work outside the cost model.
- **I3 — whole-message atomicity.** Once a message begins verification, its remaining staged
  permits cannot fail for budget reasons.
- **I4 — non-punitive, including the existing path.** No drop here may become a `PeerError`.
  This requires a code change the spec must name: `chanQueue.send`'s full-queue error
  (`msg_queue.go:55-57`) currently propagates through `reactor.go:660/668/705/741` →
  `processMsgCh` (`reactor.go:993-1010`) → `msgCh.SendError(p2p.PeerError{...})` →
  `peerManager.Errored` (`router.go:384`). Today a 20 000-slot queue makes this near-dead;
  bounded lanes make it routine, and the peer that overflows at capacity is the honest one.
  **Shed must return `nil` at those call sites** (mirroring `allowVoteChannelMessage`'s
  `continue` at `reactor.go:987-992`).
- **I5 — bypass preserved, fail-open bounded.** Local (`peerID == ""`) and WAL-replayed messages
  bypass scheduler and budget (`context.go:44-54`, `replay.go:89`). Explicit `0` config remains
  an operator bypass. But **accounting failures must not fail open**: an unexpected
  missing-cost/accounting error is a typed non-punitive local drop (or a loud internal invariant
  violation) and must never reach WAL, action dispatch, or BLS.
- **I6 — internal progress unblocked.** The internal-queue reader and timeout ticker stay
  independent of the peer scheduler. *Accepted cost:* a dispatched `C_max` message occupies the
  consensus goroutine ~178 ms (66 × 2.7 ms); at the design point the goroutine spends ~81 % of
  wall-clock in BLS, so timeout jitter worsens even though I6 holds structurally. That 178 ms is
  a **floor, not a bound**: unpriced work rides inside a priced dispatch — threshold
  recovery/interpolation (`types/vote_set.go:378,384,455`, re-run on every post-quorum vote,
  O(N·threshold)) and the ABCI `VerifyVoteExtension` round-trip (`state_add_vote.go:297`) — plus
  the §5b staged wait.
- **I7 — no unpriced dispatch.** The scheduler must never dispatch a message without a cost.
  Every type on the peer queue has a cost (§5a).

## 5. Mechanism

### 5a. Price every scheduled message; charge the per-peer limiter in work units

Mandated by plan §2 item 3 (charge Vote **and** Commit extensions). *Note:* this is **not**
load-bearing for I1–I6 — starvation is solved by 5b+5c — so removing it would not reopen
starvation; it exists for cost-proportional per-peer charging and (per I7) turn pricing.

| message | cost |
|---|---|
| prevote | 1 |
| precommit, nil block | 1 |
| precommit, non-nil, `n` extensions | `2 × (1 + n)`, max 66 |
| commit, `n` extensions | `1 + n`, max 33 |
| proposal | 1 (one pairing, `state_proposaler.go:240`) |
| block part | 1 (turn price only; byte/proof budget remains piece 5) |

Counts above `MaxVoteExtensions` are rejected **non-punitively**. Charging the raw wire count
over-charges (pairings run only over the `IsThresholdRecoverable` subset), which is the safe
direction. Pricing proposals also closes a live pivot: proposal verification is unbudgeted and
its only dedup (`rs.Proposal != nil`) is set *only by a proposal that passes*
(`state_proposaler.go:48,73`), so every forged copy re-verifies and ~3.7 peers can saturate the
verifier.

**Rate and burst.** `PeerVoteRateLimit = 600` work/s is a **provisional load-test candidate**.
(Correction to an earlier draft: old `100 messages/s` ≈ `1000 work/s` for a cost-10 workload, so
600 is a ~40 % *tightening*, not a semantic rebase. `600 > R = 300` is coherent: the global
bucket remains the aggregate authority; the private limiter only protects preprocessing and lane
admission.) **Burst must be decoupled from rate** — `2 × 600 = 1200` would let one identity
front-load ~1200 cost-1 messages or 18 maximum precommits, contradicting the documented
small-burst rationale (`reactor.go:174-178`), and fresh identities get full buckets
(`ratelimit.go:82-96`). Size burst from intended lane capacity and measured honest catch-up
burst, with `C_max` as a **floor** only. The floor is independently necessary: a rate-coupled
`voteRateBurst = 2 × limit` means a `PeerVoteRateLimit` below 33 makes a 32-extension message
permanently unadmittable.

**Chosen value: burst = 200 work units** (`max(200, C_max)`, a compile-time max so the floor
survives a raised `MaxVoteExtensions`). Rationale: an honest peer gossips ~10 vote-channel
messages/s and the heaviest message Dash validators actually produce (4 extensions) costs 10,
so 200 ≈ two seconds of an honest peer's heaviest gossip delivered at once — ample for a
catch-up burst. The upper bound is the shared queue: 64 slots × 200 = 12 800 < `msgQueueSize`
20 000. Both bounds are pinned by tests, so the value can only move within `[100, 312]`. The
rejected rate-coupled formula gave 76 800 at the same defaults.

### 5b. Whole-message affordability check — no escrow, no lease

**Simplification adopted after review.** The escrow/lease design was rejected: `rate.Limiter`
cannot express partial settlement (`ReserveN`/`CancelAt` are all-or-nothing), so it implied
replacing the tested piece-1 budget with a hand-rolled bucket; a lease cannot ride `msgInfo`
(a registered WAL record, `wal.go:54`); and it leaked across the scheduler→state handoff in at
least five paths, where — with `B == C_max` — a *single* leak pins `free` at 0 forever and
silently halts the peer path.

**Wait — never peek-and-drop.** An earlier v2 draft gated dispatch on "peek whether the bucket
can cover `W`, else drop". Review proved that **broken**: the bucket is a *level*, not a queue,
and a dropped message costs microseconds, so the offer rate is effectively unbounded while
refill is `R`. Under a demand-saturated **cheap** class the level is pinned in `[0, W_cheap)`
and never reaches `W_expensive`. Concretely, 67 attacker lanes charged 1 each consume 300 work/s
in ~1-token bites every 3.3 ms, while an honest 4-extension precommit needs a 33 ms quiet gap
that by construction never occurs ⇒ **honest admission probability ≈ 0**. DRR cannot rescue it:
DRR allocates *turns*, the peek allocates *tokens*, and a turn granted then discarded is a turn
wasted. Peek-drop is fair only under uniform cost — i.e. it fails exactly in the adversarial
(mixed-cost) case DRR exists for.

**The mechanism is bounded, ctx-aware waiting.** Because all draws are serialized on one
goroutine, *blocking for tokens is itself the reservation*: while we wait, nothing else can
take them. That buys the property the lease was trying to buy, with no lease.

1. **Scheduler (its own goroutine, may block on rate — §5c).** After DRR selects a lane, wait
   (bounded, `select` on `ctx.Done()`) until the bucket can cover **this message's own** cost
   `W` — from §5a, derived from its declared extension count, **never** the protocol maximum
   (reserving 66 for every precommit would cap throughput at `300/66 ≈ 4.5` msg/s and make a
   100-validator round take ~22 s). Then dispatch. Waiting rather than dropping is what keeps
   the turn from being wasted, preserving **I1**.
2. **State goroutine.** The staged draws use a **bounded wait** rather than a fail-fast
   `Allow`, with `maxWait ≥ C_max/R` (220 ms at defaults) plus slack. This makes **I3** hold
   *by construction* rather than by argument: stage 2 waits for its tokens instead of failing.
   In the common case the scheduler already ensured availability, so this returns immediately;
   it only actually waits in the narrow window where the scheduler pipelined one message ahead
   (§5d).

Cost charged remains the **actual** staged cost, so the plan §1.4 declared-count trap is still
avoided: an attacker short-circuiting on a bad block signature is charged **1**, not 33/66. (The
attacker's optimal play is therefore the *cheap* declaration — a 0-extension precommit charged
2 — not the 66 one.)

**Implementation constraints, all verified against the code:**
- `golang.org/x/time v0.15.0` provides `Limiter.TokensAt(t)`/`Tokens()`, so the availability
  test is a one-liner and needs no reservation.
- **Do not use `ReserveN` + `Cancel`.** `Reservation.CancelAt` refunds **nothing** once
  `timeToAct` has passed, so "reserve, wait out the delay, cancel, then charge" silently
  double-charges.
- **Do not call `rate.Limiter.WaitN` directly** — it consults real time internally and would
  defeat the §7 fake clock. Implement the wait over `TokensAt` plus the injected clock/timer.
- Shed must happen in the scheduler, *before* dispatch, so it costs zero WAL writes. Note
  `withMiddleware` (`msg_queue.go:21-26`) makes the last-appended middleware outermost: a check
  added via `cs.msgMiddlewares` runs **before** `walMiddleware`, one added inside the handler or
  as a voter middleware runs **after** it. A WAL write per dropped attacker message would be
  both disk amplification and a replay-time re-verification of attacker garbage under a nil
  budget (`replay.go:89`).
- `W` currently over-approximates safely (proposals/block parts are priced 1 but draw 0; the
  pairing count is the `IsThresholdRecoverable` subset, so `k ≤ n`). This is an *emergent*
  property of the call graph with nothing enforcing it — §7's draw-sequence test is the only
  guard against a third verification pass or a newly budgeted proposal silently breaking I3.

**Burst sizing is an enforced invariant, not a coincidence.** `verificationBudgetBurst` is
currently hardcoded `2×(1+MaxVoteExtensions) = 66 = C_max` (`verification_budget.go:14`),
independent of the configured rate, and `MaxVoteExtensions`' own comment invites raising it — at
64 it would give `C_max = 130 > B` and **no message could ever be admitted**. Assert
`B ≥ C_max` at construction (mirroring `dataRateBurstFor`, `reactor.go:868-881`), and prefer
**`B > C_max` with real margin**: at `B == C_max` a protocol-maximum message is admissible only
when the bucket is 100 % full, i.e. only on an idle node. Also validate a **minimum**
`VerificationRateLimit`: `0.5` is legal today (`config.go:1258-1263` checks only NaN/Inf/negative)
and would mean minute-scale stalls.

### 5c. Per-peer lanes + deficit round robin

- **Deficit Round Robin, not plain round-robin.** Costs vary 1–66. Plain RR with "skip a lane
  that cannot be served" lets cheap attacker messages perpetually overtake an expensive honest
  one, breaking I1. DRR (per-lane deficit counter + quantum `Q`) handles variable cost with an
  O(1), provable bound, and is the simple approximation of fair queueing — not the WFQ framework
  §6 rejects.
- **Rotation discipline (required for the bound to be testable):** newly activated lanes append
  at the **tail** of the rotation; disconnected/stale sessions may not create lanes.
- **Lane key** = `NodeID`; lanes purged on `peerDown` (`reactor.go:527-543`). A late in-flight
  message may transiently recreate a lane; add **time-based GC** for lanes of departed peers
  (cf. `ratelimit.go:125-148`), otherwise an attacker cycling free identities (connect, enqueue
  one message, disconnect) leaves a permanent lane each time and the honest share decays to
  `1/N` with unbounded `N`. Define behaviour for the empty lane key (`ctxWithPeerQueue` can route
  `peerID == ""` to the peer queue in tests).
  - **Connection sessions bind lanes (revised — the earlier deferral was wrong).** Time-based GC
    reclaims a stale lane only *after* it has been served and fallen idle, so under churn the
    active-lane count is bounded only by the shared 20 000-message aggregate, not by `P = 68`. A
    handler holding stale `PeerState` can also race `peerDown` after its purge and recreate the
    lane for a departed peer. Each connection is therefore admitted to the scheduler under a
    monotonic **session** at `peerUp`; a lane may be created or added to only for a message whose
    session is the peer's live one. `purgePeer` ends the session, so a message an ended connection
    left in flight — a departed peer, or an earlier connection of one that reconnected — is dropped
    without creating or reviving a lane. A message carrying no session (a pre-session path or
    this node's own work) keeps the former admission behaviour. This does not replace the
    time-based GC, which still reclaims the lane a sessionless or in-session straggler leaves.
    - **The session a message carries must be captured at INGRESS, not read from mutable
      `PeerState` at handling time (revised — the earlier context-from-`PeerState` scheme had a
      hole).** An inbound envelope waits in the router's shared per-channel queue, which is not
      drained on disconnect, so it can sit there across a full down/up cycle. Reading
      `PeerState.LaneSession()` when the handler finally dequeues it therefore reads the session of
      *whatever connection is live then* — after a reconnect under the same NodeID, the new one —
      and the stale envelope is admitted under it. The fix is an immutable **connection
      generation** minted per connection by the peer manager at `Ready` (`internal/p2p`), stamped
      by the router on every envelope it receives from that connection *before* the envelope enters
      the shared queue (`Envelope.ConnID`) and carried on the up `PeerUpdate` (`PeerUpdate.ConnID`).
      The reactor records the generation on `PeerState` at `peerUp` and, per message, derives the
      lane session from the generation the *envelope* carries, matched against the peer's live
      generation — never from a later reconnect's. A generation that no longer matches is a silent
      local drop (no `PeerError`). `peerDown` deletes and cancels the `PeerState` **synchronously**;
      since peer updates are processed one at a time, a following up cannot reuse or later cancel a
      state a pending down still owns.
- **Overflow policy: drop-oldest *within* a lane, drop-newest *across* lanes.** Consensus
  messages are time-valid, so when a lane sheds to fit its own arriving message it drops its
  oldest: with drop-newest, a catch-up burst fills a lane with round-`R` votes, the node advances
  to `R+1`, and every *fresh* vote is tail-dropped while turns are spent on guaranteed-stale ones
  — muting a peer for rounds with no attacker involved. But drop-oldest is *not* the right policy
  when room is taken from *another* peer's lane to fit an arriving message: there is no shared
  staleness order between two peers, and the credit a lane saves is granted for its head, so
  shedding another lane's head would strand that credit on a message the node never served — a
  colluder could flood the shared bound to evict an expensive head and let its partner spend the
  saved credit on a cheap burst out of turn. Cross-lane eviction therefore drops the victim's
  *newest*, keeping the head and its credit paired, and never resets the victim's credit (which
  would silence an expensive head — the very thing that makes a lane the longest).
- **Lane capacity must be specified** (currently unset, and liveness-critical). It must be
  chosen against the blocksync→consensus handoff, which carries a live warning: *"XXX: this can
  lead to a deadlock, if so - we need additional buffer for (at least) Commits"*
  (`reactor.go:236-238`) — today survivable only because of the 20 000-slot queue. A node syncing
  from one peer must still receive enough Commits.
- **The scheduler may block on rate; that is correct.** Delete the earlier claim that a lane
  which cannot be served must not block others — with one goroutine that is unsatisfiable, and
  blocking on rate is the rate gate doing its job and is already inside I1's bound. The wait
  **must select on `ctx.Done()`**: a non-ctx-aware wait deadlocks shutdown
  (`readQueueMessages` defer → `fanIn` → `stop()` → `receiveRoutine` never returns → SIGKILL).
  Waits must also be bounded — drop rather than stall indefinitely.
- **Placement.** Replaces the **peer-queue** reader in `msg_queue.go`
  (`chanMsgReader.readQueueMessages`), reusing that goroutine — no new goroutine, no BLS off the
  consensus goroutine. The internal reader is untouched (**I6**).
- **`SetHasVote`/`SetHasCommit`** move after successful admission — but note this amplifies
  outbound gossip on shed (we re-gossip to a peer already over budget) and degrades VoteSetBits
  accuracy under load. Measure it; revert if the feedback loop is worse than the benefit.
- **WAL.** `walMiddleware` runs inside `dispatch`, *before* the handler but *after* admission, so
  anything shed at the limiter or lane costs **zero WAL writes** (plan §5). Preserve that.

### 5d. Concurrency

`receiveRoutine` (`state.go:713-755`) dispatches one message to completion before reading the
next. The fan-in `outCh` is **unbuffered**, so it unblocks when `receiveRoutine` *starts* a
receive, not when it finishes — the scheduler therefore pipelines one message ahead. This is why
the budget must not be touched by the scheduler (§5b): with all draws on the state goroutine,
that pipelining cannot race the staged permits.

## 6. Deliberate deviation: de-duplication stays where it is

Prevotes are de-duped before the pairing (`types/vote_set.go:254-262` precedes `:264`).
Precommits have an earlier dedup covering only non-nil-BlockID votes from other validators
(`state_add_vote.go:260-266`). **Commits have none** — and a repeated *invalid* Commit never sets
`stateData.Commit`, so every admitted copy costs a turn, a WAL write, and a full direct
verification (`state_try_add_commit.go:48`). Duplicates therefore do **not** always settle near
zero; this case belongs in the load test.

Moving dedup earlier needs either the Phase-2 prepare/apply boundary **or** a separately designed
bounded seen-set with height-tied eviction — "requires Phase 2" is too absolute, but both are out
of scope here. Note the deviation is broader than dedup: the private limiter also precedes
protobuf conversion, structural validation and height/round guards, contrary to plan §1's stated
ordering.

## 7. Testing

Red-before-green; fake clock for rate/timing. **`ratelimit.go:109` hardcodes `time.Now()`, so an
explicit clock seam is a prerequisite deliverable.**

- **I1**: `P−1` saturated attacker lanes + one honest lane; assert honest wall-clock latency
  against `timeout_propose`/`timeout_vote`, not the formula. Include an expensive honest head
  vs cheap attacker heads (the DRR case), and a turn already in flight on arrival.
- **Settle-above-1 enumeration** (required to make I1 falsifiable): replayed genuine valid
  Commit while `stateData.Commit == nil`; repeated invalid Commit flooding; unpriced-type
  regression (must be impossible after §5a); pre-permit work (`makeVerifyQuorumSigns` runs a
  marshal + SHA-256 per extension *before* `Allow(1)`).
- **I2 oracle** against the **real** bucket, not a model: `work(T) ≤ R·T + B` over arbitrary
  interleavings; explicitly assert the narrowed scope (threshold-recovery work excluded).
- **I3**: a budget level that denies stage 2 under piece 1 must here complete the message via the
  bounded wait. Assert exact staged draws per priced message type — `[1, n, 1, n]` (precommit),
  `[1, n]` (commit) — since `W`'s safety is an emergent call-graph property: a third pass or a
  newly budgeted proposal would silently break I3, and this test is the only guard.
- **Single-consumer guard**: assert no budget draw occurs off the `receiveRoutine` goroutine.
  I3's whole argument rests on it, and nothing in the code enforces it.
- **Cost-bias regression** (pins the rejected peek-and-drop failure): saturate with cost-1
  messages and assert an expensive honest precommit still completes — this test must fail
  against a peek-and-drop implementation.
- **I4**: lane overflow and limiter rejection → assert `msgCh.SendError` is **not** called and
  `peerManager.Errored` is **not** reached (this test fails against today's code), plus no WAL
  write, no action dispatch, no peer-state mutation.
- **I5**: local/replay bypass; explicit-zero bypass; accounting failure does **not** fail open.
- **I6**: internal messages and timeouts serviced while the peer scheduler waits.
- **I7**: every peer-queue message type has a cost; adding a type without one fails the build/test.
- **Lifecycle**: peer up → enqueue → down → up (same NodeID); old-Down/new-Up/stale-envelope
  barriers; cancel before handoff vs disconnect after handoff; lane purge and time-based GC;
  identity-rotation lane growth is bounded; panic during verification is safe.
- **Config**: `B ≥ C_max` asserted; minimum rate validated; arbitrary configured `P` incl. 128+4.
- **Race/shutdown**: `-race`; shutdown *during* a scheduler wait must not hang.
- **Load test (gates the piece):** production profile, per-peer cap **enabled**, with explicit
  height-progress and honest-latency thresholds, and a **mixed honest/attacker lane-ratio sweep**
  to locate the cliff. **The vote gate must be disabled for this test** (or the flood run over
  Commit + Proposal, which are ungated): `voteSenderAllowed` (`reactor.go:718-723`) currently
  drops Sybil *votes* outright, so on today's tree the modelled attack is impossible and the test
  would validate nothing.

## 8. Commit split

1. **5a** — cost function (all four types) + limiter unit change + burst floor + clock seam.
2. **5b** — affordability peek + `B ≥ C_max` assertion + min-rate validation + I3 tests.
3. **5c** — lanes + DRR + rotation/GC/overflow policy + I1/I4 tests.

Each builds and is green before the next. (The charter's one-piece-per-commit rule targets not
bundling *different* pieces; splitting one large piece into independently testable commits
improves reviewability.)

## 9. Interactions and newly surfaced plan items

- **Piece 8 (remove the vote gate)** stays upstream; do not remove or depend on it here — but
  see §7's load-test requirement.
- **Piece 3b is expanded** by review finding F3: it must not merely protect the 12 outgoing
  slots from eviction but ensure enough honest slots *exist* (raise `MaxOutgoingConnections`
  and/or reserve inbound slots for current-quorum NodeIDs; per-/24 inbound diversity).
- **New Phase-1 item — evidence channel.** `internal/evidence/verify.go:142,145` does two
  pairings per `DuplicateVoteEvidence`; there is **no rate limit on the evidence channel**, and
  dedup is by evidence hash (`pool.go:149-160`) so flipping one signature byte forces
  re-verification, plus uncharged `LoadBlockMeta`/`LoadValidators` disk I/O. Separate reactor
  goroutine; not covered by this piece.
- **Load-test decision point.** Honest demand alone (~64 validators × 11 work ≈ 704/round) against
  `R = 300` makes the budget binding in *normal* operation. Removing the double-verify (plan
  Phase 2a) would roughly double headroom; the load test decides whether it must be pulled into
  Phase 1.
- **`#16`** stays upstream and untouched. **PRs `#19`/`#22`** touch `reactor.go`/`router.go` —
  merge-coordination only.
