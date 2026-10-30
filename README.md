# Tenderdash

![banner](docs/tendermint-core-image.jpg)

Tenderdash is the consensus component of [Dash Platform](https://github.com/dashpay/platform).
It orders and finalizes Platform blocks using Dash masternode quorums, threshold
signatures, and validator rotation. It is developed and supported as part of
Dash Platform, rather than as an independent general-purpose consensus product.

[![version](https://img.shields.io/github/tag/dashpay/tenderdash.svg)](https://github.com/dashpay/tenderdash/releases/latest)
[![Tests](https://github.com/dashpay/tenderdash/actions/workflows/tests.yml/badge.svg)](https://github.com/dashpay/tenderdash/actions/workflows/tests.yml)
[![Golang Linter](https://github.com/dashpay/tenderdash/actions/workflows/lint.yml/badge.svg)](https://github.com/dashpay/tenderdash/actions/workflows/lint.yml)

## Dash Platform integration and support

Use the Tenderdash version selected for your Dash Platform release and network.
A newer Tenderdash tag alone does not establish compatibility with an existing
Platform deployment. Changes to consensus rules, wire formats, ABCI, configuration,
or stored data can require a coordinated Platform upgrade.

Support and security fixes follow Dash Platform development and deployment
requirements. Tenderdash does not provide an independent support window for a
fixed number of minor releases, or support for arbitrary downstream applications.
Report security concerns through [SECURITY.md](SECURITY.md).

`dev` is the integration branch and `master` is the latest stable release line.
During the planned branch rename, `v1.8-dev` remains the integration branch until
`dev` exists. Production deployments should use the release artifacts selected
by Dash Platform, not a moving development branch. See [releases](https://github.com/dashpay/tenderdash/releases)
and the [release process](RELEASES.md).

## Background

Tenderdash originated as a fork of [Tendermint Core](https://github.com/tendermint/tendermint).
Its Dash-specific consensus uses [long-living masternode quorums](https://github.com/dashpay/dips/blob/master/dip-0006.md).
The inherited [specifications](spec/README.md) and [architecture documents](docs/architecture/)
provide background; Dash Platform integration and the current implementation
remain the context for development and compatibility decisions.

## Development

See the [installation instructions](docs/introduction/install.md),
[contributing guide](CONTRIBUTING.md), and [style guide](STYLE_GUIDE.md).
Building from source requires Go 1.27.3 or newer and the BLS native dependency;
`make build` handles the dependency build.

Tenderdash uses Semantic Versioning for releases. Compatibility review includes
exported Go APIs, RPC, ABCI, P2P and block protocols, persisted data, and operator
configuration. Release notes describe migration and coordinated rollout needs.
The [project release skill](.agents/skills/tenderdash-release/SKILL.md) reviews
actual changes before choosing a version; commit prefixes alone do not decide it.

Tenderdash is maintained by [Dash Core Group](https://www.dash.org/dcg/).
