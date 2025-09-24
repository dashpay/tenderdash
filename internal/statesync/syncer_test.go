package statesync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	clientmocks "github.com/dashpay/tenderdash/abci/client/mocks"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/proxy"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/statesync/mocks"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	ssproto "github.com/dashpay/tenderdash/proto/tendermint/statesync"
	"github.com/dashpay/tenderdash/types"
	"github.com/dashpay/tenderdash/version"
)

type SyncerTestSuite struct {
	suite.Suite

	ctx             context.Context
	conn            *clientmocks.Client
	stateProvider   *mocks.StateProvider
	syncer          *syncer
	logger          log.Logger
	snapshotChannel p2p.Channel
	snapshotInCh    chan p2p.Envelope
	snapshotOutCh   chan p2p.Envelope
	chunkChannel    p2p.Channel
	chunkInCh       chan p2p.Envelope
	chunkOutCh      chan p2p.Envelope
}

func TestSyncerTestSuite(t *testing.T) {
	suite.Run(t, new(SyncerTestSuite))
}

func (suite *SyncerTestSuite) SetupTest() {
	suite.ctx = context.Background()
	suite.stateProvider = mocks.NewStateProvider(suite.T())

	suite.snapshotChannel, suite.snapshotInCh, suite.snapshotOutCh, _ = makeChannel(SnapshotChannel, "snapshot")
	suite.chunkChannel, suite.chunkInCh, suite.chunkOutCh, _ = makeChannel(ChunkChannel, "chunk")
	suite.conn = clientmocks.NewClient(suite.T())
	suite.logger = log.NewNopLogger()
	suite.syncer = &syncer{
		logger:                suite.logger,
		stateProvider:         suite.stateProvider,
		conn:                  suite.conn,
		snapshots:             newSnapshotPool(),
		tempDir:               suite.T().TempDir(),
		fetchers:              1,
		snapshotCh:            suite.snapshotChannel,
		chunkCh:               suite.chunkChannel,
		retryTimeout:          100 * time.Millisecond,
		dequeueChunkIDTimeout: 50 * time.Millisecond,
		metrics:               NopMetrics(),
	}
}

