package consensus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	p2pproto "github.com/dashpay/tenderdash/proto/tendermint/p2p"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
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

func TestMissingVoteWire_ClassifiedAndEvicted(t *testing.T) {
	var wire p2pproto.Envelope
	require.NoError(t, proto.Unmarshal([]byte{0xca, 0x01, 0x00}, &wire))
	envelope, err := p2p.EnvelopeFromProto(wire)
	require.NoError(t, err)
	require.Nil(t, envelope.Message.(*tmcons.Vote).Vote)
	envelope.ChannelID = p2p.ConsensusStateChannel
	var output bytes.Buffer
	logger, err := log.NewLogger("info", &output)
	require.NoError(t, err)
	r := &Reactor{logger: logger}
	err = r.handleMessage(context.Background(), &envelope, channelBundle{})
	require.ErrorIs(t, err, p2p.ErrInvalidData)
	require.Empty(t, output.String(), "missing peer data must not trigger panic recovery logs")
	for _, reserved := range []bool{false, true} {
		t.Run(fmt.Sprint(reserved), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			capacity := uint16(10)
			if reserved {
				capacity = 2
			}
			pm, createErr := p2p.NewPeerManager(ctx, types.NodeID(strings.Repeat("a1", 20)), dbm.NewMemDB(), p2p.PeerManagerOptions{MaxConnected: capacity})
			require.NoError(t, createErr)
			t.Cleanup(func() { require.NoError(t, pm.Close()) })
			peer := types.NodeID(strings.Repeat("b2", 20))
			require.NoError(t, pm.Accepted(peer))
			if reserved {
				require.NoError(t, pm.Accepted(types.NodeID(strings.Repeat("c3", 20))))
				require.NoError(t, pm.SetProtectedPeers([]types.NodeID{peer}))
			}
			require.True(t, pm.ShouldDisconnectOnError(peer, errors.Is(err, p2p.ErrInvalidData)))
		})
	}
}

func TestProcessMsgCh_PeerInputLogging(t *testing.T) {
	for _, msg := range []proto.Message{&tmcons.Vote{}, &p2pproto.Echo{}} {
		t.Run(fmt.Sprintf("%T", msg), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var output bytes.Buffer
			logger, err := log.NewLogger("info", &output)
			require.NoError(t, err)
			pm, err := p2p.NewPeerManager(ctx, types.NodeID(strings.Repeat("a1", 20)), dbm.NewMemDB(), p2p.PeerManagerOptions{MaxConnected: 1})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, pm.Close()) })
			pm.SetLogger(logger)
			peer := types.NodeID(strings.Repeat("b2", 20))
			require.NoError(t, pm.Accepted(peer))
			r := &Reactor{logger: logger, stateRateLimit: client.NewRateLimitWithBurst(ctx, 0, 1, true, logger)}
			inCh := make(chan p2p.Envelope, 1)
			errCh := make(chan p2p.PeerError, 1)
			inCh <- p2p.Envelope{From: peer, ChannelID: p2p.ConsensusStateChannel, Message: msg}
			ch := p2p.NewChannel(p2p.ConsensusStateChannel, "state", inCh, nil, errCh)
			done := make(chan struct{})
			go func() { defer close(done); r.processMsgCh(ctx, ch, channelBundle{}) }()
			select {
			case peerErr := <-errCh:
				pm.Errored(peerErr.NodeID, peerErr.Err)
			case <-time.After(5 * time.Second):
				cancel()
				<-done
				t.Fatal("rejected input did not report a peer error")
			}
			cancel()
			<-done
			require.Empty(t, output.String(), "peer input must not generate production-level errors")
		})
	}
}

func TestHandleMessage_InternalPanicRemainsVisible(t *testing.T) {
	var output bytes.Buffer
	logger, err := log.NewLogger("info", &output)
	require.NoError(t, err)
	r := &Reactor{logger: logger}
	// A missing local envelope is an internal fault, not a wire payload.
	err = r.handleMessage(context.Background(), nil, channelBundle{})
	require.Error(t, err)
	require.NotErrorIs(t, err, p2p.ErrInvalidData)
	require.False(t, isPeerFloodableError(err))
	require.Contains(t, output.String(), `"level":"error"`)
	require.Contains(t, output.String(), "recovering from processing message panic")
}
