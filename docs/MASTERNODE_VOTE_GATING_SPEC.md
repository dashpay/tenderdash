# Masternode vote gating — restrict consensus votes to evonodes

Status: **IMPLEMENTED (validator-scoped gate, §10) on branch
`fix/consensus-vote-flood`.** The gate drops votes whose authenticated node ID
is not in the current or previous validator set, failing open on any incomplete
identity coverage. TDD'd and adversarially reviewed (the mixed-set fail-open was
added in review). Recommended before release: testnet validation across a quorum
rotation. Sections 4/8/9 are decision history — the full Dash-Core masternode
poll was considered and rejected in favour of the in-process validator set (§10).
Target: root-cause fix behind the vote-flood DoS (`CONSENSUS_VOTE_FLOOD_SPEC.md`).
Related: `CONSENSUS_VOTE_FLOOD_SPEC.md` (mitigations L1/L4/L5) and
`COMMIT_EXTENSION_PANIC_SPEC.md` (P0). This is the keystone the mitigations
compose with.

## 1. Problem

The vote-flood DoS is fundamentally an **access-control** gap: an unprivileged
peer — no evonode, no collateral, a freely-generated node key — can connect to
a validator and make it perform BLS verification by sending votes. Verified in
code:

- Inbound connections are **not** gated by validator/masternode membership. The
  only filters (`FilterPeerByIP`, `FilterPeerByID`) are off by default and are
  not masternode-aware (`internal/p2p/router.go:416-422`, `node/node.go:751-799`).
- The consensus reactor's `isValidator` gate checks the **receiver**, not the
  **sender** (`internal/consensus/reactor.go:649`). A validator processes votes
  from anyone.
- A peer's `ProTxHash` in `NodeInfo` is **self-reported and not bound** to its
  handshake key (`internal/p2p/router.go:662-698`, `types/node_info.go`), so a
  naive "is this peer a validator" check is spoofable.

Mitigations (rate limit, extension cap, log bounding) reduce the blast radius
but cannot close this: node identities are free, so a determined attacker
dilutes any per-peer limit across many identities/IPs.

**The fix is to require that vote-channel senders be evonodes.** That restores
the barrier the attack bypassed: instead of "anyone", the attacker must own an
evonode (collateral-backed, identifiable, PoSe-bannable). The design intent is
already that only masternodes propagate votes — the reactor even carries
`// ignore votes on non-validator nodes` — it is simply not enforced on the
sender side.

## 2. The binding that makes it possible

Gating needs a trustworthy map from a peer's **cryptographically-authenticated
Tenderdash node ID** (proven by the p2p secret handshake) to **masternode
membership**. Dash provides this via the evonode's `platformNodeID`: the
ProRegTx registration of an evonode records its Tenderdash node ID on-chain, in
the deterministic masternode list.

- The p2p handshake already proves the peer's node ID
  (`NodeIDFromPubKey`, `router.go:677`).
- If we have the set of `platformNodeID`s for all current evonodes, the gate is
  a set lookup: **authenticated node ID ∈ evonode platformNodeIDs** → accept its
  votes. No per-vote BLS, no spoofable ProTxHash.

### 2.1 Where the binding is available (findings, confirmed)

- **Authoritative source: Dash Core's deterministic masternode list.**
  `platformNodeID` is set on-chain in the evonode ProRegTx (updatable via
  ProUpServ) and is a **20-byte** value — the Tenderdash node ID, derived from
  the node's ed25519 key. In the Rust client Platform uses it is
  `DMNState.platform_node_id: Option<[u8; 20]>` (rust-dashcore
  `rpc-json/src/lib.rs:2082`), populated for `MasternodeType::Evo`. **20 bytes
  matches `types.NodeID` (`NodeIDByteLength = 20`, `types/node_id.go:15`)**, so
  the set lookup is a direct comparison — open item #3 resolved.
