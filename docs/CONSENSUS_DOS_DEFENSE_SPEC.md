# Consensus DoS Defense — Design Spec (v2)

**Status:** DRAFT, revised after 6-way review (5 lens agents + Codex) and correction of
the network-topology premise. Supersedes the stale `CONSENSUS_VOTE_FLOOD_SPEC.md`,
`MASTERNODE_VOTE_GATING_SPEC.md`, `DOS_REMAINING_WORK_BRIEF.md`, and v1 of this file.
Companion: `CONSENSUS_DOS_OPEN_QUESTIONS.md`.

**Embargo:** describes undisclosed, remotely-exploitable vulnerabilities on a live
network. Do not push to public `dashpay/tenderdash` before coordinated release.

**What changed from v1 (and why):** v1 proposed a generalized *peer-scoring* defense
and claimed it "dissolves the membership problem." Review refuted that: a cryptographic
verification failure is **not** proof the *sending* peer misbehaved, so scoring on it
would evict honest relayers and partition the network. Separately, the operator confirmed
the real topology — **~616 evonodes gossip votes; the validator set is a 100-node quorum
that is randomly re-selected ~hourly** — which makes the shipped **vote gate a correctness
bug** (it drops votes relayed by the ~500 evonodes not in the current/previous quorum).
v2 therefore: **removes the gate**, keeps membership-free bounds for the embargo, and
recasts the generalized defense as a narrow, attribution-aware, per-vector misconduct
system for a later release.

---

## 1. Problem

Two independent, remotely-triggerable consensus DoS vulnerabilities, both reachable by
any peer that completes the node-key handshake (no validator membership, no valid
signatures, no user interaction):

- **P0 — remote crash/brick (Finding 1).** A `Commit`/precommit carrying a vote-extension
  with an undefined proto enum `Type` reaches a `default: panic(...)` on the consensus
  goroutine. Inbound messages are WAL-persisted *before* processing, so the poison message
  replays on restart → **non-recoverable crash loop**.
- **Vote-flood — CPU exhaustion (Finding 2).** An unauthenticated peer floods
  structurally-valid votes with invalid BLS signatures; each forces a ~2.7 ms verification
  on the single-threaded consensus path; the attacker-chosen block ID defeats dedup.
  Measured: 30k msgs / 4.47 MB / 32 s → 23,376 verifications, consensus stalled ~3 min.

**Generalized class (central point):** the vote-flood is one instance of
*expensive-verification-from-an-unauthenticated-peer*. The same pattern exists for
**`Commit`** (`VerifyCommit`, same channel, rate-limited but not gated) and **`Proposal`**
(proposer-sig verify). Any defense must be message-type-agnostic in *coverage* while being
per-vector-precise in *attribution* (see §7).

## 2. Network topology (the premise that drives the design)

- **~616 evonodes** (high-performance masternodes) run Tenderdash, each with a `proTxHash`.
  **All ~616 participate in vote gossip.**
- **The validator set is a rotating 100-node quorum**, randomly selected from the full
  evonode set **approximately every hour** (Dash Platform LLMQ rotation).
- Consequences:
  - A vote you receive was almost always **relayed by an honest evonode that is not the
    signer**, and frequently not even in the current quorum.
  - `stateData.Validators` = the current 100-node quorum only; `∪ LastValidators` ≈ ≤200
    specific nodes, and *which* nodes churns hourly.
  - Any gate keyed on the quorum set therefore drops legitimate relay traffic from the
    other ~400+ evonodes, and **cannot be made correct by any quorum-derived set** — only
    the full evonode list (the Core SML) describes "legitimate participant." Obtaining and
    refreshing the SML in Tenderdash is a new Dash-Core dependency we choose to avoid.

## 3. Threat model

- **Attacker:** opens p2p connections with self-generated node keys (no collateral, no
  membership, no valid signatures). May hold many node keys and, up to per-IP limits,
  many IPs.
- **Out of scope:** a malicious *validator* with valid signing keys (different trust
  domain, evidence/slashing territory); seed nodes (PEX-only); transport byte-rate floods
  (our transport defenses already lead upstream and byte-rate cannot bound a CPU-cost
  attack).
- **Assets:** liveness of consensus (both findings), node availability (P0), and — newly
  emphasized — **no self-inflicted eviction of honest evonodes** (a false-positive here
  partitions a 616-node network and is worse than the DoS).

## 4. Attack surface

Reactor handlers are lightweight; BLS verification happens **asynchronously, off the
reactor thread**, in the consensus state machine. Rate-limit guards cover only the vote
and data channels; the State and VoteSetBits channels are neither gated nor limited.

