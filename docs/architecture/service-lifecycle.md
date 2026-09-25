# Service lifecycle core

`BaseService.Start` gives `OnStart` an attempt-scoped child context. Parent
cancellation and manual `Stop` both cancel it. `Go(ctx, fn)` registers workers
using that context or a derived context; registration is synchronized with
shutdown and rejected for a different attempt or after shutdown begins.

Shutdown closes admission, marks `IsRunning` false, cancels the context and
calls `OnStop`. It then joins registered workers, calls optional `OnDrain`, and
releases `Wait`. `Stopping()` signals cancellation separately from completion.
`Stop` invokes `OnStop` synchronously but does not join registered workers.
Concurrent or recursive calls to `Stop` return immediately. During `OnStart`,
`Stop` cancels immediately and defers the shutdown hook until successful startup
returns; `Wait` still joins the attempt.

Hooks run outside the lifecycle mutex. `OnStop` must unblock workers without
waiting for its own registered work. Workers may call `Stop`; neither hooks nor
workers may call their own service's `Wait`. Release resources shared with
workers in `OnDrain`, or after `Stop` followed by `Wait`.

`IsRunning` is false during startup and shutdown. `Wait` joins the current
attempt, including startup and cleanup; it returns immediately before startup.
Failed `OnStart` cancels and joins its workers before retry becomes possible.
The implementation must roll back acquired resources: failed attempts do not
invoke `OnStop` or `OnDrain`. Successfully stopped services cannot restart.
Composite retries also require recreating children already successfully stopped.

## First migration stage

Consensus State registers its receive loop and joins its queue fan-in and WAL
before returning. The timeout ticker, WAL and autofile group use shared worker
tracking. AutoFile explicitly joins its timer/signal worker. WAL ownership lasts
until the receive loop's final write. Consensus handoff admission is also joined,
so a late blocksync handoff cannot start State after reactor shutdown. The caller
may cancel its wait after admission, but the reactor still owns and joins the
handoff. Startup and the resulting State use the reactor's lifetime.

Node waits for reactors before closing event sinks and stores. The blocked
receive-loop regression and original node teardown tests verify this path
without using leaktest as an extra production-shutdown barrier.

This is a partial migration. Plain goroutines are not tracked automatically.
RPC/WebSocket handlers, mempool rechecks, reactor gossip and other service
workers retain their existing ownership mechanisms. This change does not claim
that Node.Wait joins every helper or introduce the broader durable-finalization
and dependency-lifetime changes from #1515.

Three compatibility bridges preserve parent-context lifetimes: blocksync
application during handoff, connection error callbacks after I/O shutdown, and
an admitted consensus commit during direct State.Stop. Parent cancellation still
aborts these operations. In particular, reactor/node shutdown can interrupt
finalization and require WAL recovery. Their existing ownership mechanisms remain
in place; this does not detach application work from node shutdown.

## Cancellation compatibility audit

Cancellation precedes OnStop for every BaseService implementation, not only the
managed-worker migrations. The in-tree hook/callback audit covers these groups:

| Implementations | Cancellation and cleanup policy |
| --- | --- |
| Consensus State, reactor, ticker, WAL; autofile Group | Managed work is joined; commit uses the State parent context; file ownership ends with explicit Close. |
| Blocksync reactor and Synchronizer | Handoff preserves application work across Synchronizer.Stop; stopping its parent reactor may cancel it. |
| P2P Router and MConnection | Connection callbacks retain the parent. Router removes peer bookkeeping even with a canceled context; final PeerStatusDown broadcasts are best effort. |
| PEX, mempool, statesync and evidence reactors | Processing uses the work context; stop hooks close resources/dispatchers or require no extra signal. Existing plain-worker ownership is unchanged. |
| EventBus, pubsub Server and indexer Service | Event processing is cancellation-driven; indexer closes sinks in its existing hook. No guarantee of a live cleanup callback context. |
| ABCI local, socket, gRPC and routed clients; proxy; socket/gRPC servers | Stop hooks close transports, drain requests or stop children without requiring a live work context. |
| SignerServer, signer listener/dialer endpoints | Hooks close endpoints/listeners; dial/retry work observes cancellation. |
| ValidatorConnExecutor and light RPC Client | Unsubscribe uses a separate bounded cleanup context; client hooks invoke owned closers. |
| Node and seed node | Parent cancellation stops children; hooks wait for existing service completion before closing owned stores. |

This is a cancellation compatibility audit, not a claim that unmigrated plain
workers are all joined. Subscribers must stop with their owner, without relying
on a final router broadcast. Avoid detaching broadcasts from cancellation:
unresponsive subscribers could otherwise block shutdown.

Group rotation reuses its AutoFile and closes the current descriptor before
renaming; it does not create a worker or signal registration per rotation. The
owner must Close the group, including when it never started, to join the head's
single timer/signal worker. Context cancellation alone does not release the head.
