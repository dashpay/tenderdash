# Consensus DoS — Piece 3b spec: honest connectivity

Implements `docs/CONSENSUS_DOS_PLAN.md` §2 item 3b (expanded). Lifts the ceiling that
`docs/CONSENSUS_DOS_PIECE2_SPEC.md` §2 records as out of scope for the verification
scheduler: *"honest share is bounded by connection slots, not by this scheduler"*.

## 1. What was verified against the code (not the review notes)

All numeric claims hold, and one is sharper than the review stated. Line numbers in this
section refer to the tree **before** this change; §2 onward describes the tree after it.

| Claim | Verdict | Evidence |
|---|---|---|
| `MaxConnected 64` + `MaxConnectedUpgrade 4` = 68 hard ceiling | true | `peermanager.go:811-813`, `node/setup.go:225-248` |
| `MaxOutgoingConnections = 12`, refused before the upgrade path | true | `config/config.go:804`, `peermanager.go:610` (the `MaxConnected+Upgrade` check at `:604` comes first, the outgoing check at `:610` returns early) |
| dash dialer sets `MutableScore`, never `Persistent` | true | `dash_dialer.go:60,68-72`; `configurePeer` derives `Persistent` only from `options.PersistentPeers` (`peermanager.go:437-441`) |
| `Score()` therefore caps at `MaxInt16-1` | true | `peermanager.go:1685-1709` |
| `MutableScore` is ephemeral, lost on restart | true | `peerInfo.ToProto()` (`:1612-1627`) omits it |
| `MutableScore` decremented by every peer error | true | `router.go:390` → `PeerStatusBad` → `peermanager.go:1244` |
| one IPv4 /32 may hold up to 100 concurrent connections | true | `connKey` keys IPv4 by full /32 (`conn_tracker.go:65-74`); `MaxIncomingConnectionAttempts` default 100 (`config/config.go:805`) — *above* `MaxConnections = 64`, so a single IPv4 can occupy every inbound slot. Only the 0→1 transition is rate limited, and only by `IncomingConnectionWindow = 10ms`. IPv6 is already bucketed to /64 by earlier hardening. |
| node IDs are free | true | `NodeID = hex(SHA256(ed25519 pub)[:20])`, and `handshakePeer` only proves the peer *owns* that key (`router.go:677-680`) |

**Sharper than the review found.** The review says validator protection is *weak*
(ephemeral, erodible). In the direction that matters most it is **absent**:

* Protection is applied **only on the dial path**. `ValidatorConnExecutor.filterAddresses`
  skips any validator for which `IsDialingOrConnected` is true (`validator_conn_executor.go:434`),
  so `ConnectAsync` — the only thing that ever raises the score — is never called for a
  current-quorum validator that connected to *us* first.
* That is the normal case for half of the DIP-6 ring. DIP-6 is a **directed** overlay:
  validator at sorted index `i` dials `(i+2^k) mod n`, so the peers that dial *us* are
  `(i-2^k) mod n` — a disjoint set from the one we dial (`selectpeers/dip6.go:60-77`). It is
  also the normal case for *every* neighbour after a restart, because the remote side
  retries at `MinRetryTime = 250ms` and wins the race against our own startup.
* Such a validator sits at score **0**. `Accepted` → `findUpgradeCandidate` requires a
  *strictly* lower score (`peermanager.go:1345`), so a single `PeerError` (score → −1) makes
  it evictable by **any** fresh score-0 Sybil.
* Once evicted it is not redialed until the next quorum rotation: `updateConnections()` runs
  only from `OnStart` and from validator-set-update events (`validator_conn_executor.go:230-260`).

So the exploit is cheaper than "flood + restart + 32k peer errors": **flood + one peer error
per target** silently strips a validator out of the consensus overlay for a whole quorum
lifetime.

**One claim is refuted as a *fix* option.** Per-/24 inbound diversity cannot ship: the e2e
harness allocates every node an address inside `10.186.73.0/24`
(`test/e2e/pkg/testnet.go:31`), so a per-/24 cap breaks every e2e testnet. It also penalises
honest masternodes co-located in one datacenter /24, while an attacker renting a handful of
/24s defeats it. Rejected on evidence, not on taste.

## 2. Design

One mechanism solves both halves, because both halves are the same bug: **protection is
attached to a dial action instead of to an identity.**

**Protected peer set.** `PeerManager` gains a mutable set of node IDs, `protectedPeers`,
maintained exactly like the existing static `PersistentPeers` set:

