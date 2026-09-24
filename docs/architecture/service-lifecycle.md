# Service lifecycle

`BaseService` owns one successful service lifetime. `Start` passes a child of
its caller's context to `OnStart`, preserving values and deadlines. Both parent
cancellation and `Stop` cancel that child. Services cannot restart after a
successful start; a failed start may be retried after its workers drain.

Use `Go(ctx, func(context.Context))` for service-owned background work. Pass the
context received by `OnStart`, or a child of it. Admission is synchronized with
shutdown and tied to that startup attempt; it returns false after admission
closes or if the context belongs to a different attempt. Clean up any resources
acquired before rejected admission. Plain `go` statements are not tracked.

Shutdown has three phases:

1. Close admission, mark `IsRunning` false, and cancel the service context.
2. Call `OnStop` once to unblock workers and request child shutdown.
3. Join admitted work, call optional `OnDrain` to release resources, and unblock
   `Wait`.

`Stopping()` exposes context cancellation separately from completion.
`Stop` does not join workers. Concurrent or recursive `Stop` calls return
immediately. A stop during startup cancels the context and schedules `OnStop`
after successful `OnStart`; hooks never overlap. `OnStop` must not join managed
workers. Workers may call `Stop`; neither workers nor hooks may call their own
service's `Wait`. Hooks run without the lifecycle mutex.

`IsRunning` is false during startup and from the first shutdown request onward;
it does not imply completion. `Wait` waits for the current attempt, including
startup, registered work, and finalization. It returns immediately before the
first start or after failed startup has drained. Repeated `Start` while running
succeeds; concurrent startup and startup after shutdown return errors.

On failed startup, the implementation rolls back its acquired resources.
`BaseService` cancels and joins registered work before permitting retry; it does
not call `OnStop` or `OnDrain` for that failed attempt. Avoid waiting for workers
inside startup rollback unless their operation has first been unblocked.

## Ownership and durability

Registering a wrapper does not automatically join its children. Owners stop and
wait for child services before closing their shared stores, transports or sinks.
A request or peer session can retain its own cancellation and join group when
its lifetime is shorter than the enclosing service.

Consensus keeps its WAL available until the receive loop finishes. WAL and
file groups flush after their periodic workers exit; AutoFile closes and joins
its timer/signal worker. An already persisted consensus commit finishes
application before the owner releases stores. These explicitly owned resource
lifetimes can outlive cancellation of the caller; their owners must still join
them. A timeout is not a substitute for completion.

## Migration coverage

The service audit covers consensus (state, reactor, ticker, gossip and WAL),
autofile groups, node and seed owners, event bus and pubsub, indexer, block and
state sync, mempool, evidence, P2P router/PEX/connections, quorum event handling,
ABCI clients/servers, proxy wrappers, signer services, and the light RPC wrapper.
Worker-free implementations retain their hooks without artificial workers.

Service-scoped cancel functions, completion channels and custom `Wait` methods
are replaced by the shared mechanism. Connection/request groups, peer-session
cancellation, restartable RPC broadcast admission and worker-pool synchronization
remain where they represent a separate lifetime. Explicitly owned dependency
contexts also remain where durable application must finish after I/O stops.

This does not claim a repository-wide join for every plain goroutine. RPC
websocket client reconnection/session helpers are outside `BaseService`; generic
consumer middleware can hide context-canceled, in-memory limiter housekeeping.
Those boundaries need their own ownership API before a service can promise to
join every helper. Router connection/request groups and AutoFile workers are
joined explicitly rather than converted into unrelated service lifetimes.

Retry of a composite service also requires recreating any children that already
started successfully and were then stopped during rollback. Arbitrary injected
ABCI clients cannot be recreated by the routed wrapper. The node reserves RPC
listeners before starting children, so retry after listener failure remains
supported without restarting successful child lifetimes.
