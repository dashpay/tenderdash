package blocksync

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	p2pmocks "github.com/dashpay/tenderdash/internal/p2p/mocks"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	"github.com/dashpay/tenderdash/libs/log"
	bcproto "github.com/dashpay/tenderdash/proto/tendermint/blocksync"
	"github.com/dashpay/tenderdash/types"
)

type BlockP2PMessageHandlerTestSuite struct {
	suite.Suite

	reqID          string
	logger         log.Logger
	fakeStore      *mocks.BlockStore
	fakePeerAdder  *mockPeerAdder
	fakeP2PChannel *p2pmocks.Channel
	fakeClient     *client.Client
	handler        *blockP2PMessageHandler
}

func TestBlockP2PMessageHandler(t *testing.T) {
	suite.Run(t, new(BlockP2PMessageHandlerTestSuite))
}

func (suite *BlockP2PMessageHandlerTestSuite) SetupSuite() {
	suite.reqID = uuid.NewString()
}

func (suite *BlockP2PMessageHandlerTestSuite) SetupTest() {
	handlerCtx, cancel := context.WithCancel(context.Background())
	suite.T().Cleanup(cancel)
	conf := config.TestConfig()
	suite.logger = log.NewTestingLogger(suite.T())
	suite.fakeStore = mocks.NewBlockStore(suite.T())
	suite.fakePeerAdder = newMockPeerAdder(suite.T())
	suite.fakeP2PChannel = p2pmocks.NewChannel(suite.T())
	suite.fakeClient = client.New(
		p2p.ChannelDescriptors(conf),
		func(context.Context, *p2p.ChannelDescriptor) (p2p.Channel, error) {
			return suite.fakeP2PChannel, nil
		})
	suite.handler = newBlockP2PMessageHandler(handlerCtx, suite.logger, suite.fakeStore, suite.fakePeerAdder)
	suite.T().Cleanup(suite.handler.stop)
}

func (suite *BlockP2PMessageHandlerTestSuite) TestHandleBlockRequest() {
	ctx := context.Background()
	peerID := types.NodeID("peer")
	const H1001 = int64(1001)
	blockH1001 := types.Block{Header: types.Header{Height: 1001}}
	protoBlockH1001, err := blockH1001.ToProto()
	suite.Require().NoError(err)
	commit1001 := types.Commit{Height: 1001}
	protoCommit := commit1001.ToProto()
	blockRequestH1001 := &bcproto.BlockRequest{Height: H1001}
	testCases := []struct {
		mockFn   func()
		wantResp proto.Message
	}{
		{
			mockFn: func() {
				suite.fakeStore.
					On("LoadBlock", H1001).
					Once().
					Return(nil)
			},
			wantResp: &bcproto.NoBlockResponse{Height: H1001},
		},
		{
			mockFn: func() {
				suite.fakeStore.
					On("LoadBlock", H1001).
					Once().
					Return(&blockH1001)
				suite.fakeStore.
					On("LoadSeenCommitAt", H1001).
					Once().
					Return(nil)
			},
		},
		{
			mockFn: func() {
				suite.fakeStore.
					On("LoadBlock", H1001).
					Once().
					Return(&blockH1001)
				suite.fakeStore.
					On("LoadSeenCommitAt", H1001).
					Once().
					Return(&commit1001)
			},
			wantResp: &bcproto.BlockResponse{
				Block:  protoBlockH1001,
				Commit: protoCommit,
			},
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("%d", i), func() {
			if tc.mockFn != nil {
				tc.mockFn()
			}
			if tc.wantResp != nil {
				suite.fakeP2PChannel.
					On("Send", mock.Anything, mock.MatchedBy(suite.envelopeArg(peerID, tc.wantResp))).
					Once().
					Run(notifyDelivery).
					Return(nil)
			}
			suite.Require().NoError(suite.handleMessage(ctx, blockRequestH1001, peerID))
			suite.Eventually(suite.handler.blockResponsesIdle, time.Second, time.Millisecond)
		})
	}
}

