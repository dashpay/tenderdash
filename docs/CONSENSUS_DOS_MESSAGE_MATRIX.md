# Consensus DoS — Per-Message Matrix

Bottom-up map of every consensus message, its cost, current protection, attribution,
DoS vector, and candidate solutions. Foundation for per-message research → full-coverage
picture → plan. (`#16` P0 panic fix is out of scope here and ships as-is.)

Topology reminder: **~616 evonodes gossip; validator set = 100-node hourly-rotating
quorum**. A received message is almost always **relayed by an honest evonode, not signed
by the sender** — so per-message *attribution* (origin vs relay) is a first-class column.

Channels & current limiters: Vote + Data channels have per-peer rate limits (`#17`);
**State + VoteSetBits channels have none**. Gate (`voteSenderAllowed`) is being **removed**
(quorum-scoped = wrong for 616-node gossip).

## The 11 messages

| # | Message | Channel | Cost | Current protection | Attribution | DoS vector | Candidate solutions (to research) |
|---|---|---|---|---|---|---|---|
| 1 | **Vote** (block sig) | Vote | 1 BLS pairing (after SEC-002 short-circuit) | per-peer rate-limit; crypto-hardening; gate(→removed) | relay ≠ signer | invalid-sig flood → serial CPU exhaustion (the measured attack) | rate-limit (peer+IP); forgery-only misconduct evict; backlog bound |
| 2 | **Vote** (ext sigs) | Vote | +1 BLS per threshold ext (short-circuits, SEC-003); cap 32 (SEC-007) | as above + ext cap | relay | extension amplification within one vote | ext cap (done); typed ext-sig error for classification |
| 3 | **Commit** | Vote | threshold BLS `VerifyCommit` | per-peer rate-limit; invalid-commit instant-evict (forgery-only, replay-gated — sound); hardening | relay | commit flood (was ungated by the vote gate) | rate-limit (peer+IP); keep sound forgery-evict |
| 4 | **Proposal** | Data | proposer sig (1 BLS) | per-peer rate-limit (cost 5) | relay; same-height fork / divergent state can fail honestly | proposal flood | cost-weighted rate-limit (done); guard analysis before any punishment |
| 5 | **ProposalPOL** | Data | cheap (POL bit-array) | per-peer rate-limit (1) | relay | low | rate-limit likely sufficient |
| 6 | **BlockPart** | Data | per-part decode/hash/reassembly; block-completion → `LastCommit` BLS | per-peer rate-limit (1); part-set header checks | **multi-peer assembly — NOT attributable to one sender** | part flood; bandwidth/reassembly cost; bad `LastCommit` only on completion | rate-limit; part-set bounds; never punish last-part sender |
| 7 | **NewRoundStep** | **State** | cheap (`ValidateHeight`) | **none** | relay | structural flood; peer round-state churn | per-peer State-channel budget |
| 8 | **NewValidBlock** | **State** | cheap | **none** | relay | low | State-channel budget |
| 9 | **HasVote** | **State** | cheap (bit-array set) | **none** | relay | peer-state churn flood | State-channel budget |
| 10 | **HasCommit** | **State** | cheap | **none** | relay | low | State-channel budget |
| 11 | **VoteSetMaj23** | **State** | cheap, **but forces a `VoteSetBits` RESPONSE** | **none** | relay | **response amplification** (small in → we compute+send out) | State-channel budget; bound response generation |
| 12 | **VoteSetBits** | **VoteSetBits** | cheap (bit-array apply) | **none** | relay | peer-state churn flood | per-peer VoteSetBits-channel budget |

(Rows 1–2 are the two cost components of one `Vote` message.)

## Three structural classes (early synthesis — to confirm via research)

- **Class A — BLS-verification floods** (Vote, Commit, Proposal): cost is CPU per message.
  Levers: ingress rate-limit (membership-free) + narrow forgery-only misconduct evict +
  bound the already-admitted backlog.
- **Class B — bandwidth/reassembly** (BlockPart): pre-verification cost, multi-peer, not
  attributable. Levers: rate-limit + part-set bounds. Not a misconduct vector.
- **Class C — cheap-but-ungated** (NewRoundStep, NewValidBlock, HasVote, HasCommit,
  VoteSetMaj23, VoteSetBits): no BLS, but the State + VoteSetBits channels have **no rate
  limit at all**, and VoteSetMaj23 is a response-amplifier. Lever: per-peer budgets on
  those channels.

**Implication for a "generalized solution":** it is likely *not* one mechanism but a small
stack that every message maps onto —
1. **universal per-peer + per-IP rate-limiting on ALL channels** (covers A, B, C at ingress;
   membership-free; correct for 616-node gossip),
2. **narrow per-vector forgery-only misconduct → evict/cooldown** (deterrent for the A
   subclass where attribution is provable — commit today, votes/proposals after guard
   analysis),
3. **backlog bounding** (protects the 20k consensus queue that eviction doesn't drain).

Per-message research (next) validates each row's vector + solution, confirms attribution,
and checks coverage against these three layers.

## Questions each per-message research answers

For every message: exact attack (rate/cost/amplification), current protection adequacy,
origin-vs-relay attribution, per-message solution options + trade-offs, whether the
generalized stack above covers it, and residual risk. Then: **do we need what `#17` has**
(per row), and **do we need phases** (fall out of which solutions are small/safe vs
large/risky).
