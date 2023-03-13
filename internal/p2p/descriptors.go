package p2p

import (
	"github.com/tendermint/tendermint/config"
	p2pproto "github.com/tendermint/tendermint/proto/tendermint/p2p"
	"github.com/tendermint/tendermint/types"
)

const (
	ErrorChannel = ChannelID(0x10)
	// BlockSyncChannel is a channelStore for blocks and status updates
	BlockSyncChannel = ChannelID(0x40)

	blockMaxMsgSize = 1048576 // 1MB TODO make it configurable
)

// ChannelDescriptors returns a map of all supported descriptors
func ChannelDescriptors(cfg *config.Config) map[ChannelID]*ChannelDescriptor {
	return map[ChannelID]*ChannelDescriptor{
		ErrorChannel: {
			ID:                  ErrorChannel,
			Priority:            6,
			RecvMessageCapacity: blockMaxMsgSize,
			RecvBufferCapacity:  32,
			Name:                "error",
		},
		BlockSyncChannel: {
			ID:                 BlockSyncChannel,
			Priority:           5,
			SendQueueCapacity:  1000,
			RecvBufferCapacity: 1024,
			RecvMessageCapacity: types.MaxBlockSizeBytes +
				p2pproto.BlockResponseMessagePrefixSize +
				p2pproto.BlockResponseMessageFieldKeySize,
			Name: "blockSync",
		},
	}
}
