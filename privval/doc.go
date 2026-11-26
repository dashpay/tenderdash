/*
Package privval implements types.PrivValidator with a file PV and a Dash Core signer.

FilePV uses one file for the private key and another to store signing state.
DashCoreSignerClient delegates signing to Dash Core through its RPC interface.
*/
package privval