* `configurePeer` derives `peer.Protected = m.protectedPeers[peer.ID]`, alongside the
  existing `peer.Persistent`. It runs from `newPeerInfo`, so an inbound `Accepted` of a
  never-before-seen validator is covered, and from `SetProtectedPeers`' own re-derivation
  loop, which covers peers already in the store. (Note `UpdatePeerInfo` does *not* call
  `configurePeer` itself — only its `dash_dialer.go` caller does.)
* `peerInfo.Score()` returns `PeerScorePersistent` for a protected peer, so it is
  * immune to `PeerStatusBad` decrements (the mutable score is never consulted),
  * immune to `DialFailures` subtraction,
  * never selected by `findUpgradeCandidate` (a Sybil at score 0 can never be strictly higher),
  * able to *win* an upgrade on `Accepted`, and ranked first for dialing in `TryDialNext`
    (which still refuses to dial past `MaxOutgoingConnections`).
* `evictPeerAfterTimeout` and `retryDelay` treat protected like persistent, so the seed-node
  incoming-time evictor cannot drop a quorum peer and dial retries use the shorter
  persistent backoff.
* `processPeerEvent` stops recording score changes for a peer that holds a reservation. Its
  rank comes from the reservation, so the changes are invisible while it lasts — but they
  would still accumulate, and the peer would drop to the bottom of the ranking the moment it
  rotated out. An attacker cannot shed a reserved peer, but it could bank the errors for
  later; now it cannot.
* `PeerManager.SetProtectedPeers(ids)` replaces the set atomically and re-derives the flag on
  affected stored peers.

**Source of the set: the DIP-6 neighbourhood, both directions.**
`ValidatorConnExecutor.updateConnections` already computes the outbound half
(`selectValidators`). We add the inbound half — the exact set of validators DIP-6 tells to
dial us — and publish the union through `DashDialer.SetProtectedPeers`.

`selectpeers` gains `SelectInboundValidators`, the inverse of `SelectValidators`: same
sorted list, same `count`, indices `(i-2^k) mod n` instead of `(i+2^k) mod n`, and the same
`< minValidators` "connect to everyone" fallback. The pairing is verified by a property test
over many set sizes: `j ∈ forward(i) ⟺ i ∈ inverse(j)`.

Set size is bounded and small. The two halves never overlap for `n ≥ minValidators`: an
overlap needs `n | 2^j + 2^k`, but `2^j + 2^k ≤ 2^floor(log2(n-1)) ≤ n-1 < n`. So the union is
exactly `2·(count+1)` — 4 at `n = 5..8`, 10 at `n = 33..64`, 12 at `n = 65..128` (so 12 for a
100-member LLMQ), 18 at `n = 1024` — and at most `n-1 ≤ 3` below `minValidators`, which is what
keeps 4-node testnets safe.

`SetProtectedPeers` additionally refuses any set that would take reservations past half the
connection slots, counting configured `PersistentPeers`, which hold one for the same reason.
Otherwise a misconfigured caller could reach the state where every connected peer holds a
reservation, no arrival can find a peer to upgrade from, and the node stops accepting
connections entirely. A refused set is **dropped, not ignored**: leaving the previous quorum's
reservations in force would be exactly the stale protection that keeping them out of the peer
store is meant to prevent. A single malformed node ID is skipped rather than failing the batch.

**Why this is safe against Sybils.** The set is keyed on `NodeID`, which the handshake
*authenticates* against the peer's public key (`router.go:677-680`). It is **not** keyed on
`NodeInfo.ProTxHash`, which is self-reported and never checked against the validator set —
using it would let anyone claim a reserved slot.

Authenticating each member's node ID is necessary but **not sufficient**: what also matters is
how a node ID got into the set. `resolveNodeID` fills in a *missing* node ID from the **address
book first** (`validator_conn_executor.go:286`), and the address book is written by peer
exchange — `pex/reactor.go:267-280` parses an arbitrary `nodeID@host:port` from any connected
peer and calls `peerManager.Add()` with no verification that the named node owns that address.
An attacker can therefore PEX-inject `attackerNodeID@validatorIP:port` and have
`lookupIPPort` (`dash_dialer.go:116-134`) answer with *its own* node ID for a real validator.

So reservations use **only the node ID the chain published with the validator address**, and
run no resolver at all. A validator whose address carries no node ID is dialed exactly as
before but is not granted a slot. This is both the safe choice and the cheap one:

* Safe — the input is ABCI-delivered validator-set data. Neither resolver can influence it, so
  there is no way to aim a reservation at an attacker-chosen identity, and no way to *strip* a
  reservation from a chosen validator either (using the address book would have handed an
  attacker a per-target kill switch on the whole mechanism: one PEX entry makes the untrusted
  resolver win, and the validator drops out of the reserved set).
