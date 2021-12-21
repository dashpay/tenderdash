# ADR D0002: Same-block execution

## Changelog

- 2021-12-20: Initial version

## Context

Dash Drive ABCI app requires same-block execution support in order to ... 

**TODO: Describe Dash Drive ABCI App requirements in more details**

In Tendermint, this problem will be solved with [an upgrade of ABCI protocol](https://github.com/tendermint/spec/blob/0d81bfbfe3cb8c86dded06e98303685e3de702c5/spec/abci++/abci++_basic_concepts_002_draft.md), called *ABCI++* . However, as this is a big, breaking change, we don't expect it to be ready anytime soon. That's why we need to implement a short-term solution for the problem within Tenderdash.

### 2-Phase Commit (2PC) protocols

Problem statement defined above can be reduced to a "two-phase commit" problem, as described on [Wikipedia](https://en.wikipedia.org/wiki/Two-phase_commit_protocol).

2PC protocols consist of two phases:

* **commit request** - where data is received and stored by the app, but not committed (applied),
* **commit completion** - where data received by the app is committed (applied) or rolled back.

Each participant of the 2PC protocol shall ensure that if the **commit request** succeeds, then **commit completion** will almost always succeed. Any failures shall be detected during the **commit request** phase.

### External references

*  [ABCI++ basic concepts](https://github.com/tendermint/spec/blob/0d81bfbfe3cb8c86dded06e98303685e3de702c5/spec/abci++/abci++_basic_concepts_002_draft.md)
*  [two-phase commit (2PC) protocol](https://en.wikipedia.org/wiki/Two-phase_commit_protocol)

## Alternative Approaches

### Wait for ABCI++ implementation

According to [Tendermint roadmap](https://github.com/tendermint/tendermint/blob/master/docs/roadmap/roadmap.md), ABCI++ implementation shall be done in version 0.36, scheduled for Q1-2022. In this case, implementation of same-block execution inside Dash Platform shall be postponed, and we should build on top od ABCI++.

### Implement subset of ABCI++

In this solution, we will implement a subset of features planned for ABCI++. This approach is based on the 2PC approach, as described in [Context section](#2-phase-commit-2pc-protocols) above.

This approach has the following advantages:

* it's in sync with what upstream project does, thus making it easier to backport future changes

In order to implement this solution, the following APIs need to be added:

* Process Proposal (**commit request** phase of 2PC protocol)
* Finalize Block (**commit completion** phase of 2PC protocol)

APIs to remove:

* BeginBlock - replaced by **Process Proposal**
* DeliverTX - replaced by **Process Proposal**
* EndBlock - replaced by **Finalize Block**

#### Process Proposal

Process Proposal is a commit request in terms of 2PC protocols.

In the **Process Proposal** phase, Tenderdash will deliver list of all transactions. In response the ABCI App should return Merkle tree root (`data` == `AppHash` field).

#### Finalize Block

Finalize Block is a **commit completion** request in therms of 2PC protocols. It supports "commit" and "rollback" mechanism.

### Move BeginBlock/DeliverTX/EndBlock to proposal creation/receipt step

In this case, we will keep BeginBlock/DeliverTX/EndBlock requests. The biggest change will be that these requests will be executed during:

* creation of vote proposal on the proposer
* receipt of a proposal - by all other nodes

Commit request will be sent after the block is finalized.

API changes include:

* BeginBlock shall rollback any uncommitted changes (eg. on new round)
* EndBlock shall return AppHash (current `data` field from `Commit` response)

All nodes will verify AppHash returned by EndBlock before signing a vote.

### Additional voting phase

## Decision


> This section records the decision that was made.
> It is best to record as much info as possible from the discussion that happened. This aids in not having to go back to the Pull Request to get the needed information.

## Detailed Design

> This section does not need to be filled in at the start of the ADR, but must be completed prior to the merging of the implementation.
>
> Here are some common questions that get answered as part of the detailed design:
>
> - What are the user requirements?
>
> - What systems will be affected?
>
> - What new data structures are needed, what data structures will be changed?
>
> - What new APIs will be needed, what APIs will be changed?
>
> - What are the efficiency considerations (time/space)?
>
> - What are the expected access patterns (load/throughput)?
>
> - Are there any logging, monitoring or observability needs?
>
> - Are there any security considerations?
>
> - Are there any privacy considerations?
>
> - How will the changes be tested?
>
> - If the change is large, how will the changes be broken up for ease of review?
>
> - Will these changes require a breaking (major) release?
>
> - Does this change require coordination with the SDK or other?

## Status

> A decision may be "proposed" if it hasn't been agreed upon yet, or "accepted" once it is agreed upon. Once the ADR has been implemented mark the ADR as "implemented". If a later ADR changes or reverses a decision, it may be marked as "deprecated" or "superseded" with a reference to its replacement.

{Deprecated|Proposed|Accepted|Declined}

## Consequences

> This section describes the consequences, after applying the decision. All consequences should be summarized here, not just the "positive" ones.

### Positive

### Negative

### Neutral

## References

> Are there any relevant PR comments, issues that led up to this, or articles referenced for why we made the given design choice? If so link them here!

- {reference link}
