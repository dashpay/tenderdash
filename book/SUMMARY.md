# Summary

[Welcome](README.md)

- [Tenderdash](docs/README.md)

# Introduction

- [Overview](docs/introduction/README.md)
- [Tenderdash Architectural Overview](docs/introduction/architecture.md)
- [Install Tenderdash](docs/introduction/install.md)
- [Quick Start](docs/introduction/quick-start.md)
- [What is Tenderdash](docs/introduction/what-is-tenderdash.md)

# Operating nodes

- [Overview](docs/nodes/README.md)
- [Configuration](docs/nodes/configuration.md)
- [Configure a Light Client](docs/nodes/light-client.md)
- [Output level for logging, including package level options](docs/nodes/logging.md)
- [Metrics](docs/nodes/metrics.md)
- [Remote signer](docs/nodes/remote-signer.md)
- [Running in production](docs/nodes/running-in-production.md)
- [Configure State-Sync](docs/nodes/state-sync.md)
- [Validators](docs/nodes/validators.md)

# Application development

- [Using ABCI-CLI](docs/app-dev/abci-cli.md)
- [Application Architecture Guide](docs/app-dev/app-architecture.md)
- [Getting Started with Application Development](docs/app-dev/getting-started.md)
- [Indexing Transactions](docs/app-dev/indexing-transactions.md)
- [Application development](docs/app-dev/readme.md)

# Tutorials

- [Creating a built-in application in Go](docs/tutorials/go-built-in.md)
- [Creating an external application in Go](docs/tutorials/go.md)
- [Tutorials](docs/tutorials/readme.md)

# Core components

- [Overview](docs/tenderdash-core/README.md)
- [Block Structure](docs/tenderdash-core/block-structure.md)
- [Block Sync](docs/tenderdash-core/block-sync/README.md)
- [Implementation](docs/tenderdash-core/block-sync/implementation.md)
- [Reactor](docs/tenderdash-core/block-sync/reactor.md)
- [Configuration](docs/tenderdash-core/configuration.md)
- [Consensus](docs/tenderdash-core/consensus/README.md)
- [Evidence](docs/tenderdash-core/evidence/README.md)
- [How to read logs](docs/tenderdash-core/how-to-read-logs.md)
- [Light Client](docs/tenderdash-core/light-client.md)
- [Mempool](docs/tenderdash-core/mempool/README.md)
- [Configuration](docs/tenderdash-core/mempool/config.md)
- [Metrics](docs/tenderdash-core/metrics.md)
- [Peer Strategy and Exchange](docs/tenderdash-core/pex/README.md)
- [RPC](docs/tenderdash-core/rpc.md)
- [Running In Production](docs/tenderdash-core/running-in-production.md)
- [State sync](docs/tenderdash-core/state-sync/README.md)
- [Subscribing to Events](docs/tenderdash-core/subscription.md)
- [Using Tenderdash](docs/tenderdash-core/using-tenderdash.md)
- [Validators](docs/tenderdash-core/validators.md)

# Tools and debugging

- [Overview](docs/tools/README.md)
- [Debugging](docs/tools/debugging/README.md)
- [Debug Like A Pro](docs/tools/debugging/pro.md)
- [Proposer-Based Timestamps Runbook](docs/tools/debugging/proposer-based-timestamps-runbook.md)
- [Docker Compose](docs/tools/docker-compose.md)
- [Terraform & Ansible](docs/tools/terraform-and-ansible.md)

# Protocol specifications

- [Tenderdash Specifications](spec/README.md)
- [Tenderdash Consensus Protocol](spec/consensus/consensus.md)
    - [Primary flow of a height](spec/consensus/consensus-happy-path.md)
    - [Transitions and decision rules](spec/consensus/consensus-transitions.md)
    - [Alternative flows and edge cases](spec/consensus/consensus-alternative-flows.md)
    - [Commit handling](spec/consensus/consensus-commit.md)
    - [Recovery and synchronisation](spec/consensus/consensus-recovery.md)
    - [Known issues and discrepancies](spec/consensus/consensus-known-issues.md)
    - [Full consensus parameter commitment (draft)](spec/consensus/consensus-params-commitment.md)

# ABCI++

