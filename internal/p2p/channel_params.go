package p2p

import (
	"fmt"

	"github.com/gogo/protobuf/proto"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/proto/tendermint/blocksync"
	"github.com/dashpay/tenderdash/proto/tendermint/consensus"
	"github.com/dashpay/tenderdash/proto/tendermint/mempool"
	p2pproto "github.com/dashpay/tenderdash/proto/tendermint/p2p"
	"github.com/dashpay/tenderdash/proto/tendermint/statesync"
	prototypes "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

const (
	ErrorChannel = ChannelID(0x10)
	// BlockSyncChannel is a channelStore for blocks and status updates
	BlockSyncChannel = ChannelID(0x40)

	MempoolChannel = ChannelID(0x30)

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
		MempoolChannel: {
			ID:                  MempoolChannel,
			Priority:            5,
			RecvMessageCapacity: mempoolBatchSize(cfg.Mempool.MaxTxBytes),
			RecvBufferCapacity:  128,
			Name:                "mempool",
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
		return MempoolChannel
	}
	panic(fmt.Sprintf("unsupported message type %T", msg))
}

func mempoolBatchSize(maxTxBytes int) int {
	largestTx := make([]byte, maxTxBytes)
	batchMsg := p2pproto.Envelope{
		Sum: &p2pproto.Envelope_MempoolTxs{
			MempoolTxs: &mempool.Txs{Txs: [][]byte{largestTx}},
		},
	}
	return batchMsg.Size()
}
