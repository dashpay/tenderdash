# Consensus DoS — Piece 10 spec: bound the evidence channel

Implements `docs/CONSENSUS_DOS_PLAN.md` §2 item 10. Phase 1 only. Reuses the vocabulary of
`docs/CONSENSUS_DOS_PIECE2_SPEC.md`: non-punitive local drops, per-peer **and** aggregate
ceilings, budgets denominated in verification work (pairings).

Status: **v3, post-implementation-review.** Reviewed by an independent security lens and an
independent correctness/liveness lens twice — once against the spec before any code, once
against the landed diff. §3.1, §3.2, §3.3 and §4 changed after the first round; §3's ordering
and §3.3's honesty about identity rotation changed after the second. The pre-existing problems
both rounds surfaced are recorded in §6 for other owners.

## 1. What the code actually does today (verified, not assumed)

| plan claim | verdict |
|---|---|
| `verify.go` does **two** BLS pairings per `DuplicateVoteEvidence` | **true** — `verify.go:142,145`, both reached from `VerifyDuplicateVote` |
| reached from `reactor.go:190` via `evpool.AddEvidence` | **true** |
| evidence channel has **no rate limit at all** | **true** — nothing in `internal/evidence` consults a limiter; `GetChannelDescriptor` only sets `RecvBufferCapacity: 32` |
| dedup is by evidence **hash**, so a byte flip forces re-verification | **true** — `pool.go` `isCommitted`/`isPending` key on `evidence.Hash()` = `crypto.Checksum(proto bytes)`, which covers both signatures and the ABCI fields |
| uncharged disk I/O per message | **true**, and worse than stated — `LoadBlockMeta` (`verify.go:37`) plus `LoadValidators` (`verify.go:63`), and the latter is not one read: `internal/state/store.go` also loads consensus params, a second block meta, and builds a proposer selector |
| runs on the evidence reactor goroutine, not `receiveRoutine` | **true** — `processEvidenceCh` |

**Three findings the plan does not mention, all load-bearing:**

1. **The evidence reactor is punitive today, and immediately so.** `processEvidenceCh` turns any
   handler error into `p2p.PeerError`, and `PeerManager.Errored` sets `evict[peerID]` and wakes
   the evict waker — disconnect, not a score decrement. `verify()` returns `ErrInvalidEvidence`
   for a bad signature, so the *first* mutated copy evicts the sender. Consequences:
   - It partially masks the flood the plan describes: an attacker gets roughly one connection's
     worth of queued messages per identity before eviction. Identities are free (plan §3), so
     this is a speed bump, not a bound — and the sustained vectors that matter most are the
     **non-punitive** ones (`verify()` returns a plain error, no eviction, for a missing block or
     a `LoadValidators` failure), which cost disk I/O per message forever.
   - It punishes honest relayers whenever our view of the validator set for the evidence height
     differs from theirs — exactly the "verification failure is not proof of guilt" case of plan
     §3. **Out of scope here**; recorded in §6 so the next piece can own it.
2. **Evidence gossip is retry-based.** `syncEvidence` re-sends every pending item to every
   connected peer once per second, forever, until it leaves the sender's pending set. There is no
   per-peer "already has it" bookkeeping, so a receive-side drop really is a delay, not a loss.
   Every liveness argument below rests on this. A future optimisation that made `syncEvidence`
   skip what a peer already has would silently invalidate them.
3. **Verification is skipped when the historical validator set has no public keys.**
   `reactor.go` gates inbound evidence on `hasPublicKeys()` — the **current** set — but
   `verify()` loads the set at the **evidence height** and `VerifyDuplicateVote` runs the
   pairings only when the accused validator carries a `PubKey`. When it does not, the evidence is
   accepted, stored, gossiped and proposable **unverified**. This is reachable by design (a node
   that joined the quorum inside the 48 h age window holds keyless historical sets), and it is
   the single fact that dictates §3.1's shape.

## 2. Threat model

Attacker holds a real validator `proTxHash` (public) and a height inside the age window for a
block we have. Four flood shapes:

- **F1 — byte-flip replay.** Take a *genuine* pending item (we gossip it to everyone), flip a
  signature byte, resend. New hash ⇒ misses `isPending`/`isCommitted` ⇒ two disk lookups and two
  pairings. Repeat with a fresh byte each time. This is the amplification the plan names.
