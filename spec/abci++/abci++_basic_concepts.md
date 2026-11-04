---
order: 1
title: Overview and basic concepts
---

## Outline
1. [Outline](#outline)
2. [ABCI++ vs. ABCI](#abci-vs-abci)
3. [Methods overview](#methods-overview)
   1. [Consensus/block execution methods](#consensusblock-execution-methods)
   2. [Mempool methods](#mempool-methods)
   3. [Info methods](#info-methods)
   4. [State-sync methods](#state-sync-methods)
   5. [Other methods](#other-methods)
4. [Next-block execution vs. same-block execution](#next-block-execution-vs-same-block-execution)
   1. [Tenderdash timeouts in same-block execution](#tenderdash-timeouts-in-same-block-execution)
5. [Determinism](#determinism)
6. [Errors](#errors)
   1. [`CheckTx`](#checktx)
   2. [`ExecTxResult`](#exectxresult-prepareproposal-and-processproposal)
   3. [`Query`](#query)
7. [Events](#events)
8. [Evidence](#evidence)

# Overview and basic concepts

## ABCI++ vs. ABCI
[&uparrow; Back to Outline](#outline)

With ABCI, the application can only act at one phase in consensus, immediately after a block has been finalized. This restriction on the application prevents numerous features for the application, including many scalability improvements that are now better understood than when ABCI was first written. For example, many of the scalability proposals can be boiled down to "Make the miner / block proposers / validators do work, so the network does not have to". This includes optimizations such as tx-level signature aggregation, state transition proofs, etc. Furthermore, many new security properties cannot be achieved in the current paradigm, as the application cannot enforce validators to do more than just finalize txs. This includes features such as threshold cryptography, and guaranteed IBC connection attempts.

ABCI++ overcomes these limitations by allowing the application to intervene at three key places of the block execution. The new interface allows block proposers to perform application-dependent work in a block through the `PrepareProposal` method; validators to perform application-dependent work in a proposed block through the `ProcessProposal` method; and applications to require their validators do more than just validate blocks, e.g., validator guaranteed IBC connection attempts, through the `ExtendVote` and `VerifyVoteExtension` methods. Furthermore, ABCI++ renames {`BeginBlock`, [`DeliverTx`], `EndBlock`} to `FinalizeBlock`, as a simplified way to deliver a decided block to the Application.

## Methods overview
[&uparrow; Back to Outline](#outline)

Methods can be classified into four categories: consensus, mempool, info, and state-sync.

### Consensus/block execution methods

The first time a new blockchain is started, Tenderdash calls
`InitChain`. From then on, method `FinalizeBlock` is executed at the end of each
block, resulting in an updated Application state.
During consensus execution of a block height, before method `FinalizeBlock` is
called, methods `PrepareProposal`, `ProcessProposal`, `ExtendVote`, and
`VerifyVoteExtension` may be called several times.
See [Tenderdash's expected behavior](abci++_tmint_expected_behavior.md)
for details on the possible call sequences of these methods.

* [**InitChain:**](./abci++_methods.md#initchain) This method initializes the blockchain. Tenderdash calls it once upon genesis.

* [**PrepareProposal:**](./abci++_methods.md#prepareproposal) It allows the block proposer to perform application-dependent work in a block before using it as its proposal. This enables, for instance, batch optimizations to a block, which has been empirically demonstrated to be a key component for scaling. Method `PrepareProposal` is called every time Tenderdash is about to send
a proposal message, but no previous proposal has been locked at Tenderdash level.
Tenderdash gathers outstanding transactions from the mempool, generates a block header, and uses
them to create a block to propose. Then, it calls `RequestPrepareProposal`
with the newly created proposal, called _raw proposal_. The Application can
make changes to the raw proposal, such as modifying transactions, and returns
the (potentially) modified proposal, called _prepared proposal_ in the
`Response*` call. The logic modifying the raw proposal can be non-deterministic.

* [**ProcessProposal:**](./abci++_methods.md#processproposal) It allows a validator to perform application-dependent work in a proposed block. This enables features such as allowing validators to reject a block according to whether the state machine deems it valid, and changing the block execution pipeline. Tenderdash calls it when it receives a proposal and it is not locked on a block. The Application cannot
modify the proposal at this point but can reject it if it realizes it is invalid.
If that is the case, Tenderdash will prevote `nil` on the proposal, which has
strong liveness implications for Tenderdash. As a general rule, the Application
SHOULD accept a prepared proposal passed via `ProcessProposal`, even if a part of
the proposal is invalid (e.g., an invalid transaction); the Application can
ignore the invalid part of the prepared proposal at block execution time.

* [**ExtendVote:**](./abci++_methods.md#extendvote) It allows applications to force their validators to do more than just validate within consensus. `ExtendVote` allows applications to include deterministic and non-deterministic data, opaque to Tenderdash, to precommit messages (the final round of voting.
The data, called _vote extensions_, will also be made available to the
application in the next height, along with the vote it is extending, in the rounds
where the local process is the proposer.
ExtendVote call should return the `ExtendVoteExtension` list, where each element is an object with two attributes: a type and
bytes array.
If the Application does not have vote extensions to provide, it returns an empty array as its vote extensions.
Tenderdash calls `ExtendVote` when is about to send a non-`nil` precommit message.
* [**VerifyVoteExtension:**](./abci++_methods.md#verifyvoteextension) It allows validators to validate the vote extensions data attached to a precommit message. If the validation fails, the precommit message will be deemed invalid and ignored
by Tenderdash. This has a negative impact on Tenderdash's liveness, i.e., if vote extensions repeatedly cannot be verified by correct validators, Tenderdash may not be able to finalize a block even if sufficiently many (+2/3) of the validators send precommit votes for that block. Thus, `VerifyVoteExtension` should be used with special care.
As a general rule, an Application that detects an invalid vote extension SHOULD
accept it in `ResponseVerifyVoteExtension` and ignore it in its own logic. Tenderdash calls it when
a process receives a precommit message with a (possibly empty) vote extensions.
Tenderdash also calls it, with an empty `validator_pro_tx_hash`, to verify a commit's
threshold-recovered extensions before `FinalizeBlock`; that check must reject a vector the
Application did not expect.

* [**FinalizeBlock:**](./abci++_methods.md#finalizeblock) It delivers a decided block to the Application to persist the candidate for the block and round processed last. The current block's application and result hashes were returned by `PrepareProposal`/`ProcessProposal` and are already in its header. `ResponseFinalizeBlock` returns only retention and next-block scheduling hints, not execution results or events. See [Commit and apply](../consensus/consensus-happy-path.md#9-commit-and-apply).

### Mempool methods

* [**CheckTx:**](./abci++_methods.md#checktx) This method allows the Application to validate transactions against its current state, e.g., checking signatures and account balances. If a transaction passes the validation, then Tenderdash adds it to its local mempool, discarding it otherwise. Tenderdash calls it when it receives a new transaction either coming from an external user or another node. Furthermore, Tenderdash can be configured to re-call `CheckTx` on any decided transaction (after `FinalizeBlock`).

### Info methods

* [**Info:**](./abci++_methods.md#info) Used to sync Tenderdash with the Application during a handshake that happens on startup.

* [**Query:**](./abci++_methods.md#query) Clients can use this method to query the Application for information about the application state.

### State-sync methods

State sync allows new nodes to rapidly bootstrap by discovering, fetching, and applying
state machine snapshots instead of replaying historical blocks. For more details, see the
[state sync section](../p2p/messages/state-sync.md).

New nodes will discover and request snapshots from other nodes in the P2P network.
A Tenderdash node that receives a request for snapshots from a peer will call
`ListSnapshots` on its Application. The Application returns the list of locally avaiable snapshots.
Note that the list does not contain the actual snapshot but metadata about it: height at which the snapshot was taken, application-specific verification data and more (see [snapshot data type](./abci++_methods.md#snapshot) for more details). After receiving a list of available snapshots from a peer, the new node can offer any of the snapshots in the list to its local Application via the `OfferSnapshot` method. The Application can check at this point the validity of the snapshot metadata.

Snapshots may be quite large and are thus broken into smaller "chunks" that can be
assembled into the whole snapshot. Once the Application accepts a snapshot and
begins restoring it, Tenderdash will fetch snapshot "chunks" from existing nodes.
The node providing "chunks" will fetch them from its local Application using
the `LoadSnapshotChunk` method.

As the new node receives "chunks" it will apply them sequentially to the local
application with `ApplySnapshotChunk`. When all chunks have been applied, the
Application's `AppHash` is retrieved via an `Info` query.
To ensure that the sync proceeded correctly, Tenderdash compares the local Application's `AppHash` to the `AppHash` stored on the blockchain (verified via
[light client verification](../light-client/verification/README.md)).

In summary:

* [**ListSnapshots:**](./abci++_methods.md#listsnapshots) Used by nodes to discover available snapshots on peers.

* [**LoadSnapshotChunk:**](./abci++_methods.md#loadsnapshotchunk) Used by Tenderdash to retrieve snapshot chunks from the application to send to peers.

* [**OfferSnapshot:**](./abci++_methods.md#offersnapshot) When a node receives a snapshot from a peer, Tenderdash uses this method to offer the snapshot to the Application.

* [**ApplySnapshotChunk:**](./abci++_methods.md#applysnapshotchunk) Used by Tenderdash to hand snapshot chunks to the Application.

### Other methods

Additionally, there is a [**Flush**](./abci++_methods.md#flush) method that is called on every connection,
and an [**Echo**](./abci++_methods.md#echo) method that is just for debugging.

More details on managing state across connections can be found in the section on
[ABCI Applications](../abci/apps.md).

## Next-block execution vs. same-block execution

[&uparrow; Back to Outline](#outline)

Tenderdash uses same-block execution: `PrepareProposal` and `ProcessProposal`
return the application hash and transaction results for the block being proposed.
Those commitments are included in that block's header. Validator-set and
parameter changes from height `h` take effect at `h + 1`.

The legacy next-block execution model is historical background, not an alternate
live mode of this implementation. The authoritative lifecycle is the
[primary consensus flow](../consensus/consensus-happy-path.md) and
[validator/parameter transition](../consensus/consensus-alternative-flows.md#validator-set-and-parameter-changes).

### Tenderdash timeouts in same-block execution

Proposal preparation and processing are synchronous and consume the round's
proposal-time budget. Slow execution can cause other validators to prevote nil.
Timeouts are already consensus parameters in `ConsensusParams.Timeout`; updates
from a block apply at the next height. Local unsafe overrides can desynchronize
nodes. See [Timeouts and consensus parameters](../consensus/consensus.md#timeouts-and-consensus-parameters)
for the actual fields, defaults and scheduling rules.

## Determinism

[&uparrow; Back to Outline](#outline)

Application processing must produce deterministic consensus commitments for a
block, round and last committed state. `PrepareProposal` constructs a candidate;
`ProcessProposal` verifies its current-block application/results hashes and
next-state commitments. Candidate changes must not replace committed application
state until `FinalizeBlock` persists the decided candidate. There is no subsequent
ABCI `Commit` call.

Processing can revisit the same block and round after another execution context
has replaced it. Commit verification can also call `ExtendVote` without signing
to discover an expected vector after rejection. These calls must not be treated as
once-per-height callbacks; see
[expected call sequences](./abci++_tmint_expected_behavior.md#valid-method-call-sequences).
Extension generation and verification must not mutate committed application state.

Sources of non-determinism in applications may include:

* Hardware failures
    * Cosmic rays, overheating, etc.
* Node-dependent state
    * Random numbers
    * Time
* Underspecification
    * Library version changes
    * Race conditions
    * Floating point numbers
    * JSON or protobuf serialization
    * Iterating through hash-tables/maps/dictionaries
* External Sources
    * Filesystem
    * Network calls (eg. some external REST API service)

See [#56](https://github.com/tendermint/abci/issues/56) for original discussion.

Consensus commits only the consensus-relevant subset of transaction results:
`Code`, `Data` and `GasUsed` form `ResultsHash`. Events and diagnostic fields are
not part of that hash. See [Header](../core/data_structures.md#header) and
[Events](#events); `ResponseFinalizeBlock` has no transaction results or events.

## Errors

[&uparrow; Back to Outline](#outline)

`Query` and `CheckTx` responses and `ExecTxResult` entries carry application
`Code`/`Codespace` values. Code zero means successful application processing;
a nonzero transaction code is not the same as rejecting a whole proposal.

`ProcessProposal.status` controls proposal acceptance, while
`VerifyVoteExtension.status` controls vote/commit-vector acceptance. The
[nil-vote versus panic distinction](../consensus/consensus-transitions.md#prevote-decision-prevoterdo)
and [commit rejection paths](./abci++_methods.md#verifyvoteextension) are part of
the consensus contract. ABCI transport errors and malformed responses must not be
assumed to act like a nonzero transaction code.

### `CheckTx`

When Tenderdash receives a `ResponseCheckTx` with a non-zero `Code`, the associated
transaction will not be added to Tenderdash's mempool or it will be removed if
it is already included.

<!-- markdownlint-disable-next-line MD033 -->
<a id="txresult-as-part-of-finalizeblock"></a>

### `ExecTxResult` (PrepareProposal and ProcessProposal)

`ResponsePrepareProposal.tx_results` and `ResponseProcessProposal.tx_results`
contain one result for each included transaction. The consensus-relevant result
fields contribute to the current block's `ResultsHash`, which is checked against
the Application's answer before voting. `ResponseFinalizeBlock` has no
`tx_results` field. See [ExecTxResult](./abci++_methods.md#exectxresult).

### `Query`

When Tenderdash receives a `ResponseQuery` with a non-zero `Code`, this code is
returned directly to the client that initiated the query.

## Events

[&uparrow; Back to Outline](#outline)

`ResponseCheckTx` can contain events. For block execution,
`ResponseProcessProposal.events` contains block events and each `ExecTxResult`
can contain transaction events. These results are retained for publication when
the block is finalized; `ResponseFinalizeBlock` has no events field.
Applications may respond to these ABCI++ methods with a set of events.
Events allow applications to associate metadata about ABCI++ method execution with the
transactions and blocks this metadata relates to.
Events returned via these ABCI++ methods do not impact Tenderdash consensus in any way
and instead exist to power subscriptions and queries of Tenderdash state.

An `Event` contains a `type` and a list of `EventAttributes`, which are key-value
string pairs denoting metadata about what happened during the method's (or transaction's)
execution. `Event` values can be used to index transactions and blocks according to what
happened during their execution.

Each event has a `type` which is meant to categorize the event for a particular
`Response*` or `Tx`. A `Response*` or `Tx` may contain multiple events with duplicate
`type` values, where each distinct entry is meant to categorize attributes for a
particular event. Every key and value in an event's attributes must be UTF-8
encoded strings along with the event type itself.

```protobuf
message Event {
  string                  type       = 1;
  repeated EventAttribute attributes = 2;
}
```

The attributes of an `Event` consist of a `key`, a `value`, and an `index` flag. The
index flag notifies the Tenderdash indexer to index the attribute. The value of
the `index` flag is non-deterministic and may vary across different nodes in the network.

```protobuf
message EventAttribute {
  bytes key   = 1;
  bytes value = 2;
  bool  index = 3;  // nondeterministic
}
```

Example:

```go
 abci.ResponseCheckTx{
  // ...
 Events: []abci.Event{
  {
   Type: "validator.provisions",
   Attributes: []abci.EventAttribute{
    abci.EventAttribute{Key: []byte("address"), Value: []byte("..."), Index: true},
    abci.EventAttribute{Key: []byte("amount"), Value: []byte("..."), Index: true},
    abci.EventAttribute{Key: []byte("balance"), Value: []byte("..."), Index: true},
   },
  },
  {
   Type: "validator.provisions",
   Attributes: []abci.EventAttribute{
    abci.EventAttribute{Key: []byte("address"), Value: []byte("..."), Index: true},
    abci.EventAttribute{Key: []byte("amount"), Value: []byte("..."), Index: false},
    abci.EventAttribute{Key: []byte("balance"), Value: []byte("..."), Index: false},
   },
  },
  {
   Type: "validator.slashed",
   Attributes: []abci.EventAttribute{
    abci.EventAttribute{Key: []byte("address"), Value: []byte("..."), Index: false},
    abci.EventAttribute{Key: []byte("amount"), Value: []byte("..."), Index: true},
    abci.EventAttribute{Key: []byte("reason"), Value: []byte("..."), Index: true},
   },
  },
  // ...
 },
}
```

## Evidence

[&uparrow; Back to Outline](#outline)

Tenderdash's security model relies on the use of "evidence". Evidence is proof of
malicious behavior by a network participant. It is the responsibility of Tenderdash
to detect such malicious behavior. When malicious behavior is detected, Tenderdash
will gossip evidence of the behavior to other nodes and commit the evidence to
the chain once it is verified by all validators. This evidence will then be
passed on to the Application through ABCI++. It is the responsibility of the
Application to handle the evidence and exercise punishment.

EvidenceType has the following protobuf format:

```protobuf
enum EvidenceType {
  UNKNOWN               = 0;
  DUPLICATE_VOTE        = 1;
  LIGHT_CLIENT_ATTACK   = 2;
}
```

There are two forms of evidence: Duplicate Vote and Light Client Attack. More
information can be found in either [data structures](../core/data_structures.md)
or [accountability](../light-client/accountability/)

