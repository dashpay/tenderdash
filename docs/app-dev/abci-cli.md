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
abci-cli --address tcp://127.0.0.1:26658 echo hello
abci-cli --address tcp://127.0.0.1:26658 info
abci-cli --address tcp://127.0.0.1:26658 check_tx 0x6b65793d76616c7565
```

The CLI's default address is `tcp://0.0.0.0:26658`; these examples explicitly
select loopback. The default transport is `socket`.
`CheckTx` validates a transaction; it does not persist application state.

For gRPC, start a server with `--abci grpc` and use a bare `HOST:PORT` client
address. The server accepts a `tcp://` address, but the gRPC client does not
resolve that form correctly and waits for a connection:

```sh
abci-cli kvstore --abci grpc --address tcp://127.0.0.1:26659
```

In another terminal:

```sh
abci-cli --abci grpc --address 127.0.0.1:26659 echo hello
```

Transaction and query arguments accept `0x`-prefixed hexadecimal bytes or a
string with literal double quotes. For example, these requests encode the
same bytes (`key=value`):

```sh
abci-cli --address tcp://127.0.0.1:26658 check_tx 0x6b65793d76616c7565
abci-cli --address tcp://127.0.0.1:26658 check_tx '"key=value"'
```

## Proposal and finalization requests

The implemented request commands include `echo`, `info`, `query`, `check_tx`,
`prepare_proposal`, `process_proposal` and `finalize_block`. Their block-related
argument forms are:

```text
prepare_proposal HEIGHT TX [TX ...]
process_proposal HEIGHT [TX ...]
finalize_block HEIGHT APP_HASH_HEX [TX ...]
```

The height is decimal. Each `TX` uses the byte encoding described above.
`APP_HASH_HEX` is the application hash as hexadecimal without a `0x` prefix;
it must match the state prepared and processed for that block. These commands
send simplified test requests and do not fill every field required by an
arbitrary application's consensus lifecycle. In particular, they cannot set the
round, timestamp, proposer, quorum or commit metadata. `prepare_proposal` fixes
`max_tx_bytes` at 65536, and `process_proposal` fixes the application protocol
version to the example application's version. Consult the
[command implementation](https://github.com/dashpay/tenderdash/blob/master/abci/cmd/abci-cli/abci-cli.go)
when constructing application-specific tests. The standalone `prepare_proposal`
command requires a transaction; an empty proposal can be sent through `batch`.

The CLI does not expose request commands for `InitChain`, `ExtendVote`,
`VerifyVoteExtension` or the four snapshot methods. There is no `flush` command;
`Flush` is available through the underlying client interface.

Responses are abbreviated: `info` prints only `data`, `check_tx` prints only
`code` and `data`, and proposal commands omit transaction results, parameter and
validator updates, and other response fields. `--verbose` does not restore those
fields. CLI output therefore cannot validate the full application protocol.

There is no ABCI `Commit` method or implemented `commit` command. The application
persists state during `FinalizeBlock`. For a complete working node/application
example, use the [external Go application guide](../tutorials/go.md).
The canonical [ABCI call sequence](../../spec/consensus/consensus-happy-path.md#abci-calls-per-height-on-the-happy-path)
defines the lifecycle a real node drives.
Some installed help and completion text still mentions `commit`; that text is
stale.

## Reuse a connection

`console` accepts interactive request commands; `batch` reads them from standard
input. Both reuse one connection. For example:

```sh
abci-cli --address tcp://127.0.0.1:26658 --verbose batch <<'EOF_COMMANDS'
echo hello
info
check_tx 0x6b65793d76616c7565
EOF_COMMANDS
```

The console and batch parser splits input on spaces without shell quoting.
Encode transaction or query bytes containing spaces as hexadecimal in these
modes. Query flags such as `--height`, `--path` and `--prove` apply to standalone
`query` commands; flags inside console or batch input are not applied.

Use an ABCI implementation compatible with your Tenderdash release. See the
[current application interface](https://github.com/dashpay/tenderdash/blob/master/abci/types/application.go)
and [protobuf definitions](https://github.com/dashpay/tenderdash/blob/master/proto/tendermint/abci/types.proto).
