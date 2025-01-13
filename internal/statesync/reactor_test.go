package statesync

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/dashpay/dashd-go/btcjson"
	"github.com/fortytw2/leaktest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	clientmocks "github.com/dashpay/tenderdash/abci/client/mocks"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/crypto"
	dashcore "github.com/dashpay/tenderdash/dash/core"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/proxy"
	smmocks "github.com/dashpay/tenderdash/internal/state/mocks"
	"github.com/dashpay/tenderdash/internal/statesync/mocks"
	"github.com/dashpay/tenderdash/internal/store"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/light/provider"
	ssproto "github.com/dashpay/tenderdash/proto/tendermint/statesync"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

const (
	chainID        = "test-chain"
	llmqType       = btcjson.LLMQType_5_60
	testAppVersion = 9
)

var (
	m = PrometheusMetrics(config.TestConfig().Instrumentation.Namespace)
)

type reactorTestSuite struct {
	reactor *Reactor
	syncer  *syncer

	conn          *clientmocks.Client
	stateProvider *mocks.StateProvider

	snapshotChannel   p2p.Channel
	snapshotInCh      chan p2p.Envelope
	snapshotOutCh     chan p2p.Envelope
	snapshotPeerErrCh chan p2p.PeerError

	chunkChannel   p2p.Channel
	chunkInCh      chan p2p.Envelope
	chunkOutCh     chan p2p.Envelope
	chunkPeerErrCh chan p2p.PeerError

	blockChannel   p2p.Channel
	blockInCh      chan p2p.Envelope
	blockOutCh     chan p2p.Envelope
	blockPeerErrCh chan p2p.PeerError

	paramsChannel   p2p.Channel
	paramsInCh      chan p2p.Envelope
	paramsOutCh     chan p2p.Envelope
	paramsPeerErrCh chan p2p.PeerError

	peerUpdateCh chan p2p.PeerUpdate
	peerUpdates  *p2p.PeerUpdates

	stateStore *smmocks.Store
	blockStore *store.BlockStore

	dashcoreClient dashcore.Client
	privVal        *types.MockPV
}

func setup(
	ctx context.Context,
	t *testing.T,
	conn *clientmocks.Client,
	stateProvider *mocks.StateProvider,
	chBuf uint,
) *reactorTestSuite {
	t.Helper()

	if conn == nil {
		conn = clientmocks.NewClient(t)
	}

	rts := &reactorTestSuite{
		snapshotInCh:      make(chan p2p.Envelope, chBuf),
		snapshotOutCh:     make(chan p2p.Envelope, chBuf),
		snapshotPeerErrCh: make(chan p2p.PeerError, chBuf),
		chunkInCh:         make(chan p2p.Envelope, chBuf),
		chunkOutCh:        make(chan p2p.Envelope, chBuf),
		chunkPeerErrCh:    make(chan p2p.PeerError, chBuf),
		blockInCh:         make(chan p2p.Envelope, chBuf),
		blockOutCh:        make(chan p2p.Envelope, chBuf),
		blockPeerErrCh:    make(chan p2p.PeerError, chBuf),
		paramsInCh:        make(chan p2p.Envelope, chBuf),
		paramsOutCh:       make(chan p2p.Envelope, chBuf),
		paramsPeerErrCh:   make(chan p2p.PeerError, chBuf),
		conn:              conn,
		stateProvider:     stateProvider,
	}

	rts.peerUpdateCh = make(chan p2p.PeerUpdate, chBuf)
	rts.peerUpdates = p2p.NewPeerUpdates(rts.peerUpdateCh, int(chBuf), "statesync")

	rts.snapshotChannel = p2p.NewChannel(
		SnapshotChannel,
		"snapshot",
		rts.snapshotInCh,
		rts.snapshotOutCh,
		rts.snapshotPeerErrCh,
	)

	rts.chunkChannel = p2p.NewChannel(
		ChunkChannel,
		"chunk",
		rts.chunkInCh,
		rts.chunkOutCh,
		rts.chunkPeerErrCh,
	)

	rts.blockChannel = p2p.NewChannel(
		LightBlockChannel,
		"lightblock",
		rts.blockInCh,
		rts.blockOutCh,
		rts.blockPeerErrCh,
	)

	rts.paramsChannel = p2p.NewChannel(
		ParamsChannel,
		"params",
		rts.paramsInCh,
		rts.paramsOutCh,
		rts.paramsPeerErrCh,
	)

	rts.stateStore = smmocks.NewStore(t)
	rts.blockStore = store.NewBlockStore(dbm.NewMemDB())

	cfg := config.DefaultStateSyncConfig()

	rts.privVal = types.NewMockPV()
	rts.dashcoreClient = dashcore.NewMockClient(chainID, llmqType, rts.privVal, false)

	chCreator := func(_ctx context.Context, desc *p2p.ChannelDescriptor) (p2p.Channel, error) {
		switch desc.ID {
		case SnapshotChannel:
			return rts.snapshotChannel, nil
		case ChunkChannel:
			return rts.chunkChannel, nil
		case LightBlockChannel:
			return rts.blockChannel, nil
		case ParamsChannel:
			return rts.paramsChannel, nil
		default:
			return nil, fmt.Errorf("invalid channel; %v", desc.ID)
		}
	}

	logger := log.NewTestingLogger(t)

	rts.reactor = NewReactor(
		factory.DefaultTestChainID,
		1,
		*cfg,
		logger.With("component", "reactor"),
		conn,
		chCreator,
		func(context.Context, string) *p2p.PeerUpdates { return rts.peerUpdates },
		rts.stateStore,
		rts.blockStore,
		"",
		m,
		nil,   // eventbus can be nil
		nil,   // post-sync-hook
		false, // run Sync during Start()
		rts.dashcoreClient,
		nil,
	)

	rts.syncer = &syncer{
		logger:        logger,
		stateProvider: stateProvider,
		conn:          conn,
		snapshots:     newSnapshotPool(),
		snapshotCh:    rts.snapshotChannel,
		chunkCh:       rts.chunkChannel,
		tempDir:       t.TempDir(),
		fetchers:      cfg.Fetchers,
		retryTimeout:  cfg.ChunkRequestTimeout,
		metrics:       rts.reactor.metrics,
	}

	ctx, cancel := context.WithCancel(ctx)

	require.NoError(t, rts.reactor.Start(ctx))
	require.True(t, rts.reactor.IsRunning())

	t.Cleanup(cancel)
	t.Cleanup(rts.reactor.Wait)
	t.Cleanup(leaktest.Check(t))

	return rts
}

