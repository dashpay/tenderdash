---
order: 13
---

# Light Client

A light client verifies block headers and commits without running a full
application replica. Application state can then be verified through proofs
bound to the verified chain state, provided the application supplies compatible
proofs.

Tenderdash's [light package](https://pkg.go.dev/github.com/dashpay/tenderdash/light)
retrieves light blocks from providers, validates their structure, checks the
validator-set binding and verifies commit signatures. Dash Core verification
uses quorum information to verify the threshold block signature.

The client can cross-check a primary provider with witnesses. Configure
independent providers and the correct Dash Core network; provider availability
and correct quorum verification remain part of the security model. A successful
RPC response without a verified proof does not establish application-state integrity.

For the exact verification path, see the
[client implementation](https://github.com/dashpay/tenderdash/blob/master/light/client.go).
For running the proxy, see [Configure a Light Client](../nodes/light-client.md).