| Message | Channel | Rate-limited | Expensive verification | Attribution to sender |
|---|---|---|---|---|
| Vote (block sig) | Vote | yes(1) | `Vote.Verify` block sig | sender is a relayer, usually not signer |
| Vote (ext sig) | Vote | yes(1) | threshold ext sig | relayer; error currently **untyped** |
| **Commit** | Vote | yes(1) | `VerifyCommit` threshold | relayer; **soundly classified** (see §7) |
| Proposal | Data | yes(5) | proposer sig | relayer; same-height fork can fail honestly |
| BlockPart | Data | yes(1) | block-completion `LastCommit` | **multi-peer; not attributable** |
| VoteSetMaj23 | **State** | **no** | none; forces `VoteSetBits` response | amplification, never a crypto failure |
| VoteSetBits | **VoteSetBits** | **no** | none | never a crypto failure |
| HasVote/HasCommit/NewRoundStep/NewValidBlock | State | no | cheap | — |

## 5. Design — two phases

### 5.1 Phase 1 — Embargo release

Goal: fix P0 outright; bound the vote-flood with **membership-free** controls that are
correct for 616-node gossip; introduce **no** honest-eviction path.

Implemented and kept:
- **P0 fix** (`#16`): fail-closed unknown-extension construction; unknown-type rejection on
  the verification path only (WAL-decode stays lenient so poisoned nodes un-brick).
- **Crypto verification hardening** (`#17`, `types/`): SEC-002 block-sig short-circuit;
  SEC-003 extension short-circuit + error truncation; SEC-004 typed
  `ErrVoteExtensionCountMismatch` excluded from the evictable class; SEC-007 extension cap
  on the verification path only.
- **Log/connection hygiene:** SEC-008; connection-attempt + per-IP(/64) conn tracking.
- **Per-peer rate limiting** (vote + data channels) — membership-free, correct for
  616-node gossip.
- **Existing invalid-commit eviction** (`handleCommitVerifyError`, forgery-only,
  replay-gated) — sound per review; retained.

Changes in this release:
- **REMOVE the vote gate `voteSenderAllowed`** and its **SEC-006** metric. It narrows
  616→~100 (§2) and drops honest relayed votes — a correctness bug, not a control we can
  keep.
- **ADD SEC-005 — per-IP aggregate limiter** on the consensus vote/data channels, so the
  flood is bounded across an attacker's many node keys, not just per-connection.

Explicitly deferred: the generalized misconduct system (§5.2). It is **not** an embargo
prerequisite.

**Residual-risk note (must be measured, not asserted):** with the gate gone, the
vote-flood is bounded only by (max inbound connections) × (per-peer + per-IP rate) ×
(per-message cost, now ~1 pairing after SEC-002). §9 requires computing and load-testing
this bound; if it does not keep consensus live, Finding 2's *complete* fix ships with
Phase 2 and the embargo is disclosed as "P0 fixed; vote-flood substantially hardened,
bounded rate."

### 5.2 Phase 2 — Generalized misconduct system (later release, own review + testnet)

A narrow, attribution-aware mechanism that punishes only **provable, unambiguous**
misbehavior, message-type-agnostic in coverage but per-vector-precise in evidence. Built
on the *shape* already proven by the commit path, not the naive "any verification failure
→ score." Staged (Codex): 2a typed feedback for unambiguous events + backlog control; 2b
short NodeID reconnect cooldown + measurement; 2c durable reputation / IP policy **only if
measurements justify it**; plus independent per-peer State/VoteSetBits budgets.

## 6. Why the gate is removed (not kept), and why not the SML

- Keeping it: ships a correctness bug (drops ~500 evonodes' relayed votes; hourly quorum
  rotation guarantees the dropped set churns network-wide).
- Fixing it "properly": requires the full evonode SML, refreshed from Dash Core — a new
  cross-process dependency and refresh loop. Rejected as scope/complexity for a DoS fix.
- Not needing it: membership-free bounds (rate-limit per-peer + per-IP, cheap
  verification) bound the flood without any membership question, and are *correct* for
  616-node gossip. The economic-exclusion value a gate would add is exactly what the
  SML-dependency would cost; we decline the trade.

## 7. Classification — "verification failure ≠ sender guilt" (the core safety rule)

A failed BLS check proves only that a signature does not verify **under the receiver's
locally-selected quorum/key/context**. In a 616-relay, hourly-rotating network the sender
is almost always an honest relayer. Punishment must therefore be restricted to
**per-vector, provably-unambiguous forgery**, with every honest-reachable failure excluded.

**Per-vector evidence matrix (Phase 2 contract):**