- **F2 — fabricated identities.** Fresh `BlockID` per message ⇒ genuinely novel evidence to us;
  no de-duplication can help. Bounded only by a work budget.
- **F3 — structural / out-of-window junk.** Mismatched `VoteB` height, or a height we do not
  have. Costs no pairings but 1-3 disk reads each, and (for the missing-block case) does **not**
  evict, so it is sustainable from a single identity today.
- **F4 — identity rotation.** A fresh node ID gets a fresh, full per-peer bucket
  (`ratelimit.go` `getLimiter`), so an attacker's real supply is one burst per handshake rather
  than the sustained per-peer rate. Only the aggregate ceiling bounds this. See §3.3.

## 3. Design

An **admission gate** in `handleEvidenceMessage`. Order is load-bearing — cheapest and most
certain refusals first, budget charged only for messages that would actually cost work:

1. **structural consistency** (free, no state): `VoteA`/`VoteB` agree on height, round, type and
   `ValidatorProTxHash`, and the block IDs differ. These are checks `VerifyDuplicateVote` already
   makes, but only after the disk reads;
2. **extension bound**: neither vote may carry more than `MaxVoteExtensions` extensions. Nothing
   on this path reads them — the signature check covers the block signature only and `ABCI()`
   ignores them — so above a real vote's cap they are pure payload inflating a message we hash
   and store;
3. **identity dedup** — §3.1;
4. **height window**: the block store can serve the height (`Base() ≤ h ≤ Height()`). Outside it
   verification could only fail on the missing block meta, after paying for the lookup. Those
   two bounds each build a database iterator in the real store — more work than the single point
   lookup they save — so the pool caches them and refreshes once per block rather than once per
   message. Being a block behind is harmless in both directions: too low a ceiling delays
   evidence the sender re-offers a second later, too high a one admits a lookup the budget has
   already paid for;
5. *(existing)* hash dedup, `isPending`/`isCommitted`;
6. **node-wide budget check, without charging** — §3.3;
7. **per-peer work ceiling** — `client.RateLimit`, drop mode, charged in pairings;
8. **node-wide charge.**

Steps 1-4 run **before** step 5 because `isPending`/`isCommitted` compute `evidence.Hash()` —
a full proto marshal plus SHA-256 of a message that may be near the 1 MB channel limit, twice.
Putting the free refusals first means a byte-flip flood costs a digest over a few hundred bytes
and a map lookup, not a megabyte of hashing. A *novel* identity still reaches the hash before
the budget; bounding that would mean bounding the message size, which §6 records rather than
guesses at.

Everything from step 1 on drops **non-punitively**: `return nil`, no `PeerError`, no peer-state
mutation, no broadcast, and a counter incremented under a per-reason label so the shedding is
visible to an operator. Step 1 is a deliberate *softening*: today a structurally inconsistent
message evicts on a node that has the block and is silently ignored on a node that does not, so
the punishment depends on our sync state rather than on the message. We take the lenient branch
(plan §3). Note this softening is narrower than it looks — `DuplicateVoteEvidenceFromProto` runs
`ValidateBasic` at decode, which already enforces that the block IDs differ and are
lexicographically ordered, and rejects (punitively) before the gate is reached.

No age (`MaxAgeDuration`) pre-check: expiry needs `blockMeta.Header.Time`, i.e. the disk read we
are trying to avoid, and the evidence's own `Timestamp` is attacker-supplied. Duplicating the
rule with an untrusted input could drop genuine old evidence. The rate limit bounds it instead.

**Scope — what the gate must never touch.** It is reactor-only. `CheckEvidence` (block
validation), `processConsensusBuffer` (our own consensus-detected equivocation),
`ReportConflictingVotes`, the RPC `BroadcastEvidence` escape hatch, and the outbound
`syncEvidence` walk are all unrated and un-deduplicated. Block validity must be a function of the
block and the chain state alone: making it depend on a size-capped, eviction-ordered,
gossip-history-dependent memory would let two honest nodes disagree about the same block. Pinned
by `TestBlockValidationIsNotRateLimited`.

### 3.1 Identity dedup — positive only, verified only

