package p2p

import (
	"fmt"

	"github.com/cosmos/gogoproto/proto"

	"github.com/dashpay/tenderdash/proto/tendermint/blocksync"
	"github.com/dashpay/tenderdash/proto/tendermint/consensus"
	"github.com/dashpay/tenderdash/proto/tendermint/mempool"
	"github.com/dashpay/tenderdash/proto/tendermint/statesync"
	"github.com/dashpay/tenderdash/proto/tendermint/types"
)

const (
	BlockResponseMessagePrefixSize   = 4
	BlockResponseMessageFieldKeySize = 1
)

func (m *Envelope) Wrap(pb proto.Message) error {
	switch msg := pb.(type) {
	case *blocksync.BlockRequest:
		m.Sum = &Envelope_BlockRequest{BlockRequest: msg}
	case *blocksync.BlockResponse:
		m.Sum = &Envelope_BlockResponse{BlockResponse: msg}
	case *blocksync.NoBlockResponse:
		m.Sum = &Envelope_NoBlockResponse{NoBlockResponse: msg}
	case *blocksync.StatusRequest:
		m.Sum = &Envelope_StatusRequest{StatusRequest: msg}
	case *blocksync.StatusResponse:
		m.Sum = &Envelope_StatusResponse{StatusResponse: msg}
	case *consensus.NewRoundStep:
		m.Sum = &Envelope_NewRoundStep{NewRoundStep: msg}
	case *consensus.NewValidBlock:
		m.Sum = &Envelope_NewValidBlock{NewValidBlock: msg}
	case *consensus.Proposal:
		m.Sum = &Envelope_Proposal{Proposal: msg}
	case *consensus.ProposalPOL:
		m.Sum = &Envelope_ProposalPol{ProposalPol: msg}
	case *consensus.BlockPart:
		m.Sum = &Envelope_BlockPart{BlockPart: msg}
	case *consensus.Vote:
		m.Sum = &Envelope_Vote{Vote: msg}
	case *consensus.HasVote:
		m.Sum = &Envelope_HasVote{HasVote: msg}
	case *consensus.VoteSetMaj23:
		m.Sum = &Envelope_VoteSetMaj23{VoteSetMaj23: msg}
	case *consensus.VoteSetBits:
		m.Sum = &Envelope_VoteSetBits{VoteSetBits: msg}
	case *consensus.Commit:
		m.Sum = &Envelope_Commit{Commit: msg}
	case *consensus.HasCommit:
		m.Sum = &Envelope_HasCommit{HasCommit: msg}
	case *statesync.SnapshotsRequest:
		m.Sum = &Envelope_SnapshotsRequest{SnapshotsRequest: msg}
	case *statesync.SnapshotsResponse:
		m.Sum = &Envelope_SnapshotsResponse{SnapshotsResponse: msg}
	case *statesync.ChunkRequest:
		m.Sum = &Envelope_ChunkRequest{ChunkRequest: msg}
	case *statesync.ChunkResponse:
		m.Sum = &Envelope_ChunkResponse{ChunkResponse: msg}
	case *statesync.LightBlockRequest:
		m.Sum = &Envelope_LightBlockRequest{LightBlockRequest: msg}
	case *statesync.LightBlockResponse:
		m.Sum = &Envelope_LightBlockResponse{LightBlockResponse: msg}
	case *statesync.ParamsRequest:
		m.Sum = &Envelope_ParamsRequest{ParamsRequest: msg}
	case *statesync.ParamsResponse:
		m.Sum = &Envelope_ParamsResponse{ParamsResponse: msg}
	case *PexRequest:
		m.Sum = &Envelope_PexRequest{PexRequest: msg}
	case *PexResponse:
		m.Sum = &Envelope_PexResponse{PexResponse: msg}
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
	case *Envelope_BlockRequest:
		return msg.BlockRequest, nil
	case *Envelope_BlockResponse:
		return msg.BlockResponse, nil
	case *Envelope_NoBlockResponse:
		return msg.NoBlockResponse, nil
	case *Envelope_StatusRequest:
		return msg.StatusRequest, nil
	case *Envelope_StatusResponse:
		return msg.StatusResponse, nil
	case *Envelope_NewRoundStep:
		return msg.NewRoundStep, nil
	case *Envelope_NewValidBlock:
		return msg.NewValidBlock, nil
	case *Envelope_Proposal:
		return msg.Proposal, nil
	case *Envelope_ProposalPol:
		return msg.ProposalPol, nil
	case *Envelope_BlockPart:
		return msg.BlockPart, nil
	case *Envelope_Vote:
		return msg.Vote, nil
	case *Envelope_HasVote:
		return msg.HasVote, nil
	case *Envelope_VoteSetMaj23:
		return msg.VoteSetMaj23, nil
	case *Envelope_VoteSetBits:
		return msg.VoteSetBits, nil
	case *Envelope_Commit:
		return msg.Commit, nil
	case *Envelope_HasCommit:
		return msg.HasCommit, nil
	case *Envelope_SnapshotsRequest:
		return msg.SnapshotsRequest, nil
	case *Envelope_SnapshotsResponse:
		return msg.SnapshotsResponse, nil
	case *Envelope_ChunkRequest:
		return msg.ChunkRequest, nil
	case *Envelope_ChunkResponse:
		return msg.ChunkResponse, nil
	case *Envelope_LightBlockRequest:
		return msg.LightBlockRequest, nil
	case *Envelope_LightBlockResponse:
		return msg.LightBlockResponse, nil
	case *Envelope_ParamsRequest:
		return msg.ParamsRequest, nil
	case *Envelope_ParamsResponse:
		return msg.ParamsResponse, nil
	case *Envelope_PexRequest:
		return msg.PexRequest, nil
	case *Envelope_PexResponse:
		return msg.PexResponse, nil
	case *Envelope_Evidence:
		return msg.Evidence, nil
	case *Envelope_MempoolTxs:
		return msg.MempoolTxs, nil
	case *Envelope_Echo:
		return msg.Echo, nil
	default:
		return nil, fmt.Errorf("unknown message: %T", msg)
	}
}