func (suite *SyncerTestSuite) TestSyncAny() {
	if testing.Short() {
		suite.T().Skip("skipping test in short mode")
	}

	ctx, cancel := context.WithCancel(suite.ctx)
	defer cancel()

	state := sm.State{
		ChainID: "chain",
		Version: sm.Version{
			Consensus: version.Consensus{
				Block: version.BlockProtocol,
				App:   testAppVersion,
			},
			Software: version.TMCoreSemVer,
		},
		InitialHeight:   1,
		LastBlockHeight: 1,
		LastBlockID:     types.BlockID{Hash: []byte("blockhash")},
		LastBlockTime:   time.Now(),
		LastResultsHash: []byte("last_results_hash"),
		LastAppHash:     []byte("app_hash"),

		LastValidators: &types.ValidatorSet{
			Validators: []*types.Validator{{ProTxHash: crypto.Checksum([]byte("val1"))}},
		},
		Validators: &types.ValidatorSet{
			Validators: []*types.Validator{{ProTxHash: crypto.Checksum([]byte("val2"))}},
		},

		ConsensusParams:                  *types.DefaultConsensusParams(),
		LastHeightConsensusParamsChanged: 1,
	}
	commit := &types.Commit{
		Height:  1,
		BlockID: types.BlockID{Hash: []byte("blockhash")},
	}

	s := &snapshot{Height: 1, Version: 1, Hash: []byte{0}}
	chunks := []*chunk{
		{Height: 1, Version: 1, ID: []byte{0}, Chunk: []byte{0}},
		{Height: 1, Version: 1, ID: []byte{1}, Chunk: []byte{1}},
		{Height: 1, Version: 1, ID: []byte{2}, Chunk: []byte{2}},
		{Height: 1, Version: 1, ID: []byte{3}, Chunk: []byte{3}},
	}

	suite.stateProvider.
		On("AppHash", mock.Anything, uint64(1)).
		Return(state.LastAppHash, nil)
	suite.stateProvider.
		On("AppHash", mock.Anything, uint64(2)).
		Return(tmbytes.HexBytes("app_hash_2"), nil)
	suite.stateProvider.
		On("Commit", mock.Anything, uint64(1)).
		Return(commit, nil)
	suite.stateProvider.
		On("State", mock.Anything, uint64(1)).
		Return(state, nil)

	peerAID := types.NodeID("aa")
	peerBID := types.NodeID("bb")
	peerCID := types.NodeID("cc")

	// Adding a chunk should error when no sync is in progress
	_, err := suite.syncer.AddChunk(&chunk{Height: 1, Version: 1, ID: []byte{0}, Chunk: []byte{1}})
	suite.Require().Error(err)

	// Adding a couple of peers should trigger snapshot discovery messages
	err = suite.syncer.AddPeer(ctx, peerAID)
	suite.Require().NoError(err)
	e := <-suite.snapshotOutCh
	suite.Require().Equal(&ssproto.SnapshotsRequest{}, e.Message)
	suite.Require().Equal(peerAID, e.To)

	err = suite.syncer.AddPeer(ctx, peerBID)
	suite.Require().NoError(err)
	e = <-suite.snapshotOutCh
	suite.Require().Equal(&ssproto.SnapshotsRequest{}, e.Message)
	suite.Require().Equal(peerBID, e.To)

	// Both peers report back with snapshots. One of them also returns a snapshot we don't want, in
	// format 2, which will be rejected by the ABCI application.
	added, err := suite.syncer.AddSnapshot(peerAID, s)
	suite.Require().NoError(err)
	suite.Require().True(added)

	added, err = suite.syncer.AddSnapshot(peerBID, s)
	suite.Require().NoError(err)
	suite.Require().False(added)

	s2 := &snapshot{Height: 2, Version: 2, Hash: []byte{1}}
	added, err = suite.syncer.AddSnapshot(peerBID, s2)
	suite.Require().NoError(err)
	suite.Require().True(added)

	added, err = suite.syncer.AddSnapshot(peerCID, s2)
	suite.Require().NoError(err)
	suite.Require().False(added)

	// We start a sync, with peers sending back chunks when requested. We first reject the snapshot
	// with height 2 format 2, and accept the snapshot at height 1.
	suite.conn.
		On("OfferSnapshot", mock.Anything, &abci.RequestOfferSnapshot{
			Snapshot: &abci.Snapshot{
				Height:  2,
				Version: 2,
				Hash:    []byte{1},
			},
			AppHash: []byte("app_hash_2"),
		}).
		Once().
		Return(&abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_REJECT_FORMAT}, nil)
	suite.conn.
		On("OfferSnapshot", mock.Anything, &abci.RequestOfferSnapshot{
			Snapshot: &abci.Snapshot{
				Height:   s.Height,
				Version:  s.Version,
				Hash:     s.Hash,
				Metadata: s.Metadata,
			},
			AppHash: []byte("app_hash"),
		}).
		Once().
		Return(&abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_ACCEPT}, nil)

	chunkRequests := make([]int, len(chunks))
	go func() {
		for i := 0; i < 5; i++ {
			select {
			case <-ctx.Done():
				return
			case e := <-suite.chunkOutCh:
				msg, ok := e.Message.(*ssproto.ChunkRequest)
				suite.Require().True(ok)

				suite.Require().EqualValues(1, msg.Height)
				suite.Require().EqualValues(1, msg.Version)

				added, err := suite.syncer.AddChunk(chunks[msg.ChunkId[0]])
				suite.Require().NoError(err)
				suite.Require().True(added)

				chunkRequests[msg.ChunkId[0]]++

				suite.T().Logf("added chunkID %x", msg.ChunkId)
			}
		}
	}()
	var reqs []*abci.RequestApplySnapshotChunk
	for i := 0; i < len(chunks); i++ {
		reqs = append(reqs, &abci.RequestApplySnapshotChunk{
			ChunkId: chunks[i].ID,
			Chunk:   chunks[i].Chunk,
		})
	}
	resps := []*abci.ResponseApplySnapshotChunk{
		{
			Result:     abci.ResponseApplySnapshotChunk_ACCEPT,
			NextChunks: [][]byte{chunks[1].ID},
		},
		{
			Result:     abci.ResponseApplySnapshotChunk_ACCEPT,
			NextChunks: [][]byte{chunks[2].ID},
		},
		{
			Result:        abci.ResponseApplySnapshotChunk_ACCEPT,
			RefetchChunks: [][]byte{chunks[0].ID},
		},
		{
			Result:     abci.ResponseApplySnapshotChunk_ACCEPT,
			NextChunks: [][]byte{chunks[3].ID},
		},
		{Result: abci.ResponseApplySnapshotChunk_COMPLETE_SNAPSHOT},
	}
	applySnapshotChunks := []struct {
		req  *abci.RequestApplySnapshotChunk
		resp *abci.ResponseApplySnapshotChunk
	}{
		{req: reqs[0], resp: resps[0]},
		{req: reqs[1], resp: resps[1]},
		{req: reqs[2], resp: resps[2]},
		{req: reqs[0], resp: resps[3]},
		{req: reqs[3], resp: resps[4]},
	}
	for _, asc := range applySnapshotChunks {
		suite.conn.
			On("ApplySnapshotChunk", mock.Anything, asc.req).
			Once().
			Return(asc.resp, nil)
	}

	suite.conn.
		On("Info", mock.Anything, &proxy.RequestInfo).
		Once().
		Return(&abci.ResponseInfo{
			AppVersion:       testAppVersion,
			LastBlockHeight:  1,
			LastBlockAppHash: []byte("app_hash"),
		}, nil)

	newState, lastCommit, err := suite.syncer.SyncAny(ctx, 0, 0, func() error { return nil })
	suite.Require().NoError(err)

	suite.Require().Equal([]int{0: 2, 1: 1, 2: 1, 3: 1}, chunkRequests)

	expectState := state
	suite.Require().Equal(expectState, newState)
	suite.Require().Equal(commit, lastCommit)

	suite.Require().Equal(expectState.LastBlockHeight, suite.syncer.lastSyncedSnapshotHeight)
	suite.Require().True(suite.syncer.avgChunkTime > 0)
}