- **The current-quorum binding is already inside Tenderdash.** The ABCI
  `ValidatorUpdate.node_address` URI (`tcp://<node_id>@<ip>:<port>`, built by
  rs-drive-abci from `DMNState.platform_node_id`) is parsed into
  `ValidatorAddress.NodeID` (`types/validator_address.go:24,74`), a validated
  `types.NodeID`. So for the **active validator set**, Tenderdash already has
  trustworthy `(proTxHash, nodeID)` pairs via `validator.NodeAddress.NodeID` —
  no new plumbing. This is what makes a quorum-scoped gate essentially free.
- **The full evonode set is NOT exposed to Tenderdash today.** rs-drive-abci
  holds it (`hpmn_masternode_list`, refreshed from `protx listdiff` every
  block) but exposes no gRPC/ABCI endpoint for it. `dashd-go@v0.26.1`'s
  `ProTxState` / `MasternodelistResultJSON` structs do not parse
  `platformNodeID` at all (confirmed by reading the structs). So obtaining the
  full set requires either a dashd-go/client extension or a new Platform
  endpoint.

## 3. Enforcement point (decided: reactor vote gate)

Two options were considered; the maintainer chose the second:

1. **Reject the connection** (`FilterPeerByID` at accept). Cleanest, but cuts
   off non-evonode full nodes that legitimately connect for block/state sync.
2. **Accept the connection; drop votes/commits from non-evonode peers** in the
   consensus reactor's vote-channel path. **Chosen.** Surgical: full nodes still
   sync, only the expensive vote path is gated. Safe because full nodes do not
   send votes anyway.

Enforcement slots in next to the L1 rate-limit gate already added in
`Reactor.processMsgCh` / `allowVoteChannelMessage`
(`internal/consensus/reactor.go`): for a `ConsensusVoteChannel` envelope, if the
sender's node ID is not in the evonode set, drop it (and likely emit a
`PeerError` so the peer is disconnected — a non-evonode sending votes is
unambiguous misbehaviour, unlike the false-positive-prone cases in the DoS
spec's L2). This composes with L1/L5: the gate reduces the sender set to
evonodes; L1/L5 bound a misbehaving evonode.

## 4. Design decision — binding source & refresh (DECIDED)

**Chosen: poll Dash Core directly for the full evonode set (research option A/1).**

Rationale: it delivers the full evonode set the maintainer wants (not just the
active quorum), it is the same authoritative trust root rs-drive-abci itself
consumes (`protx listdiff`), Tenderdash nodes already run beside a trusted
`dashd`, and it does not require adding a new Platform query endpoint. The
Platform-ABCI alternative (B) was rejected: rs-drive-abci holds the full set
but exposes no endpoint for it, so B needs a new Platform gRPC surface and
couples the gate to the app for no benefit over polling Core.

Mechanism:

1. **Fetch.** Periodically call Dash Core `masternodelist`/`protx listdiff`,
   filter to `type == "Evo"`, drop PoSe-banned entries (`pose_ban_height`),
   collect the set of 20-byte `platformNodeID`s.
2. **Refresh.** On a cadence tied to Core blocks / quorum changes (the DML
   mutates with registrations, ProUpServ, and PoSe bans). Keep the set in a
   shared, atomically-swappable registry the reactor reads without locking the
   hot path.
3. **Gate.** In the reactor vote path (§3), drop a `ConsensusVoteChannel`
   envelope whose authenticated `envelope.From` node ID is not in the set.

**Dependency risk (must resolve first): `dashd-go` does not parse
`platformNodeID`.** `dashd-go@v0.26.1`'s `ProTxState` /
`MasternodelistResultJSON` lack the field. Options, in preference order:
   (a) bump `dashd-go` to a version whose masternode/protx structs include
       `platformNodeID`/`platformP2PPort` (verify one exists);
   (b) contribute the fields upstream to `dashd-go` and bump;
   (c) add a thin custom RPC call + struct in Tenderdash's `dash/core` client
       that parses the field, until (a)/(b) land.