func TestReactor_Sync(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const snapshotHeight = 7
	rts := setup(ctx, t, nil, nil, 100)
	chain := buildLightBlockChain(ctx, t, 1, 10, time.Now(), rts.privVal)
	// app accepts any snapshot
	rts.conn.
		On("OfferSnapshot", ctx, mock.IsType(&abci.RequestOfferSnapshot{})).
		Return(&abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_ACCEPT}, nil)

	rts.conn.
		On("ApplySnapshotChunk", ctx, mock.IsType(&abci.RequestApplySnapshotChunk{})).
		Once().
		Return(&abci.ResponseApplySnapshotChunk{Result: abci.ResponseApplySnapshotChunk_COMPLETE_SNAPSHOT}, nil)

	// app query returns valid state app hash
	rts.conn.
		On("Info", mock.Anything, &proxy.RequestInfo).
		Return(&abci.ResponseInfo{
			AppVersion:       testAppVersion,
			LastBlockHeight:  snapshotHeight,
			LastBlockAppHash: chain[snapshotHeight+1].AppHash,
		}, nil)

	// store accepts state and validator sets
	rts.stateStore.
		On("Bootstrap", mock.AnythingOfType("state.State")).
		Return(nil)
	rts.stateStore.
		On("SaveValidatorSets",
			mock.AnythingOfType("int64"),
			mock.AnythingOfType("int64"),
			mock.AnythingOfType("*types.ValidatorSet")).
		Return(nil)

	closeCh := make(chan struct{})
	defer close(closeCh)

	appHash := []byte{1, 2, 3}

	go handleLightBlockRequests(ctx, t, chain, rts.blockOutCh, rts.blockInCh, closeCh, 0, 0)
	go graduallyAddPeers(ctx, t, rts.peerUpdateCh, closeCh, 1*time.Second)
	go handleSnapshotRequests(ctx, t, rts.snapshotOutCh, rts.snapshotInCh, closeCh, []snapshot{
		{
			Height:  uint64(snapshotHeight),
			Version: 1,
			Hash:    appHash,
		},
	})

	go handleChunkRequests(ctx, t, rts.chunkOutCh, rts.chunkInCh, closeCh, appHash, []byte("abc"))

	go handleConsensusParamsRequest(ctx, t, rts.paramsOutCh, rts.paramsInCh, closeCh)

	// update the config to use the p2p provider
	rts.reactor.cfg.UseP2P = true
	rts.reactor.cfg.TrustHeight = 1
	rts.reactor.cfg.TrustHash = fmt.Sprintf("%X", chain[1].Hash())
	rts.reactor.cfg.DiscoveryTime = 1 * time.Second

	// Run state sync
	_, err := rts.reactor.Sync(ctx)
	require.NoError(t, err)
}

