# Service lifecycle

`BaseService.Start` gives `OnStart` an attempt-scoped child context. Parent
cancellation and manual `Stop` both cancel it. `Go(ctx, fn)` registers workers
using that context or a derived context; registration is synchronized with
shutdown and rejected for a different attempt, a canceled context, or after
shutdown begins. Clean up any resources acquired before a rejected admission.
Plain `go` statements are not tracked.

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

## Ownership and durability

Registering a wrapper does not automatically join its children. Owners stop and
wait for child services before closing their shared stores, transports or sinks.
A request or peer session can retain its own cancellation and join group when
its lifetime is shorter than the enclosing service.

Consensus keeps its WAL available until the receive loop finishes. WAL and
file groups flush after their periodic workers exit; AutoFile closes and joins
its timer/signal worker. The node runs application
connections, the event bus and the indexer on a dependency context that outlives
reactor cancellation, and cancels it only after reactors have been joined. These
explicitly owned lifetimes can outlive cancellation of the caller; their owners
must still join them. A timeout is not a substitute for completion.

Three compatibility bridges preserve parent-context lifetimes: blocksync
application during handoff, connection error callbacks after I/O shutdown, and
an admitted consensus commit during direct State.Stop. Parent cancellation still
aborts these operations. In particular, reactor/node shutdown can interrupt
finalization and require WAL recovery; durable finalization across node
shutdown is not guaranteed. Owners still join these operations before releasing
the resources they use.

## Migration coverage

The service audit covers consensus (state, reactor, ticker, gossip and WAL),
autofile groups, node and seed owners, event bus and pubsub, indexer, block and
state sync, mempool, evidence, P2P router/PEX/connections, quorum event handling,
ABCI clients/servers, proxy wrappers, signer services, HTTP/WebSocket RPC servers,
and the light RPC wrapper. Worker-free implementations retain their hooks
without artificial workers.

Service-scoped cancel functions, completion channels and custom `Wait` methods
are replaced by the shared mechanism. Connection/request groups, peer-session
cancellation, restartable RPC broadcast admission and worker-pool synchronization
remain where they represent a separate lifetime.

This does not claim a repository-wide join for every plain goroutine. RPC
websocket client reconnection/session helpers are outside `BaseService`; generic
consumer middleware can hide context-canceled, in-memory limiter housekeeping.
Those boundaries need their own ownership API before a service can promise to
join every helper. Router connection/request groups and AutoFile workers are
joined explicitly rather than converted into unrelated service lifetimes.

RPC shutdown closes admission and joins active handlers, including hijacked
WebSocket sessions and their subscription workers. Node shutdown then drains
async broadcasts and reactors, cancels and joins every mempool recheck batch,
and finally releases application connections and stores.

Arbitrary injected ABCI clients cannot be recreated by the routed wrapper. The
node reserves RPC listeners before starting children, so retry after listener
failure remains supported without restarting successful child lifetimes. After
a later node startup failure, construct a fresh node graph: rollback can also
permanently close non-service resources such as mempool recheck admission.

## Cancellation compatibility audit

Cancellation precedes `OnStop` for every `BaseService` implementation. The
in-tree hook/callback audit covers these groups:

| Implementations | Cancellation and cleanup policy |
| --- | --- |
| Consensus State, reactor, ticker, WAL; autofile Group | Managed work is joined; commit uses the State parent context; file ownership ends with explicit Close. |
| Blocksync reactor and Synchronizer | Handoff preserves application work across Synchronizer.Stop; stopping its parent reactor may cancel it. The reactor joins the Synchronizer in OnStop, and again in OnDrain for one started by an admitted handoff. |
| P2P Router and MConnection | Connection callbacks retain the parent context and are joined by `Wait`. Router removes peer bookkeeping even with a canceled context; final PeerStatusDown broadcasts are best effort. |
| PEX, mempool, statesync and evidence reactors | Processing uses the work context; stop hooks close resources/dispatchers or require no extra signal. |
| EventBus, pubsub Server and indexer Service | Event processing is cancellation-driven; the indexer closes its sinks in OnDrain. No guarantee of a live cleanup callback context. |
| ABCI local, socket, gRPC and routed clients; proxy; socket/gRPC servers | Stop hooks close transports, drain requests or stop children without requiring a live work context. |
| SignerServer, signer listener/dialer endpoints | Hooks close endpoints/listeners; dial/retry work observes cancellation. |
| ValidatorConnExecutor and light RPC Client | Unsubscribe uses a separate bounded cleanup context. The light client scopes WebSocket subscriptions to its service and the caller session, and joins (or stops) the wrapped client in OnDrain. |
| Node and seed node | Parent cancellation stops children; the node joins reactors before canceling its dependency context and closing owned stores. |

Subscribers must stop with their owner, without relying on a final router
broadcast. Avoid detaching broadcasts from cancellation: unresponsive
subscribers could otherwise block shutdown.

Group rotation reuses its AutoFile and closes the current descriptor before
renaming; it does not create a worker or signal registration per rotation. The
owner must Close the group, including when it never started, to join the head's
single timer/signal worker. Context cancellation alone does not release the head.

## CLI shutdown deadline

The `start` command cancels the node on SIGINT or SIGTERM and waits for
`Node.Wait` before returning. `--shutdown-timeout` controls this wait (default
30 seconds; must be positive). Set the deployment supervisor's termination grace
period longer than this timeout to allow Tenderdash to report incomplete cleanup.
If the deadline expires, the command returns an error and the Tenderdash binary
exits nonzero. This does not skip reactor joins or close stores while workers
remain active. An embedded command caller must also terminate on this error;
cleanup may still be running. The deadline bounds shutdown after startup returns,
not a blocked startup hook.
