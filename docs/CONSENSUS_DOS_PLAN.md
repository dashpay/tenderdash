# Consensus DoS — Plan

Final plan after research (per-message + prior-art) and review by both the author and
Codex (per-message *and* plan-level). Scope: **`#17` + the DoS design only.** `#16` (P0
panic) is owned by another dev — not touched here — but the DoS release **depends on it
landing first** (§8).

**Two phases, distinct goals:**
- **Phase 1 (embargo)** — *stop the attack.* Bound the expensive verification work and shed
  the excess *fairly*, so a flood can't stall consensus. Throttle-based, no new threads.
- **Phase 2 (follow-up, no embargo)** — *raise the ceiling.* Move signature verification off
  the single consensus thread onto a worker pool, so honest block production is faster and
  the flood ceiling grows with cores.

**Design principle for Phase 1: match the proven pattern.** SSH `MaxStartups`, TLS server
rate-limiting, Amazon load-shedding, and libp2p **gossipsub** (authenticated consensus
gossip — our closest analog) all defend expensive-crypto floods the same way: **bound the
expensive op, charge real cost, shed excess early, drop (don't punish) under overload.** We
keep it minimal — but not so minimal it loses soundness (per the review, a *minimal fairness
reservation* and *staged permits* are required; see §1).

## 1. Phase-1 defense — the mechanism

Per-message admission, **in this order** (order is load-bearing):

1. **Cheap checks first** — structural validation + current-state guards (height/round) +
   de-dup. Reject junk for ~free, before any crypto.
2. **Per-peer cap** — each peer bounded to its share; over-cap → **drop, no punishment**.
3. **Global work budget — a RATE/burst gate, not concurrency.** A shared token budget
   denominated in verification *cost*, refilled at the verifier's safe throughput, with
   burst ≥ the most expensive single legitimate message. Empty → drop *before* verifying.
   (Verification is already single-threaded, so a "concurrency=1 semaphore" would add
   nothing — it must be a rate/burst budget.)
4. **Staged permits around the crypto.** Acquire one permit → verify the **block**
   signature; **only if it passes**, acquire N more → verify the N extensions. This matches
   the existing block-first short-circuit and defeats "declare 32 extensions + send a bad
   block sig → burn 33 tokens for 1 real check." (Charging *declared* count up front does
   **not** contain that abuse, because the global tokens are shared — so staged permits, not
   declared-count, is the correct mechanism.)

Note the gate **wraps** the BLS op: the block-signature check *is* the expensive work, so the
permit is taken *before* it, not after.

**Fairness invariant (REQUIRED — stated and tested).** A per-peer cap alone is *not*
fairness: independent per-peer buckets don't reserve a slice of the shared global budget, so
many attacker identities staying within their caps could still drain it and starve honest
votes → consensus halts while CPU stays bounded. Phase 1 must guarantee a **minimum honest
service** under maximum adversarial load. Minimal forms (choose per the load test):
- **(a)** per-peer caps sized so `max_peers × cap ≤ safe_rate` — then there's no global
  competition and every peer (incl. honest) is guaranteed its cap. Leanest, but the cap is
  low. One favorable fact holds today: honest relayed votes are **de-duped *before*
  verification** (so honest *unique* demand is small, ~tens/s, not multiplied by the
  616-node relay fan-out). A second — validators staying connected under a flood — is
  **only partly real as implemented**: they get a high but *ephemeral, error-decremented*
  `MutableScore`, **not** the true `Persistent` flag (it resets on restart and erodes under
  error-scoring), so a connection flood + restart can crowd them off. Phase 1 **hardens**
  this (§2). **Honest service has two halves: validators reliably *connected* AND a fair
  *share of the verification budget* — both are required.**
- **(b)** small **bounded per-peer ingress queues drained round-robin** — a minimal fair
  scheduler (not a general WFQ framework), if (a)'s caps prove too tight for honest
  catch-up bursts.

State the exact invariant and prove it in the load test. This is the minimum reservation for
honest liveness — distinct from the rejected full-WFQ framework (§6).

## 2. Phase 1 — the fix (embargo release)

1. **Keep** the crypto hardening (SEC-002/003/004/007) + log/connection hygiene (SEC-008).
2. **Add** the global rate/burst work budget + staged permits (§1).
3. **Per-peer cap + the fairness invariant** (§1) on all consensus channels; charge **Vote
   *and* Commit** extensions (staged `1+n` — votes have the same amplification as commits);
   keep the existing Proposal cost-weight.
