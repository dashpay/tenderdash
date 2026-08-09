# Consensus vote-flood DoS — hardening spec

Status: **IMPLEMENTED on this branch.** Every layer below was built, TDD'd
(red→green), and adversarially reviewed (each review-flagged defect was fixed —
see the per-commit notes). Recommended before release: testnet / e2e validation
across a quorum rotation.
Affected: all Tenderdash versions with the current consensus reactor.

The root-cause fix is the **validator-set vote gate** (see
`MASTERNODE_VOTE_GATING_SPEC.md`): only current/previous validators can send
votes, so the attacker must own an evonode. The layers below (§4) are
defense-in-depth that bound a misbehaving/compromised validator; they compose
with the gate.

### What shipped (this branch)

| Layer | What | Commit subject |
|---|---|---|
| **Gate** | validator-set vote gate (root-cause fix) | `gate votes to validator-set senders` |
| L5 | vote-extension count cap (32) | `cap vote-extension count …` |
| L1 | per-peer vote-channel rate limit + burst/config tuning | `per-peer vote-channel rate limit` |
| L4 | log amplification (error-type classified) | `bound invalid-vote log amplification …` |
| — | disconnect on invalid commit signature (typed) | `disconnect peers sending cryptographically invalid commits` |
| — | data-channel proposal rate limit (cost-weighted) | `per-peer data-channel rate limit …` |
| L6 | verify block signature before extension work | `verify block signature before vote-extension …` |
| — | wire `max-incoming-connection-attempts` to router | `wire max-incoming-connection-attempts …` |

Deliberately **not** built: L3 (WAL-write-after-verify, deferred — L1 already
bounds what reaches the WAL); the full Dash-Core masternode poll (the in-process
validator set is tighter and needs no new dependency — see gating spec §10). A
separate latent issue (`HeightVoteSet.peerCatchupRounds` keyed on the
attacker-chosen ProTxHash, §10) is noted for its own ticket.

The section below (§1 mechanics, §4 layer analysis) is the original design and
review record — it documents each layer and the defect review found in its
first draft, all of which were resolved in the shipped implementation.

---

A separate, more severe vulnerability found during this work — a one-packet
remote crash/brick via a Commit vote-extension type — is documented in
`COMMIT_EXTENSION_PANIC_SPEC.md` and shipped on branch
`fix/commit-extension-panic`. This document is only about the resource-exhaustion
flood.

**Independent validation (Codex, gpt-5.6-sol).** DoS-1 (the flood) confirmed;
the ~2.7 ms verification cost independently reproduced at 2.89 ms. EXT-1
(extension amplification) confirmed as PARTIAL: the pre-block-signature
extension-*pairing* amplification is real, but the earlier claim that an
unprivileged peer could force the ABCI `VerifyVoteExtension` call was **wrong**
and has been removed (the ABCI call is gated behind valid extension signatures
the attacker cannot produce — §1.5a). Codex's precision fixes (vote flood is
validator-specific; the exact benchmark figure) are incorporated. Its review of
the *mitigations* (L1–L6) was limited by its cybersecurity output filter; the
defect analysis in §4 comes from the earlier review agents, not Codex.

## 1. Problem

An unprivileged peer (one that completes the p2p node-key handshake; no
validator membership required) can force a Tenderdash **validator** to perform
an expensive BLS signature verification, a WAL write, and two `Error`-level log
lines per 149-byte vote it sends, at a rate limited only by the per-connection
byte-rate cap (5 MB/s default). Measured on a testnet validator: 30,000
messages / 4.47 MB / 32 s produced 23,376 BLS verifications and 46,752 log
entries, and consensus stopped advancing for 2m58s after the attacker
disconnected.

The vote-flood path is validator-specific: the `*tmcons.Vote` branch is gated
on `isValidator` (`internal/consensus/reactor.go:647-666`), so full nodes
ignore votes. Full nodes have a *separate* exposure through the un-gated commit
path (§1.6), at a different cost. The QA reproduction's "overload ~8
validators" model is consistent with the validator-only vote path.

Because the cost is per destination, an attacker halts consensus by targeting
more than 1/3 of voting power, not the whole network. On a 340-node mainnet
that is roughly 114 nodes.

### 1.1 Measured cost

`BenchmarkVerification` (`crypto/bls12381/bench_test.go`, Apple M1 Pro,
`-benchtime=300x`): **2.70 ms per BLS verification** (signing: 0.72 ms). An
independent run on the same hardware measured **2.89 ms/op** — confirming the
magnitude, not the exact figure. Call it ~2.7–2.9 ms.

