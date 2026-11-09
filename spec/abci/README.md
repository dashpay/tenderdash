---
order: 1
parent:
  title: ABCI
  order: 3
---

# ABCI

## Introduction

ABCI (**A**pplication **B**lock**c**hain **I**nterface) connects Tenderdash's
consensus engine to the Application's state machine.
The API consists of a set of _methods_, each with a corresponding `Request` and `Response`
message type.

The methods are always initiated by Tenderdash. The Application implements its logic
for handling all ABCI methods.
Thus, Tenderdash always sends the `Request*` messages and receives the `Response*` messages
in return.

All ABCI messages and methods are defined in [protocol buffers](../../proto/tendermint/abci/types.proto).
This allows Tenderdash to run with applications written in many programming languages.

This specification is split as follows:

- [Overview and basic concepts](basic-concepts.md) - interface's overview and concepts
  needed to understand other parts of this specification.
- [Methods](methods.md) - complete details on all ABCI methods
  and message types.
- [Requirements for the Application](app-requirements.md) - formal requirements
  on the Application's logic to ensure Tenderdash properties such as liveness. These requirements define what
  Tenderdash expects from the Application; second part on managing ABCI application state and related topics.
- [Tenderdash's expected behavior](expected-behavior.md) - specification of
  how the different ABCI methods may be called by Tenderdash. This explains what the Application
  is to expect from Tenderdash.
- [Client and Server](client-server.md) - for those looking to implement their
  own ABCI application servers
