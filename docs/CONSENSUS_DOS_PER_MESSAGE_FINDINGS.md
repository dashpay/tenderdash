# Per-Message DoS Findings (research synthesis, for review)

Five parallel research passes (one per vector), grounded in the `fix/consensus-vote-flood`
worktree. Condensed load-bearing claims + code refs for independent verification. Feeds
the plan; companion to `CONSENSUS_DOS_MESSAGE_MATRIX.md` and `CONSENSUS_DOS_DEFENSE_SPEC.md`.

Constants verified in-tree: `PeerVoteRateLimit=100` (config.go:1181), `MaxConnections=64`
(config.go:803), `PeerDataRateLimit=500` (config.go:1188), `msgQueueSize=100*100*2=20000`
(state.go:55). Serial BLS ceiling ≈ 370 verifies/s (~2.7 ms each, single consensus goroutine).

---

## 1. Vote (Vote channel)

- **Cost:** after SEC-002 (block-first short-circuit), a forged vote = **exactly 1 pairing**;
  SEC-007 cap(32) + SEC-003 first-fail only matter once the block sig is valid. Two BLS entry
  points: precommit via `VerifyBlockAndExtensionSigns` (state_add_vote.go:266), prevote via
  `VoteSet.addVote → Vote.Verify` (vote_set.go:248). Dedup keyed on `BlockID.Key()`
  (vote_set.go:273) → attacker varies BlockID.Hash to defeat it.