3b. **Harden validator connection protection (the connection-level half of honest service).**
   Current-quorum validators must stay connected under an inbound-connection flood. Today
   they only get a high but **ephemeral, error-decremented `MutableScore`** — the dash dialer
   sets `MutableScore = MaxInt16` but never the `Persistent` flag (`configurePeer` derives
   `Persistent` only from the static `PersistentPeers` config), so `Score()` caps at
   `MaxInt16-1`, the value is **not persisted (resets on restart)**, and it is **decremented
   by every `PeerError`**. A connection flood + a restart (or accumulated error-scoring) can
   therefore crowd validators off their slots. Fix: give current validators **durable,
   non-eroding** protection — mark them truly `Persistent` (or a dedicated
   "current-validator" protection) when `ValidatorConnExecutor` connects them, surviving
   restart and immune to error-score decrements — so they cannot be displaced at the
   connection level.
4. **Cover the non-BLS channels** (the BLS budget doesn't bound them): add a small separate
   per-peer **and aggregate** ceiling on the **State** and **VoteSetBits** channels, plus
   **duplicate-VoteSetMaj23 suppression** and a **bounded, non-blocking response send**
   (short-timeout `TrySend`/bounded queue — never an unbounded goroutine).
5. **Bound BlockPart work:** the accepted-part caps do **not** bound *repeated invalid-proof*
   hashing (a bad proof on an empty in-range index re-hashes a ~64 KB leaf every attempt).
   Add a coarse node-wide byte/proof budget.
6. **Shrink the backlog + add an internal-priority lane** — using a **typed local-overload
   drop that is NEVER sent as a `PeerError`** (otherwise queue-full evicts an honest sender
   at max capacity). Reserve internal-consensus progress while keeping a bounded eventual
   peer-service guarantee.
7. **Fix the log-amplification leaks** (Proposal's full-payload Error log; BlockPart
   proof-error Error volume).
8. **Remove the vote gate + SEC-006 metric.** **DONE**, prerequisite first.
   - **Prerequisite, landed.** `HeightVoteSet.addVote` was the only peer-reachable route to a
     round allocation (`internal/consensus/types/height_vote_set.go`): it minted a
     `RoundVoteSet` per unseen round, gated only by a map keyed on the vote's **claimed**
     `ValidatorProTxHash`, and the check that would reject the claim ran after the allocation
     and short-circuited before the signature check — **zero** verification budget drawn, no
     throttle bounding it, ~11.7 kB retained per 149-byte message at a 100-validator quorum.
     The claim is now tested *before* the round is entered, by the subset of the vote set's own
     validation that needs no round to exist (claimed index names a validator, that validator
     holds the claimed pro-tx hash, vote is for this height), returning the same errors.
   - **What that bounds.** Pro-tx hashes are public, so passing the check still proves nothing
     about the sender. The ceiling is structural instead: the catch-up allowance is two rounds
     per pro-tx hash, so a height admits at most **2·|valSet|** rounds it did not enter itself
     (~2.3 MB at a 100-validator quorum), cleared by `Reset` on every height — and admitting
     one now costs a signature verification charged to the sender's budget. No separate cap on
     `roundVoteSets` was added; that ceiling is the cap. Measured: 11806 bytes retained per
     message before, 13 after. `SetPeerMaj23`, the other route into the round map, still
     returns early for a round it does not already track.
   - **Then the gate went.** Against the verification flood it was measurably not load-bearing:
     forged commits and forged proposals are ungated and unattributable and settle at the same
     single verification (load suite, before → after removal: forged votes 1.130 s → 1.127 s,
     proposals 1.067 s → 1.080 s, commits 1.073 s → 1.090 s against an honest cost-5 head —
     fake-clock jitter, since the suite injects downstream of the reactor and no flood of its
     own ever met the gate).
   - **Correction to the stated rationale.** The recorded justification — that the gate narrows
     relaying from ~616 evonodes to the ~100-node quorum — does **not** hold. Vote gossip is
     already quorum-only on both sides: `shouldVoteBeGossiped` sends votes only to peers whose
     pro-tx hash is in the current validator set, and a non-validator drops peer votes on
     receipt (`stateData.isValidator`), so it has no vote set to relay from. The real case
     against the gate is that it decides admission on the relayer rather than the signature,
     and that it reads node IDs off the *state's* validator set — whatever the application
     supplied. `ValidatorConnExecutor` resolves node IDs into by-value copies
     (`newValidatorMap`) that never reach that set, so where the application supplies none the
     gate fails open permanently and protects nothing, and where it supplies stale ones the
     gate drops a live validator's votes: the partition its fail-open was written to make
     impossible, arriving through the other door.
   - **Not removed: the receive-side `isValidator` drop.** It is not the same bug. A node
     gossips votes only to peers it believes are validators, so a non-validator neither
     receives votes to relay nor has a vote set to relay them from; making it store votes it
     cannot use would add surface for nothing.
   - Per *connection slot* the channels remain non-interchangeable: one slot sustains 600
     forged votes/s against 100 forged proposals/s, so the vote channel is 6× the cheaper, and
     removal makes that rate reachable from every slot rather than only from validator slots.
     What the node spends is bounded by the node-wide budget either way; what changes is how
     much of it an attacker can contend for, which is the fairness reservation's job (§1).
   - `internal/consensus/vote_path_admission_test.go` (was `vote_sender_gate_coupling_test.go`)
     now pins the post-removal behaviour from both sides.
9. **Keep the commit-forgery disconnect** via the nested-cause check
   (`errors.As(ErrInvalidCommitSignature)` **and** `errors.Is(ErrVoteInvalidBlockSignature)`)
   — best-effort; the one place attribution is provably safe.

10. **Bound the evidence channel** (surfaced by the piece-2 review; previously unowned).
   `internal/evidence/verify.go` performs **two** BLS pairings per `DuplicateVoteEvidence`, the
   evidence channel has **no rate limit at all**, and de-duplication is by evidence *hash*
   (`pool.go`), so flipping a single signature byte forces a full re-verification. Each message
   also does uncharged `LoadBlockMeta` / `LoadValidators` disk I/O. It runs on the evidence
   reactor goroutine — so it does not stall `receiveRoutine` directly, but it steals a core and
   puts concurrent load on a BLS binding whose thread-safety §4 calls unproven. Needs a per-peer
   + aggregate ceiling and a dedup that is not defeated by a byte flip.

**Note on 3b's scope (expanded by the piece-2 review).** Protecting the dialed validator slots
from eviction does **not** raise how many there are: `MaxOutgoingConnections = 12` of `P = 68`,
and the other 56 inbound slots are fillable from one host/one IP with 56 free keypairs. Honest
share is therefore capped at ~12/68 ≈ 17.6 % **regardless of scheduling**, because identity is
free and slots are the scarce resource. 3b must also *raise honest slot count* (retuned outgoing
connections, inbound slots reserved for current-quorum node IDs, and/or per-/24 rather than
per-/32 inbound diversity).

**Gate to ship:** the §5 load test shows consensus stays live under maximum flood. **Met, with a
latency caveat that is Phase 2a's to close — see §9.**

## 3. Why throttle, not "disconnect the spammers"

Honest nodes validate before forwarding, so a *forged* message is attributable in principle
— which is exactly why the commit-forgery disconnect is sound (and vote-forgery could follow
later with the right guards). But disconnect **cannot be the primary defense:**
- **Detecting a bad signature *is* the expensive op** — you've already spent the CPU by the
  time you could punish. The *throttle* is what bounds that spend.
- **A fresh node ID is ~free**, so an attacker reconnects; disconnect is a speed bump.
- **"Verification failed" ⊋ "forged"** — it also covers non-forgery cases (equivocation →
  evidence pool; receiver momentarily missing a key; multi-key signer artifacts) that must
  **never** punish the relayer.

So: throttle-and-drop is the workhorse; disconnect is applied only where the message is
unambiguously forged (commit).

## 4. Phase 2 — parallelize verification (throughput + headroom; separate release)

**Why:** verification runs on the **single consensus thread** (~370/s, one core) — a
legitimate block-production bottleneck independent of any attack.

**Pipeline (must be exactly this shape — the primitive is pure, but *preparing* its inputs
is state-dependent and stays serial):**
- **Serial prepare:** the consensus thread runs the state-dependent steps (structural/dedup/
  context guards; derive the *immutable* sign hash, copied pubkey, expected context —
  proposer selection, key/quorum selection, etc. all stay here).
- **Parallel primitive verify:** a bounded worker pool runs **only** the pure
  `(pubkey, hash, sig) → valid/invalid` math on copied inputs. Workers touch no live state,
  no ABCI, no proposer selector, no WAL. Bind an internal result to the message digest +
  context — **no forgeable `Verified` flag on wire messages.**
- **Serial recheck/apply:** the consensus thread re-checks the context is still valid, then
  applies (count vote, transition). ABCI `VerifyVoteExtension` and all mutation stay here.

**Sub-steps / caveats:**
- **Phase 2a (pulled into Phase 1, done for votes):** the **double-verification** of a valid
  precommit is gone (see §9); the repeated `LastCommit` check is not — free throughput, and it
  clarifies the trust boundary.
- **BLS thread-safety is unproven** (the binding uses a package-global scheme) → verify
  upstream / instantiate per worker / native stress-test before shipping. "N cores → N×" is
  an upper bound, not a guarantee.
- **WAL replay** is synchronous today; async workers need a per-record barrier and no
  double-WAL-write.
- **Completion ordering** (workers finish out of order) is an explicit, tested semantic
  choice.
- **The fairness invariant still applies** — a bounded pool can be occupied by attacker jobs,
  so honest-service reservation from §1 carries over.
- **Batch verification:** no BLS batch verifier exists in-repo, and a failed aggregate batch
  (one bad sig per batch) is *worse* than individual verification → Phase 2c only, and only
  if adversarial benchmarks show a net win.

Bigger and riskier than Phase 1 (touches the verify↔state-machine boundary) → its own review
and testnet cycle; **not** the embargo.

## 5. Measurement / release gate (Phase 1)

Finding 2 is **not "fixed"** until a deterministic load test passes — accounting (fake clock
+ counting seams on the crypto/queue/log/WAL paths) **and** real end-to-end height progress +
bounded honest-message latency on the slowest supported hardware ("not wall-clock *alone*").
Scenarios: max-Sybil adversarial arrival order with a **pre-registered honest Vote/Proposal
service bound**; invalid-block Vote/Commit declaring 32 extensions (proving staged permits
charge ~1, not 33); valid block + invalid first/final extension; fully-valid remote precommit
(one verification pass, its result carried to the vote set); **repeated same + mutated
near-64 KB invalid BlockPart proofs**;
State/VoteSetBits aggregate at max connections incl. 10k-bit inputs; duplicate + novel
VoteSetMaj23; Router shared-queue occupancy/drop metrics; **queue-full/local-overload proven
to never emit `PeerError` or evict**; admission-shed → zero WAL; admitted-then-invalid → ≤1
WAL; replay → no extra WAL.

**Built** as `internal/consensus/load_*_test.go`, `internal/p2p/load_router_queue_test.go` and
`internal/evidence/load_flood_test.go`, all gated behind `testing.Short()`. Every scenario above
is covered and every measured figure is in the commit messages and §9. Two results need reading
rather than filing:
- **the router's shared inbound queue has neither an occupancy gauge nor a drop counter**, and
  it retains up to `RecvBufferCapacity²` envelopes before discarding silently — **16 777 216**
  envelopes of up to 1 MiB for the vote channel. Everything this plan bounds sits downstream of
  it. Measured, not fixed.
- **the consensus and evidence budgets are not jointly bounded**: 300 work/s plus the evidence
  channel's 160 pairings/s is 460 against a verifier measured at ~370/s on one core.

**What the suite does NOT cover, and must not be read as covering.** It injects into the peer
lanes, downstream of the reactor — so the per-peer channel limiters, protobuf conversion,
structural validation and the vote branch's sender filter are all upstream of every flood it
runs. It measures the scheduler, the budget and the cost model; it says nothing about
admission. Two further gaps worth an owner: `HeightVoteSet`'s round allocation draws no budget
at all (§2.8), and work done *before* the first permit — `makeVerifyQuorumSigns` builds a sign
item per declared extension, canonicalising, marshalling and hashing each, before the
quorum-hash guard — is unpriced.

## 6. Explicitly out of scope (kept lean, prior-art-backed)

No comparable battle-tested system uses these to defend expensive per-message crypto:
- a **general weighted-fair-queuing framework** (the *minimal* reservation in §1 is required,
  but not a full WFQ subsystem);
- a **multi-resource budget matrix** (one bottleneck: the verifier — plus the small separate
  State/VoteSetBits and BlockPart ceilings, which are cheap coarse bounds, not a matrix);
- **session-generation cancellation** of in-flight work (early shed is the equivalent);
- **client puzzles / cookies / retry tokens** (anti-spoofing; irrelevant for authenticated
  peers).

(Staged permits and the minimal fairness reservation are **not** here — they're required, §1.
Parallelization is Phase 2, a throughput improvement with direct prior art, not DoS
over-engineering.)

## 7. `#17` disposition

- **Keep:** SEC-002/003/004/007, SEC-008, connection limits, the per-peer limiter (as the
  per-peer cap), the commit-forgery disconnect (narrowed per §2.9).
- **Add:** the global rate/burst budget + staged permits; the fairness invariant; per-peer +
  aggregate caps on State/VoteSetBits; **Vote** extension cost; VoteSetMaj23 suppression +
  bounded response; the BlockPart byte/proof budget; backlog shrink + internal-priority lane
  + **non-punitive** overload drop; the Proposal/BlockPart log fixes.
- **Remove:** the vote gate + SEC-006 metric — **done** (§2.8), after bounding the round
  allocation the gate was standing in for.
- **WAL:** unchanged — an admission-shed message writes zero WAL; an *admitted* message is
  WAL-written once before verification (so a permit-acquiring invalid signature costs ≤1 WAL
  write, not zero — size the peer queue accordingly).

## 8. Release-ordering dependency — the panic-fix cluster (owned separately)

`#16` (the unknown-vote-extension-type panic) was **closed** and replaced by a cluster of PRs
on the private fork. The DoS release must build on top of them and must test them. Status as of
this writing:

**Already merged to `v1.6-dev`** (this branch builds on them):
- **#18 / #30** — `VoteExtensionFromProto` returns an error for an out-of-enum type instead of
  fabricating a `GenericVoteExtension`; the poisoned **precommit** is dropped at
  `MsgFromProto`→`VoteFromProto` rather than panicking the consensus goroutine. #30 also pins
  that a poisoned **commit** already in the WAL still decodes on restart (unbrick).
- **#19** — bounds six unchecked peer-supplied values (QuorumType allowlist, statesync
  `ConsensusParams`, SecretConnection handshake read caps, negative `VoteSetBitsMessage.Round`,
  `Envelope.Attributes` cap, non-resolving `NodeInfo.ListenAddr`). **Breaking:** a stored valset
  with an out-of-allowlist QuorumType now fails to load.
