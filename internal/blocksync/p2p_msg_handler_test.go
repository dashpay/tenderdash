package blocksync

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/dashpay/tenderdash/config"
	tmcrypto "github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/merkle"
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
	"github.com/dashpay/tenderdash/proto/tendermint/crypto"
	p2pproto "github.com/dashpay/tenderdash/proto/tendermint/p2p"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
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
	suite.fakeStore.On("LoadBlockPart", height, 0).Times(times).Return(&types.Part{Bytes: blockBytes, Proof: merkle.Proof{Total: 1}})
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

// TestServeBlockAsCommitted checks that a block is served as the parts it was
// stored with, which are the parts it was committed with, and that the part set
// rebuilt from the response is the stored one.
func TestServeBlockAsCommitted(t *testing.T) {
	ctx := context.Background()
	valSet, privVals := factory.MockValidatorSet()
	state := fakeInitialState(valSet)
	blocks := statefactory.MakeBlocks(ctx, t, 6, &state, privVals, 1)
	blockStore := store.NewBlockStore(dbm.NewMemDB())

	const smallPartSize = 100
	standard := blockBytes(t, blocks[0])
	unknownField := withUnknownField(t, blocks[1])
	smallParts := blockBytes(t, blocks[2])
	largerTree := withProofOfLargerTree(t, blocks[3])
	testCases := []struct {
		parts         *types.PartSet
		wantBytes     []byte
		wantPartSizes []uint32
		wantProofs    bool
	}{
		{types.NewPartSetFromData(standard, types.BlockPartSizeBytes), standard, nil, false},
		{types.NewPartSetFromData(unknownField, types.BlockPartSizeBytes), unknownField, nil, false},
		{
			types.NewPartSetFromData(smallParts, smallPartSize),
			smallParts,
			smallPartSizes(len(smallParts), smallPartSize),
			false,
		},
		{largerTree, blockBytes(t, blocks[3]), []uint32{uint32(largerTree.ByteSize())}, true},
	}
	for i, tc := range testCases {
		blockStore.SaveBlock(blocks[i], tc.parts, blocks[i+1].LastCommit)
	}
	// a header stored without its block, as state sync stores the one it
	// restores the state at
	header := blocks[4]
	require.NoError(t, blockStore.SaveSignedHeader(
		&types.SignedHeader{Header: &header.Header, Commit: blocks[5].LastCommit},
		blocks[5].LastCommit.BlockID,
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
		height := blocks[i].Height
		t.Run(fmt.Sprintf("height %d", height), func(t *testing.T) {
			msg, ok := request(height).(*bcproto.BlockResponse)
			require.True(t, ok)
			require.Equal(t, tc.wantBytes, msg.Block)
			require.Equal(t, tc.wantPartSizes == nil, len(msg.PartLayout) == 0)

			resp, err := BlockResponseFromProto(msg, "peer")
			require.NoError(t, err)
			require.Equal(t, tc.wantPartSizes, resp.Parts.Sizes)
			require.Equal(t, tc.wantProofs, len(resp.Parts.Proofs) > 0)
			require.Equal(t, blocks[i].Hash(), resp.Block.Hash())
			require.Equal(t, blocks[i+1].LastCommit.BlockID, resp.Commit.BlockID)
			parts, err := resp.Parts.partSet(tc.parts.Header())
			require.NoError(t, err)
			require.Equal(t, tc.parts.Header(), parts.Header())
		})
	}
	require.Equal(t, &bcproto.NoBlockResponse{Height: header.Height}, request(header.Height))
}

// smallPartSizes returns the sizes of the parts types.NewPartSetFromData cuts
// size bytes into with parts of partSize.
func smallPartSizes(size, partSize int) []uint32 {
	var sizes []uint32
	for ; size > partSize; size -= partSize {
		sizes = append(sizes, uint32(partSize))
	}
	return append(sizes, uint32(size))
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
	require.Equal(t, CommittedParts{Bytes: embedded}, resp.Parts)
}

// appendBytesField appends a length-delimited field to serialized protobuf.
func appendBytesField(bz []byte, number byte, value []byte) []byte {
	bz = append(bz, number<<3|2)
	bz = binary.AppendUvarint(bz, uint64(len(value)))
	return append(bz, value...)
}

// partLayout returns a serialized BlockPartLayout.
func partLayout(t *testing.T, sizes []uint32, proofs []crypto.Proof) []byte {
	t.Helper()
	bz, err := proto.Marshal(&bcproto.BlockPartLayout{Sizes: sizes, Proofs: proofs})
	require.NoError(t, err)
	return bz
}

// hiddenProofsBehindPackedSize returns a layout that opens with a packed size
// whose varint does not end inside its field. The generated decoder reads on
// past the field, so a scan that took the field's length at face value would
// see one unknown field where the decoder finds that many empty proofs.
func hiddenProofsBehindPackedSize(proofs int) []byte {
	return bytes.Join([][]byte{
		{0x0a, 0x01, 0x80, 0x1a, 0x82, 0x80, 0x80, 0x01, 0x00, 0x18, 0x80, 0x00},
		bytes.Repeat([]byte{2<<3 | 2, 0}, proofs+1),
	}, nil)
}

// hiddenAuntsBehindPackedSize is hiddenProofsBehindPackedSize with chunks that
// each hide a proof of seven empty aunts.
func hiddenAuntsBehindPackedSize(chunks int) []byte {
	chunk := append([]byte{0x0a, 0x01, 0x80, 0x1a, 0x12, 0x11, 0x08, 0x80, 0x01},
		bytes.Repeat([]byte{4<<3 | 2, 0}, 7)...)
	return bytes.Repeat(chunk, chunks+1)
}

// TestBlockResponseFromProtoRejectsMalformedParts checks that part metadata a
// peer sent that cannot describe its block is refused while decoding the
// response, before it can be held waiting for a lower height.
func TestBlockResponseFromProtoRejectsMalformedParts(t *testing.T) {
	ctx := context.Background()
	valSet, privVals := factory.MockValidatorSet()
	state := fakeInitialState(valSet)
	blocks := statefactory.MakeBlocks(ctx, t, 2, &state, privVals, 1)
	bz := blockBytes(t, blocks[0])
	size := uint32(len(bz))
	proof := crypto.Proof{Total: 1, LeafHash: make([]byte, tmcrypto.HashSize)}
	tooManyAunts := proof
	tooManyAunts.Aunts = make([][]byte, merkle.MaxAunts+1)
	for i := range tooManyAunts.Aunts {
		tooManyAunts.Aunts[i] = make([]byte, tmcrypto.HashSize)
	}
	longAunt := proof
	longAunt.Aunts = [][]byte{make([]byte, tmcrypto.HashSize+1)}
	tooManyParts := int(types.MaxBlockPartsCount) + 1
	testCases := map[string]struct {
		layout  []byte
		wantErr string
	}{
		"layout too big":  {make([]byte, p2p.BlockPartLayoutMaxBytes+1), "part layout too big"},
		"not a layout":    {[]byte{0x0a, 0x05}, "unexpected EOF"},
		"too many parts":  {partLayout(t, make([]uint32, tooManyParts), nil), "too many parts"},
		"unpacked parts":  {bytes.Repeat([]byte{1 << 3, 0}, tooManyParts), "too many parts"},
		"too many proofs": {bytes.Repeat([]byte{2<<3 | 2, 0}, tooManyParts), "too many proofs"},
		"truncated packed size hiding proofs": {
			hiddenProofsBehindPackedSize(int(types.MaxBlockPartsCount)), "unexpected EOF",
		},
		"truncated packed size hiding aunts": {
			hiddenAuntsBehindPackedSize(int(types.MaxBlockPartsCount)), "unexpected EOF",
		},
		"unknown field":        {[]byte{3 << 3, 0}, "unexpected field 3"},
		"group":                {[]byte{3<<3 | 3, 3<<3 | 4}, "unexpected field 3"},
		"proofs of wrong type": {[]byte{2 << 3, 0}, "unexpected field 2"},
		"unknown proof field":  {[]byte{2<<3 | 2, 2, 5 << 3, 0}, "unexpected field 5"},
		"proof total of wrong type": {
			[]byte{2<<3 | 2, 2, 1<<3 | 2, 0}, "unexpected field 1",
		},
		"too many aunts":     {partLayout(t, []uint32{size}, []crypto.Proof{tooManyAunts}), "too many aunts"},
		"aunt too long":      {partLayout(t, []uint32{size}, []crypto.Proof{longAunt}), "hash too long"},
		"part too big":       {partLayout(t, []uint32{types.BlockPartSizeBytes + 1}, nil), "too big"},
		"sizes short":        {partLayout(t, []uint32{size - 1}, nil), "part sizes cover"},
		"sizes beyond block": {partLayout(t, []uint32{size + 1}, nil), "exceed"},
		"proofs without sizes": {
			partLayout(t, nil, []crypto.Proof{proof}), "1 proofs for 0 parts",
		},
		"too few proofs": {
			partLayout(t, []uint32{size - 1, 1}, []crypto.Proof{proof}), "1 proofs for 2 parts",
		},
		"proof of another part": {
			partLayout(t, []uint32{size}, []crypto.Proof{{Total: 1, Index: 1, LeafHash: proof.LeafHash}}), "has index 1",
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			_, err := BlockResponseFromProto(&bcproto.BlockResponse{
				Block:      bz,
				Commit:     blocks[1].LastCommit.ToProto(),
				PartLayout: tc.layout,
			}, "peer")
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// TestBlockResponseSizeChargesPartMetadata checks that a response is charged to
// the backlog for its part metadata, and not only for its block.
func TestBlockResponseSizeChargesPartMetadata(t *testing.T) {
	ctx := context.Background()
	valSet, privVals := factory.MockValidatorSet()
	state := fakeInitialState(valSet)
	blocks := statefactory.MakeBlocks(ctx, t, 2, &state, privVals, 1)
	bz := blockBytes(t, blocks[0])
	proof := crypto.Proof{Total: 2, LeafHash: make([]byte, tmcrypto.HashSize)}
	layout := partLayout(t, []uint32{uint32(len(bz))}, []crypto.Proof{proof})
	resp, err := BlockResponseFromProto(&bcproto.BlockResponse{
		Block:      bz,
		Commit:     blocks[1].LastCommit.ToProto(),
		PartLayout: layout,
	}, "peer")
	require.NoError(t, err)
	require.Equal(t, len(bz)+len(layout), resp.Size)
}

// TestPartLayoutIsOpaqueAtEnvelopeBoundary checks that part metadata costs no
// per-entry allocation when a message is received, which happens before it is
// matched to a request, nor when the layout is refused. Empty proofs are the
// smallest encoding of the most entries.
func TestPartLayoutIsOpaqueAtEnvelopeBoundary(t *testing.T) {
	const (
		allowedAllocs = 32
		entries       = int(types.MaxBlockPartsCount)
	)
	testCases := map[string]struct {
		layout  []byte
		wantErr string
	}{
		"empty proofs":                {bytes.Repeat([]byte{2<<3 | 2, 0}, entries+1), "too many proofs"},
		"proofs behind a packed size": {hiddenProofsBehindPackedSize(1 << 20), "unexpected EOF"},
		"aunts behind a packed size":  {hiddenAuntsBehindPackedSize(entries), "unexpected EOF"},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			require.LessOrEqual(t, len(tc.layout), p2p.BlockPartLayoutMaxBytes)
			// block_response is field 9 of the envelope, part_layout field 3 of the response
			raw := appendBytesField(nil, 9, appendBytesField(nil, 3, tc.layout))

			require.Less(t, testing.AllocsPerRun(10, func() {
				var envelope p2pproto.Envelope
				_ = proto.Unmarshal(raw, &envelope)
			}), float64(allowedAllocs), "decoding the envelope must not allocate per layout entry")

			var envelope p2pproto.Envelope
			require.NoError(t, proto.Unmarshal(raw, &envelope))
			msg, err := envelope.Unwrap()
			require.NoError(t, err)
			resp, ok := msg.(*bcproto.BlockResponse)
			require.True(t, ok)
			require.Equal(t, tc.layout, resp.PartLayout)

			resp.Block = []byte{1}
			require.Less(t, testing.AllocsPerRun(10, func() {
				_, _ = committedPartsFromProto(resp)
			}), float64(allowedAllocs), "refusing the layout must not allocate per layout entry")
			_, err = committedPartsFromProto(resp)
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
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
	require.Equal(t, bz, resp.Parts.Bytes)
	require.Equal(t, len(bz), resp.Size)
	require.LessOrEqual(t, cap(resp.Parts.Bytes), 2*len(bz))
}

// TestBlockResponseFitsBlockSyncChannel checks that the largest block response
// a node can have to serve fits the receive capacity of the block sync channel.
func TestBlockResponseFitsBlockSyncChannel(t *testing.T) {
	const parts = int(types.MaxBlockPartsCount)
	aunts := make([][]byte, merkle.MaxAunts)
	for i := range aunts {
		aunts[i] = make([]byte, tmcrypto.HashSize)
	}
	sizes := make([]uint32, parts)
	proofs := make([]crypto.Proof, parts)
	for i := range parts {
		sizes[i] = types.BlockPartSizeBytes
		proofs[i] = crypto.Proof{Total: math.MaxInt64, Index: int64(parts), LeafHash: aunts[0], Aunts: aunts}
	}
	blockID := tmproto.BlockID{
		Hash:          make([]byte, tmcrypto.HashSize),
		PartSetHeader: tmproto.PartSetHeader{Total: uint32(parts), Hash: make([]byte, tmcrypto.HashSize)},
		StateID:       make([]byte, tmcrypto.HashSize),
	}
	layout := partLayout(t, sizes, proofs)
	require.LessOrEqual(t, len(layout), p2p.BlockPartLayoutMaxBytes)
	require.NoError(t, checkPartLayoutBounds(layout))
	envelope := &p2pproto.Envelope{Attributes: map[string]string{client.RequestIDAttribute: uuid.NewString()}}
	require.NoError(t, envelope.Wrap(&bcproto.BlockResponse{
		Block: make([]byte, types.MaxBlockSizeBytes),
		Commit: &tmproto.Commit{
			Height:                  math.MaxInt64,
			Round:                   math.MaxInt32,
			BlockID:                 blockID,
			QuorumHash:              make([]byte, tmcrypto.HashSize),
			ThresholdBlockSignature: make([]byte, 96),
			// everything a vote on the consensus vote channel can carry
			ThresholdVoteExtensions: []*tmproto.VoteExtension{{
				Extension: make([]byte, 1<<20-1024),
				Signature: make([]byte, 96),
			}},
		},
		PartLayout: layout,
	}))

	descriptor := p2p.ChannelDescriptors(config.TestConfig())[p2p.BlockSyncChannel]
	require.LessOrEqual(t, envelope.Size(), descriptor.RecvMessageCapacity)
}
