---
order: 2
---

# Using Tenderdash

> Serialized key types such as `tendermint/PubKeyBLS12381` are protocol
> identifiers. Keep those names unchanged when configuring Tenderdash.

This is a guide to using the `tenderdash` program from the command line.
It assumes only that you have the `tenderdash` binary installed and have
some rudimentary idea of what Tenderdash and ABCI are.

You can see the help menu with `tenderdash --help`, and the version
number with `tenderdash version`.

## Directory Root

The default directory for blockchain data is `~/.tenderdash`. Override
this by setting the `TMHOME` environment variable.

## Initialize

Initialize the root directory by running:

```sh
tenderdash init validator
```

This will create a new private key (`priv_validator_key.json`), and a
genesis file (`genesis.json`) containing the associated public key, in
`$TMHOME/config`. This is all that's necessary to run a local testnet
with one validator.

For more elaborate initialization, see the testnet command:

```sh
tenderdash testnet --help
```

### Genesis

`genesis.json` in the node's `config` directory defines the initial chain ID,
height, time, consensus parameters, application state and validator quorum.
Use a unique chain ID of at most 50 bytes, and use the same genesis on every
node in the network.

For a local development chain, `tenderdash init validator` writes a complete
genesis and matching signing files. Do not construct a production quorum by
copying keys from an example. The current
[GenesisDoc definition](../../types/genesis.go)
describes the supported fields, including validator pro-tx-hashes, quorum hash,
quorum type and threshold public key.

