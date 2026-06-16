package grpc_test

import (
	"context"
	"testing"
	"time"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dashpay/tenderdash/crypto"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	tmgrpc "github.com/dashpay/tenderdash/privval/grpc"
	privvalproto "github.com/dashpay/tenderdash/proto/tendermint/privval"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

const ChainID = "123"

func TestSignerServerChainIDValidation(t *testing.T) {
	const configuredChainID = "configured-chain"

	hash := tmrand.Bytes(crypto.HashSize)
	proTxHash := crypto.RandProTxHash()
	stateID := types.RandStateID()
	ts := time.Now()

	vote := (&types.Vote{
		Type:   tmproto.PrecommitType,
		Height: 1,
		Round:  2,
		BlockID: types.BlockID{
			Hash:          hash,
			PartSetHeader: types.PartSetHeader{Hash: hash, Total: 2},
			StateID:       stateID.Hash(),
		},
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     1,
	}).ToProto()

	proposal := (&types.Proposal{
		Type:      tmproto.ProposalType,
		Height:    1,
		Round:     2,
		POLRound:  2,
		BlockID:   types.BlockID{Hash: hash, PartSetHeader: types.PartSetHeader{Hash: hash, Total: 2}},
		Timestamp: ts,
	}).ToProto()

	// call invokes the handler under test with the supplied request chain ID and
	// reports the resulting error.
	handlers := map[string]func(ctx context.Context, s *tmgrpc.SignerServer, quorumHash crypto.QuorumHash, chainID string) error{
		"GetPubKey": func(ctx context.Context, s *tmgrpc.SignerServer, quorumHash crypto.QuorumHash, chainID string) error {
			_, err := s.GetPubKey(ctx, &privvalproto.PubKeyRequest{ChainId: chainID, QuorumHash: quorumHash})
			return err
		},
		"GetThresholdPubKey": func(ctx context.Context, s *tmgrpc.SignerServer, quorumHash crypto.QuorumHash, chainID string) error {
			_, err := s.GetThresholdPubKey(ctx, &privvalproto.ThresholdPubKeyRequest{ChainId: chainID, QuorumHash: quorumHash})
			return err
		},
		"GetProTxHash": func(ctx context.Context, s *tmgrpc.SignerServer, _ crypto.QuorumHash, chainID string) error {
			_, err := s.GetProTxHash(ctx, &privvalproto.ProTxHashRequest{ChainId: chainID})
			return err
		},
		"SignVote": func(ctx context.Context, s *tmgrpc.SignerServer, quorumHash crypto.QuorumHash, chainID string) error {
			_, err := s.SignVote(ctx, &privvalproto.SignVoteRequest{
				Vote:       vote,
				ChainId:    chainID,
				QuorumType: int32(btcjson.LLMQType_5_60),
				QuorumHash: quorumHash,
			})
			return err
		},
		"SignProposal": func(ctx context.Context, s *tmgrpc.SignerServer, quorumHash crypto.QuorumHash, chainID string) error {
			_, err := s.SignProposal(ctx, &privvalproto.SignProposalRequest{
				Proposal:   proposal,
				ChainId:    chainID,
				QuorumType: int32(btcjson.LLMQType_5_60),
				QuorumHash: quorumHash,
			})
			return err
		},
	}

	for name, handler := range handlers {
		handler := handler
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			logger := log.NewTestingLogger(t)

			// A spy privVal records whether the signing/key backend was reached,
			// so a rejected request can be proven to never touch the keys.
			spy := &spyPV{MockPV: types.NewMockPV()}
			s := tmgrpc.NewSignerServer(logger, configuredChainID, spy)
			quorumHash, err := spy.GetFirstQuorumHash(ctx)
			require.NoError(t, err)

			t.Run("mismatch rejected", func(t *testing.T) {
				spy.called = false
				err := handler(ctx, s, quorumHash, "other-chain")
				require.Error(t, err)
				assert.Equal(t, codes.InvalidArgument, status.Code(err))
				assert.False(t, spy.called, "privVal must not be invoked on chain ID mismatch")
			})

			t.Run("match accepted", func(t *testing.T) {
				spy.called = false
				err := handler(ctx, s, quorumHash, configuredChainID)
				require.NoError(t, err)
				assert.True(t, spy.called, "privVal must be invoked when chain ID matches")
			})
		})
	}
}