func (suite *SyncerTestSuite) TestSyncAnyNoSnapshots() {
	ctx, cancel := context.WithCancel(suite.ctx)
	defer cancel()

	_, _, err := suite.syncer.SyncAny(ctx, 0, 0, func() error { return nil })
	suite.Require().Equal(errNoSnapshots, err)
}

func (suite *SyncerTestSuite) TestSyncAnyAbort() {
	ctx, cancel := context.WithCancel(suite.ctx)
	defer cancel()

	suite.stateProvider.
		On("AppHash", mock.Anything, mock.Anything).
		Maybe().
		Return(tmbytes.HexBytes("app_hash"), nil)

	s := &snapshot{Height: 1, Version: 1, Hash: []byte{1, 2, 3}}
	peerID := types.NodeID("aa")

	_, err := suite.syncer.AddSnapshot(peerID, s)
	suite.Require().NoError(err)

	suite.conn.
		On("OfferSnapshot", mock.Anything, &abci.RequestOfferSnapshot{
			Snapshot: toABCI(s), AppHash: []byte("app_hash"),
		}).
		Once().
		Return(&abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_ABORT}, nil)

	_, _, err = suite.syncer.SyncAny(ctx, 0, 0, func() error { return nil })
	suite.Require().Equal(errAbort, err)
}

func (suite *SyncerTestSuite) TestSyncAnyReject() {
	ctx, cancel := context.WithCancel(suite.ctx)
	defer cancel()

	suite.stateProvider.
		On("AppHash", mock.Anything, mock.Anything).
		Maybe().
		Return(tmbytes.HexBytes("app_hash"), nil)

	// s22 is tried first, then s12, then s11, then errNoSnapshots
	s22 := &snapshot{Height: 2, Version: 2, Hash: []byte{1, 2, 3}}
	s12 := &snapshot{Height: 1, Version: 2, Hash: []byte{1, 2, 3}}
	s11 := &snapshot{Height: 1, Version: 1, Hash: []byte{1, 2, 3}}

	peerID := types.NodeID("aa")

	_, err := suite.syncer.AddSnapshot(peerID, s22)
	suite.Require().NoError(err)

	_, err = suite.syncer.AddSnapshot(peerID, s12)
	suite.Require().NoError(err)

	_, err = suite.syncer.AddSnapshot(peerID, s11)
	suite.Require().NoError(err)

	suite.conn.
		On("OfferSnapshot", mock.Anything, &abci.RequestOfferSnapshot{
			Snapshot: toABCI(s22), AppHash: []byte("app_hash"),
		}).
		Once().
		Return(&abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_REJECT}, nil)

	suite.conn.
		On("OfferSnapshot", mock.Anything, &abci.RequestOfferSnapshot{
			Snapshot: toABCI(s12), AppHash: []byte("app_hash"),
		}).
		Once().
		Return(&abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_REJECT}, nil)

	suite.conn.
		On("OfferSnapshot", mock.Anything, &abci.RequestOfferSnapshot{
			Snapshot: toABCI(s11), AppHash: []byte("app_hash"),
		}).
		Once().
		Return(&abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_REJECT}, nil)

	_, _, err = suite.syncer.SyncAny(ctx, 0, 0, func() error { return nil })
	suite.Require().Equal(errNoSnapshots, err)
}

