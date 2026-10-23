---
order: 1
parent:
  title: Evidence
  order: 3
---

Evidence is used to identify validators who have or are acting malicious. There are multiple types of evidence, to read more on the evidence types please see [Evidence Types](https://docs.tendermint.com/master/spec/core/data_structures.html#evidence).

The evidence reactor works similar to the mempool reactor. When evidence is observed, it is sent to all the peers in a repetitive manner. This ensures evidence is sent to as many people as possible to avoid sensoring. After evidence is received by peers and committed in a block it is pruned from the evidence module.

Sending incorrectly encoded data or data exceeding `maxMsgSize` will result
in stopping the peer.

## Disabling evidence

Consensus parameter `evidence.max_bytes = 0` disables evidence admission,
conflict collection and gossip. `broadcast_evidence` returns an explicit disabled
error. Proposals contain no evidence, and the consensus byte limit rejects blocks
containing evidence.

Applications can enable or disable evidence on an existing chain through
`PrepareProposal` and `ProcessProposal` consensus parameter updates. An update in
block H takes effect for H+1, only after H commits. A block that disables evidence
can still contain evidence under its current positive limit. `InitChain` may
also override the genesis value for the first block.

Nodes keep the evidence database, reactor and advertised P2P channel available
throughout their lifetime, so activation works with existing peer connections.
While disabled, local admission, conflict collection and periodic gossip skip
verification and database access; peer sync timers perform only a cheap mode
check. Consensus block validation and recording committed evidence remain active,
including evidence in the block that disables admission. Messages whose sends
began before disabling may still arrive; disabled receivers discard them.

Pending evidence is retained across disabled periods and restarts, then pruned
against the current expiration limits before reactivation. Committed-evidence
markers remain available to prevent submitting the same evidence again. Conflicts
observed while disabled are not buffered. Restart uses persisted consensus state;
state sync installs the restored state before block sync begins, without requiring
its evidence mode to match genesis. Existing state-provider verification and trust
requirements are unchanged. There is no local configuration override.