// spyPV records whether any of the privVal backend methods reached by the
// gRPC handlers were invoked.
type spyPV struct {
	*types.MockPV
	called bool
}

func (s *spyPV) GetPubKey(ctx context.Context, quorumHash crypto.QuorumHash) (crypto.PubKey, error) {
	s.called = true
	return s.MockPV.GetPubKey(ctx, quorumHash)
}

func (s *spyPV) GetThresholdPublicKey(ctx context.Context, quorumHash crypto.QuorumHash) (crypto.PubKey, error) {
	s.called = true
	return s.MockPV.GetThresholdPublicKey(ctx, quorumHash)
}

func (s *spyPV) GetProTxHash(ctx context.Context) (crypto.ProTxHash, error) {
	s.called = true
	return s.MockPV.GetProTxHash(ctx)
}

func (s *spyPV) SignVote(
	ctx context.Context, chainID string, quorumType btcjson.LLMQType, quorumHash crypto.QuorumHash,
	vote *tmproto.Vote, logger log.Logger,
) error {
	s.called = true
	return s.MockPV.SignVote(ctx, chainID, quorumType, quorumHash, vote, logger)
}

func (s *spyPV) SignProposal(
	ctx context.Context, chainID string, quorumType btcjson.LLMQType, quorumHash crypto.QuorumHash,
	proposal *tmproto.Proposal,
) (tmbytes.HexBytes, error) {
	s.called = true
	return s.MockPV.SignProposal(ctx, chainID, quorumType, quorumHash, proposal)
}

func TestGetPubKey(t *testing.T) {

	testCases := []struct {
		name string
		pv   types.PrivValidator
		err  bool
	}{
		{name: "valid", pv: types.NewMockPV(), err: false},
		{name: "error on pubkey", pv: types.NewErroringMockPV(), err: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			logger := log.NewTestingLogger(t)
			s := tmgrpc.NewSignerServer(logger, ChainID, tc.pv)
			quorumHash, _ := tc.pv.GetFirstQuorumHash(ctx)
			req := &privvalproto.PubKeyRequest{ChainId: ChainID, QuorumHash: quorumHash}
			resp, err := s.GetPubKey(ctx, req)
			if tc.err {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				quorumHash, err := tc.pv.GetFirstQuorumHash(ctx)
				require.NoError(t, err)
				pk, err := tc.pv.GetPubKey(ctx, quorumHash)
				require.NoError(t, err)
				assert.Equal(t, resp.PubKey.GetBls12381(), pk.Bytes())
			}
		})
	}

}