func TestReactor_ChunkRequest_InvalidRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rts := setup(ctx, t, nil, nil, 2)

	rts.chunkInCh <- p2p.Envelope{
		From:      types.NodeID("aa"),
		ChannelID: ChunkChannel,
		Message:   &ssproto.SnapshotsRequest{},
	}

	response := <-rts.chunkPeerErrCh
	require.Error(t, response.Err)
	require.Empty(t, rts.chunkOutCh)
	require.Contains(t, response.Err.Error(), "received unknown message")
	require.Equal(t, types.NodeID("aa"), response.NodeID)
}

func TestReactor_ChunkRequest(t *testing.T) {
	chunkID := []byte{1, 2, 3, 4}
	testcases := map[string]struct {
		request        *ssproto.ChunkRequest
		chunk          []byte
		expectResponse *ssproto.ChunkResponse
	}{
		"chunk is returned": {
			&ssproto.ChunkRequest{Height: 1, Version: 1, ChunkId: chunkID},
			[]byte{1, 2, 3},
			&ssproto.ChunkResponse{Height: 1, Version: 1, ChunkId: chunkID, Chunk: []byte{1, 2, 3}},
		},
		"empty chunk is returned, as empty": {
			&ssproto.ChunkRequest{Height: 1, Version: 1, ChunkId: chunkID},
			[]byte{},
			&ssproto.ChunkResponse{Height: 1, Version: 1, ChunkId: chunkID, Chunk: []byte{}},
		},
		"nil (missing) chunk is returned as missing": {
			&ssproto.ChunkRequest{Height: 1, Version: 1, ChunkId: chunkID},
			nil,
			&ssproto.ChunkResponse{Height: 1, Version: 1, ChunkId: chunkID, Missing: true},
		},
		"invalid request": {
			&ssproto.ChunkRequest{Height: 1, Version: 1, ChunkId: chunkID},
			nil,
			&ssproto.ChunkResponse{Height: 1, Version: 1, ChunkId: chunkID, Missing: true},
		},
	}

	bctx, bcancel := context.WithCancel(context.Background())
	defer bcancel()

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(bctx)
			defer cancel()

			// mock ABCI connection to return local snapshots
			conn := &clientmocks.Client{}
			conn.On("LoadSnapshotChunk", mock.Anything, &abci.RequestLoadSnapshotChunk{
				Height:  tc.request.Height,
				Version: tc.request.Version,
				ChunkId: tc.request.ChunkId,
			}).Return(&abci.ResponseLoadSnapshotChunk{Chunk: tc.chunk}, nil)

			rts := setup(ctx, t, conn, nil, 2)

			rts.chunkInCh <- p2p.Envelope{
				From:      types.NodeID("aa"),
				ChannelID: ChunkChannel,
				Message:   tc.request,
			}

			response := <-rts.chunkOutCh
			require.Equal(t, tc.expectResponse, response.Message)
			require.Empty(t, rts.chunkOutCh)

			conn.AssertExpectations(t)
		})
	}
}

func TestReactor_SnapshotsRequest_InvalidRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rts := setup(ctx, t, nil, nil, 2)

	rts.snapshotInCh <- p2p.Envelope{
		From:      types.NodeID("aa"),
		ChannelID: SnapshotChannel,
		Message:   &ssproto.ChunkRequest{},
	}

	response := <-rts.snapshotPeerErrCh
	require.Error(t, response.Err)
	require.Empty(t, rts.snapshotOutCh)
	require.Contains(t, response.Err.Error(), "received unknown message")
	require.Equal(t, types.NodeID("aa"), response.NodeID)
}

