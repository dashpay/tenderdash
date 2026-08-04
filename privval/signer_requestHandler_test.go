package privval

import (
	"context"
	"testing"
	"time"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
	privvalproto "github.com/dashpay/tenderdash/proto/tendermint/privval"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

const testChainID = "test-chain"

// testVote returns a well-formed vote to sign.
func testVote() *tmproto.Vote {
	hash := tmrand.Bytes(crypto.HashSize)
	return (&types.Vote{
		Type:   tmproto.PrecommitType,
		Height: 1,
		Round:  2,
		BlockID: types.BlockID{
			Hash:          hash,
			PartSetHeader: types.PartSetHeader{Hash: hash, Total: 2},
			StateID:       types.RandStateID().Hash(),
		},
		ValidatorProTxHash: crypto.RandProTxHash(),
		ValidatorIndex:     1,
	}).ToProto()
}

// testProposal returns a well-formed proposal to sign.
func testProposal() *tmproto.Proposal {
	hash := tmrand.Bytes(crypto.HashSize)
	return (&types.Proposal{
		Type:      tmproto.ProposalType,
		Height:    1,
		Round:     2,
		POLRound:  2,
		BlockID:   types.BlockID{Hash: hash, PartSetHeader: types.PartSetHeader{Hash: hash, Total: 2}},
		Timestamp: time.Now(),
	}).ToProto()
}

// A signing request carries its quorum type as a bare int32, so the socket
// transport must check it against the LLMQ allowlist before it reaches the
// privVal: downstream sign-hash construction narrows the type to uint8 with a
// panicking conversion, and a panic in the signer takes down the validator's
// ability to sign at all.
func TestDefaultValidationRequestHandlerRejectsUnsupportedQuorumType(t *testing.T) {
	const chainID = testChainID

	vote := testVote()
	proposal := testProposal()

	// Would panic in the uint8 narrowing of sign-hash construction (999, -1) or
	// survives it but is no known LLMQ type (200).
	invalidQuorumTypes := map[string]int32{
		"out of uint8 range": 999,
		"negative":           -1,
		"unknown type":       200,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	privVal := types.NewMockPV()
	quorumHash, err := privVal.GetFirstQuorumHash(ctx)
	require.NoError(t, err)

	for caseName, quorumType := range invalidQuorumTypes {
		t.Run("SignVote/"+caseName, func(t *testing.T) {
			req := privvalproto.Message{Sum: &privvalproto.Message_SignVoteRequest{
				SignVoteRequest: &privvalproto.SignVoteRequest{
					Vote:       vote,
					ChainId:    chainID,
					QuorumType: quorumType,
					QuorumHash: quorumHash,
				},
			}}

			res, err := DefaultValidationRequestHandler(ctx, privVal, req, chainID)
			require.Error(t, err)
			resp := res.GetSignedVoteResponse()
			require.NotNil(t, resp, "a rejected sign request must still produce a response for the client")
			require.NotNil(t, resp.Error, "the response must carry a remote signer error")
			assert.Empty(t, resp.Vote.BlockSignature, "nothing may be signed for an unsupported quorum type")
		})

		t.Run("SignProposal/"+caseName, func(t *testing.T) {
			req := privvalproto.Message{Sum: &privvalproto.Message_SignProposalRequest{
				SignProposalRequest: &privvalproto.SignProposalRequest{
					Proposal:   proposal,
					ChainId:    chainID,
					QuorumType: quorumType,
					QuorumHash: quorumHash,
				},
			}}

			res, err := DefaultValidationRequestHandler(ctx, privVal, req, chainID)
			require.Error(t, err)
			resp := res.GetSignedProposalResponse()
			require.NotNil(t, resp, "a rejected sign request must still produce a response for the client")
			require.NotNil(t, resp.Error, "the response must carry a remote signer error")
			assert.Empty(t, resp.Proposal.Signature, "nothing may be signed for an unsupported quorum type")
		})
	}
}

// The message pointer and the quorum hash of a signing request are peer-supplied
// and reach the privVal unchecked: a missing vote or proposal is dereferenced, and
// a quorum hash of the wrong size fails SignItem.Validate, which UpdateSignHash
// answers with a panic. A panicking signer stops signing altogether.
func TestDefaultValidationRequestHandlerRejectsMalformedSignRequest(t *testing.T) {
	const chainID = testChainID

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	privVal := types.NewMockPV()
	quorumHash, err := privVal.GetFirstQuorumHash(ctx)
	require.NoError(t, err)
	shortQuorumHash := crypto.QuorumHash(tmrand.Bytes(crypto.QuorumHashSize - 1))
	quorumType := int32(btcjson.LLMQType_5_60)

	signVote := func(vote *tmproto.Vote, hash crypto.QuorumHash) (privvalproto.Message, error) {
		return DefaultValidationRequestHandler(ctx, privVal, privvalproto.Message{
			Sum: &privvalproto.Message_SignVoteRequest{
				SignVoteRequest: &privvalproto.SignVoteRequest{
					Vote: vote, ChainId: chainID, QuorumType: quorumType, QuorumHash: hash,
				},
			},
		}, chainID)
	}
	signProposal := func(proposal *tmproto.Proposal, hash crypto.QuorumHash) (privvalproto.Message, error) {
		return DefaultValidationRequestHandler(ctx, privVal, privvalproto.Message{
			Sum: &privvalproto.Message_SignProposalRequest{
				SignProposalRequest: &privvalproto.SignProposalRequest{
					Proposal: proposal, ChainId: chainID, QuorumType: quorumType, QuorumHash: hash,
				},
			},
		}, chainID)
	}

	voteCases := map[string]func() (privvalproto.Message, error){
		"missing vote":              func() (privvalproto.Message, error) { return signVote(nil, quorumHash) },
		"quorum hash of wrong size": func() (privvalproto.Message, error) { return signVote(testVote(), shortQuorumHash) },
	}
	for name, sign := range voteCases {
		sign := sign
		t.Run("SignVote/"+name, func(t *testing.T) {
			var (
				res privvalproto.Message
				err error
			)
			require.NotPanics(t, func() { res, err = sign() })
			require.Error(t, err)
			resp := res.GetSignedVoteResponse()
			require.NotNil(t, resp, "a rejected sign request must still produce a response for the client")
			require.NotNil(t, resp.Error, "the response must carry a remote signer error")
			assert.Empty(t, resp.Vote.BlockSignature, "nothing may be signed for a malformed request")
		})
	}

	proposalCases := map[string]func() (privvalproto.Message, error){
		"missing proposal": func() (privvalproto.Message, error) { return signProposal(nil, quorumHash) },
		"quorum hash of wrong size": func() (privvalproto.Message, error) {
			return signProposal(testProposal(), shortQuorumHash)
		},
	}
	for name, sign := range proposalCases {
		sign := sign
		t.Run("SignProposal/"+name, func(t *testing.T) {
			var (
				res privvalproto.Message
				err error
			)
			require.NotPanics(t, func() { res, err = sign() })
			require.Error(t, err)
			resp := res.GetSignedProposalResponse()
			require.NotNil(t, resp, "a rejected sign request must still produce a response for the client")
			require.NotNil(t, resp.Error, "the response must carry a remote signer error")
			assert.Empty(t, resp.Proposal.Signature, "nothing may be signed for a malformed request")
		})
	}
}