This is the critical-path implementation item.

**Optionality.** Tenderdash is a general BFT engine used outside Dash Platform.
The gate is **off unless** the Dash Core integration is configured (a `dashd`
RPC endpoint is already required for Dash deployments — the gate keys on its
presence plus an explicit enable flag). Non-Dash deployments keep today's
behaviour (or use `allowlist-only`). Default for Dash deployments: TBD in
review (enabled with fail-open startup, see §4.1).

### 4.1 Fail-open vs fail-closed

- **Startup, before the first set loads:** fail **open** (accept votes) — a
  node must not reject all consensus traffic because its Core poll hasn't
  completed, which would itself be a self-inflicted liveness failure.
- **Steady state:** fail **closed** (drop non-evonode votes) — the whole point.
- **Refresh gap** (a just-registered or just-unbanned evonode not yet in the
  set): its votes are dropped until the next refresh. Bounded by the refresh
  cadence; acceptable because a brand-new evonode is not yet a consensus
  participant for in-flight heights. A just-unbanned node re-appears at the next
  poll. This argues for a reasonably short refresh interval.
- **Core RPC unavailable / errors:** retain the last known-good set rather than
  emptying it (emptying would fail-closed against everyone). If never loaded,
  stay fail-open. Never let a Core outage halt consensus.

## 5. Threat model after the gate

- Attacker set collapses from "anyone with a free node key" to "someone who
  controls a registered evonode." That requires collateral, is identifiable by
  proTxHash, and is PoSe-bannable.
- Residual: a malicious/compromised evonode can still flood — but that is a
  bounded, identified set, which is exactly where L1 (per-peer rate limit) and
  L5 (extension cap) become effective and where evidence/PoSe handling applies.
- Not addressed here: attacks that don't use the vote channel (statesync,
  evidence, etc. — see the completeness review), and a fully-compromised
  evonode key.

## 6. Verification plan (draft)

- A peer whose authenticated node ID is not in the evonode set has its
  votes/commits dropped (and is disconnected); its connection for sync is not
  affected.
- An evonode peer's votes are processed normally.
- Set refresh: a newly-registered evonode's votes are accepted after refresh; a
  PoSe-banned node's votes are dropped after refresh.
- The gate is disabled when no masternode integration is configured (general
  Tenderdash deployments unaffected).
- Interaction with L1: gate + rate limit compose without dropping honest
  evonode traffic.
- Full end-to-end: a non-evonode flood is rejected at the gate before BLS
  verification; consensus is unaffected.

## 7. Open items / next steps

Resolved by research: binding source (§4, poll Core), full-set availability
(yes, via `protx listdiff`), fail-open/closed policy (§4.1), format match
(§2.1, both 20-byte). Remaining:

1. **`dashd-go` `platformNodeID` support** — the critical-path dependency
   (§4). Confirm whether a `dashd-go` version exposes it, or plan the
   upstream/custom-parse path.
2. **Refresh cadence** — pick an interval and wire it to Core block/quorum
   changes; quantify the worst-case refresh-gap window.
3. **Enablement default** for Dash deployments (enabled fail-open, or opt-in) —
   decide in review.
4. **Disconnect on violation?** A non-evonode sending votes is unambiguous
   misbehaviour, so a `Fatal` PeerError is defensible here (unlike DoS-spec L2).
   Confirm it can't be triggered for an evonode caught mid-refresh-gap (which
   would argue for drop-only, not disconnect, near rotations).
5. **Multi-agent review** of this spec (feasibility, security, scope,
   Dash-domain).
6. **Versioning** — gating changes p2p-observable acceptance; note whether a
   `P2PProtocol` bump is warranted (legitimate evonode traffic is unaffected).
7. **Composition with L1** — the vote gate runs alongside `allowVoteChannelMessage`;
   order them so the cheap identity check precedes the rate-limiter token spend.