* Cheap — **this path runs on every committed block**, not once per rotation:
  `WithValidatorSetUpdate` publishes the full validator set unconditionally
  (`internal/state/events.go:100-107`), so `handleValidatorUpdateEvent`'s empty-set early
  return never fires. Resolving the inbound half would have added up to `count+1` blocking
  secret-connection handshakes per block (1s dial + 1s handshake each,
  `nodeid_resolver.go:13-18`) on the event-bus subscriber goroutine, under `vc.mux` — for
  peers this node never dials. A subscriber that falls behind is *evicted* by the pubsub
  server (`pubsub.go:403-418`) and the executor never resubscribes, which would freeze the
  reserved set on a rotated-out quorum. Reservations must therefore cost no I/O, and they
  don't.

In production the ABCI validator address carries the node ID, so this costs nothing in
coverage; where it does not, the mechanism degrades to today's behaviour rather than to an
attacker-controlled one.

### Rejected alternatives

* **Raise `MaxOutgoingConnections`.** Creates *self-chosen* slots, not *honest* ones — beyond
  the DIP-6 set the dial targets come from the PEX address book, which an attacker with free
  node IDs can flood. It also changes the topology defaults of every node on the network
  (including non-validators) for a benefit that is unquantifiable, and does nothing for the
  inbound direction, which is where the flood actually lands.
* **A numeric inbound reservation** ("hold N slots for validators"). To be useful it needs
  exactly the identity set above, so it is strictly more machinery for the same input, and it
  adds a new failure mode: slots held empty on networks with no quorum. The existing
  upgrade/eviction mechanism already *is* the reservation once the scores are right.
* **Per-/24 conn-tracker keys.** Refuted above.
* **Lowering `MaxIncomingConnectionAttempts` below `MaxConnections`.** Tempting (100 > 64 is
  indefensible), but it is a config-default change affecting every deployment, an attacker
  with two IPs undoes it, and honest full nodes behind a shared NAT would be capped. Flagged
  as a follow-up, not bundled here.

## 3. Invariants

* **P1 — non-eroding.** A protected peer's `Score()` is `PeerScorePersistent` for any number
  of `PeerStatusBad` events and any number of dial failures.
* **P2 — non-displaceable.** While the peer set is protected, `findUpgradeCandidate` never
  returns it, so neither `Accepted` nor `TryDialNext` can schedule it for eviction, at any
  connection count.
* **P3 — admissible.** A protected peer connecting inbound at `MaxConnected` is accepted and
  displaces an unprotected peer, instead of being refused. The guarantee stops at the hard
  ceiling `MaxConnected+MaxConnectedUpgrade`, which `Accepted` enforces *before* consulting
  ranks: a flood that walks the connection count to the ceiling (each Sybil reporting one
  error drops below its neighbours and becomes a legal upgrade candidate for the next) can
  refuse a protected peer for as long as it sustains that count. Admission is therefore
  *eventual*, not instant — but once admitted, P1/P2/P4 make the slot permanent.
* **P6 — not shed under pressure.** A non-fatal error reported against a protected peer never
  disconnects it. Without this, `router.go:380` disconnects *any* erroring peer whenever
  `len(connected) >= MaxConnected` — i.e. always, under the very flood being defended
  against — which would make P1 and P2 worthless.
* **P4 — direction-independent.** P1–P3 hold whether the peer dialed us or we dialed it, and
  whether or not `ConnectAsync` was ever called for it.
* **P5 — bounded and current.** The protected set is empty when this node is not in the
  active validator set, and is replaced (not accumulated) on every quorum rotation.

## 4. Test plan (each written red first)

1. `TestProtectedValidatorSurvivesFloodErrorsAndRestart` (p2p): Sybils inbound; a quorum
   validator connected **inbound** (never dialed by us); 100 `PeerStatusBad` events; more
   Sybils arriving. Asserts the validator keeps its slot and its score. Red today: it is
   evicted after the first error.
2. `TestProtectedPeerDisplacesSybilInbound` (p2p): all slots held by Sybils, a protected
   validator dials in. Red today: `Accepted` returns "already connected to maximum number of
   peers".
3. Restart: same `peerDB`, new `PeerManager`, protection re-applied from the validator set
   before the flood is re-established. Asserts the validator regains a slot.
4. `selectpeers` inverse property test (`j ∈ forward(i) ⟺ i ∈ inverse(j)`) across quorum
   sizes spanning the `< minValidators` fallback, the powers of two, and `n = 64` where the
   halves overlap.
5. `ValidatorConnExecutor` publishes the union, replaces it on rotation, and clears it when
   the node leaves the validator set.
6. A validator whose node ID resolves only from the address book is dialed but not reserved.
7. A reserved peer is not shed when an error is reported while every slot is in use.
8. A reserved peer is refused at the hard ceiling and admitted once one eviction drains,
   which is what P3 actually guarantees. The flood is returned to a clean rank before the
   second admission, so only the reservation can win it.
