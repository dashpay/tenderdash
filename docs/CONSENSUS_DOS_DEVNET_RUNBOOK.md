# Consensus DoS — devnet test runbook

> **Part of a combined campaign.** The devnet image ships this DoS work *and* the p2p /
> statesync hardening cluster together. Run both under **`DEVNET-CAMPAIGN.md`** (top-level),
> which sequences this runbook with `DEVNET-E2E-SPEC-p2p-hardening.md` and merges the shared
> scenarios. This file is the DoS half; read the campaign for run order and the cross-cutting
> watches (evidence §6.7, statesync memory §6.2, the deferred #22).

The ordered procedure for validating the Phase-1 defences on a devnet: which tool
to run, what to watch, and the pass/fail line for each step. Companion to
`docs/CONSENSUS_DOS_ROLLOUT_SPEC.md` (the design and the D1–D8 scenario
definitions) — this is the execution side.

The one question every step serves: **is the node shedding the attack, or
shedding its honest peers?**

## Image under test

- `dashpay/tenderdash:1.6.1-alpha.1`, linux/arm64
- digest `sha256:4f6d389f5266355cd889d5207b5f9ca5ae3a0d5882727834d985c25b3745db35`
- **verify by digest, not `/status`** — the binary reports version `1.6.0` (the
  branch/sha is not embedded in a container build), so a version string cannot
  distinguish it from stock 1.6.0. `docker image inspect` the digest on every node.

## Tools

| tool | what it drives | repo |
|---|---|---|
| **consensus-flood** | p2p consensus messages (the attack) | this repo, `test/floodclient/`, built with `-tags floodclient` |
| **platform-tui `load_test`** | transaction / state-transition load (honest background) | `dashpay/platform-tui@master`, `cargo run --release --bin load_test` |

Build the flood client (BLS CGO env prepended):
`go build -tags floodclient -o consensus-flood ./test/floodclient/cmd/consensus-flood`

## Prerequisites — line these up before the first run

1. **Network reach.** From the box running the tools: the target node's p2p port
   and node ID; its RPC; and the Prometheus address (metrics land under namespace
   `drive_tenderdash` on `:36660`, scraped from HP masternodes — seed nodes are
   not scraped).
2. **The validator set** — proTxHashes + index, the quorum hash, and quorum type,
   read off the network RPC. **Mandatory:** a forged vote is rejected on the
   proTxHash/index check *before* it reaches the verification budget, so without
   real validator identities the flood only exercises handshake/admission, not the
   defence. Pass via `--validators 0:HEX,1:HEX,... --quorum-hash HEX --quorum-type N`.
3. **A validator signing key we control** — one devnet validator must be ours, so
   `--signing-key`/`--signing-index` can produce genuine honest votes for mixed
   mode and the valid-signature/invalid-extension profile.
4. **Load-tool config** — `.env` with DAPI addresses, a funded wallet key, and Core
   RPC, for realistic transaction background.
5. **Thresholds agreed in advance** (do not grade after the fact). Suggested,
   confirm before running:
   - block interval stays within ~1.5× of baseline
   - `consensus_rounds` stays ≤ 1 most of the time (no chronic round-climbing)
   - accepted-vote latency (`PeerVoteVerifyLatencySeconds`, once landed) stays
     below `timeout_propose` (3 s) — and ideally near `timeout_vote` (1 s)
   - zero peer evictions attributable to the flood
   - every drop is explained by an attack profile that is running

## Metrics to watch (the dashboard, in three bands)

**Chain healthy?** (top band)
- `drive_tenderdash_consensus_latest_block_height` — rate must stay positive
- `drive_tenderdash_consensus_rounds` — climbing = struggling to reach 2/3
- `drive_tenderdash_consensus_block_interval_seconds`
- `drive_tenderdash_consensus_quorum_prevote_delay` / `..._full_prevote_delay` —
  stock proxies for honest-vote health

**Throttle firing, and where?** (middle band)
- `..._consensus_verification_budget_drops`
- `..._consensus_peer_lane_drops`
- `..._consensus_block_part_proof_drops`
- `..._consensus_state_channel_drops`
- `..._consensus_proposal_verify_failures`
- `..._evidence_dropped_evidence` by `reason`

**Honest service still landing?** (bottom band — the metrics being added now)
- `..._consensus_peer_vote_verify_latency_seconds` (honest-vote service latency)
- `..._consensus_verification_budget_saturation` (1.0 = idle, 0.0 = drained)
- `..._consensus_peer_lane_*` depth/active-count

**The reading rule:** middle band HIGH while top and bottom bands stay FLAT ⇒
shedding the attack (pass). Middle band high AND rounds climb / latency grows /
budget pinned at 0 ⇒ starving honest work (fail).

## Procedure

Run each step for long enough to be steady-state (soak steps longer). Record the
three bands at baseline and under each load. **Every step is a comparison to the
no-attacker baseline on the same network — take that first.**

### Baseline (before any attack)
Bring the devnet up on the image. Run `load_test` at a modest sustained rate for a
clean baseline of block interval, rounds, prevote delays. This is the reference
every scenario is judged against.

### D1 — clean soak, ≥ 24 h
No flood. `load_test` at steady rate.
**Pass:** block interval and round distribution within noise of baseline; zero
unexplained drops.

### D2 — each attack profile individually, at saturation
For each profile, hold most of the 68 slots and flood while `load_test` runs
honest background:
```
consensus-flood --target HOST:P --chain-id CHAIN --node-id ID \
  --validators ... --quorum-hash HEX --quorum-type N \
  --profile <P> --identities 60 --rate 600 --duration 15m
```
Profiles and the counter each should move:
- `prevote` → budget/lane drops
- `precommit-extensions` → budget/lane, and stays *under* budget (staged permits
  charge ~1 not 33 — under-saturation is the proof)
- `commit` → budget, then correct **eviction** (a forged threshold sig is provably
  malicious; this is the one attributable disconnect — expected, not a failure)
- `proposal` → `proposal_verify_failures` (only bites while the node is
  mid-consensus)
- `blockpart` → `block_part_proof_drops`
- `state` / `maj23` → `state_channel_drops`
**Pass, each:** chain keeps committing; accepted-vote latency below
`timeout_propose`; drops attributable to the running profile; no eviction except
the `commit` profile's attributable one.

### D3 — all profiles combined, all slots
Run several flood instances covering all profiles at once, slots saturated.
**Pass:** as D2, and the node returns to D1 behaviour within one height of the
flood stopping (verify the recovery explicitly).

### D4 — honest catch-up under flood
While D3 flood runs, restart a validator (or bring up a lagging full node) and let
it resync.
**Pass:** it catches up. Watch `peer_lane_drops` and vote latency *for the
catching-up peer specifically* — this is where the changed `peer-vote-rate-limit`
units would bite if a config were hand-rolled (managed nodes are fine).

### D5 — mixed version
Half the nodes on the image, half on stock 1.6.x.
**Pass:** no stall, no fork, no partition. Watch the peer graph — the slot
reservations and the DIP-6 ordering change could make new and old nodes disagree
about who connects to whom (cannot fork, can change gossip latency).

### D6 — validator slot pressure
Flood all inbound slots, then restart a validator.
**Pass:** the validator reconnects and keeps its slot — this is what the connection
reservation bought (0 → ≥18/68 guaranteed honest slots under flood).

### D7 — config traps (prove the release notes are warranted)
On a throwaway node: set `verification-rate-limit = 10` → it must fail to start
with a clear message. On another: leave `peer-vote-rate-limit = 100` (old units) →
observe the predicted vote loss.
**Pass:** both behave as documented in `UPGRADING.md`. (Only relevant for
hand-written configs; dashmate-managed nodes take the new defaults.)

### D8 — genuine evidence under an evidence flood
Run the `evidence`/`maj23` flood while a real double-sign is produced (the e2e
`runner evidence` path, or a controlled equivocation from our validator).
**Pass:** the genuine evidence is still accepted and reaches a block. This is the
safety property — a defence that suppressed real evidence would be worse than the
DoS.

### Mixed-mode honest-latency measurement (runs alongside D2/D3)
```
consensus-flood ... --mixed --signing-key key.json --signing-index 0 \
  --quorum-hash HEX --quorum-type N --profile prevote --identities 60 --rate 600
```
Our honest voter sends genuine votes while the attacker identities flood; compare
`peer_vote_verify_latency_seconds` for the honest key against baseline. This is the
in-network reproduction of the §0 answer the reactor test already proved
in-process.

## Go / no-go

**GO** if: every D-step meets its pass line; every observed drop is explained by a
running profile; the chain never stalls or forks; accepted-vote latency stays
below the agreed threshold; and the recovery (D3) and catch-up (D4) both hold.

**NO-GO** if: rounds climb chronically under flood, accepted-vote latency crosses
the vote timeout at realistic quorum size, any honest peer is evicted by the
flood, genuine evidence is suppressed (D8), or a drop counter moves with no
attack running.

Record the numbers against the pre-agreed thresholds. An honest NO-GO with numbers
is a real result — the fair-share bound means latency at full quorum under maximum
flood is expected to be seconds; whether that is acceptable is a threshold decision
made in advance, not a surprise graded afterward.

## What still needs a human / infra

- Standing up the devnet (AWS, the create-devnet workflow) — infra team.
- Reading the validator set + quorum params off the network, and providing one
  validator key we control.
- The `load_test` funded wallet.
- Agreeing the numeric thresholds before the run.