func (suite *BlockP2PMessageHandlerTestSuite) TestServeBlockRequestReportsMissingCommit() {
	block := &types.Block{Header: types.Header{Height: 1001}}
	suite.fakeStore.On("LoadBlock", int64(1001)).Once().Return(block)
	suite.fakeStore.On("LoadSeenCommitAt", int64(1001)).Once().Return(nil)

	_, _, err := suite.handler.prepareBlockResponse(blockResponseJob{
		ctx: context.Background(),
		envelope: p2p.Envelope{
			From:    types.NodeID("peer"),
			Message: &bcproto.BlockRequest{Height: 1001},
		},
	})
	suite.Require().ErrorIs(err, errStoredBlockMissingCommit)
}

func (suite *BlockP2PMessageHandlerTestSuite) TestBlockResponseBackpressureAppliesBeforeLoad() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peerID := types.NodeID("slow-peer")
	block := &types.Block{Header: types.Header{Height: 1001}}
	commit := &types.Commit{Height: 1001}
	suite.fakeStore.On("LoadBlock", int64(1001)).Times(3).Return(block)
	suite.fakeStore.On("LoadSeenCommitAt", int64(1001)).Times(3).Return(commit)

	delivery := make(chan p2p.Envelope, 3)
	suite.fakeP2PChannel.
		On("Send", mock.Anything, mock.Anything).
		Times(3).
		Run(func(args mock.Arguments) { delivery <- args.Get(1).(p2p.Envelope) }).
		Return(nil)

	request := &bcproto.BlockRequest{Height: 1001}
	suite.Require().NoError(suite.handleMessage(ctx, request, peerID))
	first := <-delivery

	// Honest block sync pipelines requests. The next request is retained as
	// lightweight metadata but is not loaded while the first block is pending.
	suite.Require().NoError(suite.handleMessage(ctx, request, peerID))
	suite.Require().NoError(suite.handleMessage(ctx, request, peerID))
	suite.Never(func() bool { return len(delivery) > 0 }, 20*time.Millisecond, time.Millisecond)
	first.NotifyDelivery()
	second := <-delivery
	second.NotifyDelivery()
	third := <-delivery
	third.NotifyDelivery()
	suite.Eventually(suite.handler.blockResponsesIdle, time.Second, time.Millisecond)
}

func (suite *BlockP2PMessageHandlerTestSuite) TestStalledPeerDeliveryDoesNotBlockOtherPeers() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	block := &types.Block{Header: types.Header{Height: 1001}}
	commit := &types.Commit{Height: 1001}
	totalRequests := maxInFlightBlockResponses + 1
	suite.fakeStore.On("LoadBlock", int64(1001)).Times(totalRequests).Return(block)
	suite.fakeStore.On("LoadSeenCommitAt", int64(1001)).Times(totalRequests).Return(commit)

	delivery := make(chan p2p.Envelope, totalRequests)
	suite.fakeP2PChannel.
		On("Send", mock.Anything, mock.Anything).
		Times(totalRequests).
		Run(func(args mock.Arguments) { delivery <- args.Get(1).(p2p.Envelope) }).
		Return(nil)

	request := &bcproto.BlockRequest{Height: 1001}
	for i := 0; i < totalRequests; i++ {
		peerID := types.NodeID(fmt.Sprintf("peer-%d", i))
		suite.Require().NoError(suite.handleMessage(ctx, request, peerID))
	}
	pending := make([]p2p.Envelope, totalRequests)
	for i := range pending {
		select {
		case pending[i] = <-delivery:
		case <-time.After(time.Second):
			suite.FailNow("a stalled peer occupied the global block preparation workers")
		}
	}
	for _, envelope := range pending {
		envelope.NotifyDelivery()
	}
	suite.Eventually(suite.handler.blockResponsesIdle, time.Second, time.Millisecond)
}

