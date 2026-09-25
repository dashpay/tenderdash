# Service lifecycle core

`BaseService.Start` gives `OnStart` an attempt-scoped child context. Parent
cancellation and manual `Stop` both cancel it. `Go(ctx, fn)` registers workers
using that context or a derived context; registration is synchronized with
shutdown and rejected for a different attempt or after shutdown begins.

Shutdown closes admission, marks `IsRunning` false, cancels the context and
calls `OnStop`. It then joins registered workers, calls optional `OnDrain`, and
releases `Wait`. `Stopping()` signals cancellation separately from completion.
`Stop` invokes `OnStop` synchronously but does not join registered workers.
Concurrent or recursive calls to `Stop` return immediately.

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
so a late blocksync handoff cannot start State after reactor shutdown.

Node waits for reactors before closing event sinks and stores. The blocked
receive-loop regression and original node teardown tests verify this path
without using leaktest as an extra production-shutdown barrier.

This is a partial migration. Plain goroutines are not tracked automatically.
RPC/WebSocket handlers, mempool rechecks, reactor gossip and other service
workers retain their existing ownership mechanisms. This change does not claim
that Node.Wait joins every helper or introduce the broader durable-finalization
and dependency-lifetime changes from #1515.

Two compatibility bridges preserve existing parent-context lifetimes: blocksync
application during handoff and connection error callbacks after I/O shutdown.
Their local cancellation and join mechanisms remain in place in this stage.