## 8. Review outcomes (four-lens review) — DECISION REVERSED

All four reviews (security, feasibility, scope, Dash-domain) independently
converged: **abandon the full Dash-Core-poll (§4) and gate on Tenderdash's
in-process validator-set node IDs instead.** The core binding and threat model
in §1–§2 hold; the §4 *source* decision was wrong. Summary of the evidence:

- **Only current-quorum members send/gossip votes.** A node gossips a vote to a
  peer only if that peer is a current validator (`gossip_handlers.go:143-145`);
  non-quorum evonodes are never sent votes and have none to relay. The
  legitimate vote-sender set is the ~100-member quorum, not ~400 evonodes.
- **The correct set is already in-process, for free and zero-lag.**
  `stateData.Validators` and `stateData.LastValidators` are loaded in
  `handleVoteMessage` (`reactor.go:648-651`); each validator's authenticated
  node ID is `validator.NodeAddress.NodeID` (`types/validator_address.go:24,74`).
- **SAFETY objection to the DML poll (security review, HIGH).** The live DML set
  ≠ the quorum snapshot that authored an in-flight height. An evonode that was a
  quorum member but is PoSe-banned or ProUpServ-rekeyed *afterward* is dropped
  by a live-DML gate, while its votes for that height are still valid and needed
  for threshold → stall at quorum boundaries (and, if disconnect is enabled,
  eviction of a legitimate validator). Gating on the validator-set snapshot
  avoids this entirely.
- **Feasibility.** The full poll is ~a week of new plumbing (custom RPC parse —
  no dashd-go version has `platformNodeID`; new poller service; reactor has no
  Core client today; new atomic registry; fail-open/closed state machine). The
  validator-set gate needs none of it.

### 8.1 Revised design (recommended)

- **Gate VOTES only** on `envelope.From ∈ (Validators ∪ LastValidators) node IDs`,
  in `handleVoteMessage` / `processMsgCh`. A few lines, no new dependency, no
  poll, no registry, no fail-open window (the set is committed consensus state,
  always present).
- **Do NOT gate commits.** `ConsensusVoteChannel` also carries `Commit`
  messages, which are legitimately relayed to/from **non-validators** for
  catch-up sync (`shouldCommitBeGossipedForCatchup`, no `isValidator` guard).
  Gating commits by sender would break block sync for regular full nodes.
- **Drop-only, not Fatal disconnect**, at least near rotations (evict≠ban makes
  disconnect a weak deterrent anyway, and it risks evicting a legit validator in
  the rotation window).
- **Keep L1 + L5 live** regardless — defense in depth, and they cover the
  fail-open case that no longer exists here but exists conceptually.
- **Optionality** still required (Dash-only: validators carry non-empty
  `NodeAddress.NodeID`; generic Tenderdash deployments do not, so the gate must
  be off there).

### 8.2 NEW vector found in review (security, HIGH) — not closed by any current work

The **data channel** has the *same* re-verifiable BLS-flood primitive: a junk
proposal fails signature verification in `verifyProposal`
(`state_proposaler.go:240`, BLS `VerifySignatureDigest`) but never sets
`rs.Proposal`, so there is no dedup (`state_proposaler.go:48`) — N junk
proposals at the current height/round force N BLS verifications on the
state-machine goroutine, and the attacker need not be the proposer (proposals
are gossiped; the signature, not the sender, is checked). This is **not** on the
vote channel, so neither L1, L5, nor the vote gate touch it. The vote-flood DoS
is therefore only *reduced to the vote channel* by all current work, not
eliminated. A follow-up must gate/limit the data-channel proposal path
(analogous validator-set gate, or a cost-aware limit).

### 8.3 Full-Core-poll — deferred

