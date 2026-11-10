package blocksync

import (
	p2pproto "github.com/dashpay/tenderdash/proto/tendermint/p2p"
	"github.com/dashpay/tenderdash/types"
)

const (
	MaxMsgSize = types.MaxBlockSizeBytes +
		p2pproto.BlockResponseMessagePrefixSize +
		p2pproto.BlockResponseMessageFieldKeySize
)