func (suite *BlockP2PMessageHandlerTestSuite) TestBlockResponseQueueHasGlobalBound() {
	handler := &blockP2PMessageHandler{
		logger:         suite.logger,
		responseQueues: make(map[types.NodeID][]blockResponseJob),
		activePeers:    make(map[types.NodeID]bool),
		responseWake:   make(chan struct{}, 1),
	}
	for i := 0; i < maxQueuedBlockResponses; i++ {
		handler.enqueueBlockResponse(blockResponseJob{
			envelope: p2p.Envelope{
				From: types.NodeID(fmt.Sprintf("peer-%d", i/maxPendingRequestsPerPeer)),
			},
		})
	}
	handler.enqueueBlockResponse(blockResponseJob{
		envelope: p2p.Envelope{From: types.NodeID("new-peer")},
	})
	suite.Equal(maxQueuedBlockResponses, handler.queuedResponses)
	suite.Len(handler.responseQueues["new-peer"], 1)
}

func (suite *BlockP2PMessageHandlerTestSuite) TestDisconnectedPeerQueueIsPurgedBeforeLoadingBlocks() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peerID := types.NodeID("disconnecting-peer")
	const connID = uint64(7)
	block := &types.Block{Header: types.Header{Height: 1001}}
	commit := &types.Commit{Height: 1001}
	suite.fakeStore.On("LoadBlock", int64(1001)).Once().Return(block)
	suite.fakeStore.On("LoadSeenCommitAt", int64(1001)).Once().Return(commit)

	delivery := make(chan p2p.Envelope, 1)
	suite.fakeP2PChannel.
		On("Send", mock.Anything, mock.Anything).
		Once().
		Run(func(args mock.Arguments) { delivery <- args.Get(1).(p2p.Envelope) }).
		Return(nil)

	suite.handler.handlePeerUpdate(p2p.PeerUpdate{
		NodeID: peerID,
		Status: p2p.PeerStatusUp,
		ConnID: connID,
	})
	request := &bcproto.BlockRequest{Height: 1001}
	for i := 0; i < 2; i++ {
		envelope := &p2p.Envelope{
			Attributes: map[string]string{client.RequestIDAttribute: suite.reqID},
			From:       peerID,
			Message:    request,
			ChannelID:  p2p.BlockSyncChannel,
			ConnID:     connID,
		}
		suite.Require().NoError(suite.handler.Handle(ctx, suite.fakeClient, envelope))
	}
	first := <-delivery
	suite.handler.handlePeerUpdate(p2p.PeerUpdate{NodeID: peerID, Status: p2p.PeerStatusDown})
	suite.Require().NoError(suite.handler.Handle(ctx, suite.fakeClient, &p2p.Envelope{
		Attributes: map[string]string{client.RequestIDAttribute: suite.reqID},
		From:       peerID,
		Message:    request,
		ChannelID:  p2p.BlockSyncChannel,
		ConnID:     connID,
	}))
	first.NotifyDelivery()
	suite.Eventually(suite.handler.blockResponsesIdle, time.Second, time.Millisecond)
	suite.NotContains(suite.handler.peerConnections, peerID)
}

func (suite *BlockP2PMessageHandlerTestSuite) TestDisconnectDuringLoadSkipsResponseConversion() {
	ctx := context.Background()
	peerID := types.NodeID("disconnect-during-load")
	const connID = uint64(8)
	started := make(chan struct{})
	release := make(chan struct{})
	suite.fakeStore.
		On("LoadBlock", int64(1001)).
		Once().
		Run(func(mock.Arguments) {
			close(started)
			<-release
		}).
		Return(&types.Block{Header: types.Header{Height: 1001}})

	suite.handler.handlePeerUpdate(p2p.PeerUpdate{
		NodeID: peerID,
		Status: p2p.PeerStatusUp,
		ConnID: connID,
	})
	suite.Require().NoError(suite.handler.Handle(ctx, suite.fakeClient, &p2p.Envelope{
		Attributes: map[string]string{client.RequestIDAttribute: suite.reqID},
		From:       peerID,
		Message:    &bcproto.BlockRequest{Height: 1001},
		ChannelID:  p2p.BlockSyncChannel,
		ConnID:     connID,
	}))
	<-started
	suite.handler.handlePeerUpdate(p2p.PeerUpdate{NodeID: peerID, Status: p2p.PeerStatusDown})
	close(release)
	suite.Eventually(suite.handler.blockResponsesIdle, time.Second, time.Millisecond)
}

