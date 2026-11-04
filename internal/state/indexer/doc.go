/*
Package indexer defines Tenderdash's block and transaction event indexing logic.

The "kv" sink uses an embedded key-value database and supports event queries
through RPC. An ABCI application can emit indexed events during execution as
<abci.Event.Type>.<abci.EventAttributeKey>=<abci.EventAttributeValue>, for example
"transfer.amount=10000".

Enable indexing in the configuration with:

	[tx-index]
	indexer = ["kv"]

An empty indexer list or the "null" sink disables indexing. All configured sink
names must be supported, even when "null" is also present.

When enabled, Service passes block and transaction events to the sink through
IndexBlockEvents and IndexTxEvents.
*/
package indexer
