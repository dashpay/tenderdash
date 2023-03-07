package blocksync

import (
	p2pproto "github.com/tendermint/tendermint/proto/tendermint/p2p"
	"github.com/tendermint/tendermint/types"
)

const (
	MaxMsgSize = types.MaxBlockSizeBytes +
		p2pproto.BlockResponseMessagePrefixSize +
		p2pproto.BlockResponseMessageFieldKeySize
)
