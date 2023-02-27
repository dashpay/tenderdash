package blocksync

import (
	"context"
	"fmt"
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/tendermint/tendermint/internal/p2p"
	p2pmocks "github.com/tendermint/tendermint/internal/p2p/mocks"
	"github.com/tendermint/tendermint/internal/state/mocks"
	tmrequire "github.com/tendermint/tendermint/internal/test/require"
	"github.com/tendermint/tendermint/libs/log"
	bcproto "github.com/tendermint/tendermint/proto/tendermint/blocksync"
	"github.com/tendermint/tendermint/types"
)

type BlockP2PMessageHandlerTestSuite struct {
	suite.Suite

	logger         log.Logger
	fakeStore      *mocks.BlockStore
	fakePeerAdder  *mockPeerAdder
	fakeP2PChannel *p2pmocks.Channel
	fakeChannel    *Channel
	handler        *blockP2PMessageHandler
}

func TestBlockP2PMessageHandler(t *testing.T) {
	suite.Run(t, new(BlockP2PMessageHandlerTestSuite))
}

func (suite *BlockP2PMessageHandlerTestSuite) SetupTest() {
	suite.logger = log.NewTestingLogger(suite.T())
	suite.fakeStore = mocks.NewBlockStore(suite.T())
	suite.fakePeerAdder = newMockPeerAdder(suite.T())
	suite.fakeP2PChannel = p2pmocks.NewChannel(suite.T())
	suite.fakeChannel = NewChannel(suite.fakeP2PChannel)
	suite.handler = newBlockMessageHandler(suite.logger, suite.fakeStore, suite.fakePeerAdder)

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
					On("Send", ctx, p2p.Envelope{
						To:      peerID,
						Message: tc.wantResp,
					}).
					Once().
					Return(nil)
			}
			err := suite.handleMessage(ctx, blockRequestH1001, peerID)
			tmrequire.Error(suite.T(), tc.wantErr, err)
		})
	}
}

func (suite *BlockP2PMessageHandlerTestSuite) TestHandleBlockResponse() {
	ctx := context.Background()
	const H1001 = int64(1001)
	blockH1001 := types.Block{Header: types.Header{Height: H1001}}
	protoBlockH1001, err := blockH1001.ToProto()
	suite.Require().NoError(err)
	commit1001 := types.Commit{Height: H1001}
	protoCommit := commit1001.ToProto()
	peerID := types.NodeID("peer")
	reqID := makeGetBlockReqID(H1001, peerID)
	resultCh := suite.fakeChannel.addPending(reqID)
	blockResponse := bcproto.BlockResponse{
		Block:  protoBlockH1001,
		Commit: protoCommit,
	}
	testCases := []struct {
		peerID     types.NodeID
		wantResult *bcproto.BlockResponse
		wantErr    string
	}{
		{
			peerID:  "peer0",
			wantErr: "cannot resolve a result",
		},
		{
			peerID:     peerID,
			wantResult: &blockResponse,
		},
	}
	for i, tc := range testCases {
		suite.Run(fmt.Sprintf("%d", i), func() {
			err := suite.handleMessage(ctx, &blockResponse, tc.peerID)
			tmrequire.Error(suite.T(), tc.wantErr, err)
			if tc.wantResult == nil {
				suite.Require().Len(resultCh, 0)
				return
			}
			suite.Require().Len(resultCh, 1)
			res := <-resultCh
			resp := res.Value.(*bcproto.BlockResponse)
			suite.Require().Equal(tc.wantResult, resp)
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
				envl := p2p.Envelope{
					To:      peerID,
					Message: tc.wantResp,
				}
				suite.fakeP2PChannel.On("Send", ctx, envl).Once().Return(nil)
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
	return suite.handler.Handle(ctx, suite.fakeChannel, &p2p.Envelope{
		From:      peerID,
		Message:   msg,
		ChannelID: BlockSyncChannel,
	})
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
