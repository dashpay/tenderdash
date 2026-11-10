package blocksync

import (
	"context"
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	p2pmocks "github.com/dashpay/tenderdash/internal/p2p/mocks"
	"github.com/dashpay/tenderdash/internal/state/mocks"
	statefactory "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/store"
	"github.com/dashpay/tenderdash/internal/test/factory"
	dbm "github.com/dashpay/tenderdash/libs/db"
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
					On("LoadBlockMeta", H1001).
					Once().
					Return(nil)
			},
			wantResp: &bcproto.NoBlockResponse{Height: H1001},
		},
		{
			mockFn: func() {
				suite.expectStoredBlock(H1001, 1)
				suite.fakeStore.
					On("LoadSeenCommitAt", H1001).
					Once().
					Return(nil)
			},
		},
		{
			mockFn: func() {
				suite.expectStoredBlock(H1001, 1)
				suite.fakeStore.
					On("LoadSeenCommitAt", H1001).
					Once().
					Return(&commit1001)
			},
			wantResp: &bcproto.BlockResponse{
				Block:  suite.storedBlockBytes(H1001),
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

// expectStoredBlock makes the store serve the block at height, in one part,
// times times. It returns the call that loads the block's meta.
func (suite *BlockP2PMessageHandlerTestSuite) expectStoredBlock(height int64, times int) *mock.Call {
	blockBytes := suite.storedBlockBytes(height)
	meta := &types.BlockMeta{
		BlockID:   types.BlockID{PartSetHeader: types.PartSetHeader{Total: 1}},
		BlockSize: len(blockBytes),
	}
	suite.fakeStore.On("LoadBlockPart", height, 0).Times(times).Return(&types.Part{Bytes: blockBytes})
	return suite.fakeStore.On("LoadBlockMeta", height).Times(times).Return(meta)
}

// storedBlockBytes returns the bytes of the block expectStoredBlock stores at
// height.
func (suite *BlockP2PMessageHandlerTestSuite) storedBlockBytes(height int64) []byte {
	return blockBytes(suite.T(), &types.Block{Header: types.Header{Height: height}})
}

func (suite *BlockP2PMessageHandlerTestSuite) TestServeBlockRequestReportsMissingCommit() {
	suite.handler.handlePeerUpdate(p2p.PeerUpdate{NodeID: "peer", Status: p2p.PeerStatusUp, ConnID: 1})
	suite.expectStoredBlock(1001, 1)
	suite.fakeStore.On("LoadSeenCommitAt", int64(1001)).Once().Return(nil)

	_, _, err := suite.handler.prepareBlockResponse(blockResponseJob{
		ctx: context.Background(),
		envelope: p2p.Envelope{
			From:    types.NodeID("peer"),
			ConnID:  1,
			Message: &bcproto.BlockRequest{Height: 1001},
		},
	})
	suite.Require().ErrorIs(err, errStoredBlockMissingCommit)
}

func (suite *BlockP2PMessageHandlerTestSuite) TestBlockResponseBackpressureAppliesBeforeLoad() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peerID := types.NodeID("slow-peer")
	commit := &types.Commit{Height: 1001}
	suite.expectStoredBlock(1001, 3)
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

	commit := &types.Commit{Height: 1001}
	totalRequests := maxInFlightBlockResponses + 1
	suite.expectStoredBlock(1001, totalRequests)
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
				From:   types.NodeID(fmt.Sprintf("peer-%d", i/maxPendingRequestsPerPeer)),
				ConnID: 1,
			},
		})
	}
	handler.enqueueBlockResponse(blockResponseJob{
		envelope: p2p.Envelope{From: types.NodeID("new-peer"), ConnID: 1},
	})
	suite.Equal(maxQueuedBlockResponses, handler.queuedResponses)
	suite.Len(handler.responseQueues["new-peer"], 1)
}

