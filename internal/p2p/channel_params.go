package p2p

import (
	"fmt"

	"github.com/cosmos/gogoproto/proto"

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
	//
	// Consensus channels
	//
	ConsensusStateChannel = ChannelID(0x20)
	ConsensusDataChannel  = ChannelID(0x21)
	ConsensusVoteChannel  = ChannelID(0x22)
	VoteSetBitsChannel    = ChannelID(0x23)

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
	// consensusMsgSize is the maximum size of a consensus message
	maxMsgSize = 1048576 // 1MB; NOTE: keep in sync with types.PartSet sizes.

)

// ChannelDescriptors returns a map of all supported descriptors
func ChannelDescriptors(cfg *config.Config) map[ChannelID]*ChannelDescriptor {
	channels := map[ChannelID]*ChannelDescriptor{
		ErrorChannel: {
			ID:                  ErrorChannel,
			Priority:            7,
			RecvMessageCapacity: blockMaxMsgSize,
			RecvBufferCapacity:  32,
			Name:                "error",
		},
		BlockSyncChannel: {
			ID:                 BlockSyncChannel,
			Priority:           6,
			SendQueueCapacity:  1000,
			RecvBufferCapacity: 1024,
			RecvMessageCapacity: types.MaxBlockSizeBytes +
				p2pproto.BlockResponseMessagePrefixSize +
				p2pproto.BlockResponseMessageFieldKeySize,
			Name: "blockSync",
		},
		MempoolChannel: {
			ID:                  MempoolChannel,
			Priority:            2, // 5
			RecvMessageCapacity: mempoolBatchSize(cfg.Mempool.MaxTxBytes),
			RecvBufferCapacity:  1000,
			Name:                "mempool",
			EnqueueTimeout:      cfg.Mempool.TxEnqueueTimeout,
		},
	}

	for k, v := range StatesyncChannelDescriptors() {
		channels[k] = v
	}
	for k, v := range ConsensusChannelDescriptors() {
		channels[k] = v
	}

	return channels
}

// ChannelDescriptors returns a map of all supported descriptors
func StatesyncChannelDescriptors() map[ChannelID]*ChannelDescriptor {
	return map[ChannelID]*ChannelDescriptor{
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
			Priority:            4,
			SendQueueCapacity:   4,
			RecvMessageCapacity: chunkMsgSize,
			RecvBufferCapacity:  128,
			Name:                "chunk",
		},
		LightBlockChannel: {
			ID:                  LightBlockChannel,
			Priority:            6,
			SendQueueCapacity:   10,
			RecvMessageCapacity: lightBlockMsgSize,
			RecvBufferCapacity:  128,
			Name:                "light-block",
		},
		ParamsChannel: {
			ID:                  ParamsChannel,
			Priority:            3,
			SendQueueCapacity:   10,
			RecvMessageCapacity: paramMsgSize,
			RecvBufferCapacity:  128,
			Name:                "params",
		},
	}
}

// GetChannelDescriptor produces an instance of a descriptor for this
// package's required channels.
func ConsensusChannelDescriptors() map[ChannelID]*ChannelDescriptor {
	return map[ChannelID]*ChannelDescriptor{
		ConsensusStateChannel: {
			ID:                  ConsensusStateChannel,
			Priority:            18,
			SendQueueCapacity:   64,
			RecvMessageCapacity: maxMsgSize,
			RecvBufferCapacity:  128,
			Name:                "state",
		},
		ConsensusDataChannel: {
			// TODO: Consider a split between gossiping current block and catchup
			// stuff. Once we gossip the whole block there is nothing left to send
			// until next height or round.
			ID:                  ConsensusDataChannel,
			Priority:            22,
			SendQueueCapacity:   64,
			RecvBufferCapacity:  512,
			RecvMessageCapacity: maxMsgSize,
			Name:                "data",
		},
		ConsensusVoteChannel: {
			ID:                  ConsensusVoteChannel,
			Priority:            20,
			SendQueueCapacity:   64,
			RecvBufferCapacity:  4096,
			RecvMessageCapacity: maxMsgSize,
			Name:                "vote",
		},
		VoteSetBitsChannel: {
			ID:                  VoteSetBitsChannel,
			Priority:            15,
			SendQueueCapacity:   8,
			RecvBufferCapacity:  128,
			RecvMessageCapacity: maxMsgSize,
			Name:                "voteSet",
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
	// State sync
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
	// Consensus messages
	case *consensus.VoteSetBits:
		return VoteSetBitsChannel
	case *consensus.Vote, *consensus.Commit:
		return ConsensusVoteChannel
	case *consensus.ProposalPOL,
		*consensus.Proposal,
		*consensus.BlockPart:
		return ConsensusDataChannel
	case *consensus.NewRoundStep, *consensus.NewValidBlock,
		*consensus.HasCommit,
		*consensus.HasVote,
		*consensus.VoteSetMaj23:
		return ConsensusStateChannel
	// pex
	case *p2pproto.PexRequest,
		*p2pproto.PexResponse,
		*p2pproto.Echo:
	// evidence
	case *prototypes.Evidence:
	// mempool
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