Keep §4's full-evonode-set poll as a possible phase 2, justified only if a
concrete need beyond the vote DoS emerges (e.g. gating a channel where
non-quorum evonodes legitimately send). It is a safe superset but broader than
the threat model needs and reintroduces a refresh-gap the in-process data
avoids. Not day-one.

## 9. FINAL DESIGN (maintainer decision) — supersedes §3/§4/§8 where they conflict

Maintainer chose the **full HPMN set** for the gate, over the reviews'
quorum-scoped recommendation, with an explicit rationale: **network stability /
propagation redundancy** — more relay hops than the ~100-member quorum. The
security review's safety objection to a *DML-only* gate is resolved by unioning
with the in-process validator set (below), so this choice is safe.

### 9.1 Gate set = HPMN set ∪ validator-set snapshot

Accept a vote if its authenticated `envelope.From` node ID is in **either**:

1. **Full HPMN set** — all Dash masternodes that have a non-empty
   `platformNodeID` (i.e. evonodes; regular masternodes without a platform node
   id are excluded), from a periodic Dash Core poll. This is the propagation
   mesh the maintainer wants.
2. **Current + last validator-set node IDs** — `Validators ∪ LastValidators`,
   already in-process (`validator.NodeAddress.NodeID`), zero-lag.

The union closes the security-review HIGH (DML ≠ quorum-snapshot): a validator
that authored an in-flight height but is PoSe-banned/rekeyed afterward is still
in the validator-set snapshot, so its still-needed votes are never dropped even
if the DML poll has dropped it. It also means the node is protected *before* the
first DML poll completes (the validator set is always present) — **removing the
startup fail-open window** the security review flagged.

### 9.2 What this keeps from §4 (the DML-poll plumbing — accepted cost)

The full HPMN set requires the Dash Core poll, so the feasibility items stand
and are accepted: parse `platformNodeID` (dashd-go lacks it → thin custom RPC in
`dash/core` via `RawRequest`, per feasibility review), a periodic poller
service, an atomically-swappable registry read on the hot path, and a config
enable flag (default on for Dash deployments, off otherwise). PoSe handling: a
banned HPMN may be dropped from set (1), but the validator-set union (2) still
admits it while its in-flight votes matter — so PoSe filtering on set (1) is
safe.

### 9.3 Votes gated, commits NOT (unchanged from §8.1)

Gate **votes only**. Do **not** gate commits — they are relayed by
non-validators for catch-up (`shouldCommitBeGossipedForCatchup`); gating them
breaks sync. Drop-only, no Fatal disconnect (evict≠ban; avoids evicting a legit
node in a refresh window).

### 9.4 Data-channel proposal flood — FOLDED IN (maintainer decision)

The same BLS re-verification flood exists on the data channel: a junk proposal
fails signature verification but never dedups (`state_proposaler.go:48,240`), so
N junk proposals force N BLS verifies, from any gossiping peer. Apply the **same
HPMN∪validator-set gate** to the proposal path so a non-HPMN cannot force
proposal verification. Block parts on the data channel need separate care (they
are gossiped for catch-up like commits) — gate the *proposal* message, not
block parts. Details to be worked in implementation; this is now in scope.

### 9.5 OPEN — does "HPMNs relay votes" need a gossip change? (needs maintainer input)

The gate *accepts* votes from any HPMN, but that alone does not create more
propagation hops. Today only validators are **sent** votes and only validators
process/relay them (`shouldVoteBeGossiped` gates on the peer being a validator;
the receive side gates on `isValidator`). For non-quorum HPMNs to actually
*relay* (the stated stability goal), the gossip send-side and receive-side gates
must change from "is validator" to "is HPMN" — a consensus-gossip change,
separate from the security gate, with its own tradeoff (more redundancy vs.
non-quorum HPMNs now BLS-verifying relayed votes → more total verification work,
distributed). This should be decided and, if wanted, specced separately; the
security gate does not depend on it.

## 10. FINAL DESIGN (authoritative) — validator-scoped vote gate + layered defense