- **#33** — removes dead commit-timeout override knobs (a remote statesync witness could trigger
  their stderr warnings).

**Open — must land before/with the DoS release**, dependency order:
1. **#31** (statesync: verify a backfilled commit before persisting — a peer could self-propagate
   a forged commit with an attacker-chosen extension list). Independent, base `v1.6-dev`.
2. **#32** (reject unknown vote-extension types in `Commit.ValidateBasic` — symmetry with #18).
   **Stacked on #31.** Sequencing constraint: must not land until deployed nodes are one height
   past any poisoned WAL entry (it shares the WAL-decode `ValidateBasic` path — it *inverts*
   #30's unbrick test).
3. **#22** (punish peers that send invalid data) and **#34** (address-eviction hardening).
   Independent.
4. **#17** (this work) rebases on top of all of the above.

**Integration hotspots with `#17`:**
- **#22 ↔ #17 — a reconciliation decision, not just a rebase.** #22 *adds* score+disconnect for
  invalid data; #17's principle is *every shed is a local drop, never punish* (a verification
  failure is not proof of who sent it), and #17 *removes* a pre-existing full-queue honest-sender
  eviction. The boundary must be drawn deliberately or #22 re-introduces that eviction under a
  flood: **invalid *decode* (unmarshalable proto) = proof of fault → #22 may punish; failed *BLS
  verify* / shed under flood = not proof → #17 sheds silently, must NOT be scored/evicted.**
  Decide this when #22 merges.
