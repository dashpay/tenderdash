---
order: 5
title: Client and Server
---

# Client and Server

This section is for those looking to implement their own ABCI Server, perhaps in
a new programming language.

You are expected to have read all previous sections of ABCI specification, namely
[Basic Concepts](basic-concepts.md),
[Methods](methods.md),
[Application Requirements](app-requirements.md), and
[Expected Behavior](expected-behavior.md).

## Message Protocol and Synchrony

The message protocol consists of pairs of requests and responses defined in the
[protobuf file](../../proto/tendermint/abci/types.proto).

Some messages have no fields, while others may include byte-arrays, strings, integers,
or custom protobuf types.

For more details on protobuf, see the [documentation](https://developers.google.com/protocol-buffers/docs/overview).

Tenderdash creates one application client and delegates the request classes to
it. Ordinary socket and gRPC configurations do not create separate consensus,
mempool, query and state-sync connections automatically. A `routed` client can
use multiple underlying clients when explicitly configured.

Each synchronous call waits for its own response. This does not serialize calls
from independent callers. Execution order and concurrency depend on the
transport and server implementation; see [Connections](app-requirements.md#connections).

## Server Implementations

To use ABCI in your programming language of choice, there must be an ABCI
server in that language. Tenderdash provides three Go implementations:

- In-process
- ABCI-socket
- gRPC

See the [server implementations](../../abci/server) and
[ABCI CLI guide](../../docs/app-dev/abci-cli.md). Out-of-process servers must
implement Tenderdash's protobuf schema and application requirements.

### In Process

The simplest implementation uses function calls in Golang.
This means ABCI applications written in Golang can be linked with Tenderdash and run as a single binary.
The local client delegates directly to application methods without a shared
execution lock, so the application must synchronize concurrent access to its state.

### GRPC

If you are not using Golang,
but [GRPC](https://grpc.io/) is available in your language, this is the easiest approach,
though it will have significant performance overhead.

Please check GRPC's documentation to know to set up the Application as an
ABCI GRPC server.

The client multiplexes calls over one `grpc.ClientConn`; application methods can
execute concurrently. The `abci.grpc-concurrency` configuration limits streams
per method or through the default `*` rule. Its default is `{"*": 0}`, which
imposes no limit. A limit for one method does not serialize it with methods
using a different limit. Protect state shared by concurrent calls.

### Socket

Tenderdash's socket-based ABCI interface is an asynchronous,
raw socket server which provides ordered message passing over unix or tcp.
Messages are serialized using Protobuf3. Each message is prefixed by its byte
length encoded as an unsigned protobuf varint. For example, a four-byte message
`DEADBEEF` is sent as `04DEADBEEF`. The receiver reads the varint length and then
that many message bytes. See the [codec](../../abci/types/messages.go).

This framing does not apply to gRPC. The socket client sends requests and matches
responses in order on its connection. Tenderdash's reference socket server
executes requests sequentially per connection, while separate connections can
execute concurrently. A compatible socket server must preserve response order;
it must not assume a fixed number of role-specific connections.

## Client

There are currently two use-cases for an ABCI client. One is testing
tools that allow ABCI requests to be sent to the actual application via
command line. An example of this is `abci-cli`, which accepts CLI commands
to send corresponding ABCI requests.
The other is a consensus engine, such as Tenderdash,
which makes ABCI requests to the application as prescribed by the consensus
algorithm used.
