---
order: 2
---

# Quick Start

## Overview

This is a quick start guide. If you have a vague idea about how Tenderdash
works and want to get started right away, continue. Make sure you've installed the binary.
Check out [install](./install.md) if you haven't.

## Initialization

Running:

```sh
tenderdash init validator
```

will create the required files for a single, local node.

These files are found in `$HOME/.tenderdash`:

```sh
$ ls $HOME/.tenderdash

config  data

$ ls $HOME/.tenderdash/config/

config.toml  genesis.json  node_key.json  priv_validator.json
```

For a single, local node, no further configuration is required.
Configuring a cluster is covered further below.

## Local Node

Start Tenderdash with a simple in-process application:

```sh
tenderdash start --proxy-app=kvstore
```

> Note: `kvstore` is a non persistent app, if you would like to run an application with persistence run `--proxy-app=persistent_kvstore`

and blocks will start to stream in:

```sh
I[01-06|01:45:15.592] Executed block                               module=state height=1 validTxs=0 invalidTxs=0
I[01-06|01:45:15.624] Committed state                              module=state height=1 txs=0 appHash=
```

Check the status with:

```sh
curl -s localhost:26657/status
```

### Sending Transactions

With the KVstore app running, we can send transactions:

```sh
curl -s 'localhost:26657/broadcast_tx_commit?tx="abcd"'
```

and check that it worked with:

```sh
curl -s 'localhost:26657/abci_query?data="abcd"'
```

We can send transactions with a key and value too:

```sh
curl -s 'localhost:26657/broadcast_tx_commit?tx="name=satoshi"'
```

and query the key:

```sh
curl -s 'localhost:26657/abci_query?data="name"'
```

where the value is returned in hex.

## Cluster of Nodes

Prepare four hosts with [Tenderdash installed](./install.md). Use the same
release and genesis on every node, and restrict administrative RPC access.

Next, use the `tenderdash testnet` command to create four directories of config files (found in `./mytestnet`) and copy each directory to the relevant machine in the cloud, so that each machine has `$HOME/mytestnet/node[0-3]` directory.

Before you can start the network, you'll need peers identifiers (IPs are not enough and can change). We'll refer to them as ID1, ID2, ID3, ID4.

```sh
tenderdash show-node-id --home ./mytestnet/node0
tenderdash show-node-id --home ./mytestnet/node1
tenderdash show-node-id --home ./mytestnet/node2
tenderdash show-node-id --home ./mytestnet/node3
```

Finally, from each machine, run:

```sh
tenderdash start --home ./mytestnet/node0 --proxy-app=kvstore --p2p.persistent-peers="ID1@IP1:26656,ID2@IP2:26656,ID3@IP3:26656,ID4@IP4:26656"
tenderdash start --home ./mytestnet/node1 --proxy-app=kvstore --p2p.persistent-peers="ID1@IP1:26656,ID2@IP2:26656,ID3@IP3:26656,ID4@IP4:26656"
tenderdash start --home ./mytestnet/node2 --proxy-app=kvstore --p2p.persistent-peers="ID1@IP1:26656,ID2@IP2:26656,ID3@IP3:26656,ID4@IP4:26656"
tenderdash start --home ./mytestnet/node3 --proxy-app=kvstore --p2p.persistent-peers="ID1@IP1:26656,ID2@IP2:26656,ID3@IP3:26656,ID4@IP4:26656"
```

Note that after the third node is started, blocks will start to stream in
because >2/3 of validators (defined in the `genesis.json`) have come online.
Persistent peers can also be specified in the `config.toml`. See [here](../tenderdash-core/configuration.md) for more information about configuration options.

Transactions can then be sent as covered in the single, local node example above.