func TestReactor_SnapshotsRequest(t *testing.T) {
	testcases := map[string]struct {
		snapshots       []*abci.Snapshot
		expectResponses []*ssproto.SnapshotsResponse
	}{
		"no snapshots": {nil, []*ssproto.SnapshotsResponse{}},
		">10 unordered snapshots": {
			[]*abci.Snapshot{
				{Height: 1, Version: 2, Hash: []byte{1, 2}, Metadata: []byte{1}},
				{Height: 2, Version: 2, Hash: []byte{2, 2}, Metadata: []byte{2}},
				{Height: 3, Version: 2, Hash: []byte{3, 2}, Metadata: []byte{3}},
				{Height: 1, Version: 1, Hash: []byte{1, 1}, Metadata: []byte{4}},
				{Height: 2, Version: 1, Hash: []byte{2, 1}, Metadata: []byte{5}},
				{Height: 3, Version: 1, Hash: []byte{3, 1}, Metadata: []byte{6}},
				{Height: 1, Version: 4, Hash: []byte{1, 4}, Metadata: []byte{7}},
				{Height: 2, Version: 4, Hash: []byte{2, 4}, Metadata: []byte{8}},
				{Height: 3, Version: 4, Hash: []byte{3, 4}, Metadata: []byte{9}},
				{Height: 1, Version: 3, Hash: []byte{1, 3}, Metadata: []byte{10}},
				{Height: 2, Version: 3, Hash: []byte{2, 3}, Metadata: []byte{11}},
				{Height: 3, Version: 3, Hash: []byte{3, 3}, Metadata: []byte{12}},
			},
			[]*ssproto.SnapshotsResponse{
				{Height: 3, Version: 4, Hash: []byte{3, 4}, Metadata: []byte{9}},
				{Height: 3, Version: 3, Hash: []byte{3, 3}, Metadata: []byte{12}},
				{Height: 3, Version: 2, Hash: []byte{3, 2}, Metadata: []byte{3}},
				{Height: 3, Version: 1, Hash: []byte{3, 1}, Metadata: []byte{6}},
				{Height: 2, Version: 4, Hash: []byte{2, 4}, Metadata: []byte{8}},
				{Height: 2, Version: 3, Hash: []byte{2, 3}, Metadata: []byte{11}},
				{Height: 2, Version: 2, Hash: []byte{2, 2}, Metadata: []byte{2}},
				{Height: 2, Version: 1, Hash: []byte{2, 1}, Metadata: []byte{5}},
				{Height: 1, Version: 4, Hash: []byte{1, 4}, Metadata: []byte{7}},
				{Height: 1, Version: 3, Hash: []byte{1, 3}, Metadata: []byte{10}},
			},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for name, tc := range testcases {
		tc := tc

		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			// mock ABCI connection to return local snapshots
			conn := &clientmocks.Client{}
			conn.On("ListSnapshots", mock.Anything, &abci.RequestListSnapshots{}).Return(&abci.ResponseListSnapshots{
				Snapshots: tc.snapshots,
			}, nil)

			rts := setup(ctx, t, conn, nil, 100)

			rts.snapshotInCh <- p2p.Envelope{
				From:      types.NodeID("aa"),
				ChannelID: SnapshotChannel,
				Message:   &ssproto.SnapshotsRequest{},
			}

			if len(tc.expectResponses) > 0 {
				retryUntil(ctx, t, func() bool { return len(rts.snapshotOutCh) == len(tc.expectResponses) }, time.Second)
			}

			responses := make([]*ssproto.SnapshotsResponse, len(tc.expectResponses))
			for i := 0; i < len(tc.expectResponses); i++ {
				e := <-rts.snapshotOutCh
				responses[i] = e.Message.(*ssproto.SnapshotsResponse)
			}

			require.Equal(t, tc.expectResponses, responses)
			require.Empty(t, rts.snapshotOutCh)
		})
	}
}

func TestReactor_LightBlockResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rts := setup(ctx, t, nil, nil, 2)

	var height int64 = 10
	// generates a random header
	h := factory.MakeHeader(t, &types.Header{})
	h.Height = height
	blockID := factory.MakeBlockIDWithHash(h.Hash())

	vals, pv := types.RandValidatorSet(1)
	vote, err := factory.MakeVote(ctx, pv[0], vals, h.ChainID, 0, h.Height, 0, 2,
		blockID)
	require.NoError(t, err)

	sh := &types.SignedHeader{
		Header: h,
		Commit: &types.Commit{
			Height:                  h.Height,
			BlockID:                 blockID,
			QuorumHash:              crypto.RandQuorumHash(),
			ThresholdBlockSignature: vote.BlockSignature,
		},
	}

	lb := &types.LightBlock{
		SignedHeader: sh,
		ValidatorSet: vals,
	}

	require.NoError(t, rts.blockStore.SaveSignedHeader(sh, blockID))

	rts.stateStore.On("LoadValidators", height, mock.Anything).Return(vals, nil)

	rts.blockInCh <- p2p.Envelope{
		From:      types.NodeID("aa"),
		ChannelID: LightBlockChannel,
		Message: &ssproto.LightBlockRequest{
			Height: 10,
		},
	}
	require.Empty(t, rts.blockPeerErrCh)

	select {
	case response := <-rts.blockOutCh:
		require.Equal(t, types.NodeID("aa"), response.To)
		res, ok := response.Message.(*ssproto.LightBlockResponse)
		require.True(t, ok)
		receivedLB, err := types.LightBlockFromProto(res.LightBlock)
		require.NoError(t, err)
		require.Equal(t, lb, receivedLB)
	case <-time.After(1 * time.Second):
		t.Fatal("expected light block response")
	}
}