| Vector | Punishable sentinel | Preceding guards that must be non-evictable | Excluded (honest-reachable) |
|---|---|---|---|
| Commit | `ErrInvalidCommitSignature` only | height, blockID, quorumHash, count, cap (all already non-evictable) + `!fromReplay` | quorum-hash / count / height mismatch |
| Vote block sig | `ErrVoteInvalidBlockSignature` only | off-height already dropped with **no** error; `!fromReplay`; receiver-pubkey OK | pubkey-missing/size (receiver state), stale/step |
| Vote ext sig | **new** `ErrVoteInvalidExtensionSignature` (currently an untyped string — must be typed) | as above | count mismatch (SEC-004) |
| Proposal | `ErrInvalidProposalSignature` — **only after** a commit-style guard analysis proves same-height-fork/divergent-state cannot reach it | TBD in 2a | same-height fork, divergent app/validator state |
| BlockPart | **none** | — | block completed from parts supplied by *multiple* peers; last-part sender ≠ author |

**Never punishable (route elsewhere or ignore):** `ErrVoteConflictingVotes`
(equivocation → evidence pool; the relayer is honest and punishing it *suppresses
slashing evidence*), `ErrVoteNonDeterministicSignature` (equivocation / tmkms
multiplexing), `ErrVoteUnexpectedStep` (stale height/round), `ErrVoteExtensionCountMismatch`
(version), unknown-inert extension type, and all receiver-state key errors.

**Do not reuse `isPeerFloodableError` as the classifier** — it is a *log-verbosity*
predicate and includes `ErrVoteUnexpectedStep` (honest). The punishable set is a distinct,
explicitly-enumerated allowlist, with a test asserting the two sets are disjoint.

## 8. Phase-2 mechanism constraints (from review — each is a must, not an option)

1. **Dedicated typed misconduct event**, separate from generic `PeerError`. Reusing
   `PeerError` conflates generic reactor errors with security evidence.
2. **Do not route through capacity-promotion.** `router.go` promotes *any* non-fatal
   `PeerError` to immediate eviction when `len(connected) ≥ MaxConnected` — true in steady
   state and *guaranteed* during a flood. A misconduct signal must have its own
   eviction decision independent of connection-capacity replacement, or "tolerate a few"
   is impossible.
3. **Replay gate.** Votes are WAL-persisted and re-dispatched under their original
   `PeerID` on `catchupReplay`. `AddVoteEvent` currently lacks `FromReplay` (the only
   peer event that does) — add it and gate emission on `!fromReplay`, mirroring the
   commit path.
4. **Bound already-admitted work.** Eviction stops *future* ingress only; the consensus
   message queue holds up to **20,000** admitted messages, processed regardless. Tag work
   with a connection/session generation and check it immediately before expensive
   verification, or use purgeable per-peer queues. Eviction alone does not bound cost.
5. **Fair feedback, correct loss semantics.** The existing feedback queue is a single
   global FIFO whose producer drops the **new** report when full (not the oldest, as v1
   wrongly claimed) — one attacker can monopolize it and mask reports about others. Use a
   bounded **per-peer coalescing** counter + nonblocking wakeup.
6. **Ban is a reconnect cooldown, not a Sybil defense.** NodeID is authenticated but
   cheap to regenerate. IP bans hit NAT/IPv6-/64 collateral and need composite-identity
   plumbing into `PeerManager.Accepted`. Durable score/ban need a `p2pproto.PeerInfo`
   migration (`MutableScore` is ephemeral / same-process only) **and** exemption from
   `prunePeers` (which deletes lowest-scored, disconnected entries first — i.e. it would
   delete fresh bans, an evasion). Treat durable/IP bans as a separate measurement-driven
   P2P project (2c).
7. **Cover the non-crypto vectors separately.** Scoring never fires for State /
   VoteSetBits floods or `VoteSetMaj23` response-amplification (no crypto check). Add
   per-peer budgets on those channels (reuse the existing `ThrottledChannelIterator`,
   currently wired only for mempool) — independent of the misconduct system.

## 9. Test / verification plan

Phase 1 (mostly done): red→green unit tests for SEC-002/003/004/007; P0 WAL-replay-no-panic;
full `types` + `internal/consensus` suites green. **New for the embargo:**
- SEC-005 per-IP limiter unit tests.
- **Measured flood bound:** compute and load-test the max verifications/sec an attacker can
  force with the gate removed (connections × per-peer × per-IP × ~1 pairing), asserting a
  deterministic consensus-progress criterion — **wall-clock alone is insufficient**.

