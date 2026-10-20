package consensus

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

// TestHandleMessage_InvalidPayload_IsMarkedInvalidData asserts a known message
// type carrying an invalid value is reported as proof of misbehavior, so the
// router evicts the sender rather than merely lowering its score.
//
// SignedMsgType is a proto3 enum and therefore open: an undefined value decodes
// cleanly and is only caught by ValidateBasic.
func TestHandleMessage_InvalidPayload_IsMarkedInvalidData(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := &Reactor{logger: log.NewNopLogger()}
	envelope := &p2p.Envelope{
		ChannelID: p2p.ConsensusStateChannel,
		Message: &tmcons.HasVote{
			Height: 1,
			Round:  0,
			Type:   tmproto.SignedMsgType(9999),
			Index:  0,
		},
	}

	err := r.handleMessage(ctx, envelope, channelBundle{})
	require.Error(t, err)
	require.ErrorIs(t, err, p2p.ErrInvalidData,
		"an undefined enum value in a known message type must be punished as invalid data")
}

// TestHandleMessage_UnrecognizedPayload_IsNotInvalidData pins the version-skew
// carve-out: a payload type this build has never heard of is reported but not
// treated as proof of misbehavior, because a peer on a newer release produces
// it legitimately and evicting it would break rolling upgrades.
func TestHandleMessage_UnrecognizedPayload_IsNotInvalidData(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := &Reactor{logger: log.NewNopLogger()}
	envelope := &p2p.Envelope{
		ChannelID: p2p.ConsensusStateChannel,
		// Not a consensus message type at all, standing in for one this build
		// does not yet know.
		Message: &tmproto.Validator{},
	}

	err := r.handleMessage(ctx, envelope, channelBundle{})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnrecognizedMessage),
		"an unknown payload type must be reported as unrecognized")
	require.NotErrorIs(t, err, p2p.ErrInvalidData,
		"an unknown payload type must not be punished as invalid data: that is what version skew looks like")
}