func TestReactor_BlockProviders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rts := setup(ctx, t, nil, nil, 2)
	rts.peerUpdateCh <- p2p.PeerUpdate{
		NodeID: "aa",
		Status: p2p.PeerStatusUp,
		Channels: p2p.ChannelIDSet{
			SnapshotChannel:   struct{}{},
			ChunkChannel:      struct{}{},
			LightBlockChannel: struct{}{},
			ParamsChannel:     struct{}{},
		},
	}
	rts.peerUpdateCh <- p2p.PeerUpdate{
		NodeID: "bb",
		Status: p2p.PeerStatusUp,
		Channels: p2p.ChannelIDSet{
			SnapshotChannel:   struct{}{},
			ChunkChannel:      struct{}{},
			LightBlockChannel: struct{}{},
			ParamsChannel:     struct{}{},
		},
	}

	closeCh := make(chan struct{})
	defer close(closeCh)

	chain := buildLightBlockChain(ctx, t, 1, 10, time.Now(), rts.privVal)
	go handleLightBlockRequests(ctx, t, chain, rts.blockOutCh, rts.blockInCh, closeCh, 0, 0)

	peers := rts.reactor.peers.All()
	require.Len(t, peers, 2)

	providers := make([]provider.Provider, len(peers))
	for idx, peer := range peers {
		providers[idx] = NewBlockProvider(peer, factory.DefaultTestChainID, rts.reactor.dispatcher)
	}

	wg := sync.WaitGroup{}

	for _, p := range providers {
		wg.Add(1)
		go func(t *testing.T, p provider.Provider) {
			defer wg.Done()
			for height := 2; height < 10; height++ {
				lb, err := p.LightBlock(ctx, int64(height))
				require.NoError(t, err)
				require.NotNil(t, lb)
				require.Equal(t, height, int(lb.Height))
			}
		}(t, p)
	}

	go func() { wg.Wait(); cancel() }()

	select {
	case <-time.After(time.Second):
		// not all of the requests to the dispatcher were responded to
		// within the timeout
		t.Fail()
	case <-ctx.Done():
	}

}

func TestReactor_StateProviderP2P(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rts := setup(ctx, t, nil, nil, 2)
	// make syncer non nil else test won't think we are state syncing
	rts.reactor.syncer = rts.syncer
	peerA := types.NodeID(strings.Repeat("a", 2*types.NodeIDByteLength))
	peerB := types.NodeID(strings.Repeat("b", 2*types.NodeIDByteLength))
	rts.peerUpdateCh <- p2p.PeerUpdate{
		NodeID: peerA,
		Status: p2p.PeerStatusUp,
	}
	rts.peerUpdateCh <- p2p.PeerUpdate{
		NodeID: peerB,
		Status: p2p.PeerStatusUp,
	}

	closeCh := make(chan struct{})
	defer close(closeCh)

	chain := buildLightBlockChain(ctx, t, 1, 10, time.Now(), rts.privVal)
	go handleLightBlockRequests(ctx, t, chain, rts.blockOutCh, rts.blockInCh, closeCh, 0, 0)
	go handleConsensusParamsRequest(ctx, t, rts.paramsOutCh, rts.paramsInCh, closeCh)

	rts.reactor.cfg.UseP2P = true
	rts.reactor.cfg.TrustHeight = 1
	rts.reactor.cfg.TrustHash = fmt.Sprintf("%X", chain[1].Hash())

	for _, p := range []types.NodeID{peerA, peerB} {
		if !rts.reactor.peers.Contains(p) {
			rts.reactor.peers.Append(p)
		}
	}
	require.True(t, rts.reactor.peers.Len() >= 2, "peer network not configured")

	ictx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	err := rts.reactor.initStateProvider(ictx, factory.DefaultTestChainID, 1, nil)
	require.NoError(t, err)

	rts.reactor.getSyncer().stateProvider = rts.reactor.stateProvider

	actx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	appHash, err := rts.reactor.stateProvider.AppHash(actx, 5)
	require.NoError(t, err)
	require.Len(t, appHash, 32)

	state, err := rts.reactor.stateProvider.State(actx, 5)
	require.NoError(t, err)
	require.Equal(t, appHash, state.LastAppHash)
	require.Equal(t, types.DefaultConsensusParams(), &state.ConsensusParams)

	commit, err := rts.reactor.stateProvider.Commit(actx, 5)
	require.NoError(t, err)
	require.Equal(t, commit.BlockID, state.LastBlockID)

	added, err := rts.reactor.getSyncer().AddSnapshot(peerA, &snapshot{
		Height: 1, Version: 2, Hash: []byte{1, 2}, Metadata: []byte{1},
	})
	require.NoError(t, err)
	require.True(t, added)
}