**Key** = everything that identifies the alleged equivocation and nothing an attacker can mutate
without changing what is alleged: `height ‖ round ‖ vote type ‖ proTxHash ‖ the two block-ID
keys, order-canonicalised`. Signatures, `TotalVotingPower`, `ValidatorPower` and `Timestamp` are
excluded — those are exactly the byte-flip surface. Variable-length fields are hex-encoded and
separated by a character hex cannot contain, so no field can be shifted into its neighbour to
make two different accusations collide; the result is digested so an entry is a fixed 32 bytes.

**An identity is recorded only when the pool accepted the evidence *and* its signatures were
actually checked.** The second half is not a detail — it is what makes positive dedup safe.
Per §1 finding 3, unverified evidence can enter the pool. Were it remembered, an attacker who
knows a real equivocation — they are public gossip — could send that identity with garbage
signatures, claim the key, and the genuine proof would be refused ever after. So `verify()`
reports whether the pairings ran, and only a verified acceptance is remembered. Forgetting is the
safe direction: the worst it costs is a repeated verification. Pinned by
`TestUnverifiableEvidenceIsNotRememberedAsAnIdentity`, which fails against the naive version.

Recording happens where verification demonstrably happened — inside `verify()`'s success path
(covering `AddEvidence`, `CheckEvidence` and the `ValidateABCI`-failure branch, always *after*
the store write succeeded) and in `processConsensusBuffer`, where consensus has already verified
the conflicting votes' signatures. It deliberately does **not** happen in `addPendingEvidence`
(which cannot know) or at `Start()` (which would re-import whatever a previous lifetime wrote,
including anything that entered unverified).

**Why not a negative cache** (remember identities that *failed* verification and refuse them):
this is the suppression trap. A bad signature is a property of the (identity, signature) pair,
not of the identity. A TTL does not fix it — after expiry whoever arrives first re-poisons, and
the attacker sends faster than the 1 Hz honest retry. **So: no negative caching. Ever.**
Repeated *fabricated* identities are bounded by the work budget instead.

**Why positive dedup cannot suppress:** the only way in is passing `VerifyDuplicateVote` *with
keys available*, which requires two valid BLS signatures from that validator over two distinct
block IDs at the same height/round/type. That is equivocation; it cannot be forged. When we drop
a later copy we already hold — and gossip, and can propose — a verified item proving the same
misbehaviour, and `ABCI()` reports the validator, height and power, not which of two proofs we
hold.

**Bounding.** Unbounded seen-sets are themselves a memory DoS. Cap at 1024 entries, each carrying
its evidence height; on overflow evict the lowest heights (oldest ⇒ closest to expiry). Eviction
only forfeits a free refusal — the item is still hash-deduped and rate-limited — so it can never
cause a correctness failure. It does mean the set is a **bounded, best-effort memory, not a
durable guarantee**: an equivocating validator can mint unlimited *provable* equivocations at a
high height (two signatures each) and push older identities out, restoring F1 against them. The
work budget, not the set, is the durable bound. Identities are also not removed on expiry —
expired evidence can never be legitimately re-admitted, so keeping the entry is strictly better
than the current behaviour, where expired evidence returns `ErrInvalidEvidence` and evicts the
sender.

**The memory does not survive a restart, and does not re-learn.** `Start()` deliberately does not
re-import the pending set: those entries would carry whatever a previous lifetime accepted,
including anything that entered unverified, which is exactly what the verified-only rule exists
to keep out. And nothing re-learns them afterwards, because both the reactor and `AddEvidence`
return early on `isPending` before reaching verification. So for evidence already in the pool at
startup, a re-encoded copy is re-verified once per copy for the rest of that evidence's life,
bounded by the work budget rather than by the memory. Committed identities cannot be rebuilt at
all (`keyCommitted` stores the height as its value, not the evidence).

### 3.2 Ceilings

Cost unit = **pairings**, so the numbers are comparable with the consensus budget.
`DuplicateVoteEvidence` = 2.

| knob | value | why |
|---|---|---|
| per-peer rate | 1 work/s | one evidence item per 2 s, sustained, per peer |
| per-peer burst | 16 work | eight items at once; kept small because a rotated identity gets a fresh full bucket (F4) |
| node rate | 160 work/s | ≥ 2 × 68 × per-peer rate |
| node burst | 160 work | eighty items at once node-wide |