func TestSignVote(t *testing.T) {

	hash := tmrand.Bytes(crypto.HashSize)
	proTxHash := crypto.RandProTxHash()
	stateID := types.RandStateID()

	testCases := []struct {
		name       string
		pv         types.PrivValidator
		have, want *types.Vote
		err        bool
	}{
		{name: "valid", pv: types.NewMockPV(), have: &types.Vote{
			Type:   tmproto.PrecommitType,
			Height: 1,
			Round:  2,
			BlockID: types.BlockID{
				Hash:          hash,
				PartSetHeader: types.PartSetHeader{Hash: hash, Total: 2},
				StateID:       stateID.Hash(),
			},
			ValidatorProTxHash: proTxHash,
			ValidatorIndex:     1,
		}, want: &types.Vote{
			Type:   tmproto.PrecommitType,
			Height: 1,
			Round:  2,
			BlockID: types.BlockID{
				Hash:          hash,
				PartSetHeader: types.PartSetHeader{Hash: hash, Total: 2},
				StateID:       stateID.Hash(),
			},
			ValidatorProTxHash: proTxHash,
			ValidatorIndex:     1,
		},
			err: false},
		{name: "invalid vote", pv: types.NewErroringMockPV(), have: &types.Vote{
			Type:   tmproto.PrecommitType,
			Height: 1,
			Round:  2,
			BlockID: types.BlockID{
				Hash:          hash,
				PartSetHeader: types.PartSetHeader{Hash: hash, Total: 2},
				StateID:       stateID.Hash(),
			},
			ValidatorProTxHash: proTxHash,
			ValidatorIndex:     1,
			BlockSignature:     []byte("signed"),
		}, want: &types.Vote{
			Type:   tmproto.PrecommitType,
			Height: 1,
			Round:  2,
			BlockID: types.BlockID{
				Hash:          hash,
				PartSetHeader: types.PartSetHeader{Hash: hash, Total: 2},
				StateID:       stateID.Hash(),
			},
			ValidatorProTxHash: proTxHash,
			ValidatorIndex:     1,
			BlockSignature:     []byte("signed"),
		},
			err: true},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			logger := log.NewTestingLogger(t)

			s := tmgrpc.NewSignerServer(logger, ChainID, tc.pv)

			quorumHash, _ := tc.pv.GetFirstQuorumHash(context.Background())
			req := &privvalproto.SignVoteRequest{
				Vote:       tc.have.ToProto(),
				ChainId:    ChainID,
				QuorumType: int32(btcjson.LLMQType_5_60),
				QuorumHash: quorumHash,
			}
			resp, err := s.SignVote(ctx, req)
			if tc.err {
				require.Error(t, err)
			} else {
				pbVote := tc.want.ToProto()
				require.NoError(t, tc.pv.SignVote(ctx, ChainID, btcjson.LLMQType_5_60, quorumHash,
					pbVote, log.NewTestingLogger(t)))

				assert.Equal(t, pbVote.BlockSignature, resp.Vote.BlockSignature)
			}
		})
	}
}

func TestSignProposal(t *testing.T) {

	ts := time.Now()
	hash := tmrand.Bytes(crypto.HashSize)
	quorumHash := crypto.RandQuorumHash()

	testCases := []struct {
		name       string
		pv         types.PrivValidator
		have, want *types.Proposal
		err        bool
	}{
		{name: "valid", pv: types.NewMockPVForQuorum(quorumHash), have: &types.Proposal{
			Type:      tmproto.ProposalType,
			Height:    1,
			Round:     2,
			POLRound:  2,
			BlockID:   types.BlockID{Hash: hash, PartSetHeader: types.PartSetHeader{Hash: hash, Total: 2}},
			Timestamp: ts,
		}, want: &types.Proposal{
			Type:      tmproto.ProposalType,
			Height:    1,
			Round:     2,
			POLRound:  2,
			BlockID:   types.BlockID{Hash: hash, PartSetHeader: types.PartSetHeader{Hash: hash, Total: 2}},
			Timestamp: ts,
		},
			err: false},
		{name: "invalid proposal", pv: types.NewErroringMockPV(), have: &types.Proposal{
			Type:      tmproto.ProposalType,
			Height:    1,
			Round:     2,
			POLRound:  2,
			BlockID:   types.BlockID{Hash: hash, PartSetHeader: types.PartSetHeader{Hash: hash, Total: 2}},
			Timestamp: ts,
			Signature: []byte("signed"),
		}, want: &types.Proposal{
			Type:      tmproto.ProposalType,
			Height:    1,
			Round:     2,
			POLRound:  2,
			BlockID:   types.BlockID{Hash: hash, PartSetHeader: types.PartSetHeader{Hash: hash, Total: 2}},
			Timestamp: ts,
			Signature: []byte("signed"),
		},
			err: true},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			logger := log.NewTestingLogger(t)

			s := tmgrpc.NewSignerServer(logger, ChainID, tc.pv)

			req := &privvalproto.SignProposalRequest{
				Proposal:   tc.have.ToProto(),
				ChainId:    ChainID,
				QuorumType: int32(btcjson.LLMQType_5_60),
				QuorumHash: quorumHash,
			}
			resp, err := s.SignProposal(ctx, req)
			if tc.err {
				require.Error(t, err)
			} else {
				pbProposal := tc.want.ToProto()
				_, err = tc.pv.SignProposal(ctx, ChainID, btcjson.LLMQType_5_60, quorumHash, pbProposal)
				require.NoError(t, err)
				assert.Equal(t, pbProposal.Signature, resp.Proposal.Signature)
			}
		})
	}
}
