# Tenderdash

![banner](docs/tendermint-core-image.jpg)

[Byzantine-Fault Tolerant](https://en.wikipedia.org/wiki/Byzantine_fault_tolerance)
[State Machine Replication](https://en.wikipedia.org/wiki/State_machine_replication).
Or [Blockchain](<https://en.wikipedia.org/wiki/Blockchain_(database)>), for short.

[![version](https://img.shields.io/github/tag/dashpay/tenderdash.svg)](https://github.com/dashpay/tenderdash/releases/latest)
[![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/dashpay/tenderdash)](https://github.com/moovweb/gvm)
[![dashpay/tenderdash](https://tokei.rs/b1/github/dashpay/tenderdash?category=lines)](https://github.com/dashpay/tenderdash)

| Branch                                   | Tests                                                                                                                                                      | Coverage                                                                                                       | Linting                                                                                                                                                          |
| ---------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Development (latest `vMAJOR.MINOR-dev`) | [![Tests](https://github.com/dashpay/tenderdash/actions/workflows/tests.yml/badge.svg)](https://github.com/dashpay/tenderdash/actions/workflows/tests.yml) | [![codecov](https://codecov.io/gh/dashpay/tenderdash/graph/badge.svg)](https://codecov.io/gh/dashpay/tenderdash) | [![Golang Linter](https://github.com/dashpay/tenderdash/actions/workflows/lint.yml/badge.svg)](https://github.com/dashpay/tenderdash/actions/workflows/lint.yml) |

Tenderdash is a Byzantine Fault Tolerant (BFT) middleware that takes a state transition machine -
written in any programming language - and securely replicates it on many machines.

## Background

Tenderdash started as a fork of the [Tendermint Core](https://www.github.com/tendermint/tendermint)
project and has been used in public environments such as the Cosmos Network. Although based on
Tendermint, Tenderdash differs from Tendermint through its use of Dash's [long-living masternode
quorums (LLMQs)](https://github.com/dashpay/dips/blob/master/dip-0006.md) to support threshold
signatures, quorum-based voting, and dynamic validator set rotation. These enhancements to support
rapid transaction finality and maintain strong security guarantees make Tenderdash ideal for Dash
Platform’s needs.

For Tendermint protocol details, refer to the [Tendermint Specification](./spec/README.md). For a
detailed analysis of the consensus protocol, including safety and liveness proofs, read the
Tendermint paper, "[The latest gossip on BFT consensus](https://arxiv.org/abs/1807.04938)".
Tendermint documentation can be found on [docs.tendermint.com](https://docs.tendermint.com/).

## Releases

Please do not depend on the development branch (latest `vMAJOR.MINOR-dev`) as
your production branch. Use the binaries provided on the [GitHub releases
page](https://github.com/dashpay/tenderdash/releases) instead.

## Install

See the [install instructions](./docs/introduction/install.md). Make sure to meet the minimum
requirements if installing from source.

### Minimum requirements

| Requirement | Notes              |
| ----------- | ------------------ |
| Go version  | Go1.25.7 or higher |

## Versioning

### Semantic Versioning

Tenderdash uses [Semantic Versioning](http://semver.org/) to determine when and how the version
changes.

The Tenderdash API includes all publicly exposed types, functions, and methods in non-internal Go
packages as well as the types and methods accessible via the RPC interface. Breaking changes to
these public APIs will be documented in the [CHANGELOG](./CHANGELOG.md).

### Supported Versions

Because we are a small core team, we only ship patch updates, including security updates, to the
most recent minor release and the second-most recent minor release. Consequently, we strongly
recommend keeping Tenderdash up-to-date.

## Resources

- [The latest gossip on BFT consensus](https://arxiv.org/abs/1807.04938)
- [Master's Thesis on Tendermint](https://atrium.lib.uoguelph.ca/xmlui/handle/10214/9769)
- [Original Whitepaper: "Tendermint: Consensus Without Mining"](https://tendermint.com/static/docs/tendermint.pdf)

## Contributing

Before contributing to the project, please take a look at the [contributing
guidelines](CONTRIBUTING.md) and the [style guide](STYLE_GUIDE.md). You may also find it helpful to
read the [Tendermint specifications](./spec/README.md), and familiarize yourself with the
[Architectural Decision Records (ADRs)](./docs/architecture/) and [Request For Comments
(RFCs)](./docs/rfc/).

## Join us

Tenderdash is maintained by [Dash Core Group](https://www.dash.org/dcg/). If you'd like to work
full-time on Tenderdash, [see our Jobs page](https://www.dash.org/dcg/jobs/).