`P = 68` is the hard connection ceiling (`MaxConnected 64 + MaxConnectedUpgrade 4`), quoted from
piece 2 §4. The *effective* sustained bound against a fixed peer set is `min(P × 1, 160) = 68`
work/s ≈ 34 evidence verifications/s; the aggregate is the bound that survives identity rotation.
Both bursts are asserted at compile time to exceed one message's cost — a bucket below it rejects
that message forever rather than throttling it — and the rate relation is asserted by test so the
constants cannot drift apart silently.

Deliberately package constants, not config: `config/config.go` is being edited by parallel
pieces, and an evidence channel an operator can accidentally disable is a safety regression, not
a feature.

### 3.3 What is guaranteed about genuine evidence, and what is not

This is fairness form **(a)** of plan §1 — defensible here, unlike on the vote channel, because
evidence demand is tiny and the per-peer cap does not have to be near a message's cost. State it
precisely, because v1 of this spec overclaimed:

- **I1 — per-peer reservation, while the aggregate is not binding.** `client.RateLimit` gives
  every peer its own bucket, and against a *fixed* peer set total admissible demand is
  `≤ P × 1 = 68` work/s against a 160 work/s refill, so the node bucket returns to full and stops
  being the binding constraint. In that regime an honest peer's item is admitted within one
  refill of its own allowance.
- **I2 — a peer never pays for the node's congestion.** The node-wide budget is *inspected*
  before the per-peer bucket is charged and only charged after it. Otherwise an honest peer would
  spend its allowance on attempts the aggregate then discards, halving its retry rate exactly
  when the channel is congested.
- **I3 — drops are delays.** Senders retry pending evidence at 1 Hz forever (§1 finding 2), over
  an age window of 48 h. A refused item is re-offered; it is not lost.
- **I4 — non-punitive.** No gate refusal produces a `PeerError`, so a flood cannot get an honest
  peer evicted through this path, and a shed message costs zero pool writes.

**Where I1 does not hold, stated plainly.** Under **identity rotation** (F4) the attacker's
supply is one burst per handshake, not the sustained per-peer rate, so the node-wide bucket can
be held near empty and honest admission becomes a race rather than a guarantee. Recovery is then
I3 — unbounded retry against a 48 h window — which is a real but *probabilistic* bound, not the
seconds-scale one I1 gives against a fixed peer set. Three things follow, and none of them are
this piece's to fix: the per-peer burst is kept small (8 items) because it is exactly what a
rotation harvests; the connection-level defences are **plan §2 item 3b**'s job (durable
protection for current-quorum peers, honest slot count, per-/24 inbound diversity); and the
`conn_tracker` reconnect window only applies when an address holds zero connections, which is
worth an owner.

Other residual risks:
- A synchronised burst from all peers (`P × 16 = 1088 > 160` node burst) can drop a genuine item;
  the 1 Hz retry recovers it. The guarantee is over sustained rate, not over a synchronised burst.
- The set is cyclable by a genuinely equivocating validator, and empty after a restart (§3.1).
- A peer that goes quiet for a minute gets its limiter garbage-collected and returns with a full
  bucket — strictly less useful to an attacker than rotating identities, which is already covered.
- Eclipse is out of scope, as always.

### 3.4 Rejected alternatives

- **Negative/seen-set caching of failed verifications** — §3.1. Rejected: suppression.
- **Dedup keyed on the equivocation slot `(height, round, type, proTxHash)` only** — coarser, so
  it would also drop a *second, different* equivocation by the same validator in the same slot,
  and it widens the blast radius of the unverified-entry hole. Rejected.
- **Reusing the consensus `verification_budget`.** Different package, owned by a parallel piece,
  and the evidence reactor is a different goroutine with a different safe rate. A one-line
  `rate.Limiter` here is cheaper than the coupling.
- **Closing §1 finding 3 by refusing evidence we cannot verify.** Tempting, and it would make
  "in the pool ⇒ verified" true everywhere — but `verify()` is shared with `CheckEvidence`, so it
  would make block validity depend on whether a node holds historical keys. That is a chain
  split. Not here; see §6.

## 4. Tests

Work oracle: `LoadBlockMeta` call count. It is the first thing `verify()` does, so
*work performed ⟺ oracle incremented*, and it is itself one of the uncharged disk reads. A fake
clock meters both budgets, so no tokens refill unless a test asks for them.