func TestReactor_Backfill(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		startHeight int64 = 20
		stopHeight  int64 = 10
	)
	stopTime := time.Date(2020, 1, 1, 0, 100, 0, 0, time.UTC)

	// test backfill algorithm with varying failure rates [0, 10]

	testCases := []struct {
		failureRate int
		numPeers    int
	}{
		{
			failureRate: 0,
			numPeers:    4,
		},
		{
			failureRate: 2,
			numPeers:    4,
		},
		{
			failureRate: 9,
			numPeers:    20,
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("failure rate: %d", tc.failureRate), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			t.Cleanup(leaktest.CheckTimeout(t, 1*time.Minute))
			rts := setup(ctx, t, nil, nil, 21)

			peers := genPeerIDs(tc.numPeers)
			for _, peer := range peers {
				rts.peerUpdateCh <- p2p.PeerUpdate{
					NodeID: types.NodeID(peer),
					Status: p2p.PeerStatusUp,
					Channels: p2p.ChannelIDSet{
						SnapshotChannel:   struct{}{},
						ChunkChannel:      struct{}{},
						LightBlockChannel: struct{}{},
						ParamsChannel:     struct{}{},
					},
				}
			}

			trackingHeight := startHeight
			rts.stateStore.
				On("SaveValidatorSets",
					mock.AnythingOfType("int64"),
					mock.AnythingOfType("int64"),
					mock.AnythingOfType("*types.ValidatorSet")).
				Maybe().
				Return(func(lh, uh int64, _vals *types.ValidatorSet) error {
					require.Equal(t, trackingHeight, lh)
					require.Equal(t, lh, uh)
					require.GreaterOrEqual(t, lh, stopHeight)
					trackingHeight--
					return nil
				})

			chain := buildLightBlockChain(ctx, t, stopHeight-1, startHeight+1, stopTime, rts.privVal)

			closeCh := make(chan struct{})
			defer close(closeCh)
			go handleLightBlockRequests(ctx, t, chain, rts.blockOutCh, rts.blockInCh, closeCh, tc.failureRate, uint64(stopHeight))

			err := rts.reactor.backfill(
				ctx,
				factory.DefaultTestChainID,
				startHeight,
				stopHeight,
				1,
				factory.MakeBlockIDWithHash(chain[startHeight].Header.Hash()),
				stopTime,
				10*time.Millisecond,
				100*time.Millisecond,
			)
			require.Equal(t, startHeight-stopHeight+1, rts.reactor.backfillBlockTotal)
			require.Equal(t, rts.reactor.backfilledBlocks, rts.reactor.BackFilledBlocks())
			require.Equal(t, rts.reactor.backfillBlockTotal, rts.reactor.BackFillBlocksTotal())
			if tc.failureRate > 3 {
				require.Error(t, err)
				require.NotEqual(t, rts.reactor.backfilledBlocks, rts.reactor.backfillBlockTotal)
				return
			}
			require.NoError(t, err)
			for height := startHeight; height <= stopHeight; height++ {
				blockMeta := rts.blockStore.LoadBlockMeta(height)
				require.NotNil(t, blockMeta)
			}
			require.Nil(t, rts.blockStore.LoadBlockMeta(stopHeight-1))
			require.Nil(t, rts.blockStore.LoadBlockMeta(startHeight+1))
			require.Equal(t, startHeight-stopHeight+1, rts.reactor.backfilledBlocks)
		})
	}
}

// retryUntil will continue to evaluate fn and will return successfully when true
// or fail when the timeout is reached.
func retryUntil(ctx context.Context, t *testing.T, fn func() bool, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		if fn() {
			return
		}
		require.NoError(t, ctx.Err())
	}
}