- One core sustains ~370 verifications/s. The reproduction's 23,376
  verifications = ~63 s of single-core CPU extracted from 32 s of attack —
  which is why consensus stayed pinned for 2m58s while the queue drained.
- Verification runs on the single consensus goroutine, so this is a hard
  serial bottleneck.
- Saturating one core costs the attacker ~370 messages/s ≈ **55 KB/s per
  target** — cheaper than the raw byte-rate cap suggests.

### 1.2 Why the existing defenses do not apply

| Defense | Why it does not help |
|---|---|
| Secret-connection handshake (`internal/p2p/conn/secret_connection.go`) | Authenticates that a peer owns its node key. It does not require the peer to be a validator; any keypair may open the consensus channels. |
| `RecvRate` / `SendRate` (5 MB/s, `config/config.go:781-784`) | A per-connection **byte** cap. The attack needs only ~55 KB/s per target. |
| `MaxIncomingConnectionAttempts` (`config/config.go:756`) | Limits connection *attempts*, not messages on an established connection. |
| `PeerError` from the consensus reactor (`internal/consensus/reactor.go:779`) | Sent with `Fatal: false`, which only decrements the peer score by 1 (`internal/p2p/router.go:386-391`). No disconnect. |
| Per-peer message rate limiting | Wired for the mempool channel only (`internal/mempool/p2p_msg_handler.go:49`). Consensus channels have none. |

### 1.3 Confirmed mechanics

Path of one hostile vote:

1. `Reactor.processMsgCh` (`internal/consensus/reactor.go:773`) reads the
   envelope off `ConsensusVoteChannel`.
2. `Reactor.handleVoteMessage` (`reactor.go:618`) runs `Vote.ValidateBasic()`
   — structural only, no signature verification — then enqueues onto
   `msgInfoQueue` (`msgQueueSize = 20000`, `state.go:55`) and returns `nil`, so
   no `PeerError` is produced.
3. `walMiddleware` (`internal/consensus/msg_handlers.go:138-161`) writes the
   message to the WAL **before** the handler runs — before any signature check.
4. `voteMessageHandler` → `AddVoteEvent` → `VoteSet.addVote`
   (`types/vote_set.go:197`) does cheap checks, then `vote.Verify(...)` at
   `vote_set.go:248` — the expensive BLS operation.
5. On failure the error is logged twice: `addVoteLoggingMw`
   (`state_add_vote.go:360`) and `loggingMiddleware` (`msg_handlers.go:176`) —
   matching the observed 2:1 log-to-verification ratio.
6. `loggingMiddleware` returns `nil` (`msg_handlers.go:177`), so the failure
   never propagates back toward the reactor or the peer.

`msg_handlers.go:113-117` documents the current stance (`// TODO: punish
peer`). That reasoning is sound for stale or conflicting votes; it is not sound
for a vote whose signature does not verify, which no honest peer relays.

### 1.4 The amplification primitive

`VoteSet.addVote` deduplicates on `(ValidatorIndex, BlockID.Key())`
(`vote_set.go:238`), and a repeated signature is caught cheaply
(`ErrVoteNonDeterministicSignature`, `vote_set.go:242`). But an attacker who
**randomizes `BlockID.Hash`** produces a fresh dedup key every message and
reaches `vote.Verify` every time. Nothing between `ValidateBasic` and the BLS
call constrains `BlockID` to a known block — and it cannot, because a lagging
node legitimately receives votes for proposals it has not yet seen.

### 1.5 Extension amplification (worse than the reproduction measured)

The reproduction used simple votes. The precommit-with-extensions path is worse:

- **(a) Extension-signature verification before the block signature is
  checked.** `addVoteVerifyVoteExtensionMw`
  (`internal/consensus/state_add_vote.go:223-272`) runs `VerifyExtensionSign`
  at `:261` — which verifies every extension signature via
  `VerifyVoteExtensions` (no short-circuit) — *before* `next(...)` reaches
  `VoteSet.addVote` and the block-signature check. So an attacker forces a full
  set of extension pairings on a vote whose block signature is never validated.
  **Correction (independent review):** the ABCI round-trip
  `blockExec.VerifyVoteExtension` at `:264` is **not** reachable by an
  unauthenticated attacker — it runs only if `VerifyExtensionSign` *succeeds*
  (`:261-263` returns on error), which requires valid signatures under the
  target validator's BLS key that the attacker does not hold. Earlier drafts
  claimed the ABCI call was reachable; it is not. The amplification here is the
  extension-signature pairings, not an ABCI call.
- **(b) Weaker dedup.** The short-circuit at `state_add_vote.go:245-250` keys
  on equal `BlockSignature` and equal extension fingerprint. Varying only the
  96-byte `BlockSignature` defeats it, without even varying `BlockID`.
