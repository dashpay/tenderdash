//go:build floodclient

package floodclient

import (
	"context"
	"fmt"

	"github.com/dashpay/dashd-go/btcjson"

	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// SigningIdentity is a real validator's signing capability: its private
// validator, its index in the set, and the quorum context needed to produce a
// signature the target will actually verify.
//
// On a real network only the validator itself holds its private key, so
// everything that needs a SigningIdentity — the honest client and the
// valid-block-signature profile — is for a devnet where you control a validator
// key. What it needs beyond the key is the chain ID and the active quorum
// (type and hash), which are read off the live network.
type SigningIdentity struct {
	PrivVal    types.PrivValidator
	Index      int32
	ChainID    string
	QuorumType btcjson.LLMQType
	QuorumHash []byte
}

// proTxHash returns the validator's proTxHash.
func (s *SigningIdentity) proTxHash(ctx context.Context) ([]byte, error) {
	return s.PrivVal.GetProTxHash(ctx)
}

// signVote fills in a proto vote's block signature (and, for a precommit, its
// vote-extension signatures) using the real key. After it returns, the vote
// verifies against the validator's public key — which is what an honest vote
// has and a forged one does not.
func (s *SigningIdentity) signVote(ctx context.Context, v *tmproto.Vote) error {
	return s.PrivVal.SignVote(ctx, s.ChainID, s.QuorumType, s.QuorumHash, v, nil)
}

// HonestVoter sends genuinely-signed votes over a real connection, so honest
// service can be measured against the same target the attackers flood (mixed
// mode). It is the honest counterpart of a flood Conn.
//
// It votes nil (an empty BlockID), which is always a valid thing to sign and
// needs no knowledge of the proposed block. It votes at the target's live
// height/round (see SendPrevoteLive), so its acceptance reflects real service
// the node provides under flood. Voting FOR the proposed block would
// additionally require tracking the target's round state to learn the block ID
// (hash, part-set header and state ID) — deeper proposal tracking that is out of
// scope; here the point is that a correctly-signed vote at the current
// height/round is accepted while the flood is shed.
type HonestVoter struct {
	conn   *Conn
	signer *SigningIdentity
}

// NewHonestVoter wraps a live connection with a signer.
func NewHonestVoter(conn *Conn, signer *SigningIdentity) *HonestVoter {
	return &HonestVoter{conn: conn, signer: signer}
}

// LiveState reports the target's current consensus height and round. The honest
// voter reads it before each vote so it votes at the height/round the network is
// actually on, not a value captured at startup. On a live network this is backed
// by the target's RPC (RPCLiveState); in-process tests back it with the node's
// own round state.
type LiveState interface {
	CurrentHeightRound(ctx context.Context) (height int64, round int32, err error)
}

// SendPrevoteLive reads the target's current consensus height and round from
// state and sends a genuinely-signed nil prevote for it, returning the
// height/round it voted at. Tracking the live height/round is what makes the
// honest-latency signal meaningful: a vote for a stale height/round would not be
// the honest service the node is actually being asked to provide under flood.
func (h *HonestVoter) SendPrevoteLive(ctx context.Context, state LiveState) (height int64, round int32, err error) {
	height, round, err = state.CurrentHeightRound(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("honest voter live height/round: %w", err)
	}
	if err := h.SendPrevote(ctx, height, round); err != nil {
		return height, round, err
	}
	return height, round, nil
}

// SendPrevote signs and sends a nil prevote for the given height and round. The
// target verifies the signature against the validator's public key and accepts
// the vote; nothing about it is forged, so it is the honest traffic the node
// must keep serving while it sheds the flood.
func (h *HonestVoter) SendPrevote(ctx context.Context, height int64, round int32) error {
	proTxHash, err := h.signer.proTxHash(ctx)
	if err != nil {
		return fmt.Errorf("honest voter proTxHash: %w", err)
	}
	v := &tmproto.Vote{
		Type:               tmproto.PrevoteType,
		Height:             height,
		Round:              round,
		BlockID:            tmproto.BlockID{}, // nil vote: no block, no extensions
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     h.signer.Index,
	}
	if err := h.signer.signVote(ctx, v); err != nil {
		return fmt.Errorf("honest voter sign: %w", err)
	}
	return h.conn.Send(ctx, &tmcons.Vote{Vote: v})
}

// Close tears down the underlying connection.
func (h *HonestVoter) Close() error { return h.conn.Close() }