9. A refused reservation set leaves no reservations behind, and self/malformed IDs are
   skipped without costing the rest of the set.
10. A reserved slot outlives the seed-node incoming-connection timer.

## 5. Honest share

`P = 68`. DIP-6 at `n = 100` gives `count+1 = 6` outbound and `6` inbound quorum neighbours.

* Before: the 6 dialed neighbours hold `MaxInt16-1` but erode, and — because `router.go:380`
  disconnects any erroring peer once the node is full — a single peer error dropped them too.
  The 6 inbound neighbours are unprotected at score 0 and fall to the same single error.
  **Guaranteed** honest slots under a flood with one peer error per target: `0`.
  Optimistically (no peer errors, no restart): the 12 dialed slots, `17.6%`.
* After: the 12 DIP-6 neighbours hold their slots unconditionally — they cannot be eroded,
  displaced, or shed under pressure — plus the 6 remaining outbound dial slots, which an
  attacker can only claim through address-book poisoning: `≥ 18/68 = 26.5%`.

The share number is the smaller half of the result. The larger half is that the DIP-6 overlay
itself — the topology consensus votes travel on — is now flood-proof as a whole, in both
directions, rather than being half unprotected and wholly revocable by one error.

## 6. Residual risk

* The `NodeID` for a validator whose address carries no node ID is learned by dialing the
  hostname in the validator set. An attacker who can hijack that DNS name gets protected.
  This is pre-existing (the same resolution already decides who we dial and score to
  `MaxInt16`); it is not widened here, but it bounds how strong the guarantee can be.
* A validator whose node ID is not published with its validator address is dialed but not
  reserved. The underlying weakness — `lookupIPPort` trusting unverified addresses, which
  also lets a PEX-poisoned address book aim `ConnectAsync`'s existing `MaxInt16` score grant
  at an attacker-chosen node ID — should be fixed at the source by only consulting addresses
  with a successful past dial (`peerAddressInfo.LastDialSuccess`, which is persisted and is
  set only after a handshake bound that node ID to that address). Recommended follow-up; it
  is deliberately not bundled here because it would push more lookups onto the per-block TCP
  resolver.
* Once peer exchange supplies addresses for the inbound half, those peers rank first in
  `TryDialNext`, so a validator's `MaxOutgoingConnections = 12` will tend to be spent on
  quorum members rather than on PEX-discovered peers. That is the intended preference, but it
  leaves fewer outbound slots for blocksync/mempool diversity; operators of validator nodes
  may want to raise `max-outgoing-connections`.
* `ValidatorConnExecutor`'s event subscription is evicted by the pubsub server if it falls
  behind (`pubsub.go:403-418`), and the driving loop does not resubscribe. The reservation set
  would then freeze on a rotated-out quorum. Pre-existing for dialing; the reservation
  inherits it. Recommended follow-up.
* The router's accept loop starts before `ValidatorConnExecutor` (`node/node.go:552-561`), so
  there is a startup window with no reservations in force. It self-heals through the
  `Accepted` upgrade path once the set is published.
* `MaxIncomingConnectionAttempts = 100 > MaxConnections = 64` still lets one IPv4 hold every
  unprotected inbound slot. Out of scope here; recommended follow-up.
* Protected peers remain subject to explicit `Errored`/`EvictPeer` eviction on a **fatal**
  peer error. That is deliberate: it is the misbehaviour path, and making quorum members
  immune to it would weaken an existing protection. Note the fatal sites include the per-peer
  receive rate limiter (`internal/p2p/channel.go`), which by construction fires under a flood
  — a cross-workstream note for whoever ships that limiter, since it bypasses reservations.
* `ShouldDisconnectOnError` spares **any** peer holding a reserved slot, which includes
  statically configured `PersistentPeers`. That is a deliberate widening: an operator who
  configures a persistent peer is asking for it to be kept, and the shed decision is about
  relieving connection pressure, not about judging the peer. It does mean a persistent peer
  can now emit non-fatal errors indefinitely at capacity and keep its slot.
* A quorum member that moves from the half of the overlay this node dials to the half that
  dials it is still disconnected on rotation by `disconnectValidators`, and has to reconnect
  inwards to claim its reserved slot. Keeping it connected instead was considered and
  rejected: holding an outbound connection to a member DIP-6 says should dial us contradicts
  the overlay's own semantics, and the existing tests encode that.
* `updateConnections` runs on every committed block, and now computes the DIP-6 selection
  twice per block (once for dialing, once for the reserved union). That is pure CPU — a sort
  of the member list plus one SHA256 per member — and no additional I/O.