func (suite *BlockP2PMessageHandlerTestSuite) TestHandleStatusResponse() {
	ctx := context.Background()
	peerID := types.NodeID("peer")
	testCases := []struct {
		name       string
		msg        *bcproto.StatusResponse
		wantAdded  bool
		wantBase   int64
		wantHeight int64
	}{
		{
			name:       "valid base below height",
			msg:        &bcproto.StatusResponse{Height: 1001, Base: 1000},
			wantAdded:  true,
			wantBase:   1000,
			wantHeight: 1001,
		},
		{
			name:       "fresh node reports zero",
			msg:        &bcproto.StatusResponse{Height: 0, Base: 0},
			wantAdded:  true,
			wantBase:   0,
			wantHeight: 0,
		},
		{
			name:      "negative base",
			msg:       &bcproto.StatusResponse{Height: 5, Base: -1},
			wantAdded: false,
		},
		{
			name:      "base above height",
			msg:       &bcproto.StatusResponse{Height: 4, Base: 5},
			wantAdded: false,
		},
		{
			name:      "height above plausible bound",
			msg:       &bcproto.StatusResponse{Height: maxPlausiblePeerHeight + 1, Base: 0},
			wantAdded: false,
		},
	}
	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			if tc.wantAdded {
				suite.fakePeerAdder.
					On("AddPeer", mock.MatchedBy(func(peerData PeerData) bool {
						return peerData.height == tc.wantHeight && peerData.base == tc.wantBase
					})).
					Once()
			}
			err := suite.handleMessage(ctx, tc.msg, peerID)
			suite.Require().NoError(err)
		})
	}
}

func (suite *BlockP2PMessageHandlerTestSuite) TestHandleStatusRequest() {
	ctx := context.Background()
	peerID := types.NodeID("peer")
	testCases := []struct {
		mockFn   func()
		wantResp *bcproto.StatusResponse
	}{
		{
			mockFn: func() {
				suite.fakeStore.On("Height").Once().Return(int64(1001))
				suite.fakeStore.On("Base").Once().Return(int64(1000))
			},
			wantResp: &bcproto.StatusResponse{
				Height: 1001,
				Base:   1000,
			},
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("%d", i), func() {
			if tc.mockFn != nil {
				tc.mockFn()
			}
			if tc.wantResp != nil {
				suite.fakeP2PChannel.
					On("Send", ctx, mock.MatchedBy(suite.envelopeArg(peerID, tc.wantResp))).
					Once().
					Run(notifyDelivery).
					Return(nil)
			}
			err := suite.handleMessage(ctx, &bcproto.StatusRequest{}, peerID)
			suite.Require().NoError(err)
		})
	}
}

func (suite *BlockP2PMessageHandlerTestSuite) handleMessage(
	ctx context.Context,
	msg proto.Message,
	peerID types.NodeID,
) error {
	return suite.handler.Handle(ctx, suite.fakeClient, &p2p.Envelope{
		Attributes: map[string]string{
			client.RequestIDAttribute: suite.reqID,
		},
		From:      peerID,
		Message:   msg,
		ChannelID: p2p.BlockSyncChannel,
	})
}

func (suite *BlockP2PMessageHandlerTestSuite) envelopeArg(
	peerID types.NodeID,
	resp proto.Message,
) func(envelope p2p.Envelope) bool {
	called := false
	return func(envelope p2p.Envelope) bool {
		if called {
			return true
		}
		called = true
		_, hasRespID := envelope.Attributes[client.ResponseIDAttribute]
		return suite.Equal(peerID, envelope.To) &&
			suite.Equal(resp, envelope.Message) &&
			hasRespID
	}
}

func notifyDelivery(args mock.Arguments) {
	envelope := args.Get(1).(p2p.Envelope)
	envelope.NotifyDelivery()
}

type mockPeerAdder struct {
	mock.Mock
}

func (m *mockPeerAdder) AddPeer(peer PeerData) {
	_ = m.Called(peer)
}

func newMockPeerAdder(t *testing.T) *mockPeerAdder {
	fake := &mockPeerAdder{}
	fake.Mock.Test(t)
	t.Cleanup(func() { mock.AssertExpectationsForObjects(t) })
	return fake
}