| test | property |
|---|---|
| `TestByteFlipFloodDoesNotReverify` | 200 mutated copies of evidence we hold ⇒ zero verifications, zero errors |
| `TestPoisonedIdentityDoesNotSuppressGenuineEvidence` | a forged copy sent first does not lock the genuine proof out |
| `TestUnverifiableEvidenceIsNotRememberedAsAnIdentity` | the §1-finding-3 path cannot claim an identity |
| `TestGenuineEvidenceAdmittedUnderFlood` | 2400-message flood from 60 identities ⇒ bounded work, and an honest item lands within a few sync ticks |
| `TestFabricatedFloodIsBounded` | one peer's verifications ≤ what its budget buys |
| `TestSheddingIsNeverPunitive` | every refusal returns nil — no `PeerError`, no eviction |
| `TestOutOfWindowEvidenceCostsNoDiskIO`, `TestStructurallyInconsistentEvidenceCostsNoDiskIO` | free refusals touch no disk |
| `TestCommittedEvidenceMutationIsFree` | identity memory survives pending → committed |
| `TestBlockValidationIsNotRateLimited` | `CheckEvidence` is unaffected by budgets and by the memory |
| `TestRefusingAnUnservableHeightIsADelay` | evidence refused for an unreachable height is accepted once we reach it |
| `TestOnePeersFloodDoesNotSpendAnother` | one peer emptying its bucket leaves another's full |
| `TestEvidenceIdentity*`, `TestIdentitySetEvictsOldestFirst`, `TestAllegesOneEquivocation` | key ignores mutable bytes, distinguishes different accusations, set is bounded and evicts oldest first |
| `TestAdmissionBudgetsAreSpendable` | no bucket is below one message's cost; node rate exceeds `2 × P × peer rate` |

All red before the change (except the two that are guards against designs never shipped), all
green after, `-race` clean.

## 5. Not covered here

Load testing against the plan §5 gate, and any change to the punitive handling of evidence that
fails verification. This piece bounds the work; it does not re-open who gets blamed for it.

## 6. Findings recorded for other owners

Surfaced by review of this piece, all **pre-existing** and none made worse by it:

1. **Unverified evidence is accepted, stored, gossiped and proposable** when the historical
   validator set carries no public keys (§1 finding 3). A node in that state can be made to
   propose evidence other nodes will reject. Fixing it means changing `verify()`, which
   `CheckEvidence` shares — so it needs a consensus-safe design, not a patch.
2. **`ErrInvalidEvidence` evicts the sender** on the first bad signature, including when the
   disagreement is our own validator-set view (§1 finding 1). Plan §3 says this must never punish
   a relayer.
3. **The pending pool has no size cap**, and `Pool.Start()` reloads it with a per-item size
   recomputation that is O(n²) with `maxBytes == -1`. Combined with finding 1 this is remotely
   reachable.
4. **`Pool.Start` writes `evpool.state` without the mutex**, racing `State()`/`hasPublicKeys()`,
   and `isStarted` is checked but never set, so the double-start guard is dead.
5. **`conn_tracker`'s per-address reconnect window only applies when that address holds zero
   connections**, so keeping one connection open makes the rest of an address's churn unthrottled
   — an input to F4 above, and to plan §2 item 3b.
6. **Evidence can be inflated to the 1 MB channel limit with vote extensions that nothing
   verifies or reads.** `Vote.SignBytes` does not cover extensions, so they are free padding on
   an otherwise genuine equivocation: the copy still verifies. The pool then re-sends it to every
   peer every second for as long as it stays pending. Worse, a single item above
   `ConsensusParams.Evidence.MaxBytes` truncates `PendingEvidence`, so it also blocks everything
   behind it from ever being proposed. Bounding the *count* of extensions (done here) does not
   bound their bytes, and picking a byte threshold requires knowing what an application's
   extensions may legitimately weigh — guess low and genuine evidence is refused, which is the
   one outcome worse than the flood. Needs an owner who can set that number.
7. **`Pool.Start` re-reads the whole pending set with `listEvidence(prefixPending, -1)`**, which
   recomputes the cumulative encoded size on every iteration — quadratic in the pool's size, with
   no cap on that size (finding 3).
