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

A chain with consensus parameter `evidence.max_bytes` set to `0` disables the
Tenderdash evidence subsystem. Nodes use the persisted chain state on restart,
falling back to genesis for a new chain. They do not open the evidence database,
run the evidence reactor, or register or advertise its P2P channel. Conflicting
votes are not buffered, and `broadcast_evidence` returns an evidence-disabled
error for an otherwise valid submission. Proposals contain no evidence, and the
existing consensus byte-limit validation rejects blocks containing evidence.
Existing evidence databases are left untouched.

The evidence mode is fixed by genesis. `InitChain`, `PrepareProposal`, and
`ProcessProposal` consensus parameter updates cannot change `evidence.max_bytes`
between zero and positive. Updates between positive limits remain supported.
State sync also rejects a snapshot whose evidence mode differs from the locally
trusted chain state, before applying snapshot chunks or bootstrapping that state.
There is no local configuration override.

This transition restriction is a consensus behavior change. Coordinate upgrades
across validators; a chain that needs to switch evidence mode requires a separate
coordinated protocol change. An application starting a new chain must preserve
the evidence mode specified in genesis in its `InitChain` response.
