package consensus

import (
	"context"
	"fmt"
	"testing"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/mocks"
	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func TestP2PMsgSender_Send(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewTestingLogger(t)
	nodeID := types.NodeID("test-peer")
	ps := NewPeerState(logger, nodeID)
	mockVoteChannelFn := func(bundle *channelBundle) *mocks.Channel {
		bundle.vote = mocks.NewChannel(t)
		return bundle.vote.(*mocks.Channel)
	}
	mockDataChannelFn := func(bundle *channelBundle) *mocks.Channel {
		bundle.data = mocks.NewChannel(t)
		return bundle.data.(*mocks.Channel)
	}
	mockStateChannelFn := func(bundle *channelBundle) *mocks.Channel {
		bundle.state = mocks.NewChannel(t)
		return bundle.state.(*mocks.Channel)
	}
	testCases := []struct {
		mockChannel func(bundle *channelBundle) *mocks.Channel
		msg         proto.Message
		want        proto.Message
	}{
		{
			mockChannel: mockVoteChannelFn,
			msg:         &tmproto.Commit{},
			want: &tmcons.Commit{
				Commit: &tmproto.Commit{},
			},
		},
		{
			mockChannel: mockVoteChannelFn,
			msg:         &tmproto.Vote{},
			want: &tmcons.Vote{
				Vote: &tmproto.Vote{},
			},
		},
		{
			mockChannel: mockDataChannelFn,
			msg:         &tmcons.BlockPart{},
			want:        &tmcons.BlockPart{},
		},
		{
			mockChannel: mockDataChannelFn,
			msg:         &tmcons.ProposalPOL{},
			want:        &tmcons.ProposalPOL{},
		},
		{
			mockChannel: mockDataChannelFn,
			msg:         &tmproto.Proposal{},
			want: &tmcons.Proposal{
				Proposal: tmproto.Proposal{},
			},
		},
		{
			mockChannel: mockStateChannelFn,
			msg:         &tmcons.VoteSetMaj23{},
			want:        &tmcons.VoteSetMaj23{},
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			envelope := p2p.Envelope{
				To:      nodeID,
				Message: tc.want,
			}
			sender := p2pMsgSender{
				logger: log.NewTestingLogger(t),
				ps:     ps,
				chans:  channelBundle{},
			}
			mockCh := tc.mockChannel(&sender.chans)
			mockCh.
				On("Send", ctx, envelope).
				Once().
				Return(nil)
			err := sender.send(ctx, tc.msg)
			require.NoError(t, err)
		})
	}
}

func TestP2PMsgSender_UnsupportedMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nodeID := types.NodeID("test-peer")
	ps := NewPeerState(log.NewNopLogger(), nodeID)
	sender := p2pMsgSender{
		logger: log.NewNopLogger(),
		ps:     ps,
	}
	err := sender.send(ctx, nil)
	require.ErrorContains(t, err, "given unsupported p2p message")
}

func TestP2PMsgSender_ContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	nodeID := types.NodeID("test-peer")
	ps := NewPeerState(log.NewNopLogger(), nodeID)
	sender := p2pMsgSender{
		logger: log.NewNopLogger(),
		ps:     ps,
	}
	err := sender.sendTo(ctx, nil, nil)
	require.ErrorIs(t, errReactorClosed, err)
}
