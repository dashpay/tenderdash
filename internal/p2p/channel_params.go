package p2p

import (
	"fmt"

	"github.com/gogo/protobuf/proto"

	"github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/proto/tendermint/blocksync"
	"github.com/tendermint/tendermint/proto/tendermint/consensus"
	"github.com/tendermint/tendermint/proto/tendermint/mempool"
	p2pproto "github.com/tendermint/tendermint/proto/tendermint/p2p"
	"github.com/tendermint/tendermint/proto/tendermint/statesync"
	prototypes "github.com/tendermint/tendermint/proto/tendermint/types"
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

// ResolveChannelID returns channel ID according to message type
// currently only is supported blocksync channelID, the remaining channelIDs should be added as it will be necessary
func ResolveChannelID(msg proto.Message) ChannelID {
	switch msg.(type) {
	case *blocksync.BlockRequest,
		*blocksync.BlockResponse,
		*blocksync.NoBlockResponse,
		*blocksync.StatusRequest,
		*blocksync.StatusResponse:
		return BlockSyncChannel
	case *consensus.NewRoundStep,
		*consensus.NewValidBlock,
		*consensus.Proposal,
		*consensus.ProposalPOL,
		*consensus.BlockPart,
		*consensus.Vote,
		*consensus.HasVote,
		*consensus.VoteSetMaj23,
		*consensus.VoteSetBits,
		*consensus.Commit,
		*consensus.HasCommit,
		*statesync.SnapshotsRequest,
		*statesync.SnapshotsResponse,
		*statesync.ChunkRequest,
		*statesync.ChunkResponse,
		*statesync.LightBlockRequest,
		*statesync.LightBlockResponse,
		*statesync.ParamsRequest,
		*statesync.ParamsResponse:
	case *p2pproto.PexRequest,
		*p2pproto.PexResponse,
		*p2pproto.Echo:
	case *prototypes.Evidence:
	case *mempool.Txs:
	}
	panic(fmt.Sprintf("unsupported message type %T", msg))
}
