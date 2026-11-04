---
order: 2
---

# Using ABCI-CLI

`abci-cli` sends requests directly to an ABCI application for development and
debugging. It does not run consensus or construct a verified quorum commit.
Use it against an isolated test application, not an application serving a live
network.

## Install

Build the tool from the same Tenderdash checkout as the application:

```sh
make install_abci
```

Inspect the options for your installed version:

```sh
abci-cli --help
abci-cli kvstore --help
```

## Start an example application

Start the in-memory key-value application in one terminal:

```sh
abci-cli kvstore --address tcp://127.0.0.1:26658
```

In another terminal, send basic requests:

```sh
abci-cli echo hello
abci-cli info
abci-cli check_tx 0x6b65793d76616c7565
```

The default client address is `tcp://127.0.0.1:26658`. Use `--address` and
`--abci` (`socket` or `grpc`) to select another endpoint and transport.
`CheckTx` validates a transaction; it does not persist application state.

Transaction and query arguments accept `0x`-prefixed hexadecimal bytes or a
string with literal double quotes. For example, these requests encode the
same bytes (`key=value`):

```sh
abci-cli check_tx 0x6b65793d76616c7565
abci-cli check_tx '"key=value"'
```

## Proposal and finalization requests

The implemented request commands include `echo`, `info`, `query`, `check_tx`,
`prepare_proposal`, `process_proposal` and `finalize_block`. Their block-related
argument forms are:

```text
prepare_proposal HEIGHT [TX ...]
process_proposal HEIGHT [TX ...]
finalize_block HEIGHT APP_HASH_HEX [TX ...]
```

The height is decimal. Each `TX` uses the byte encoding described above.
`APP_HASH_HEX` is the application hash as hexadecimal without a `0x` prefix;
it must match the state prepared and processed for that block. These commands
send simplified test requests and do not fill every field required by an
arbitrary application's consensus lifecycle. Consult the
[command implementation](https://github.com/dashpay/tenderdash/blob/master/abci/cmd/abci-cli/abci-cli.go)
when constructing application-specific tests.

There is no ABCI `Commit` method or implemented `commit` command. The application
persists state during `FinalizeBlock`. For a complete working node/application
example, use the [external Go application guide](../tutorials/go.md).
The canonical [ABCI call sequence](../../spec/consensus/consensus-happy-path.md#abci-calls-per-height-on-the-happy-path)
defines the lifecycle a real node drives.

## Reuse a connection

`console` accepts interactive request commands; `batch` reads them from standard
input. Both reuse one connection. For example:

```sh
abci-cli --verbose batch <<'EOF_COMMANDS'
echo hello
info
check_tx 0x6b65793d76616c7565
EOF_COMMANDS
```

Use an ABCI implementation compatible with your Tenderdash release. See the
[current application interface](https://github.com/dashpay/tenderdash/blob/master/abci/types/application.go)
and [protobuf definitions](https://github.com/dashpay/tenderdash/blob/master/proto/tendermint/abci/types.proto).