func (suite *SyncerTestSuite) TestSyncAnyRejectFormat() {
	ctx, cancel := context.WithCancel(suite.ctx)
	defer cancel()

	suite.stateProvider.
		On("AppHash", mock.Anything, mock.Anything).
		Maybe().
		Return(tmbytes.HexBytes("app_hash"), nil)

	// s22 is tried first, which reject s22 and s12, then s11 will abort.
	s22 := &snapshot{Height: 2, Version: 2, Hash: []byte{1, 2, 3}}
	s12 := &snapshot{Height: 1, Version: 2, Hash: []byte{1, 2, 3}}
	s11 := &snapshot{Height: 1, Version: 1, Hash: []byte{1, 2, 3}}

	peerID := types.NodeID("aa")

	_, err := suite.syncer.AddSnapshot(peerID, s22)
	suite.Require().NoError(err)

	_, err = suite.syncer.AddSnapshot(peerID, s12)
	suite.Require().NoError(err)

	_, err = suite.syncer.AddSnapshot(peerID, s11)
	suite.Require().NoError(err)

	suite.conn.
		On("OfferSnapshot", mock.Anything, &abci.RequestOfferSnapshot{
			Snapshot: toABCI(s22), AppHash: []byte("app_hash"),
		}).
		Once().
		Return(&abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_REJECT_FORMAT}, nil)

	suite.conn.
		On("OfferSnapshot", mock.Anything, &abci.RequestOfferSnapshot{
			Snapshot: toABCI(s11), AppHash: []byte("app_hash"),
		}).
		Once().
		Return(&abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_ABORT}, nil)

	_, _, err = suite.syncer.SyncAny(ctx, 0, 0, func() error { return nil })
	suite.Require().Equal(errAbort, err)
}

func (suite *SyncerTestSuite) TestSyncAnyRejectSender() {
	ctx, cancel := context.WithCancel(suite.ctx)
	defer cancel()

	suite.stateProvider.
		On("AppHash", mock.Anything, mock.Anything).
		Maybe().
		Return(tmbytes.HexBytes("app_hash"), nil)

	peerAID := types.NodeID("aa")
	peerBID := types.NodeID("bb")
	peerCID := types.NodeID("cc")

	// sbc will be offered first, which will be rejected with reject_sender, causing all snapshots
	// submitted by both b and c (i.e. sb, sc, sbc) to be rejected. Finally, sa will reject and
	// errNoSnapshots is returned.
	sa := &snapshot{Height: 1, Version: 1, Hash: []byte{1, 2, 3}}
	sb := &snapshot{Height: 2, Version: 1, Hash: []byte{1, 2, 3}}
	sc := &snapshot{Height: 3, Version: 1, Hash: []byte{1, 2, 3}}
	sbc := &snapshot{Height: 4, Version: 1, Hash: []byte{1, 2, 3}}

	snapshots := []struct {
		peerID   types.NodeID
		snapshot *snapshot
	}{
		{peerAID, sa},
		{peerBID, sb},
		{peerCID, sc},
		{peerBID, sbc},
		{peerCID, sbc},
	}
	for _, s := range snapshots {
		_, err := suite.syncer.AddSnapshot(s.peerID, s.snapshot)
		suite.Require().NoError(err)
	}

	suite.conn.
		On("OfferSnapshot", mock.Anything, &abci.RequestOfferSnapshot{
			Snapshot: toABCI(sbc), AppHash: []byte("app_hash"),
		}).
		Once().
		Return(&abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_REJECT_SENDER}, nil)

	suite.conn.
		On("OfferSnapshot", mock.Anything, &abci.RequestOfferSnapshot{
			Snapshot: toABCI(sa), AppHash: []byte("app_hash"),
		}).
		Once().
		Return(&abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_REJECT}, nil)

	_, _, err := suite.syncer.SyncAny(ctx, 0, 0, func() error { return nil })
	suite.Require().Equal(errNoSnapshots, err)
}

func (suite *SyncerTestSuite) TestSyncAnyAbciError() {
	ctx, cancel := context.WithCancel(suite.ctx)
	defer cancel()

	suite.stateProvider.
		On("AppHash", mock.Anything, mock.Anything).
		Maybe().
		Return(tmbytes.HexBytes("app_hash"), nil)

	errBoom := errors.New("boom")
	s := &snapshot{Height: 1, Version: 1, Hash: []byte{1, 2, 3}}

	peerID := types.NodeID("aa")

	_, err := suite.syncer.AddSnapshot(peerID, s)
	suite.Require().NoError(err)

	suite.conn.
		On("OfferSnapshot", mock.Anything, &abci.RequestOfferSnapshot{
			Snapshot: toABCI(s), AppHash: []byte("app_hash"),
		}).
		Once().
		Return(nil, errBoom)

	_, _, err = suite.syncer.SyncAny(ctx, 0, 0, func() error { return nil })
	suite.Require().True(errors.Is(err, errBoom))
}