Supersedes §4, §8, §9. The maintainer reasoned to this directly: since only
validators send/relay votes today (HPMNs do not relay — `shouldVoteBeGossiped`
gates on the peer being a validator, `gossip_handlers.go:143`), a validator-
scoped gate matches the actual propagation topology exactly, needs no Dash Core
poll, and the earlier full-HPMN/DML design (with its dashd-go dependency,
poller, registry, and fail-open window) is unnecessary.

### 10.1 Vote gate (the new piece)

- Accept a `ConsensusVoteChannel` **vote** only if `envelope.From` (the
  handshake-authenticated node ID) is in **`Validators ∪ LastValidators`** node
  IDs (`validator.NodeAddress.NodeID`, already loaded in `handleVoteMessage`,
  `reactor.go:648-651`). Union of current + previous covers the rotation
  boundary; height-match in gossip (`rs.Height == prs.Height`) makes that
  complete.
- **Drop-only, no disconnect** (disconnect = L2, deliberately skipped).
- **Genesis/height-1 guard:** skip the gate until `Validators` (and, where
  relied on, `LastValidators`) are populated; never reject when the set is
  empty (fail-open at startup by construction — the set is committed consensus
  state, so this window is only genesis).
- No new dependency, no poll, no registry, no config beyond an optional
  Dash-only enable flag; a few lines next to L1's `allowVoteChannelMessage`.

### 10.2 Commits — bounded, not sender-gated

Commits ride the same channel and are already rate-limited per peer by **L1**
(`allowVoteChannelMessage` gates all vote-channel messages), cost-capped by
**L5**, and only reach `VerifyCommit` at the current height with no commit yet
(`TryAddCommit` early-returns on `stateData.Commit != nil`). They are **not**
sender-gated (non-validators relay commits for catch-up —
`shouldCommitBeGossipedForCatchup`). P0 removes the commit crash.

### 10.3 Data-channel proposal flood (folded in)

Same BLS re-verification primitive (`state_proposaler.go:48,240`). Mitigate by
extending the **L1-style per-peer rate limit to the data channel** (bounds junk-
proposal spam without risking catch-up), and/or a validator-scoped gate on
*proposal* messages — `shouldProposalBeGossiped` gates on validator too
(`gossip_handlers.go:115`), so a validator-set gate is plausible, but confirm no
non-validator catch-up path relays proposals before gating (block parts must NOT
be gated — they are catch-up-gossiped). Rate-limit is the safer default.

### 10.4 Connection-level limits (maintainer point) — the residual layer

The per-peer limits' weakness is connection multiplication. Tighten p2p
connection caps: `MaxConnections` (64), per-IP `MaxIncomingConnectionAttempts`
(**currently NOT wired from config — bug to fix**, per feasibility review) and
`IncomingConnectionWindow`. This bounds how many connections/IPs an attacker can
use to dilute the per-peer rate limits — the lever that hardens L1/commit
bounds.

### 10.5 Composed defense (summary)

| Threat | Defense | Status |
|---|---|---|
| Non-validator vote flood | Validator-set vote gate (§10.1) | to build |
| Commit spam | L1 rate limit + L5 + narrow window | L1/L5 done |
| Data-channel proposal flood | L1-style limit on data channel (§10.3) | to build |
| Connection multiplication | Tighten p2p connection caps (§10.4) | to build |
| Per-message crypto cost | L5 extension cap | done |
| Remote crash/brick | P0 | done |
| Log flood | L4 | done |

### 10.6 What is explicitly NOT built

- No Dash Core masternode poll, no dashd-go `platformNodeID` change, no
  refreshable registry, no fail-open state machine — all obviated by the
  validator-set scope.
- No L2 disconnect.
- The full-HPMN gate and "HPMNs relay votes" gossip change are **out of scope**
  (the latter is a separate consensus/perf decision; the former is unnecessary
  given HPMNs don't relay today).