- **Self-gate:** `handleVoteMessage` only enqueues to consensus if `isValidator(self)`
  (reactor.go:725) — so only the ~100 current validators verify votes. Severity unchanged
  (that's the liveness-critical set).
- **Attribution:** sender = relay, never signer (verified vs `stateData.Validators` keys,
  not `envelope.From`). Honest-reachable failures: `ErrVoteUnexpectedStep` (stale),
  `ErrVoteMissing/InvalidValidatorPubKey(Size)` (receiver state), `ErrVoteConflictingVotes`
  (→evidence pool, state_add_vote.go:348), `ErrVoteNonDeterministicSignature`,
  `ErrVoteExtensionCountMismatch`. Height/no-pubkeys/dedup dropped with **no error**.
- **Rate-limit INSUFFICIENT:** gate removed ⇒ `64×100 = 6,400/s` offered vs `370/s` ceiling
  = **~17× over → still stalls**; even 4 conns exceed one core. Plus 20k queue ≈ **54 s** of
  un-cancellable CPU. **[MEASURE] = NO, #17 does not fix Finding 2.**
- **Classification (Phase 2, careful):** punishable = `ErrVoteInvalidBlockSignature` (match via
  `errors.Is` — it's `%w`-wrapped through `ErrInvalidVoteSignature`, errors.go:13) + a **new
  typed** `ErrVoteInvalidExtensionSignature` (currently an untyped string, quorum_sign_data.go:85).
  Requires adding `FromReplay` to `AddVoteEvent` (state_add_vote.go:23 — the only peer event
  lacking it). Exclude the honest set above. `isPeerFloodableError` must NOT be the classifier
  (it includes `ErrVoteUnexpectedStep`).
- **Rx:** retune per-peer rate to ~ceiling + per-IP (SEC-005) + backlog bound = the fix;
  forgery-evict is an optional later deterrent.

## 2. Commit (Vote channel)

- **Cost/guards:** `VerifyCommit` guards **a–g all non-evictable** (ValidateBasic, height×2,
  blockID, ext-cap, quorumHash) precede the threshold check **h**; only a full-match-then-
  threshold-fail = evictable `ErrInvalidCommitSignature` (validator_set.go:905→910). Block sig
  first + short-circuit ⇒ 1 pairing for a forgery.
- **Sound one-strike evict:** `handleCommitVerifyError` sends `Fatal:true` (state_try_add_commit.go:116),
  replay-gated (`!fromReplay`). **QuorumHash⟹ThresholdPublicKey** ⇒ rotation-safe (a stale-quorum
  commit exits non-evictable at guard g, never reaches h under a wrong key). Fatal:true bypasses
  the capacity-promotion side-channel by construction.
- **Never-gated:** the vote gate only guarded the Vote arm; removing it doesn't change Commit.
- **Shared budget:** votes+commits draw one per-peer vote-channel bucket (100/s, burst 200).
- **Residual:** Sybil (cheap node keys) × ~200 admitted/identity + 20k backlog; SEC-005 is the
  main Sybil lever (not present). **One narrow honest edge:** a genuine commit with a same-count
  but content-divergent extension whose ext-sig fails is wrapped evictable (near-zero; fix by
  excluding ext-sig failures — only block-sig is unambiguous).
- **Rx:** keep the sound Fatal:true block-sig evict; exclude ext-sig; add per-IP + backlog.

## 3. Proposal (Data channel)

- **Cost:** 1 pairing `proposer.PubKey.VerifySignatureDigest` (state_proposaler.go:240), serial.
- **NOT attributable (decisive):** `ErrInvalidProposalSignature` is honest-reachable and lacks
  commit's guards — sign-id built from **local** `committedState.Validators` quorumType/Hash +
  **locally-selected** proposer; the proposal carries no independent quorumHash to compare, so
  **fork / mixed proposer-selection version / rotation-skew all collapse into the same error**
  as forgery. Sender = relay. **Never punish.**
- **Load-bearing mitigations (don't regress):** `rs.Proposal != nil` short-circuits before verify
  (state_proposaler.go:48); exact-(H,R) required (off-(H,R) dropped no-error, :53). So the attack
  window is only the pre-proposal slice of the current round; within it each invalid proposal =
  1 pairing and can delay the honest proposal in the FIFO.
- **Bound:** `N×100/s` (cost 5, 500/s data budget). **Rx:** rate-limit + per-IP + backlog only.

## 4. BlockPart (Data channel)

- **NOT attributable (structural):** parts from multiple peers merge into one shared `PartSet`
  (state_add_prop_block.go:115); every part is merkle-verified against the **proposer-signed
  header** (part_set.go:305) ⇒ content is fixed by the proposer, last-part sender is irrelevant;
  state machine swallows part errors (loggingMiddleware, msg_handlers.go:163) → never a PeerError.
  **Never punish.**
- **Class B (bandwidth/reassembly):** per novel part = one ~64KB SHA-256 (+≤100 inner);
  duplicates/over-range cheap (before hash). Existing bounds sufficient: MaxBlockPartsCount=1601,
  64KB/part, MaxAunts=100, ByteSize vs MaxBytes, **drop-if-no-header** (no hashing for a header we
  don't hold). Only BLS = `LastCommit VerifyCommit`, **deferred, one-per-completed-block**
  (validation.go:86).
- **Bound:** `~500 parts/s × conns` cheap hashes; transport proto-decode is upstream of the limiter.
- **Rx:** keep cost 1 + existing bounds + per-IP; no misconduct path.

## 5. State + VoteSetBits channels (NewRoundStep, NewValidBlock, HasVote, HasCommit, VoteSetMaj23, VoteSetBits)

- **Completely ungated:** rate limiters cover only Vote+Data; **State (0x20) and VoteSetBits (0x23)
  have none**. No BLS on any of these.
- **VoteSetMaj23 = forced-*work*, not byte-amplifier:** authenticated p2p ⇒ the forced `VoteSetBits`
  response goes only to the sender (~1.2× bytes, not reflection). But **resending the same request
  emits a response every time** (unbounded 1→1); each forces locks + two bit-array copies + marshal
  + an outbound slot. `maxPeerMaj23s=4096` bounds memory only, not response generation.
- **Queue-stall DoS:** VoteSetBits `SendQueueCapacity=8`; once full, `voteSetCh.Send` (reactor.go:559)
  **blocks the serial state goroutine → stalls all state-channel handling**. Inbound queue is shared
  per-channel (router.go:171) — no per-peer fairness; one flooder crowds out honest HasVote/NewRoundStep
  and hammers the `VoteSet`/`HeightVoteSet` locks the live consensus loop needs.
- **Rx:** per-peer `client.RateLimit` on both channels (the mempool pattern — NOT the global
  `ThrottledChannelIterator`, which isn't per-peer) + a per-peer VoteSetMaj23 **response** bound.
  This class is uncovered by #17 today.

---

## Cross-cutting conclusions

- **Primary defense = rate-limiting done right + backlog bound + close Class C** — membership-free,
  covers all 11. NOT the gate, NOT peer-scoring.
- **Eviction is a deterrent, sound only for Commit** (shipped). Vote = careful later; Proposal /
  BlockPart = never (unattributable).
- **#17 verdict:** keep hardening (SEC-002/3/4/7) + Commit evict + log/conn; **retune** the per-peer
  rate (100/s ≫ 370/s ceiling) + **extend to State/VoteSetBits**; **remove** the gate+SEC-006; **add**
  per-IP (SEC-005) + backlog + VoteSetMaj23 response bound.
