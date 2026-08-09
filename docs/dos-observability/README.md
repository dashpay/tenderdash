# Consensus DoS — observability

A Grafana dashboard and Prometheus alert rules for watching the consensus DoS
throttle, built to answer one question at a glance:

> **Is the node shedding the attack, or shedding its honest peers?**

## Files

- `grafana-dashboard.json` — three-band dashboard (chain health / throttle firing /
  honest service). Import into Grafana or drop into a provisioning directory.
- `prometheus-alerts.yml` — alert rules that page on *honest* symptoms (chain
  struggling, honest-vote latency high), not on drop counters. Load alongside the
  deploy repo's existing `ChainHalt` / `RoundTooHigh` rules.

## Reading it

The dashboard has three rows:

1. **Is the chain healthy?** — block production rate, rounds, prevote delays.
2. **Is the throttle firing, and where?** — drops by mechanism, evidence by reason.
   Rising here under attack is *expected and good*.
3. **Is honest service still landing?** — accepted-vote latency (p50/p99), budget
   saturation, lane depth.

The judgement:

| middle band (drops) | bottom band (honest) | verdict |
|---|---|---|
| high | latency low, saturation off the floor, rounds flat | **shedding the attack — healthy** |
| high | latency climbing toward the vote/propose timeout, saturation pinned at 0, rounds climbing | **starving honest work** |

The single most important panel is **accepted peer-vote latency p99**. Threshold
lines are drawn at the vote timeout (1 s) and propose timeout (3 s).

## Deployment facts (dash-network-deploy)

- Metrics carry the namespace prefix **`drive_tenderdash`** (set in dashmate's
  `config.toml.dot`). If your deployment uses a different `[instrumentation]
  namespace`, adjust the metric prefixes in both files.
- Prometheus scrapes **HP masternodes at `:36660`** — **seed nodes are not
  scraped** (they expose the endpoint but no job targets them). The dashboard
  therefore reflects validator nodes.
- The metrics endpoint is enabled by the deploy tool's `dashmate.json.j2` override,
  not by vanilla dashmate (whose default is off / localhost). A first devnet needs
  no config change to observe these — the endpoint and scrape already exist.
- Provisioning: the deploy repo's `roles/metrics/` ships Prometheus + Grafana but
  no dashboards in git. Add `grafana-dashboard.json` to a Grafana provisioning
  path (or import via the UI), and append `prometheus-alerts.yml` to the
  Prometheus rules the metrics role loads.

## Metrics used

New with the DoS work — throttle firing:
`consensus_verification_budget_drops`, `consensus_peer_lane_drops`,
`consensus_block_part_proof_drops`, `consensus_state_channel_drops`,
`consensus_proposal_verify_failures`, `evidence_pool_dropped_evidence{reason}`.

New — honest service:
`consensus_peer_vote_verify_latency_seconds` (histogram),
`consensus_verification_budget_saturation` (gauge, 1 = idle → 0 = drained),
`consensus_peer_lane_active_count`, `consensus_peer_lane_max_depth`.

Stock, for chain health:
`consensus_latest_block_height`, `consensus_rounds`,
`consensus_quorum_prevote_delay`, `consensus_full_prevote_delay`.

(All shown without the `drive_tenderdash_` namespace prefix that the deployment adds.)