func (suite *SyncerTestSuite) TestOfferSnapshot() {
	unknownErr := errors.New("unknown error")
	boom := errors.New("boom")

	testCases := map[string]struct {
		result    abci.ResponseOfferSnapshot_Result
		err       error
		expectErr error
	}{
		"accept":           {abci.ResponseOfferSnapshot_ACCEPT, nil, nil},
		"abort":            {abci.ResponseOfferSnapshot_ABORT, nil, errAbort},
		"reject":           {abci.ResponseOfferSnapshot_REJECT, nil, errRejectSnapshot},
		"reject_version":   {abci.ResponseOfferSnapshot_REJECT_FORMAT, nil, errRejectFormat},
		"reject_sender":    {abci.ResponseOfferSnapshot_REJECT_SENDER, nil, errRejectSender},
		"unknown":          {abci.ResponseOfferSnapshot_UNKNOWN, nil, unknownErr},
		"error":            {0, boom, boom},
		"unknown non-zero": {9, nil, unknownErr},
	}

	for name, tc := range testCases {
		suite.Run(name, func() {
			suite.SetupTest() // reset
			ctx, cancel := context.WithCancel(suite.ctx)
			defer cancel()

			s := &snapshot{Height: 1, Version: 1, Hash: []byte{1, 2, 3}, trustedAppHash: []byte("app_hash")}
			suite.conn.
				On("OfferSnapshot", mock.Anything, &abci.RequestOfferSnapshot{
					Snapshot: toABCI(s),
					AppHash:  []byte("app_hash"),
				}).
				Return(&abci.ResponseOfferSnapshot{Result: tc.result}, tc.err)

			err := suite.syncer.offerSnapshot(ctx, s)
			if tc.expectErr == unknownErr {
				suite.Require().Error(err)
			} else {
				unwrapped := errors.Unwrap(err)
				if unwrapped != nil {
					err = unwrapped
				}
				suite.Require().Equal(tc.expectErr, err)
			}
		})
	}
}

func (suite *SyncerTestSuite) TestApplyChunksResults() {
	unknownErr := errors.New("unknown error")
	boom := errors.New("boom")

	suite.stateProvider.
		On("AppHash", mock.Anything, mock.Anything).
		Maybe().
		Return(tmbytes.HexBytes("app_hash"), nil)

	testCases := map[string]struct {
		resps     []*abci.ResponseApplySnapshotChunk
		err       error
		expectErr error
	}{
		"accept": {
			resps: []*abci.ResponseApplySnapshotChunk{
				{Result: abci.ResponseApplySnapshotChunk_ACCEPT, NextChunks: [][]byte{{1}}},
				{Result: abci.ResponseApplySnapshotChunk_COMPLETE_SNAPSHOT},
			},
		},
		"abort": {
			resps: []*abci.ResponseApplySnapshotChunk{
				{Result: abci.ResponseApplySnapshotChunk_ABORT},
			},
			err:       errAbort,
			expectErr: errAbort,
		},
		"retry": {
			resps: []*abci.ResponseApplySnapshotChunk{
				{Result: abci.ResponseApplySnapshotChunk_RETRY},
				{Result: abci.ResponseApplySnapshotChunk_COMPLETE_SNAPSHOT},
			},
		},
		"retry_snapshot": {
			resps: []*abci.ResponseApplySnapshotChunk{
				{Result: abci.ResponseApplySnapshotChunk_RETRY_SNAPSHOT},
			},
			err:       errRejectSnapshot,
			expectErr: errRejectSnapshot,
		},
		"reject_snapshot": {
			resps: []*abci.ResponseApplySnapshotChunk{
				{Result: abci.ResponseApplySnapshotChunk_RETRY_SNAPSHOT},
			},
			err:       errRejectSnapshot,
			expectErr: errRejectSnapshot,
		},
		"unknown": {
			resps: []*abci.ResponseApplySnapshotChunk{
				{Result: abci.ResponseApplySnapshotChunk_UNKNOWN},
			},
			err:       unknownErr,
			expectErr: unknownErr,
		},
		"error": {
			resps: []*abci.ResponseApplySnapshotChunk{
				{Result: abci.ResponseApplySnapshotChunk_ACCEPT},
			},
			err:       boom,
			expectErr: boom,
		},
		"unknown non-zero": {
			resps: []*abci.ResponseApplySnapshotChunk{
				{Result: abci.ResponseApplySnapshotChunk_ACCEPT},
			},
			err:       unknownErr,
			expectErr: unknownErr,
		},
	}

	for name, tc := range testCases {
		tc := tc
		suite.Run(name, func() {
			suite.SetupTest() // reset
			ctx, cancel := context.WithCancel(suite.ctx)
			defer cancel()

			s := &snapshot{Height: 1, Version: 1, Hash: []byte{1, 2, 3}}
			body := []byte{1, 2, 3}
			chunks, err := newChunkQueue(s, suite.T().TempDir(), 10)
			suite.Require().NoError(err)

			fetchStartTime := time.Now()

			chunkID := []byte{0}
			chunks.Enqueue(chunkID)

			for _, resp := range tc.resps {
				suite.conn.
					On("ApplySnapshotChunk", mock.Anything, mock.Anything).
					Once().
					Return(resp, tc.err)
			}
			go func() {
				for i := 0; i < len(tc.resps); i++ {
					for chunks.IsRequestQueueEmpty() {
						time.Sleep(5 * time.Millisecond)
					}
					chunkID, err := chunks.Dequeue()
					suite.Require().NoError(err)
					added, err := chunks.Add(&chunk{Height: 1, Version: 1, ID: chunkID, Chunk: body})
					suite.Require().NoError(err)
					suite.Require().True(added)
				}
			}()

			err = suite.syncer.applyChunks(ctx, chunks, fetchStartTime)
			if tc.expectErr == unknownErr {
				suite.Require().Error(err)
			} else {
				unwrapped := errors.Unwrap(err)
				if unwrapped != nil {
					err = unwrapped
				}
				suite.Require().Equal(tc.expectErr, err)
			}
		})
	}
}