Phase 2:
- **Negative (no-false-positive) matrix** — for Vote/Proposal/Commit: same-height different
  quorum-hash; same proTx, changed pubkey; receiver stale/divergent app state; current↔prev
  transition + late old-height data; honest non-origin relay; wrong blockID / quorum hash;
  extension count/version + unknown inert type; **WAL replay carrying historical peer
  metadata**; **router at MaxConnected**. Assert **no** misconduct signal, disconnect, or
  ban in every non-evidence case.
- Classifier disjointness test (punishable set ∩ floodable-log set = ∅).
- Backlog test: exact max crypto ops admitted before *and after* eviction.
- Feedback fairness: one peer cannot starve reports about others; shutdown/leak; replay
  exclusion; disconnected/reconnected session.
- Ban: many NodeIDs on one IPv4 and one IPv6 /64; one NodeID changing IP; `prunePeers`
  under store pressure does not delete an unexpired ban; clock-jump/expiry.
- **Testnet validation** across a quorum rotation before tagging.

## 10. Alternatives rejected

- **Membership gate (quorum or SML-scoped).** Quorum-scoped = correctness bug (§2/§6);
  SML-scoped = new Dash-Core dependency + refresh loop. Both declined.
- **Naive peer-scoring ("any verification failure → score/evict").** Refuted by review:
  partitions the network and suppresses slashing evidence (§7).
- **Transport byte-rate limiting as the lever.** Cost is CPU-per-message, not bytes; our
  transport already leads upstream.
- **Backporting upstream p2p.** None to port — our p2p is the Tendermint v0.36 Router at
  upstream EOL; we lead on DoS defense. CometBFT's ban design is a *pattern* reference only.

## 11. Review disposition (durable record of the 6-way review)

- **Scope:** Phase 2 over-scoped for the embargo; close the concrete gap cheaply, measure
  before asserting urgency, decouple Phase 2. → Adopted (gate removed instead of extended;
  SEC-005 as the embargo aggregate bound; measurement required).
- **Feasibility:** commit-forgery feedback already ships as `Fatal:true` instant-evict (not
  score); `isPeerFloodableError` includes an honest error; `MutableScore` non-persistent;
  gap-1 plumbing already exists. → §7/§8 fixed classifier + typed event; §5.1 keeps sound
  commit path.
- **Adversarial-premise:** "verification failure = sender guilt" refuted; three honest
  mechanisms (rotation skew, relayed equivocation, threshold-recovery timing). → §7 core rule.
- **Security:** classifier includes honest error; pre-existing unconditional `SendError`
  scores structural errors; `prunePeers` deletes bans; State/VoteSetBits ungated; commit
  aggregate bound. → §8.2/§8.6/§8.7, §9.
- **Domain-correctness:** commit path already rotation-safe (QuorumHash guard →
  non-evictable) and replay-gated — the **template**; vote path lacks replay-gate,
  evidence-pool collision (equivocation), pubkey-fault conflation, capacity-promotion. →
  §7 matrix, §8.2/§8.3.
- **Codex:** eviction doesn't cancel the 20k backlog; feedback drops *new* not oldest;
  `Score()` wrong predicate; per-vector table; keep the gate until a proven replacement —
  *superseded here by removing it, since the topology makes it a bug, with membership-free
  bounds taking its place.* → §8.4/§8.5, §5.1.

## 12. Rollout

1. **Embargo release:** P0 fix + crypto hardening + log/conn hygiene + per-peer rate limit
   + **SEC-005** + **gate removed**; sound invalid-commit eviction retained. Ship once the
   §9 measured flood bound is acceptable.
2. **Phase 2 (2a→2c):** dedicated typed misconduct + backlog control → reconnect cooldown +
   measurement → durable reputation/IP only if justified; plus independent State/VoteSetBits
   budgets. Separate review + testnet cycle.

## 13. Open questions (remaining)

- **[DOMAIN] RESOLVED:** vote relay is evonode-wide (~616), validator set is a 100-node
  hourly-rotating quorum, SML is the only correct membership set and is not in-process →
  gate removed, no SML dependency taken. (Closes A1/A2/A5.)
- **[MEASURE]** Does the §9 flood bound (gate removed) keep consensus live? Decides whether
  Finding 2 is "fixed" or "hardened, full fix in Phase 2" for disclosure.
- **[DESIGN]** Proposal-vector guard analysis (is `ErrInvalidProposalSignature` ever
  honest-reachable?) — gates whether proposals are punishable in 2a.
- **[DECISION]** #16 placeholder-vs-typed-marker (reply-and-keep vs distinct
  `unknownVoteExtension`).
- **[DESIGN]** Phase-2 ban keying / persistence (2c), only if measurements justify durable
  bans.
