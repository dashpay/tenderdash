package grpc

import (
	"context"

	"github.com/dashpay/dashd-go/btcjson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/encoding"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/privval"
	privvalproto "github.com/dashpay/tenderdash/proto/tendermint/privval"
	"github.com/dashpay/tenderdash/types"
)

// SignerServer implements PrivValidatorAPIServer 9generated via protobuf services)
// Handles remote validator connections that provide signing services
type SignerServer struct {
	logger  log.Logger
	chainID string
	privVal types.PrivValidator
}

func NewSignerServer(logger log.Logger, chainID string, privVal types.PrivValidator) *SignerServer {
	return &SignerServer{
		logger:  logger,
		chainID: chainID,
		privVal: privVal,
	}
}

var _ privvalproto.PrivValidatorAPIServer = (*SignerServer)(nil)

// validateChainID guards the signer against serving requests for the wrong
// chain. It returns FailedPrecondition when the server itself has no chain ID
// configured, and InvalidArgument when the request omits the chain ID or does
// not match the chain ID the server was configured with.
func (ss *SignerServer) validateChainID(reqChainID string) error {
	if ss.chainID == "" {
		return status.Error(codes.FailedPrecondition, "server chain ID is not configured")
	}
	if reqChainID == "" {
		return status.Error(codes.InvalidArgument, "missing chain ID")
	}
	if reqChainID != ss.chainID {
		return status.Errorf(codes.InvalidArgument, "unexpected chain ID: want %s, got %s", ss.chainID, reqChainID)
	}
	return nil
}

// GetPubKey receives a request for the pubkey
// returns the pubkey on success and error on failure
func (ss *SignerServer) GetPubKey(ctx context.Context, req *privvalproto.PubKeyRequest) (
	*privvalproto.PubKeyResponse, error) {
	if err := ss.validateChainID(req.ChainId); err != nil {
		return nil, err
	}

	var pubKey crypto.PubKey

	pubKey, err := ss.privVal.GetPubKey(ctx, req.QuorumHash)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "error getting pubkey: %v", err)
	}

	pk, err := encoding.PubKeyToProto(pubKey)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error transitioning pubkey to proto: %v", err)
	}

	ss.logger.Info("SignerServer: GetPubKey Success")

	return &privvalproto.PubKeyResponse{PubKey: pk}, nil
}

// GetThresholdPubKey receives a request for the threshold pubkey
// returns the pubkey on success and error on failure
func (ss *SignerServer) GetThresholdPubKey(ctx context.Context, req *privvalproto.ThresholdPubKeyRequest) (
	*privvalproto.ThresholdPubKeyResponse, error) {
	if err := ss.validateChainID(req.ChainId); err != nil {
		return nil, err
	}

	var pubKey crypto.PubKey

	pubKey, err := ss.privVal.GetThresholdPublicKey(ctx, req.QuorumHash)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "error getting pubkey: %v", err)
	}

	pk, err := encoding.PubKeyToProto(pubKey)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error transitioning pubkey to proto: %v", err)
	}

	ss.logger.Info("SignerServer: GetPubKey Success")

	return &privvalproto.ThresholdPubKeyResponse{PubKey: pk}, nil
}

// GetProTxHash receives a request for the proTxHash
// returns the proTxHash on success and error on failure
func (ss *SignerServer) GetProTxHash(ctx context.Context, req *privvalproto.ProTxHashRequest) (
	*privvalproto.ProTxHashResponse, error) {
	if err := ss.validateChainID(req.ChainId); err != nil {
		return nil, err
	}

	var proTxHash crypto.ProTxHash

	proTxHash, err := ss.privVal.GetProTxHash(ctx)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "error getting proTxHash: %v", err)
	}

	ss.logger.Info("SignerServer: GetProTxHash Success")

	return &privvalproto.ProTxHashResponse{ProTxHash: proTxHash}, nil
}

// SignVote receives a vote sign requests, attempts to sign it
// returns SignedVoteResponse on success and error on failure
//
//nolint:dupl // parallel to SignProposal by design: same validation order over a different request type
func (ss *SignerServer) SignVote(ctx context.Context, req *privvalproto.SignVoteRequest) (*privvalproto.SignedVoteResponse, error) {
	if err := ss.validateChainID(req.ChainId); err != nil {
		return nil, err
	}

	if req.Vote == nil {
		return nil, status.Error(codes.InvalidArgument, "error signing vote: vote is missing")
	}
	if err := privval.ValidateQuorumParams(req.QuorumType, req.QuorumHash); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "error signing vote: %v", err)
	}

	vote := req.Vote

	err := ss.privVal.SignVote(ctx, req.ChainId, btcjson.LLMQType(req.QuorumType), req.QuorumHash, vote, nil)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "error signing vote: %v", err)
	}

	ss.logger.Info("SignerServer: SignVote Success", "height", req.Vote.Height)

	return &privvalproto.SignedVoteResponse{Vote: *vote}, nil
}

// SignProposal receives a proposal sign requests, attempts to sign it
// returns SignedProposalResponse on success and error on failure
//
//nolint:dupl // parallel to SignVote by design: same validation order over a different request type
func (ss *SignerServer) SignProposal(ctx context.Context, req *privvalproto.SignProposalRequest) (*privvalproto.SignedProposalResponse, error) {
	if err := ss.validateChainID(req.ChainId); err != nil {
		return nil, err
	}

	if req.Proposal == nil {
		return nil, status.Error(codes.InvalidArgument, "error signing proposal: proposal is missing")
	}
	if err := privval.ValidateQuorumParams(req.QuorumType, req.QuorumHash); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "error signing proposal: %v", err)
	}

	proposal := req.Proposal

	_, err := ss.privVal.SignProposal(ctx, req.ChainId, btcjson.LLMQType(req.QuorumType), req.QuorumHash, proposal)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "error signing proposal: %v", err)
	}

	ss.logger.Info("SignerServer: SignProposal Success", "height", req.Proposal.Height)

	return &privvalproto.SignedProposalResponse{Proposal: *proposal}, nil
}
