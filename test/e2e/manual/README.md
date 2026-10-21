# Manual block-sync application backpressure test

This scenario is opt-in. It is outside `networks/`, the default runner flow,
`test/e2e/tests`, and CI. Run it from the repository root with Docker Compose
available and the native build prerequisites installed:

```bash
make -C test/e2e docker runner
run_dir=$(mktemp -d /data/tmp/tenderdash-slow-apply-XXXXXX)
cp test/e2e/manual/slow-apply.toml "$run_dir/network.toml"
test/e2e/build/runner -f "$run_dir/network.toml" slow-apply
```

Use `--image <tag>` for an E2E node image built from this checkout. The standard
E2E subnet and host ports must be unused; do not run another E2E network in
parallel. Existing named containers and output directories are rejected. The
command removes only its own Compose project on completion, SIGINT, or SIGTERM (SIGKILL cannot run cleanup),
and preserves generated files, node logs, goroutine profiles, and metrics in
`$run_dir/network`. A normal run takes about five minutes; the overall deadline
is twelve minutes.

One genesis validator produces at least 100 small blocks (each checked to be
at most 2 KiB), using a one-second empty-block interval to bound production
without throttling response delivery. Three serving full nodes then catch up sequentially before the
gated target starts. All four sources must reach height 100 and finish block
sync. The target is another full node starting from genesis with state sync
disabled. Full nodes use the existing mock Dash Core service for quorum
verification. A single genesis validator enters consensus directly; this keeps
source bootstrap independent of initial multi-validator block-sync startup. Only the target application
is held inside its first `FinalizeBlock`; Docker processes and networking keep
running. The runner requires more than 16 KiB of received block responses while
the application is blocked, establishing local buffering rather than a slow
source or an empty network. It does not claim that the result queue is full.

For 70 seconds, longer than the 60-second historical stall window, the runner
checks that block sync remains active, the block store stays at the first
block, at least two peers remain connected, and no peer eviction is recorded.
The eviction counter is lazily exported, so its absence means no recorded
events; the block-sync and connected-peer gauges must be present. The gate's
entry marker and goroutine profile distinguish the injected application wait
from a network timeout.

After releasing the application, the runner requires catchup within two minutes:
at least height 100, within two blocks of a source, consensus handoff complete,
and additional block requests and responses beyond those seen during the
hold. Requests are measured at the sources, filtered by the target peer ID;
responses are measured at the target. This demonstrates continued source usability beyond the initial request
window. Aggregate counters prove that at least one source remains usable;
they do not establish fairness or future eligibility for every individual peer.
This scenario does not inject a slow or malicious remote source.
