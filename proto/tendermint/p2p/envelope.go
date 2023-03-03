package p2p

import (
	"fmt"

	"github.com/gogo/protobuf/proto"

	"github.com/tendermint/tendermint/proto/tendermint/blocksync"
	"github.com/tendermint/tendermint/proto/tendermint/consensus"
	"github.com/tendermint/tendermint/proto/tendermint/mempool"
	"github.com/tendermint/tendermint/proto/tendermint/statesync"
	"github.com/tendermint/tendermint/proto/tendermint/types"
)

func (m *Envelope) Wrap(pb proto.Message) error {
	switch msg := pb.(type) {
	case *blocksync.BlockRequest,
		*blocksync.BlockResponse,
		*blocksync.NoBlockResponse,
		*blocksync.StatusRequest,
		*blocksync.StatusResponse:
		pbMsg := blocksync.Message{}
		err := pbMsg.Wrap(msg)
		if err != nil {
			return err
		}
		m.Sum = &Envelope_BlocksyncMessage{BlocksyncMessage: &pbMsg}
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
		pbMsg := consensus.Message{}
		err := pbMsg.Wrap(msg)
		if err != nil {
			return err
		}
		m.Sum = &Envelope_ConsensusMessage{ConsensusMessage: &pbMsg}
	case *statesync.SnapshotsRequest,
		*statesync.SnapshotsResponse,
		*statesync.ChunkRequest,
		*statesync.ChunkResponse,
		*statesync.LightBlockRequest,
		*statesync.LightBlockResponse,
		*statesync.ParamsRequest,
		*statesync.ParamsResponse:
		pbMsg := statesync.Message{}
		err := pbMsg.Wrap(msg)
		if err != nil {
			return err
		}
		m.Sum = &Envelope_StatesyncMessage{StatesyncMessage: &pbMsg}
	case *PexRequest, *PexResponse:
		mm := PexMessage{}
		err := mm.Wrap(msg)
		if err != nil {
			return err
		}
		m.Sum = &Envelope_PexMessage{PexMessage: &mm}
	case *types.Evidence:
		m.Sum = &Envelope_Evidence{Evidence: msg}
	case *mempool.Txs:
		m.Sum = &Envelope_MempoolTxs{MempoolTxs: msg}
	case *Echo:
		m.Sum = &Envelope_Echo{Echo: msg}
	default:
		return fmt.Errorf("unknown message: %T", msg)
	}
	return nil
}

func (m *Envelope) Unwrap() (proto.Message, error) {
	switch msg := m.Sum.(type) {
	case *Envelope_BlocksyncMessage:
		return m.GetBlocksyncMessage().Unwrap()
	case *Envelope_ConsensusMessage:
		return m.GetConsensusMessage().Unwrap()
	case *Envelope_StatesyncMessage:
		return m.GetStatesyncMessage().Unwrap()
	case *Envelope_Evidence:
		return m.GetEvidence(), nil
	case *Envelope_MempoolTxs:
		return m.GetMempoolTxs(), nil
	case *Envelope_PexMessage:
		return m.GetPexMessage().Unwrap()
	case *Envelope_Echo:
		return m.GetEcho(), nil
	default:
		return nil, fmt.Errorf("unknown message: %T", msg)
	}
}