// handleLightBlockRequests will handle light block requests and respond with the appropriate light block
// based on the height of the request. It will also simulate failures based on the failure rate.
// The function will return when the context is done.
// # Arguments
// * `ctx` - the context
// * `t` - the testing.T instance
// * `chain` - the light block chain
// * `receiving` - the channel to receive requests
// * `sending` - the channel to send responses
// * `close` - the channel to close the function
// * `failureRate` - the rate of failure
// * `stopHeight` - minimum height for which to respond; below this height, the function will not respond to requests,
// causing timeouts. Use 0 to disable this mechanism.
func handleLightBlockRequests(
	ctx context.Context,
	t *testing.T,
	chain map[int64]*types.LightBlock,
	receiving chan p2p.Envelope,
	sending chan p2p.Envelope,
	close chan struct{},
	failureRate int,
	stopHeight uint64,
) {
	requests := 0
	errorCount := 0
	for {
		select {
		case <-ctx.Done():
			return
		case envelope := <-receiving:
			if msg, ok := envelope.Message.(*ssproto.LightBlockRequest); ok {
				if msg.Height < stopHeight {
					// this causes timeout; needed for backfill tests
					// to ensure heights below stopHeight are not processed
					// before all heights above stopHeight are processed
					continue
				}
				if requests%10 >= failureRate {
					lb, err := chain[int64(msg.Height)].ToProto()
					require.NoError(t, err)
					sendMsgToChan(ctx, sending, newLBMessage(envelope.To, lb))
				} else {
					switch errorCount % 3 {
					case 0: // send a different block
						vals, pv := types.RandValidatorSet(3)
						_, _, lb := mockLB(ctx, t, int64(msg.Height), factory.DefaultTestTime, factory.MakeBlockID(), vals, pv)
						badLB, err := lb.ToProto()
						require.NoError(t, err)
						sendMsgToChan(ctx, sending, newLBMessage(envelope.To, badLB))
					case 1: // send nil block i.e. pretend we don't have it
						sendMsgToChan(ctx, sending, newLBMessage(envelope.To, nil))
					case 2: // don't do anything
					}
					errorCount++
				}
			}
		case <-close:
			return
		}
		requests++
	}
}

func handleConsensusParamsRequest(
	ctx context.Context,
	t *testing.T,
	receiving, sending chan p2p.Envelope,
	closeCh chan struct{},
) {
	t.Helper()
	params := types.DefaultConsensusParams()
	paramsProto := params.ToProto()
	for {
		select {
		case <-ctx.Done():
			return
		case envelope := <-receiving:
			msg, ok := envelope.Message.(*ssproto.ParamsRequest)
			if !ok {
				t.Errorf("message was %T which is not a params request", envelope.Message)
				return
			}
			select {
			case sending <- p2p.Envelope{
				From:      envelope.To,
				ChannelID: ParamsChannel,
				Message: &ssproto.ParamsResponse{
					Height:          msg.Height,
					ConsensusParams: paramsProto,
				},
			}:
			case <-ctx.Done():
				return
			case <-closeCh:
				return
			}

		case <-closeCh:
			return
		}
	}
}

func buildLightBlockChain(ctx context.Context, t *testing.T, fromHeight, toHeight int64, startTime time.Time, privVal *types.MockPV) map[int64]*types.LightBlock {
	t.Helper()
	chain := make(map[int64]*types.LightBlock, toHeight-fromHeight)
	lastBlockID := factory.MakeBlockID()
	blockTime := startTime.Add(time.Duration(fromHeight-toHeight) * time.Minute)
	vals, pv := types.RandValidatorSet(3)
	for height := fromHeight; height < toHeight; height++ {
		pk, _ := pv[0].GetPrivateKey(ctx, vals.QuorumHash)
		privVal.UpdatePrivateKey(ctx, pk, vals.QuorumHash, vals.ThresholdPublicKey, height)
		vals, pv, chain[height] = mockLB(ctx, t, height, blockTime, lastBlockID, vals, pv)

		lastBlockID = chain[height].Commit.BlockID
		blockTime = blockTime.Add(1 * time.Minute)
	}
	return chain
}