func (suite *SyncerTestSuite) TestApplyChunksRefetchChunks() {
	suite.stateProvider.
		On("AppHash", mock.Anything, mock.Anything).
		Maybe().
		Return(tmbytes.HexBytes("app_hash"), nil)

	// Discarding chunks via refetch_chunks should work the same for all results
	testCases := map[string]struct {
		resp []*abci.ResponseApplySnapshotChunk
	}{
		"accept": {
			resp: []*abci.ResponseApplySnapshotChunk{
				{Result: abci.ResponseApplySnapshotChunk_ACCEPT, NextChunks: [][]byte{{1}}},
				{
					Result:        abci.ResponseApplySnapshotChunk_ACCEPT,
					NextChunks:    [][]byte{{2}},
					RefetchChunks: [][]byte{{1}},
				},
				{Result: abci.ResponseApplySnapshotChunk_ACCEPT},
				{Result: abci.ResponseApplySnapshotChunk_COMPLETE_SNAPSHOT},
			},
		},
		// TODO: disabled because refetch works the same for all results
		//"abort":           {abci.ResponseApplySnapshotChunk_ABORT},
		//"retry":           {abci.ResponseApplySnapshotChunk_RETRY},
		//"retry_snapshot":  {abci.ResponseApplySnapshotChunk_RETRY_SNAPSHOT},
		//"reject_snapshot": {abci.ResponseApplySnapshotChunk_REJECT_SNAPSHOT},
	}

	chunks := []*chunk{
		{Height: 1, Version: 1, ID: []byte{0}, Chunk: []byte{0}},
		{Height: 1, Version: 1, ID: []byte{1}, Chunk: []byte{1}},
		{Height: 1, Version: 1, ID: []byte{2}, Chunk: []byte{2}},
	}
	ctx, cancel := context.WithCancel(suite.ctx)
	defer cancel()

	for name, tc := range testCases {
		suite.Run(name, func() {
			s := &snapshot{Height: 1, Version: 1, Hash: []byte{1, 2, 3}}
			queue, err := newChunkQueue(s, suite.T().TempDir(), 1)
			suite.Require().NoError(err)
			queue.Enqueue(chunks[0].ID)
			fetchStartTime := time.Now()
			for _, resp := range tc.resp {
				suite.conn.
					On("ApplySnapshotChunk", mock.Anything, mock.Anything).
					Once().
					Return(resp, nil)
			}
			go func() {
				for i := 0; i < len(tc.resp); i++ {
					for queue.IsRequestQueueEmpty() {
						time.Sleep(10 * time.Millisecond)
					}
					chunkID, err := queue.Dequeue()
					suite.Require().NoError(err)
					added, err := queue.Add(chunks[int(chunkID[0])])
					suite.Require().NoError(err)
					suite.Require().True(added)
				}
			}()
			_ = suite.syncer.applyChunks(ctx, queue, fetchStartTime)
			suite.Require().NoError(queue.Close())
		})
	}
}

