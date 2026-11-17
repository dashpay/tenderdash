# Tenderdash

Tenderdash is the Byzantine Fault Tolerant consensus engine used by Dash Platform.
It replicates application state across validators and connects to the application
through the Application Blockchain Interface (ABCI).

- [Introduction](introduction/README.md) and [installation](introduction/install.md)
- [Operating a node](nodes/README.md)
- [Application development](app-dev/readme.md)
- [Core components](tenderdash-core/README.md)
- [Protocol specifications](../spec/README.md)
- [Consensus protocol](../spec/consensus/consensus.md): primary flow, transitions,
  alternative flows, commit handling, recovery, and known discrepancies
- [RPC API](../api/rpc/index.html) (available in the generated site)
- [Building and contributing to these docs](DOCS_README.md)

The protocol specification describes the implementation on this branch and links
to the functions that define each rule. Historical research and design documents
remain available alongside it.