- [ABCI++](spec/abci++/README.md)
- [Application Requirements](spec/abci++/abci++_app_requirements.md)
- [Overview and basic concepts](spec/abci++/abci++_basic_concepts.md)
- [Client and Server](spec/abci++/abci++_client_server.md)
- [Methods](spec/abci++/abci++_methods.md)
- [Tenderdash's expected behavior](spec/abci++/abci++_tmint_expected_behavior.md)
- [Protocol Documentation](spec/abci++/api.md)

# Core data structures and encoding

- [Data Structures](spec/core/data_structures.md)
- [Encoding](spec/core/encoding.md)
- [Genesis](spec/core/genesis.md)
- [Core protocol](spec/core/readme.md)
- [State](spec/core/state.md)

# Consensus reference and research

- [BFT Time](spec/consensus/bft-time.md)
- [Consensus Paper](spec/consensus/consensus-paper/README.md)
- [Evidence](spec/consensus/evidence.md)
- [Tenderdash Light Client Protocol](spec/consensus/light-client/README.md)
- [Fork accountability](spec/consensus/light-client/accountability.md)
- [Detection](spec/consensus/light-client/detection.md)
- [Core Verification](spec/consensus/light-client/verification.md)
- [Proposer-Based Timestamps (PBTS)](spec/consensus/proposer-based-timestamp/README.md)
- [PBTS: Protocol Specification](spec/consensus/proposer-based-timestamp/pbts-algorithm_002_draft.md)
- [PBTS: System Model and Properties](spec/consensus/proposer-based-timestamp/pbts-sysmodel_002_draft.md)
- [PBTS: Protocol Specification (first draft)](spec/consensus/proposer-based-timestamp/v1/pbts-algorithm_001_draft.md)
- [PBTS: System Model and Properties (first draft)](spec/consensus/proposer-based-timestamp/v1/pbts-sysmodel_001_draft.md)
- [Proposer-Based Time (first draft)](spec/consensus/proposer-based-timestamp/v1/pbts_001_draft.md)
- [Proposer Selection Procedure](spec/consensus/proposer-selection.md)
- [Consensus](spec/consensus/readme.md)
- [Validator Signing](spec/consensus/signing.md)
- [WAL](spec/consensus/wal.md)

# Peer-to-peer protocol

- [P2P Config](spec/p2p/config.md)
- [P2P Multiplex Connection](spec/p2p/connection.md)
- [Messages](spec/p2p/messages/README.md)
- [Block Sync](spec/p2p/messages/block-sync.md)
- [Consensus](spec/p2p/messages/consensus.md)
- [Evidence](spec/p2p/messages/evidence.md)
- [Mempool](spec/p2p/messages/mempool.md)
- [Peer Exchange](spec/p2p/messages/pex.md)
- [State Sync](spec/p2p/messages/state-sync.md)
- [Peer Discovery](spec/p2p/node.md)
- [Peers](spec/p2p/peer.md)
- [P2P protocol](spec/p2p/readme.md)

# RPC protocol

- [RPC spec](spec/rpc/README.md)

# Historical ABCI reference

- [ABCI](spec/abci/README.md)
- [Methods and Types](spec/abci/abci.md)
- [Applications](spec/abci/apps.md)
- [Client and Server](spec/abci/client-server.md)

# Light-client research

- [Light Client Specification](spec/light-client/README.md)
- [Fork accountability](spec/light-client/accountability/README.md)
- [Synopsis](spec/light-client/accountability/Synopsis.md)
- [Results of 001indinv-apalache](spec/light-client/accountability/results/001indinv-apalache-report.md)
- [Lightclient Attackers Isolation](spec/light-client/attacks/isolate-attackers_001_draft.md)
- [Lightclient Attackers Isolation](spec/light-client/attacks/isolate-attackers_002_reviewed.md)
- [Light client attacks](spec/light-client/attacks/notes-on-evidence-handling.md)
- [Tendermint fork detection and IBC fork detection](spec/light-client/detection/README.md)
- [***This an unfinished draft. Comments are welcome!***](spec/light-client/detection/detection_001_reviewed.md)
- [Light Client Attack Detector](spec/light-client/detection/detection_003_reviewed.md)
- [Results of Discussions and Decisions](spec/light-client/detection/discussions.md)
- [Draft of Functions for Fork detection and Proof of Fork Submisstion](spec/light-client/detection/draft-functions.md)
- [Requirements for Fork Detection in the IBC Context](spec/light-client/detection/req-ibc-detection.md)
- [Draft of Light Client Supervisor for discussion](spec/light-client/supervisor/supervisor_001_draft.md)
- [Draft of Light Client Supervisor for discussion](spec/light-client/supervisor/supervisor_002_draft.md)
- [Core Verification](spec/light-client/verification/README.md)
- [Light Client Verification](spec/light-client/verification/verification_001_published.md)
- [Light Client Verification](spec/light-client/verification/verification_002_draft.md)
- [Light Client Verificaiton](spec/light-client/verification/verification_003_draft.md)

# Formal proofs

- [Ivy Proofs](spec/ivy-proofs/README.md)

# Security

- [Security](docs/security/README.md)
- [Vulnerability names](docs/security/vulnerability-names.md)

# Architecture decisions

- [ADR 073: Adopt LibP2P](docs/architecture/adr-073-libp2p.md)
- [ADR 74: Migrate Timeout Parameters to Consensus Parameters](docs/architecture/adr-074-timeout-params.md)
- [ADR 075: RPC Event Subscription Interface](docs/architecture/adr-075-rpc-subscription.md)
- [ADR 076: Combine Spec and Tendermint Repositories](docs/architecture/adr-076-combine-spec-repo.md)
- [ADR 077: Configurable Block Retention](docs/architecture/adr-077-block-retention.md)
- [ADR 078: Non-Zero Genesis](docs/architecture/adr-078-nonzero-genesis.md)
- [ADR 079: Ed25519 Verification](docs/architecture/adr-079-ed25519-verification.md)
- [ADR 080: ReverseSync - fetching historical data](docs/architecture/adr-080-reverse-sync.md)
- [ADR 081: Protocol Buffers Management](docs/architecture/adr-081-protobuf-mgmt.md)
- [ADR D001: Inter validator set messaging](docs/architecture/adr-d001-inter-validator-set-messaging.md)
- [Service lifecycle core](docs/architecture/service-lifecycle.md)

# Requests for comments

- [RFC 006: Event Subscription](docs/rfc/rfc-006-event-subscription.md)
- [RFC 007 : Deterministic Proto Byte Serialization](docs/rfc/rfc-007-deterministic-proto-bytes.md)
- [RFC 008: Don't Panic](docs/rfc/rfc-008-do-not-panic.md)
- [RFC 009 : Consensus Parameter Upgrade Considerations](docs/rfc/rfc-009-consensus-parameter-upgrades.md)
- [RFC 011: Remove Gas From Tenderdash](docs/rfc/rfc-011-delete-gas.md)
- [RFC 012: Event Indexing Revisited](docs/rfc/rfc-012-custom-indexing.md)
- [RFC 013: ABCI++](docs/rfc/rfc-013-abci++.md)
- [RFC 014: Semantic Versioning](docs/rfc/rfc-014-semantic-versioning.md)
- [RFC 015: ABCI++ TX Mutation](docs/rfc/rfc-015-abci++-tx-mutation.md)
- [RFC 016: Node Architecture](docs/rfc/rfc-016-node-architecture.md)
- [RFC 017: ABCI++ Vote Extension Propagation](docs/rfc/rfc-017-abci++-vote-extension-propag.md)
- [RFC 018: BLS Signature Aggregation Exploration](docs/rfc/rfc-018-bls-agg-exploration.md)
- [RFC 019: Configuration File Versioning](docs/rfc/rfc-019-config-version.md)
- [RFC 021: The Future of the Socket Protocol](docs/rfc/rfc-021-socket-protocol.md)

# Roadmap

- [Roadmap](docs/roadmap/README.md)
- [Tenderdash Development](docs/roadmap/roadmap.md)

# DoS observability

- [Consensus DoS — observability](docs/dos-observability/README.md)

# Engineering notes

- [Consensus DoS Defense — Design Spec (v2)](docs/CONSENSUS_DOS_DEFENSE_SPEC.md)
- [Consensus DoS — devnet test runbook](docs/CONSENSUS_DOS_DEVNET_RUNBOOK.md)
- [Consensus DoS — Per-Message Matrix](docs/CONSENSUS_DOS_MESSAGE_MATRIX.md)
- [Per-Message DoS Findings (research synthesis, for review)](docs/CONSENSUS_DOS_PER_MESSAGE_FINDINGS.md)
- [Consensus DoS — Piece 10 spec: bound the evidence channel](docs/CONSENSUS_DOS_PIECE10_SPEC.md)
- [Consensus DoS — Piece 2 spec: verification fairness](docs/CONSENSUS_DOS_PIECE2_SPEC.md)
- [Consensus DoS — Piece 3b spec: honest connectivity](docs/CONSENSUS_DOS_PIECE3B_SPEC.md)
- [Consensus DoS — Plan](docs/CONSENSUS_DOS_PLAN.md)
- [Consensus DoS — rollout spec (devnet → testnet)](docs/CONSENSUS_DOS_ROLLOUT_SPEC.md)
- [Consensus vote-flood DoS — hardening spec](docs/CONSENSUS_VOTE_FLOOD_SPEC.md)
- [Devnet: custom Tenderdash DoS-hardened image + config-knob plumbing](docs/DEVNET_DOS_IMAGE_AND_TUNING.md)
- [Building the documentation](docs/DOCS_README.md)
- [Masternode vote gating — restrict consensus votes to evonodes](docs/MASTERNODE_VOTE_GATING_SPEC.md)
