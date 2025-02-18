package blocksync

import (
	"context"
	"fmt"
	"testing"

	"github.com/cosmos/gogoproto/proto"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	p2pmocks "github.com/dashpay/tenderdash/internal/p2p/mocks"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	tmrequire "github.com/dashpay/tenderdash/internal/test/require"
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
	suite.handler = &blockP2PMessageHandler{
		logger:    suite.logger,
		store:     suite.fakeStore,
		peerAdder: suite.fakePeerAdder,
	}
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
		wantErr  string
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
			wantErr: "found block in store with no commit",
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
					On("Send", ctx, mock.MatchedBy(suite.envelopeArg(peerID, tc.wantResp))).
					Once().
					Return(nil)
			}
			err := suite.handleMessage(ctx, blockRequestH1001, peerID)
			tmrequire.Error(suite.T(), tc.wantErr, err)
		})
	}
}

func (suite *BlockP2PMessageHandlerTestSuite) TestHandleStatusResponse() {
	ctx := context.Background()
	peerID := types.NodeID("peer")
	testCases := []struct {
		mockFn func()
		msg    *bcproto.StatusResponse
	}{
		{
			mockFn: func() {
				suite.fakePeerAdder.
					On("AddPeer", mock.MatchedBy(func(peerData PeerData) bool {
						return peerData.height == 1001 && peerData.base == 1000
					})).
					Once()
			},
			msg: &bcproto.StatusResponse{
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
