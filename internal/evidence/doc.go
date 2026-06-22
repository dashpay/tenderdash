/*
Package evidence handles all evidence storage and gossiping from detection to block proposal.
For the different types of evidence refer to the `evidence.go` file in the types package
or https://github.com/tendermint/tendermint/blob/master/spec/consensus/light-client/accountability.md.

# Gossiping

The core functionality begins with the evidence reactor (see reactor.go) which operates both the
sending and receiving of evidence.

The `Receive` function takes a list of evidence and does the following:

1. Checks that it does not already have the evidence stored

2. Verifies the evidence against the node's state (see state/validation.go#VerifyEvidence)

3. Stores the evidence to a db and a concurrent list

Evidence propagation uses two complementary paths:

  - Per-peer sync loop: when a peer connects (PeerStatusUp), a dedicated goroutine walks the pool
    and sends all pending evidence to that peer. The loop repeats every evidenceSyncInterval (default 1 s)
    until the peer disconnects. This ensures evidence that pre-dates the connection is eventually
    delivered even if the first attempt is dropped because the p2p channel route was not yet ready
    when PeerStatusUp fired. The receiver deduplicates silently via isPending / isCommitted guards —
    no ACK is required.

  - Broadcast on new evidence: when a node adds new evidence received from another peer, it
    re-broadcasts it to all currently connected peers via broadcastEvidence.

It uses a concurrent list to store the evidence and before sending verifies that each evidence is still valid in the
sense that it has not exceeded the max evidence age and height (see types/params.go#EvidenceParams).

There are two buckets that evidence can be stored in: Pending & Committed.

1. Pending is awaiting to be committed (evidence is usually broadcasted then)

2. Committed is for those already on the block and is to ensure that evidence isn't submitted twice

All evidence is proto encoded to disk.

# Proposing

When a new block is being proposed (in state/execution.go#CreateProposalBlock),
`PendingEvidence(maxBytes)` is called to send up to the maxBytes of uncommitted evidence, from the evidence store,
prioritized in order of age. All evidence is checked for expiration.

When a node receives evidence in a block it will use the evidence module as a cache first to see if it has
already verified the evidence before trying to verify it again.

Once the proposed evidence is submitted,
the evidence is marked as committed and is moved from the broadcasted set to the committed set.
As a result it is also removed from the concurrent list so that it is no longer gossiped.

# Minor Functionality

As all evidence (including POLC's) are bounded by an expiration date, those that exceed this are no longer needed
and hence pruned. Currently, only committed evidence in which a marker to the height that the evidence was committed
and hence very small is saved. All updates are made from the `Update(block, state)` function which should be called
when a new block is committed.
*/
package evidence