func (suite *BlockP2PMessageHandlerTestSuite) TestDisconnectedPeerQueueIsPurgedBeforeLoadingBlocks() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peerID := types.NodeID("disconnecting-peer")
	const connID = uint64(7)
	commit := &types.Commit{Height: 1001}
	suite.expectStoredBlock(1001, 1)
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
	suite.expectStoredBlock(1001, 1).
		Run(func(mock.Arguments) {
			close(started)
			<-release
		})

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
						return peerData.height == tc.wantHeight && peerData.base == tc.wantBase &&
							peerData.connID == 1
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
	suite.handler.handlePeerUpdate(p2p.PeerUpdate{NodeID: peerID, Status: p2p.PeerStatusUp, ConnID: 1})
	return suite.handler.Handle(ctx, suite.fakeClient, &p2p.Envelope{
		Attributes: map[string]string{
			client.RequestIDAttribute: suite.reqID,
		},
		From:      peerID,
		ConnID:    1,
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

func (suite *BlockP2PMessageHandlerTestSuite) TestRequestWaitsForConnectionUpdate() {
	peerID := types.NodeID("early-peer")
	suite.fakeStore.On("LoadBlockMeta", int64(1001)).Once().Return(nil)
	delivered := make(chan struct{}, 1)
	suite.fakeP2PChannel.On("Send", mock.Anything, mock.Anything).Once().Run(func(mock.Arguments) { delivered <- struct{}{} }).Return(nil)
	suite.Require().NoError(suite.handler.Handle(context.Background(), suite.fakeClient, &p2p.Envelope{
		From: peerID, ConnID: 9, ChannelID: p2p.BlockSyncChannel,
		Attributes: map[string]string{client.RequestIDAttribute: suite.reqID},
		Message:    &bcproto.BlockRequest{Height: 1001},
	}))
	suite.handler.responseMtx.Lock()
	suite.Equal(1, suite.handler.queuedResponses)
	suite.handler.responseMtx.Unlock()
	suite.handler.handlePeerUpdate(p2p.PeerUpdate{NodeID: peerID, Status: p2p.PeerStatusUp, ConnID: 9})
	select {
	case <-delivered:
	case <-time.After(time.Second):
		suite.Fail("request preceding peer update was lost")
	}
}

func (suite *BlockP2PMessageHandlerTestSuite) TestUnidentifiedConnectionIsRejected() {
	suite.False(suite.handler.connectionIsCurrent("unknown-peer", 0))
}

func (suite *BlockP2PMessageHandlerTestSuite) TestReconnectRequestSurvivesEarlierDisconnectUpdate() {
	h := &blockP2PMessageHandler{
		logger: suite.logger, responseQueues: make(map[types.NodeID][]blockResponseJob),
		activePeers: make(map[types.NodeID]bool), responseWake: make(chan struct{}, 1),
		peerConnections: make(map[types.NodeID]peerConnectionState),
	}
	peer := types.NodeID("reconnected-peer")
	h.handlePeerUpdate(p2p.PeerUpdate{NodeID: peer, Status: p2p.PeerStatusUp, ConnID: 10})
	for _, id := range []uint64{11, 10} {
		h.enqueueBlockResponse(blockResponseJob{envelope: p2p.Envelope{From: peer, ConnID: id}})
	}
	h.handlePeerUpdate(p2p.PeerUpdate{NodeID: peer, Status: p2p.PeerStatusDown})
	h.enqueueBlockResponse(blockResponseJob{envelope: p2p.Envelope{From: peer, ConnID: 10}})
	suite.Equal(1, h.queuedResponses)
	_, _, ready := h.nextBlockResponse()
	suite.False(ready)
	h.handlePeerUpdate(p2p.PeerUpdate{NodeID: peer, Status: p2p.PeerStatusUp, ConnID: 11})
	gotPeer, job, ready := h.nextBlockResponse()
	suite.True(ready)
	suite.Equal(peer, gotPeer)
	suite.Equal(uint64(11), job.envelope.ConnID)
	suite.Zero(h.queuedResponses)
}

// TestServeBlockAsCommitted checks that a block is served as the bytes of the
// parts it was stored with, which are the parts it was committed with, and that
// cutting them the standard way rebuilds the stored part set.
func TestServeBlockAsCommitted(t *testing.T) {
	ctx := context.Background()
	valSet, privVals := factory.MockValidatorSet()
	state := fakeInitialState(valSet)
	blocks := statefactory.MakeBlocks(ctx, t, 5, &state, privVals, 1)
	blockStore := store.NewBlockStore(dbm.NewMemDB())

	const smallPartSize = 100
	standard := blockBytes(t, blocks[0])
	unknownField := withUnknownField(t, blocks[1])
	smallParts := blockBytes(t, blocks[2])
	testCases := []struct {
		name  string
		parts *types.PartSet
		bytes []byte
		// whether the standard cut of the bytes is the stored part set
		standardCut bool
	}{
		{"standard", types.NewPartSetFromData(standard, types.BlockPartSizeBytes), standard, true},
		{"unknown field", types.NewPartSetFromData(unknownField, types.BlockPartSizeBytes), unknownField, true},
		// served as stored; block sync does not rebuild a cut other than the
		// standard one, so a syncing node rejects it against the commit
		{"small parts", types.NewPartSetFromData(smallParts, smallPartSize), smallParts, false},
	}
	for i, tc := range testCases {
		blockStore.SaveBlock(blocks[i], tc.parts, blocks[i+1].LastCommit)
	}
	// a header stored without its block, as state sync stores the one it
	// restores the state at
	header := blocks[3]
	require.NoError(t, blockStore.SaveSignedHeader(
		&types.SignedHeader{Header: &header.Header, Commit: blocks[4].LastCommit},
		blocks[4].LastCommit.BlockID,
	))

	handler := newBlockP2PMessageHandler(ctx, log.NewNopLogger(), blockStore, newMockPeerAdder(t))
	t.Cleanup(handler.stop)
	handler.handlePeerUpdate(p2p.PeerUpdate{NodeID: "peer", Status: p2p.PeerStatusUp, ConnID: 1})
	request := func(height int64) proto.Message {
		msg, _, err := handler.prepareBlockResponse(blockResponseJob{
			ctx:      ctx,
			envelope: p2p.Envelope{From: "peer", ConnID: 1, Message: &bcproto.BlockRequest{Height: height}},
		})
		require.NoError(t, err)
		return msg
	}

	for i, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msg, ok := request(blocks[i].Height).(*bcproto.BlockResponse)
			require.True(t, ok)
			require.Equal(t, tc.bytes, msg.Block)

			resp, err := BlockResponseFromProto(msg, "peer")
			require.NoError(t, err)
			require.Equal(t, tc.bytes, resp.BlockBytes)
			require.Equal(t, blocks[i].Hash(), resp.Block.Hash())
			require.Equal(t, blocks[i+1].LastCommit.BlockID, resp.Commit.BlockID)
			cut := types.NewPartSetFromData(resp.BlockBytes, types.BlockPartSizeBytes)
			require.Equal(t, tc.standardCut, tc.parts.Header().Equals(cut.Header()))
		})
	}
	require.Equal(t, &bcproto.NoBlockResponse{Height: header.Height}, request(header.Height))
}

