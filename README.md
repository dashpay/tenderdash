# Tendermint

![banner](docs/tendermint-core-image.jpg)

[Byzantine-Fault Tolerant](https://en.wikipedia.org/wiki/Byzantine_fault_tolerance)
[State Machine Replication](https://en.wikipedia.org/wiki/State_machine_replication).
Or [Blockchain](<https://en.wikipedia.org/wiki/Blockchain_(database)>), for short.

[![version](https://img.shields.io/github/tag/dashpay/tenderdash.svg)](https://github.com/dashpay/tenderdash/releases/latest)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/dashpay/tenderdash)
[![Discord chat](https://img.shields.io/badge/discord-Dev_chat-738adb)](https://chat.dashdevs.org)
[![dashpay/tenderdash](https://tokei.rs/b1/github/dashpay/tenderdash?category=lines)](https://github.com/dashpay/tenderdash)

| Branch | Tests | Coverage | Linting |
|--------|-------|----------|---------|
| master | [![Tests](https://github.com/dashpay/tenderdash/actions/workflows/tests.yml/badge.svg)](https://github.com/dashpay/tenderdash/actions/workflows/tests.yml) | [![codecov](https://codecov.io/gh/dashpay/tenderdash/branch/master/graph/badge.svg)](https://codecov.io/gh/dashpay/tenderdash) | [![Golang Linter](https://github.com/dashpay/tenderdash/actions/workflows/lint.yml/badge.svg)](https://github.com/dashpay/tenderdash/actions/workflows/lint.yml) |

Tendermint Core is a Byzantine Fault Tolerant (BFT) middleware that takes a state transition machine - written in any programming language - and securely replicates it on many machines.

For protocol details, refer to the [Tendermint Specification](./spec/README.md).

For detailed analysis of the consensus protocol, including safety and liveness proofs,
read our paper, "[The latest gossip on BFT consensus](https://arxiv.org/abs/1807.04938)".

## Documentation

Complete documentation can be found on the [website](https://docs.tendermint.com/).

## Releases

Please do not depend on master as your production branch. Use the
[GitHub releases page](https://github.com/dashpay/tenderdash/releases) instead.

More on how releases are conducted can be found [here](./RELEASES.md).

## Minimum requirements

| Requirement | Notes            |
|-------------|------------------|
| Go version  | Go1.22 or higher |

### Install

See the [install instructions](./docs/introduction/install.md).

### Quick Start

- [Single node](./docs/introduction/quick-start.md)
- [Local cluster using docker-compose](./docs/tools/docker-compose.md)
- [Remote cluster using Terraform and Ansible](./docs/tools/terraform-and-ansible.md)

## Contributing

Before contributing to the project, please take a look at the [contributing
guidelines](CONTRIBUTING.md) and the [style guide](STYLE_GUIDE.md). You may also find it helpful to
read the [Tendermint specifications](./spec/README.md), and familiarize yourself with the
[Architectural Decision Records (ADRs)](./docs/architecture/README.md) and [Request For Comments
(RFCs)](./docs/rfc/README.md).

## Versioning

### Semantic Versioning

Tenderdash uses [Semantic Versioning](http://semver.org/) to determine when and how the version
changes.

The Tenderdash API includes all publicly exposed types, functions, and methods in non-internal Go
packages as well as the types and methods accessible via the RPC interface. Breaking changes to
these public APIs will be documented in the [CHANGELOG](./CHANGELOG.md).

### Supported Versions

Because we are a small core team, we only ship patch updates, including security updates,
to the most recent minor release and the second-most recent minor release. Consequently,
we strongly recommend keeping Tendermint up-to-date. Upgrading instructions can be found
in [UPGRADING.md](./UPGRADING.md).

## Resources

### Libraries

- [Cosmos SDK](http://github.com/cosmos/cosmos-sdk); A framework for building applications in Golang
- [Tendermint in Rust](https://github.com/informalsystems/tendermint-rs)
- [ABCI Tower](https://github.com/penumbra-zone/tower-abci)

### Applications

- [Cosmos Hub](https://hub.cosmos.network/)
- [Terra](https://www.terra.money/)
- [Celestia](https://celestia.org/)
- [Anoma](https://anoma.network/)
- [Vocdoni](https://docs.vocdoni.io/)

### Research

- [The latest gossip on BFT consensus](https://arxiv.org/abs/1807.04938)
- [Master's Thesis on Tendermint](https://atrium.lib.uoguelph.ca/xmlui/handle/10214/9769)
- [Original Whitepaper: "Tendermint: Consensus Without Mining"](https://tendermint.com/static/docs/tendermint.pdf)

## Join us

Tenderdash is maintained by [Dash Core Group](https://www.dash.org/dcg/).
If you'd like to work full-time on Tenderdash, [see our Jobs page](https://www.dash.org/dcg/jobs/).