func (suite *SyncerTestSuite) TestApplyChunksRejectSenders() {
	suite.stateProvider.
		On("AppHash", mock.Anything, mock.Anything).
		Maybe().
		Return(tmbytes.HexBytes("app_hash"), nil)

	// Set up three peers across two snapshots, and ask for one of them to be banned.
	// It should be banned from all snapshots.
	peerAID := types.NodeID("aa")
	peerBID := types.NodeID("bb")
	peerCID := types.NodeID("cc")

	chunks := []*chunk{
		{Height: 1, Version: 1, ID: []byte{0}, Chunk: []byte{0}, Sender: peerAID},
		{Height: 1, Version: 1, ID: []byte{1}, Chunk: []byte{1}, Sender: peerBID},
		{Height: 1, Version: 1, ID: []byte{2}, Chunk: []byte{2}, Sender: peerCID},
	}

	s1 := &snapshot{Height: 1, Version: 1, Hash: []byte{1, 2, 3}}
	s2 := &snapshot{Height: 2, Version: 1, Hash: []byte{1, 2, 3}}

	peerSnapshots := []struct {
		peerID   types.NodeID
		snapshot []*snapshot
	}{
		{peerID: peerAID, snapshot: []*snapshot{s1, s2}},
		{peerID: peerBID, snapshot: []*snapshot{s1, s2}},
		{peerID: peerCID, snapshot: []*snapshot{s1, s2}},
	}

	// Banning chunks senders via ban_chunk_senders should work the same for all results
	testCases := map[string]struct {
		chunks []*chunk
		resps  []*abci.ResponseApplySnapshotChunk
	}{
		"accept": {
			chunks: chunks,
			resps: []*abci.ResponseApplySnapshotChunk{
				{Result: abci.ResponseApplySnapshotChunk_ACCEPT, NextChunks: [][]byte{{1}}},
				{
					Result:        abci.ResponseApplySnapshotChunk_ACCEPT,
					NextChunks:    [][]byte{{2}},
					RejectSenders: []string{string(peerBID)},
				},
				{Result: abci.ResponseApplySnapshotChunk_COMPLETE_SNAPSHOT},
			},
		},
		"abort": {
			chunks: chunks,
			resps: []*abci.ResponseApplySnapshotChunk{
				{Result: abci.ResponseApplySnapshotChunk_ACCEPT, NextChunks: [][]byte{{1}}},
				{Result: abci.ResponseApplySnapshotChunk_ACCEPT, NextChunks: [][]byte{{2}}},
				{Result: abci.ResponseApplySnapshotChunk_ABORT, RejectSenders: []string{string(peerBID)}},
			},
		},
		"retry": {
			chunks: chunks,
			resps: []*abci.ResponseApplySnapshotChunk{
				{Result: abci.ResponseApplySnapshotChunk_ACCEPT, NextChunks: [][]byte{{1}, {2}}},
				{Result: abci.ResponseApplySnapshotChunk_ACCEPT},
				{Result: abci.ResponseApplySnapshotChunk_RETRY, RejectSenders: []string{string(peerBID)}},
				{Result: abci.ResponseApplySnapshotChunk_COMPLETE_SNAPSHOT},
			},
		},
		"retry_snapshot": {
			chunks: chunks,
			resps: []*abci.ResponseApplySnapshotChunk{
				{Result: abci.ResponseApplySnapshotChunk_ACCEPT, NextChunks: [][]byte{{1}, {2}}},
				{Result: abci.ResponseApplySnapshotChunk_ACCEPT},
				{Result: abci.ResponseApplySnapshotChunk_RETRY_SNAPSHOT, RejectSenders: []string{string(peerBID)}},
			},
		},
		"reject_snapshot": {
			chunks: chunks,
			resps: []*abci.ResponseApplySnapshotChunk{
				{Result: abci.ResponseApplySnapshotChunk_ACCEPT, NextChunks: [][]byte{{1}, {2}}},
				{Result: abci.ResponseApplySnapshotChunk_ACCEPT},
				{Result: abci.ResponseApplySnapshotChunk_REJECT_SNAPSHOT, RejectSenders: []string{string(peerBID)}},
			},
		},
	}
	ctx, cancel := context.WithCancel(suite.ctx)
	defer cancel()

	for name, tc := range testCases {
		tc := tc
		suite.Run(name, func() {
			for _, peerSnapshot := range peerSnapshots {
				for _, s := range peerSnapshot.snapshot {
					_, err := suite.syncer.AddSnapshot(peerSnapshot.peerID, s)
					suite.Require().NoError(err)
				}
			}

			queue, err := newChunkQueue(s1, suite.T().TempDir(), 10)
			suite.Require().NoError(err)
			queue.Enqueue(tc.chunks[0].ID)

			fetchStartTime := time.Now()

			go func() {
				for i := 0; i < len(tc.resps); i++ {
					for queue.IsRequestQueueEmpty() {
						time.Sleep(10 * time.Millisecond)
					}
					chunkID, err := queue.Dequeue()
					suite.Require().NoError(err)
					added, err := queue.Add(chunks[int(chunkID[0])])
					suite.Require().True(added)
					suite.Require().NoError(err)
				}
			}()

			for _, resp := range tc.resps {
				suite.conn.
					On("ApplySnapshotChunk", mock.Anything, mock.Anything).
					Once().
					Return(resp, nil)
			}

			// We don't really care about the result of applyChunks, since it has separate test.
			// However, it will block on e.g. retry result, so we spawn a goroutine that will
			// be shut down when the chunk requestQueue closes.

			_ = suite.syncer.applyChunks(ctx, queue, fetchStartTime)

			s1peers := suite.syncer.snapshots.GetPeers(s1)
			suite.Require().Len(s1peers, 2)
			suite.Require().EqualValues(peerAID, s1peers[0])
			suite.Require().EqualValues(peerCID, s1peers[1])

			suite.syncer.snapshots.GetPeers(s1)
			suite.Require().Len(s1peers, 2)
			suite.Require().EqualValues(peerAID, s1peers[0])
			suite.Require().EqualValues(peerCID, s1peers[1])

			suite.Require().NoError(queue.Close())
		})
	}
}