- **(c) Unbounded extension count.** `Vote.ValidateBasic` →
  `VoteExtensions.Validate()` (`types/vote_extension.go:89`) never bounds the
  count; the only limit is `maxMsgSize` (1 MB). Each extension with a
  well-formed 96-byte signature costs one pairing in
  `QuorumSignData.VerifyVoteExtensions` (`types/quorum_sign_data.go:71-92`),
  which has no short-circuit. Empty-signature extensions fast-fail before the
  pairing (`crypto/bls12381/bls12381.go` checks `len(sig)==0`), so the
  realistic worst case is ~9,500 well-formed extensions per 1 MB message ≈
  ~25 s of single-core CPU from one message. The danger is not raw efficiency
  but that it **concentrates cost into few messages, defeating any
  per-message rate limit.**

### 1.6 Non-validator nodes

The `*tmcons.Commit` branch (`reactor.go:635-646`) is not gated on
`isValidator` (that gate covers only `*tmcons.Vote`), so full nodes process
commit threshold-signature verification too. Seed nodes are unaffected
(`node/seed.go:42-51`, PEX reactor only).

## 2. Threat model

In scope: any peer that can handshake (no validator membership, no valid
signatures); structurally-valid but cryptographically-invalid messages; single
source IP, one connection per target.

Out of scope: a validator equivocating or gossiping stale-but-valid votes
(evidence system's job, must keep working); attacks requiring valid signatures;
network-layer bandwidth exhaustion.

## 3. Design goals

- **G1** — A peer must not be able to force more than a bounded amount of
  verification work per second, denominated in *work units* (not messages).
- **G2** — A peer that sends a cryptographically invalid consensus message
  should be disconnected, not merely score-decremented.
- **G3** — No honest peer may be disconnected or have its votes dropped in a
  way that stalls it. Stale votes, votes for unknown proposals, duplicates,
  conflicting (equivocating) votes, and catch-up traffic must keep working.
  **Getting this wrong partitions the network, which is worse than the DoS.**
- **G4** — Log volume bounded by peer count and time, not inbound rate.
- **G5** — No consensus-breaking or wire-format change without an explicit
  protocol-version decision (see §6).

## 4. Proposed layers — and the defect each must fix before shipping

Independent review found every first-draft mitigation defective. Each layer
below states the idea *and* the blocking defect. Only L4 survived review
unchanged.

### L1 — Per-peer cost-weighted receive rate limit (REWORK REQUIRED)

Idea: reuse the mempool's per-peer token bucket
(`internal/p2p/client/ratelimit.go`) on the consensus channels, charging tokens
by work (`nTokens = 1 + len(vote.VoteExtensions)`, plus a byte term).

Blocking defects:

- **`drop=true` wedges consensus.** Senders optimistically mark votes delivered
  even when the send failed (`internal/consensus/gossiper.go:299`,
  `gossip_peer_worker.go:49` sets `optimistic: true`); dropped votes/parts are
  **never resent** (`GossipVote`, `GossipProposal`, `GossipCommit`, same-height
  `GossipProposalBlockParts`). This is the exact failure CHANGELOG #1365 fixed
  for the catch-up path. A dropping limiter on the vote or data channel
  re-introduces it. Fix: `drop=false`, or make gossip non-optimistic first.
- **A message heavier than `burst` is dropped forever.** `burst = 10 × limit`
  (`ratelimit.go:55`); `rate.Limiter.AllowN` returns false permanently when
  `n > burst`. A precommit with more extensions than `burst` becomes silently
  invisible → halt. Fix: validate at config load that `burst` exceeds the
  maximum legitimate single-message weight.
- **Per-keypair, not per-attacker.** Fresh node IDs are free
  (`types/node_id.go`), each grants a full `10 × limit` burst, and
  `PeerManager.Errored` does not ban (`peermanager.go:1009-1029`). A limit
  keyed only on NodeID is bypassed by identity churn; needs a coarse
  per-remote-IP companion (the router already tracks IPs in `connTracker`).
- **Latent bug to fix in the same PR:** `RateLimit.Limit`'s non-drop path
  discards `nTokens` and charges 1 (`ratelimit.go:101`).

Honest peers are self-limiting at ~10 msg/s/channel (`PeerGossipSleepDuration`,
100 ms) against the attack's ~940/s — so the headroom for a correctly-built
limiter is real; the danger is entirely in dropping and weighting.

### L2 — Disconnect peers sending cryptographically invalid messages (REWORK REQUIRED)

Idea: set `PeerError.Fatal = true` (already means "evict",
`internal/p2p/router.go:381-391`) for errors no honest peer can produce,
propagated from the consensus goroutine to the reactor via a non-blocking queue
(like the existing `statsQueue`, drained by `peerStatsRoutine`).

Blocking defects in the first-draft error classification:

- **`ErrVoteInvalidSignature` is dead code** — never returned; the live error
  is `ErrVoteInvalidBlockSignature` surfaced via the `ErrInvalidVoteSignature`
  conversion (`types/vote_set.go:256`). A predicate on the sentinel never
  fires.
- **`ErrVoteInvalidValidatorIndex` / `…ProTxHash` are unreachable** (height
  guard fires first) — drop them; they add partition risk for no value.
- **"commit/proposal signature errors" evict honest peers.** Commits are
  verified against *our local proposal* (`state_data.go:479`), so an honest
  peer relaying a round-0 commit after we moved to round 1 produces a
  signature-shaped error. Proposal errors depend on block-store-dependent
  proposer selection, so a state-synced/pruned node could evict every peer at
  a height.
- **`"msg queue is full"` (`msg_queue.go:53`) must be excluded** — it is
  attributed to whichever honest peer's message arrived during the flood,
  handing the attacker an eviction primitive against honest peers.
- **Replay must be excluded** — `readReplayMessage` re-dispatches with the
  original `PeerID` (`replay.go:89`); gate any report on `envelope.fromReplay`.

Also: L2's deterrent value is limited. Eviction does not ban
(`peermanager.go:1009`), and the handshake is symmetric (costs the victim as
much as the attacker), so eviction alone does not raise the attacker's cost
above the work one message causes. L2 is worth doing for log/state hygiene and
to stop a single persistent connection, but a real deterrent needs a ban list,
which is a larger change.

### L3 — Stop writing unverified peer messages to the WAL (FOLLOW-UP)

Move the WAL write for `PeerID != ""` messages to *after* successful handling.
Removes disk amplification and shrinks replay surface. Riskiest item (changes
replay semantics); a correct L1 already prevents the messages from reaching the
WAL. Deferred.

### L4 — Bound log amplification (SHIP — survived review)

- Drop one of the two duplicate `Error` logs (`state_add_vote.go:360`,
  `msg_handlers.go:176`).
- Log peer-attributable validation failures at `Debug`; emit an aggregated
  `Error` / counter per peer per interval.
- Add `consensus_invalid_votes_total` (by peer / reason).

Cheapest change, independently removes half the measured I/O, no liveness risk.

### L5 — Bound the vote-extension count (REWORK REQUIRED)

Idea: cap `len(vote.VoteExtensions)` so one message cannot demand ~10⁴
verifications.

Blocking defects:

- **Not in `Vote.ValidateBasic`** — that function is also the WAL decoder
  (`WALFromProto` → `MsgFromProto` → `ValidateBasic`), so a cap there bricks a
  flooded node on restart (its WAL already holds over-cap votes). Put the cap
  in the reactor ingest path, or gate it on a not-from-WAL flag.
- **The node exempts itself.** Locally produced votes bypass `MsgFromProto`
  (`msg_queue.go:67-74`); a node whose app returns more than the cap accepts
  its own precommit and rejects every peer's → instant halt. The cap must be
  consistent with what the local app produces.
- **Count is app-controlled per height** (`internal/state/execution.go:566-577`);
  Dash Platform's real number lives in `rs-drive-abci`, not this repo. A fixed
  constant is a guess and, if the app ever exceeds it, a runtime halt with no
  activation height. The #1362/SEC-001 work already resolves extension-count
  disagreement by **voting power** (`types/vote_set.go:552-568`); a
  state-derived bound is the right primary design, a constant only a stopgap.
- `Commit.ValidateBasic` bounds nothing either — cap both (this overlaps the
  P0 fix in `COMMIT_EXTENSION_PANIC_SPEC.md`).

### L6 — Verify block signature before extension-signature work (FOLLOW-UP)

Reorder so a vote whose block signature fails never reaches
`VerifyExtensionSign` (§1.5a) — removing the extension-pairing amplification.
(The ABCI call is already unreachable by an unauthenticated attacker, so this
is only about the extension-signature pairings.) A correctness-relevant
reordering of the state machine; needs analysis that it does not change which
votes are admitted. Largely subsumed by a correct L5 (which caps the extension
count at ingest); deferred.

## 5. Rejected alternatives

- **Per-validator verification budget** — an attacker burns a legitimate
  validator's budget with garbage attributed to it, rejecting that validator's
  real vote. Accountability must be per **peer**, never per **claimed
  validator**.
- **Reject votes with unknown `BlockID`** — a lagging node legitimately gets
  votes for unseen proposals. Violates G3.
- **Verify signatures in the reactor before enqueueing** — duplicates work,
  needs validator sets the reactor does not track, moves BLS cost onto the
  shared vote goroutine.
- **Restrict consensus channels to validators** — breaks full nodes.
- **`ThrottledChannelIterator`** (`internal/p2p/channel.go:320`, currently
  unused) — one limiter for the whole channel, so an attacker starves honest
  validators. Do not wire it in.

## 6. Versioning

`RELEASES.md:24-27` requires a `BlockProtocol` bump for block-validity changes
and a `P2PProtocol` bump for p2p message-format changes. L4 needs neither.
L5 adds a new rejection reason on the p2p message-admission path (not the
on-chain block-validity path — `Commit.ValidateBasic` does not call
`VoteExtensions.Validate`), which argues against `BlockProtocol` but is a
genuine `P2PProtocol` question. **Decide explicitly before opening a PR** — an
unpatched peer relaying an over-cap vote that a patched peer rejects is exactly
the interop case the version number exists to signal.

## 7. Revised recommendation

1. **Fix the P0 first** (`COMMIT_EXTENSION_PANIC_SPEC.md`) — separate, more
   severe, and its Commit extension-cap overlaps L5.
2. **Advise validators to enable `allowlist-only` now** (§9) — closes the
   reported attack with no code change.
3. **Ship L4** in the patch release — safe, halves measured I/O.
4. **Rework then ship L5** — cap at reactor ingest, apply to local votes and
   commits, derive the bound from state.
5. **Rework then ship L2** — rebuild the error classification against the
   errors actually returned, excluding queue-full, commit/proposal-signature,
   and replay-sourced errors; gate on the false-positive tests (§8.3).
6. **Do not ship L1 in the emergency release.** If it ships later it must be
   `drop=false` (or non-optimistic gossip), disabled by default per the
   `TxRecvRateLimit` precedent, with a per-IP companion and the burst-clamp
   guard.
7. L3, L6 as follow-ups.

Non-negotiable: **L2 must not ship before its false-positive tests are green.**

## 8. Verification plan

Failing-then-passing per repo discipline; each shown red first.

1. **Flood reproduction (must fail first):** N votes with random `BlockID.Hash`
   and garbage signatures; assert bounded `vote.Verify` count and that
   consensus keeps advancing.
2. **Extension amplification (must fail first):** one precommit with the max
   extensions that fit in `maxMsgSize`; assert L5 rejects it without
   per-extension verification and without an ABCI call.
3. **False-positive guards (G3):** peers sending (a) stale-height, (b)
   duplicate, (c) unseen-proposal, (d) equivocating, (e) ahead-by-one-round
   commit for a round we left — none disconnected; (d) still reaches the
   evidence pool.
4. **Cost-weight / burst guard:** assert `max legitimate nTokens < burst` at
   startup; a drop-free full round at the *maximum* extension count the app
   can produce, with an attacker present (queue fairness, not just aggregate).
5. `make test_race` and the `internal/consensus` + `internal/p2p` suites pass.
6. Re-run the QA reproduction on testnet (CPU, WAL growth, log volume,
   liveness), including the precommit+extensions variant.

## 9. Mitigation available today with no code change

`p2p.allowlist-only` shipped in v1.6.0 (CHANGELOG #1248), off by default
(`config/config.go:819`). Enabled, `buildAllowlist` (`node/node.go:825`)
installs a `FilterPeerByID` (`node/node.go:751-763`) that rejects any peer not
in `persistent-peers`/`bootstrap-peers`, at connection time, before any
consensus channel opens — so an unauthenticated attacker never reaches the vote
path.

```toml
[p2p]
allowlist-only = true
persistent-peers = "<nodeID>@host:port,..."
```

Caveats: it changes topology (allowlist too few peers and you self-partition —
needs a verified peer list); it does not protect nodes that must accept
arbitrary inbound connections (seeds, public full nodes); it does not stop an
allowlisted peer. Operational mitigation, not a fix — but it means the code fix
can be designed properly rather than rushed.

## 10. Related latent issue (separate ticket)

`HeightVoteSet.AddVote` keys `peerCatchupRounds` on the attacker-chosen
`vote.ValidatorProTxHash` and allows 2
(`internal/consensus/types/height_vote_set.go:155-165`). An attacker can burn a
specific validator's two catch-up slots with garbage so that validator's real
high-round vote is rejected — the per-claimed-validator anti-pattern §5
rejects, already present in code. Distinct from the flood; track separately.