func mockLB(ctx context.Context, t *testing.T, height int64, time time.Time, lastBlockID types.BlockID,
	currentVals *types.ValidatorSet, currentPrivVals []types.PrivValidator,
) (*types.ValidatorSet, []types.PrivValidator, *types.LightBlock) {
	t.Helper()
	header := factory.MakeHeader(t, &types.Header{
		Height:      height,
		LastBlockID: lastBlockID,
		Time:        time,
		AppHash:     make([]byte, crypto.DefaultHashSize),
	})
	header.Version.App = testAppVersion

	nextVals, nextPrivVals := types.RandValidatorSet(3)
	header.ValidatorsHash = currentVals.Hash()
	header.NextValidatorsHash = nextVals.Hash()
	header.ConsensusHash = types.DefaultConsensusParams().HashConsensusParams()
	// lastBlockID = factory.MakeBlockIDWithHash(header.Hash())
	stateID := header.StateID()
	lastBlockID = types.BlockID{
		Hash: header.Hash(),
		PartSetHeader: types.PartSetHeader{
			Total: 100,
			Hash:  factory.RandomHash(),
		},
		StateID: stateID.Hash(),
	}
	voteSet := types.NewVoteSet(factory.DefaultTestChainID, height, 0, tmproto.PrecommitType, currentVals)
	commit, err := factory.MakeCommit(ctx, lastBlockID, height, 0, voteSet, currentVals, currentPrivVals)
	require.NoError(t, err)
	return nextVals, nextPrivVals, &types.LightBlock{
		SignedHeader: &types.SignedHeader{
			Header: header,
			Commit: commit,
		},
		ValidatorSet: currentVals,
	}
}

// graduallyAddPeers delivers a new randomly-generated peer update on peerUpdateCh once
// per interval, until closeCh is closed. Each peer update is assigned a random node ID.
func graduallyAddPeers(
	ctx context.Context,
	t *testing.T,
	peerUpdateCh chan p2p.PeerUpdate,
	closeCh chan struct{},
	interval time.Duration,
) {
	ticker := time.NewTicker(interval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-closeCh:
			return
		case <-ticker.C:
			peerUpdateCh <- p2p.PeerUpdate{
				NodeID: factory.RandomNodeID(t),
				Status: p2p.PeerStatusUp,
				Channels: p2p.ChannelIDSet{
					SnapshotChannel:   struct{}{},
					ChunkChannel:      struct{}{},
					LightBlockChannel: struct{}{},
					ParamsChannel:     struct{}{},
				},
			}
		}
	}
}

func handleSnapshotRequests(
	ctx context.Context,
	t *testing.T,
	receivingCh chan p2p.Envelope,
	sendingCh chan p2p.Envelope,
	closeCh chan struct{},
	snapshots []snapshot,
) {
	t.Helper()
	for {
		select {
		case <-ctx.Done():
			return
		case <-closeCh:
			return
		case envelope := <-receivingCh:
			_, ok := envelope.Message.(*ssproto.SnapshotsRequest)
			require.True(t, ok)
			for _, snapshot := range snapshots {
				sendingCh <- p2p.Envelope{
					From:      envelope.To,
					ChannelID: SnapshotChannel,
					Message: &ssproto.SnapshotsResponse{
						Height:   snapshot.Height,
						Version:  snapshot.Version,
						Hash:     snapshot.Hash,
						Metadata: snapshot.Metadata,
					},
				}
			}
		}
	}
}

func handleChunkRequests(
	ctx context.Context,
	t *testing.T,
	receivingCh chan p2p.Envelope,
	sendingCh chan p2p.Envelope,
	closeCh chan struct{},
	chunkID []byte,
	chunk []byte,
) {
	t.Helper()
	for {
		select {
		case <-ctx.Done():
			return
		case <-closeCh:
			return
		case envelope := <-receivingCh:
			msg, ok := envelope.Message.(*ssproto.ChunkRequest)
			require.True(t, ok)
			sendingCh <- p2p.Envelope{
				From:      envelope.To,
				ChannelID: ChunkChannel,
				Message: &ssproto.ChunkResponse{
					ChunkId: chunkID,
					Height:  msg.Height,
					Version: msg.Version,
					Chunk:   chunk,
					Missing: false,
				},
			}

		}
	}
}

func newLBMessage(peerID types.NodeID, lb *tmproto.LightBlock) p2p.Envelope {
	return p2p.Envelope{
		From:      peerID,
		ChannelID: LightBlockChannel,
		Message: &ssproto.LightBlockResponse{
			LightBlock: lb,
		},
	}
}

func sendMsgToChan(ctx context.Context, ch chan p2p.Envelope, msg p2p.Envelope) {
	select {
	case ch <- msg:
	case <-ctx.Done():
		return
	}
}

func genPeerIDs(num int) []string {
	if num == 0 {
		return nil
	}
	peers := make([]string, num)
	for i := 0; i < num; i++ {
		peers[i] = strconv.Itoa(i)
	}
	return peers
}