The application may initialize the validator set and consensus parameters via
`InitChain`. See the canonical [startup handshake](../../spec/consensus/consensus-recovery.md#start-up-abci-handshake)
and [validator-set rules](../../spec/consensus/consensus-alternative-flows.md#validator-set-and-parameter-changes).

## Run

To run a Tenderdash node, use:

```bash
tenderdash start
```

By default, Tenderdash will try to connect to an ABCI application on
`127.0.0.1:26658`. If you have the `kvstore` ABCI app installed, run it in
another window. If you don't, kill Tenderdash and run an in-process version of
the `kvstore` app:

```bash
tenderdash start --proxy-app=kvstore
```

After a few seconds, you should see blocks start streaming in. Note that blocks
are produced regularly, even if there are no transactions. See _No Empty
Blocks_, below, to modify this setting.

Tenderdash supports in-process versions of the `counter`, `kvstore`, and `noop`
apps that ship as examples with `abci-cli`. It's easy to compile your app
in-process with Tenderdash if it's written in Go. If your app is not written in
Go, run it in another process, and use the `--proxy-app` flag to specify the
address of the socket it is listening on, for instance:

```bash
tenderdash start --proxy-app=/var/run/abci.sock
```

You can find out what flags are supported by running `tenderdash start --help`.

## Transactions

To send a transaction, use `curl` to make requests to the Tenderdash RPC
server, for example:

```sh
curl http://localhost:26657/broadcast_tx_commit?tx=\"abcd\"
```

We can see the chain's status at the `/status` end-point:

```sh
curl http://localhost:26657/status | json_pp
```

and the `latest_app_hash` in particular:

```sh
curl http://localhost:26657/status | json_pp | grep latest_app_hash
```

Visit `http://localhost:26657` in your browser to see the list of other
endpoints. Some take no arguments (like `/status`), while others specify
the argument name and use `_` as a placeholder.


> TIP: See the [RPC API reference](../../api/rpc/index.html).

### Formatting

When sending transactions to the RPC interface, the following formatting rules
must be followed:

Using `GET` (with parameters in the URL):

To send a UTF8 string as transaction data, enclose the value of the `tx`
parameter in double quotes:

```sh
curl 'http://localhost:26657/broadcast_tx_commit?tx="hello"'
```

which sends a 5-byte transaction: "h e l l o" \[68 65 6c 6c 6f\].

Note that the URL in this example is enclosed in single quotes to prevent the
shell from interpreting the double quotes. Alternatively, you may escape the
double quotes with backslashes:

```sh
curl http://localhost:26657/broadcast_tx_commit?tx=\"hello\"
```

The double-quoted format works with for multibyte characters, as long as they
are valid UTF8, for example:

```sh
curl 'http://localhost:26657/broadcast_tx_commit?tx="€5"'
```

sends a 4-byte transaction: "€5" (UTF8) \[e2 82 ac 35\].

Arbitrary (non-UTF8) transaction data may also be encoded as a string of
hexadecimal digits (2 digits per byte). To do this, omit the quotation marks
and prefix the hex string with `0x`:

```sh
curl http://localhost:26657/broadcast_tx_commit?tx=0x68656C6C6F
```

which sends the 5-byte transaction: \[68 65 6c 6c 6f\].

Using `POST` (with parameters in JSON), the transaction data are sent as a JSON
string in base64 encoding:

```sh
curl http://localhost:26657 -H 'Content-Type: application/json' --data-binary '{
  "jsonrpc": "2.0",
  "id": "anything",
  "method": "broadcast_tx_commit",
  "params": {
    "tx": "aGVsbG8="
  }
}'
```

which sends the same 5-byte transaction: \[68 65 6c 6c 6f\].

Note that the hexadecimal encoding of transaction data is _not_ supported in
JSON (`POST`) requests.

## Reset

> :warning: **UNSAFE** Only do this in development and only if you can
afford to lose all blockchain data!


To reset a blockchain, stop the node and run:

```sh
tenderdash unsafe-reset-all
```

This command will remove the data directory and reset private validator and
address book files.

## Configuration

Tenderdash uses a `config.toml` for configuration. For details, see [the
config specification](./configuration.md).

Notable options include the socket address of the application
(`proxy-app`), the listening address of the Tenderdash peer
(`p2p.laddr`), and the listening address of the RPC server
(`rpc.laddr`).

Some fields from the config file can be overwritten with flags.

## No Empty Blocks

Configure `create-empty-blocks` and `create-empty-blocks-interval` in
`config.toml` to control waiting for transactions at round 0 (and, on consensus
version 2, in a round entered after a silent proposer's round was skipped). The application
can request an immediate next block through `ResponseFinalizeBlock`.
An app hash change does not itself require an extra block: Tenderdash uses
same-block execution.

See the [configuration guide](../nodes/configuration.md#empty-blocks) and the
canonical [empty-block rules](../../spec/consensus/consensus-alternative-flows.md#mempool-and-empty-blocks).

## Broadcast API

Earlier, we used the `broadcast_tx_commit` endpoint to send a
transaction. When a transaction is sent to a Tenderdash node, it will
run via `CheckTx` against the application. If it passes `CheckTx`, it
can be admitted to the mempool and gossiped to peers. Admission does not
guarantee later inclusion in a block.

Since there are multiple phases to processing a transaction, we offer
multiple endpoints to broadcast a transaction:

```md
/broadcast_tx_async
/broadcast_tx_sync
/broadcast_tx_commit
```

These correspond to no-processing, processing through the mempool, and
processing through a block, respectively. That is, `broadcast_tx_async`,
will return right away without waiting to hear if the transaction is
even valid, while `broadcast_tx_sync` will return with the result of
running the transaction through `CheckTx`. Using `broadcast_tx_commit`
will wait until the transaction is committed in a block or until some
timeout is reached, but will return right away if the transaction does
not pass `CheckTx`. The return value for `broadcast_tx_commit` includes
`check_tx` for mempool validation and `tx_result` for the committed
transaction execution result.

The benefit of using `broadcast_tx_commit` is that the request returns
after the transaction is committed (i.e. included in a block). For a quick result, use
`broadcast_tx_sync`, but the transaction will not be committed until
later, and by that point its effect on the state may change.

Note the mempool does not provide strong guarantees - just because a tx passed
CheckTx (ie. was accepted into the mempool), doesn't mean it will be committed,
as nodes with the tx in their mempool may crash before they get to propose.
For more information, see the [mempool guide](./mempool/README.md).

## Tenderdash Networks

All nodes joining a network must use its agreed genesis and compatible software.
For a local development chain, initialize files with `tenderdash init validator`;
for a deployed network, obtain its genesis and validator/quorum configuration
through the network's deployment process.

`priv_validator_key.json` contains secret signing material. Protect both keys and
signing state; never copy a live consensus identity to a second active signer.
See [validator keys](../nodes/validators.md#validator-keys) and
[recovery](../../spec/consensus/consensus-recovery.md).

A validator signs only when it belongs to the active quorum. A non-validator
full node follows committed blocks without voting. See the
[validator guide](../nodes/validators.md) for membership updates and
[proposer selection](../../spec/consensus/proposer-selection.md) for the
versioned, equal-power rotation rules.

### Peers

#### Seed

A seed node is a node who relays the addresses of other peers which they know
of. These nodes constantly crawl the network to try to get more peers. The
addresses which the seed node relays get saved into a local address book. Once
these are in the address book, you will connect to those addresses directly.
Basically the seed nodes job is just to relay everyones addresses. You won't
connect to seed nodes once you have received enough addresses, so typically you
only need them on the first start. The seed node will immediately disconnect
from you after sending you some addresses.

#### Persistent Peer

Persistent peers are people you want to be constantly connected with. If you
disconnect you will try to connect directly back to them as opposed to using
another address from the address book. On restarts you will always try to
connect to these peers regardless of the size of your address book.

All peers relay peers they know of by default. This is called the peer exchange
protocol (PeX). With PeX, peers will be gossiping about known peers and forming
a network, storing peer addresses in the addrbook. Because of this, you don't
have to use a seed node if you have a live persistent peer.

#### Connecting to Peers

To connect to peers on start-up, specify them in the
`$TMHOME/config/config.toml` or on the command line. Use `seeds` to
specify seed nodes, and
`persistent-peers` to specify peers that your node will maintain
persistent connections with.

For example,

```sh
tenderdash start --p2p.seeds "f9baeaa15fedf5e1ef7448dd60f46c01f1a9e9c4@1.2.3.4:26656,0491d373a8e0fcf1023aaf18c51d6a1d0d4f31bd@5.6.7.8:26656"
```

Alternatively, you can use the `/dial_seeds` endpoint of the RPC to
specify seeds for a running node to connect to:

```sh
curl 'localhost:26657/dial_seeds?seeds=\["f9baeaa15fedf5e1ef7448dd60f46c01f1a9e9c4@1.2.3.4:26656","0491d373a8e0fcf1023aaf18c51d6a1d0d4f31bd@5.6.7.8:26656"\]'
```

Note, with PeX enabled, you
should not need seeds after the first start.

If you want Tenderdash to connect to specific set of addresses and
maintain a persistent connection with each, you can use the
`--p2p.persistent-peers` flag or the corresponding setting in the
`config.toml` or the `/dial_peers` RPC endpoint to do it without
stopping Tenderdash core instance.

```sh
tenderdash start --p2p.persistent_peers "429fcf25974313b95673f58d77eacdd434402665@10.11.12.13:26656,96663a3dd0d7b9d17d4c8211b191af259621c693@10.11.12.14:26656"

curl 'localhost:26657/dial_peers?persistent=true&peers=\["429fcf25974313b95673f58d77eacdd434402665@10.11.12.13:26656","96663a3dd0d7b9d17d4c8211b191af259621c693@10.11.12.14:26656"\]'
```

### Adding a Non-Validator

Adding a non-validator is simple. Just copy the original `genesis.json`
to `~/.tenderdash/config` on the new machine and start the node,
specifying seeds or persistent peers as necessary. If no seeds or
persistent peers are specified, the node won't make any blocks, because
it's not a validator, and it won't hear about any blocks, because it's
not connected to the other peer.

### Adding a Validator

Editing a local genesis file does not change the validator set of a running
network. Initial validators are established through genesis and `InitChain`;
later changes come from the application and take effect at the next height.
Follow the network's quorum deployment process and the canonical
[validator-set update rules](../../spec/consensus/consensus-alternative-flows.md#validator-set-and-parameter-changes).

### Local Network

To run a network locally, say on a single machine, you must change the `_laddr`
fields in the `config.toml` (or using the flags) so that the listening
addresses of the various sockets don't conflict. Additionally, you must set
`addr_book_strict=false` in the `config.toml`, otherwise Tenderdash's p2p
library will deny making connections to peers with the same IP address.

### Upgrading

See the
[UPGRADING.md](../../UPGRADING.md)
guide. You may need to reset your chain between major breaking releases.
Although, we expect Tenderdash to have fewer breaking releases in the future
(especially after 1.0 release).