func (suite *SyncerTestSuite) TestSyncerVerifyApp() {
	ctx, cancel := context.WithCancel(suite.ctx)
	defer cancel()

	boom := errors.New("boom")
	const appVersion = 9
	appVersionMismatchErr := errors.New("app version mismatch. Expected: 9, got: 2")
	s := &snapshot{Height: 3, Version: 1, Hash: []byte{1, 2, 3}, trustedAppHash: []byte("app_hash")}

	testCases := map[string]struct {
		response  *abci.ResponseInfo
		err       error
		expectErr error
	}{
		"verified": {&abci.ResponseInfo{
			LastBlockHeight:  3,
			LastBlockAppHash: []byte("app_hash"),
			AppVersion:       appVersion,
		}, nil, nil},
		"invalid app version": {&abci.ResponseInfo{
			LastBlockHeight:  3,
			LastBlockAppHash: []byte("app_hash"),
			AppVersion:       2,
		}, nil, appVersionMismatchErr},
		"invalid height": {&abci.ResponseInfo{
			LastBlockHeight:  5,
			LastBlockAppHash: []byte("app_hash"),
			AppVersion:       appVersion,
		}, nil, errVerifyFailed},
		"invalid hash": {&abci.ResponseInfo{
			LastBlockHeight:  3,
			LastBlockAppHash: []byte("xxx"),
			AppVersion:       appVersion,
		}, nil, errVerifyFailed},
		"error": {nil, boom, boom},
	}

	for name, tc := range testCases {
		suite.Run(name, func() {
			suite.conn.
				On("Info", mock.Anything, &proxy.RequestInfo).
				Once().
				Return(tc.response, tc.err)
			err := suite.syncer.verifyApp(ctx, s, appVersion)
			unwrapped := errors.Unwrap(err)
			if unwrapped != nil {
				err = unwrapped
			}
			suite.Require().Equal(tc.expectErr, err)
		})
	}
}

func toABCI(s *snapshot) *abci.Snapshot {
	return &abci.Snapshot{
		Height:   s.Height,
		Version:  s.Version,
		Hash:     s.Hash,
		Metadata: s.Metadata,
	}
}

func makeChannel(ID p2p.ChannelID, name string) (p2p.Channel, chan p2p.Envelope, chan p2p.Envelope, chan p2p.PeerError) {
	inCh := make(chan p2p.Envelope, 1)
	outCh := make(chan p2p.Envelope, 1)
	errCh := make(chan p2p.PeerError, 1)
	return p2p.NewChannel(ID, name, inCh, outCh, errCh), inCh, outCh, errCh
}