- **#32 ↔ #17** — shared files (`types/block.go`, `types/vote_extension.go`,
  `internal/consensus/state_data.go`, `UPGRADING.md`): real textual conflicts, manageable.
- **#31 ↔ #17** (2 lines in `vote_extension.go`) and **#34 ↔ #17** (no overlap): trivial/clean.

See §5a for the test coverage these add.

## 8a. Test additions the panic cluster requires (fold into §5 / the devnet runbook)

The load suite currently floods forged-signature precommits/commits. Extend it:

- **Unknown vote-extension type on all three decode boundaries** — precommit (`VoteFromProto`,
  #18/#30), commit at the p2p boundary (`Commit.ValidateBasic`, #32), and backfilled commit
  (#31). Each must be a **local drop / error, never a panic**, and #17's budget must charge it as
  a cheap reject *before* the BLS permit is taken.
- **Duplicate vote extensions in a commit** (#31 bounds the list and rejects repeats) — flood a
  commit with a repeated genuine extension; confirm it's rejected before it "buys a BLS pairing
  per copy" (interacts directly with #17's per-message cost accounting).
- **#19 bounded fields as flood vectors** — out-of-range QuorumType, oversized handshake, negative
  `VoteSetBitsMessage.Round`, over-cap `Envelope.Attributes`: reject cheaply, consume no #17 budget.
- **Statesync backfill flood (#31)** — sybil swarm serving forged commits: quarantine + fail-fast,
  no bad interaction with #17's connection-slot reservation.
- **Mixed-version accept/reject strictness** (these change what a node accepts — test old+new nodes):
  - **#22**: unknown channel-ID / unknown message-type must NOT disconnect (rolling-upgrade
    carve-out); undecodable proto / ValidateBasic failure on a known type IS disconnected+scored.
    **Critically test #17 × #22:** honest peers *shed* by #17 under flood must not be *scored* by #22.
  - **#32**: a future node adding a new extension type has its commits rejected by #32-nodes —
    test and document the forward-compat / rollback constraint.
  - **#18/#30 vs #32**: a pre-#32 WAL entry with a poisoned commit must still decode on restart
    while #32 rejects new ones before the WAL — test the upgrade path.
  - **#19**: a node carrying a stored valset/genesis with an out-of-allowlist QuorumType now fails
    to load — test that upgrade.

## 9. Open items

- **[MEASURE]** the verifier's real per-platform throughput → sizes the Phase-1 gate (ship a
  conservative offline-derived default; no startup auto-calibration).
- **[DECIDED]** fairness form **(b)** (bounded per-peer queues drained round-robin) — form (a)
  is disqualified by arithmetic: at `P = 68` and a 300 work/s safe rate its cap is ~4.4 work/s,
  below the cost of a *single* realistic 4-extension precommit (10). The decisive property is
  that round-robin **decouples** the latency bound from the per-peer rate cap, giving bounded
  delay *and* full-rate catch-up, which (a) structurally cannot. Provisional pending the §5 load
  test; see `docs/CONSENSUS_DOS_PIECE2_SPEC.md`.
- **[DONE]** whether removing the **double verification** (Phase 2a) must be pulled into
  Phase 1. **It must, for any quorum near Dash's size — and it has been.** A remote precommit is
  now verified once, in the vote-extension middleware; the result is carried to the vote set as
  `types.VoteVerification`, evidence naming the vote, chain, quorum and validator key it covers,
  which the vote set accepts only where all of them match what it would have verified against
  itself. Nothing else changed: prevotes, nil precommits, votes attributed to this node, replayed
  votes, commits and the light client verify where they always did.

  What the load suite (`internal/consensus/load_*_test.go`) measured against the 300 work/s
  budget and a fake clock, before → after:
  - a valid 4-extension precommit draws `[1 4 1 4]` → `[1 4]`, so honest demand per validator per
    round falls from **11** work to **6** (prevote 1 + precommit 10 → 5). A 100-validator quorum
    demands **1100 → 600** work per round, **3.67 s → 2.00 s** of budget against a 4 s
    propose+vote window; the budget stops binding in normal operation until about **200**
    validators, up from **109**.
  - a round waits for two thirds of its votes, not one. Serving one head from each of `H` honest
    lanes costs `(H+A)·W` and delivers `H` votes, so a quorum needs `ceil(2Q/3 / H)` rotations.
    At `H = 18`, `A = 50`, one rotation is **2.233 s → 1.117 s**; extrapolated to a 100-validator
    quorum at the 18 honest slots piece 3b guarantees, **9.07 s → 4.53 s**.
  - an honest 4-extension precommit stops meeting the vote timeout at **60 of 68** attacker
    lanes, up from **30 of 68**. One height under a 67-of-68 flood finishes in **689 ms →
    488 ms**.

  The most expensive peer message now costs **33** rather than 66, which carries the cost model
  (`maxPrecommitCost`, `maxPeerMessageCost`), the budget burst and `MinVerificationRateLimit`
  with it. A full-size quorum under maximum flood still takes longer than one round budget to
  gather precommits; closing the rest of that gap is Phase 2's parallel verification.
- **[PHASE 2]** worker-pool sizing, the prepare/verify/apply boundary, BLS thread-safety, and
  the replay barrier — designed in the Phase-2 cycle, not now.
- **[DECISION]** `#16` placeholder-vs-typed-marker (independent, small — owner's call).