// TestBlockResponseWireCompatible checks that a BlockResponse encodes as it did
// when its block field was an embedded tendermint.types.Block, so peers on
// either side of the change read what the other sends.
func TestBlockResponseWireCompatible(t *testing.T) {
	ctx := context.Background()
	valSet, privVals := factory.MockValidatorSet()
	state := fakeInitialState(valSet)
	blocks := statefactory.MakeBlocks(ctx, t, 2, &state, privVals, 1)
	block, commit := blocks[0], blocks[1].LastCommit.ToProto()
	pbb, err := block.ToProto()
	require.NoError(t, err)
	commitBytes, err := proto.Marshal(commit)
	require.NoError(t, err)

	embedded, err := proto.Marshal(pbb)
	require.NoError(t, err)
	// an embedded message is its encoding, length-delimited
	older := appendBytesField(appendBytesField(nil, 1, embedded), 2, commitBytes)

	newer, err := proto.Marshal(&bcproto.BlockResponse{Block: blockBytes(t, block), Commit: commit})
	require.NoError(t, err)
	require.Equal(t, older, newer)

	var decoded bcproto.BlockResponse
	require.NoError(t, proto.Unmarshal(older, &decoded))
	resp, err := BlockResponseFromProto(&decoded, "peer")
	require.NoError(t, err)
	require.Equal(t, block.Hash(), resp.Block.Hash())
	require.Equal(t, embedded, resp.BlockBytes)
}

// appendBytesField appends a length-delimited field to serialized protobuf.
func appendBytesField(bz []byte, number byte, value []byte) []byte {
	bz = append(bz, number<<3|2)
	bz = binary.AppendUvarint(bz, uint64(len(value)))
	return append(bz, value...)
}

// TestBlockResponseDoesNotRetainOverwrittenBlockField checks that a repeated
// block field does not leave the allocation of an earlier, larger occurrence
// held behind the small one that is decoded and charged to the backlog.
func TestBlockResponseDoesNotRetainOverwrittenBlockField(t *testing.T) {
	ctx := context.Background()
	valSet, privVals := factory.MockValidatorSet()
	state := fakeInitialState(valSet)
	blocks := statefactory.MakeBlocks(ctx, t, 2, &state, privVals, 1)
	bz := blockBytes(t, blocks[0])
	commitBytes, err := proto.Marshal(blocks[1].LastCommit.ToProto())
	require.NoError(t, err)
	const overwritten = 1 << 20
	raw := appendBytesField(appendBytesField(appendBytesField(nil, 1, make([]byte, overwritten)), 1, bz), 2, commitBytes)

	var decoded bcproto.BlockResponse
	require.NoError(t, proto.Unmarshal(raw, &decoded))
	require.GreaterOrEqual(t, cap(decoded.Block), overwritten, "the decoder is expected to reuse the larger allocation")

	resp, err := BlockResponseFromProto(&decoded, "peer")
	require.NoError(t, err)
	require.Equal(t, bz, resp.BlockBytes)
	require.Equal(t, len(bz), resp.Size)
	require.LessOrEqual(t, cap(resp.BlockBytes), 2*len(bz))
}
