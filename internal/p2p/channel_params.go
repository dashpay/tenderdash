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
	// SnapshotChannel exchanges snapshot metadata
	SnapshotChannel = ChannelID(0x60)
	// ChunkChannel exchanges chunk contents
	ChunkChannel = ChannelID(0x61)
	// LightBlockChannel exchanges light blocks
	LightBlockChannel = ChannelID(0x62)
	// ParamsChannel exchanges consensus params
	ParamsChannel = ChannelID(0x63)

	MempoolChannel = ChannelID(0x30)

	blockMaxMsgSize = 1048576 // 1MB TODO make it configurable

	// snapshotMsgSize is the maximum size of a snapshotResponseMessage
	snapshotMsgSize = int(4e6) // ~4MB
	// chunkMsgSize is the maximum size of a chunkResponseMessage
	chunkMsgSize = int(16e6) // ~16MB
	// lightBlockMsgSize is the maximum size of a lightBlockResponseMessage
	lightBlockMsgSize = int(1e7) // ~1MB
	// paramMsgSize is the maximum size of a paramsResponseMessage
	paramMsgSize = int(1e5) // ~100kb
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
		SnapshotChannel: {
			ID:                  SnapshotChannel,
			Priority:            6,
			SendQueueCapacity:   10,
			RecvMessageCapacity: snapshotMsgSize,
			RecvBufferCapacity:  128,
			Name:                "snapshot",
		},
		ChunkChannel: {
			ID:                  ChunkChannel,
			Priority:            3,
			SendQueueCapacity:   4,
			RecvMessageCapacity: chunkMsgSize,
			RecvBufferCapacity:  128,
			Name:                "chunk",
		},
		LightBlockChannel: {
			ID:                  LightBlockChannel,
			Priority:            5,
			SendQueueCapacity:   10,
			RecvMessageCapacity: lightBlockMsgSize,
			RecvBufferCapacity:  128,
			Name:                "light-block",
		},
		ParamsChannel: {
			ID:                  ParamsChannel,
			Priority:            2,
			SendQueueCapacity:   10,
			RecvMessageCapacity: paramMsgSize,
			RecvBufferCapacity:  128,
			Name:                "params",
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
	case *statesync.ChunkRequest,
		*statesync.ChunkResponse:
		return ChunkChannel
	case *statesync.SnapshotsRequest,
		*statesync.SnapshotsResponse:
		return SnapshotChannel
	case *statesync.ParamsRequest,
		*statesync.ParamsResponse:
		return ParamsChannel
	case *statesync.LightBlockRequest,
		*statesync.LightBlockResponse:
		return LightBlockChannel
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
		*consensus.HasCommit:
		// TODO: enable these channels when they are implemented
		//*statesync.SnapshotsRequest,
		//*statesync.SnapshotsResponse,
		//*statesync.ChunkRequest,
		//*statesync.ChunkResponse,
		//*statesync.LightBlockRequest,
		//*statesync.LightBlockResponse,
		//*statesync.ParamsRequest,
		//*statesync.ParamsResponse:
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
