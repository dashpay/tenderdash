package consensus

import (
	"context"
	"fmt"
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/internal/p2p"
	"github.com/tendermint/tendermint/internal/p2p/mocks"
	"github.com/tendermint/tendermint/libs/log"
	tmcons "github.com/tendermint/tendermint/proto/tendermint/consensus"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

func TestP2PMsgSender_Send(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewTestingLogger(t)
	nodeID := types.NodeID("test-peer")
	ps := NewPeerState(logger, nodeID)
	mockStateCh := mocks.NewChannel(t)
	mockDataCh := mocks.NewChannel(t)
	mockVoteCh := mocks.NewChannel(t)
	sender := p2pMsgSender{
		logger: log.NewTestingLogger(t),
		ps:     ps,
		chans: channelBundle{
			state: mockStateCh,
			data:  mockDataCh,
			vote:  mockVoteCh,
		},
	}
	testCases := []struct {
		ch   *mocks.Channel
		msg  proto.Message
		want proto.Message
	}{
		{
			ch:  mockVoteCh,
			msg: &tmproto.Commit{},
			want: &tmcons.Commit{
				Commit: &tmproto.Commit{},
			},
		},
		{
			ch:  mockVoteCh,
			msg: &tmproto.Vote{},
			want: &tmcons.Vote{
				Vote: &tmproto.Vote{},
			},
		},
		{
			ch:   mockDataCh,
			msg:  &tmcons.BlockPart{},
			want: &tmcons.BlockPart{},
		},
		{
			ch:   mockDataCh,
			msg:  &tmcons.ProposalPOL{},
			want: &tmcons.ProposalPOL{},
		},
		{
			ch:  mockDataCh,
			msg: &tmproto.Proposal{},
			want: &tmcons.Proposal{
				Proposal: tmproto.Proposal{},
			},
		},
		{
			ch:   mockStateCh,
			msg:  &tmcons.VoteSetMaj23{},
			want: &tmcons.VoteSetMaj23{},
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			envelope := p2p.Envelope{
				To:      nodeID,
				Message: tc.want,
			}
			tc.ch.
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
