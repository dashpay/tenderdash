package blocksync

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	dbm "github.com/dashpay/tenderdash/libs/db"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/crypto/merkle"
	"github.com/dashpay/tenderdash/internal/consensus"
	"github.com/dashpay/tenderdash/internal/eventbus"
	mpmocks "github.com/dashpay/tenderdash/internal/mempool/mocks"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/internal/p2p/p2ptest"
	"github.com/dashpay/tenderdash/internal/proxy"
	sm "github.com/dashpay/tenderdash/internal/state"
	sf "github.com/dashpay/tenderdash/internal/state/test/factory"
	"github.com/dashpay/tenderdash/internal/store"
	"github.com/dashpay/tenderdash/internal/test/factory"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

type reactorTestSuite struct {
	network *p2ptest.Network
	config  *config.Config
	logger  log.Logger
	nodes   []types.NodeID

	reactors map[types.NodeID]*Reactor
	app      map[types.NodeID]abciclient.Client

	blockSyncChannels map[types.NodeID]p2p.Channel
	peerChans         map[types.NodeID]chan p2p.PeerUpdate
	peerUpdates       map[types.NodeID]*p2p.PeerUpdates

	// blockParts returns the parts the nodes commit their blocks with
	blockParts blockPartsFunc
}

// blockPartsFunc returns the parts a block at height is committed with, or nil
// for the parts types.Block.MakePartSet cuts it into.
type blockPartsFunc func(t *testing.T, height int64, block *types.Block) *types.PartSet

func setup(
	ctx context.Context,
	t *testing.T,
	conf *config.Config,
	genDoc *types.GenesisDoc,
	privVal types.PrivValidator,
	maxBlockHeights []int64,
	opts ...ReactorOption,
) *reactorTestSuite {
	t.Helper()
	return setupWithBlockParts(ctx, t, conf, genDoc, privVal, maxBlockHeights, nil, opts...)
}

// setupWithBlockParts is setup with the nodes committing their blocks with the
// parts blockParts returns.
func setupWithBlockParts(
	ctx context.Context,
	t *testing.T,
	conf *config.Config,
	genDoc *types.GenesisDoc,
	privVal types.PrivValidator,
	maxBlockHeights []int64,
	blockParts blockPartsFunc,
	opts ...ReactorOption,
) *reactorTestSuite {
	t.Helper()

	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(ctx)

	numNodes := len(maxBlockHeights)
	require.True(t, numNodes >= 1, "must specify at least one block height (nodes)")

	rts := &reactorTestSuite{
		config:            conf,
		logger:            log.NewTestingLogger(t).With("module", "block_sync", "testCase", t.Name()),
		network:           p2ptest.MakeNetwork(ctx, t, p2ptest.NetworkOptions{Config: conf, NumNodes: numNodes}, log.NewNopLogger()),
		nodes:             make([]types.NodeID, 0, numNodes),
		reactors:          make(map[types.NodeID]*Reactor, numNodes),
		app:               make(map[types.NodeID]abciclient.Client, numNodes),
		blockSyncChannels: make(map[types.NodeID]p2p.Channel, numNodes),
		peerChans:         make(map[types.NodeID]chan p2p.PeerUpdate, numNodes),
		peerUpdates:       make(map[types.NodeID]*p2p.PeerUpdates, numNodes),
		blockParts:        blockParts,
	}

	chDesc := &p2p.ChannelDescriptor{ID: p2p.BlockSyncChannel}
	rts.blockSyncChannels = rts.network.MakeChannelsNoCleanup(ctx, t, chDesc)

	i := 0
	for nodeID := range rts.network.Nodes {
		rts.addNode(ctx, t, nodeID, genDoc, privVal, maxBlockHeights[i], opts...)
		i++
	}

	t.Cleanup(func() {
		cancel()
		for _, nodeID := range rts.nodes {
			if rts.reactors[nodeID].IsRunning() {
				rts.reactors[nodeID].Wait()
				rts.app[nodeID].Wait()

				require.False(t, rts.reactors[nodeID].IsRunning())
			}
		}
	})
	t.Cleanup(leaktest.Check(t))

	return rts
}

func makeReactor(
	ctx context.Context,
	t *testing.T,
	conf *config.Config,
	proTxHash types.ProTxHash,
	_nodeID types.NodeID,
	genDoc *types.GenesisDoc,
	_privVal types.PrivValidator,
	channelCreator p2p.ChannelCreator,
	peerEvents p2p.PeerEventSubscriber,
	opts ...ReactorOption) *Reactor {

	logger := log.NewTestingLogger(t)

	app := proxy.New(abciclient.NewLocalClient(logger, &abci.BaseApplication{}), logger, proxy.NopMetrics())
	require.NoError(t, app.Start(ctx))

	blockDB := dbm.NewMemDB()
	stateDB := dbm.NewMemDB()
	stateStore := sm.NewStore(stateDB)
	blockStore := store.NewBlockStore(blockDB)

	state, err := sm.MakeGenesisState(genDoc)
	require.NoError(t, err)
	require.NoError(t, stateStore.Save(state))
	mp := &mpmocks.Mempool{}
	mp.On("Lock").Return()
	mp.On("Unlock").Return()
	mp.On("FlushAppConn", mock.Anything).Return(nil)
	mp.On("Update",
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything).Return(nil)

	eventBus := eventbus.NewDefault(logger)
	require.NoError(t, eventBus.Start(ctx))

	blockExec := sm.NewBlockExecutor(
		stateStore,
		app,
		mp,
		sm.EmptyEvidencePool{},
		blockStore,
		eventBus,
	)

	return NewReactor(
		logger,
		stateStore,
		blockExec,
		blockStore,
		proTxHash,
		nil,
		client.New(p2p.ChannelDescriptors(conf), channelCreator, client.WithLogger(logger)),
		peerEvents,
		true,
		consensus.NopMetrics(),
		nil, // eventbus, can be nil
		opts...,
	)
}

func (rts *reactorTestSuite) addNode(
	ctx context.Context,
	t *testing.T,
	nodeID types.NodeID,
	genDoc *types.GenesisDoc,
	privVal types.PrivValidator,
	maxBlockHeight int64,
	opts ...ReactorOption,
) {
	t.Helper()

	logger := log.NewNopLogger()

	rts.nodes = append(rts.nodes, nodeID)
	rts.app[nodeID] = proxy.New(abciclient.NewLocalClient(logger, &abci.BaseApplication{}), logger, proxy.NopMetrics())
	require.NoError(t, rts.app[nodeID].Start(ctx))

	rts.peerChans[nodeID] = make(chan p2p.PeerUpdate)
	rts.peerUpdates[nodeID] = p2p.NewPeerUpdates(rts.peerChans[nodeID], 1, "blocksync")
	rts.network.Nodes[nodeID].PeerManager.Register(ctx, rts.peerUpdates[nodeID])

	chCreator := func(ctx context.Context, chdesc *p2p.ChannelDescriptor) (p2p.Channel, error) {
		return rts.blockSyncChannels[nodeID], nil
	}

	proTxHash := rts.network.Nodes[nodeID].NodeInfo.ProTxHash
	peerEvents := func(ctx context.Context, _ string) *p2p.PeerUpdates { return rts.peerUpdates[nodeID] }
	reactor := makeReactor(ctx, t, rts.config, proTxHash, nodeID, genDoc, privVal, chCreator, peerEvents, opts...)

	commit := types.NewCommit(0, 0, types.BlockID{}, nil, nil)

	state, err := reactor.stateStore.Load()
	require.NoError(t, err)
	for blockHeight := int64(1); blockHeight <= maxBlockHeight; blockHeight++ {
		block, blockID, partSet, seenCommit := makeNextBlockWithParts(ctx, t, state, privVal, blockHeight, commit,
			rts.blockParts)
		state, err = reactor.blockExec.ApplyBlock(ctx, state, blockID, block, seenCommit, types.VerifiedCommit{})
		require.NoError(t, err)
		reactor.store.SaveBlock(block, partSet, seenCommit)
		commit = seenCommit
	}

	rts.reactors[nodeID] = reactor
	require.NoError(t, reactor.Start(ctx))
	require.True(t, reactor.IsRunning())
}

func makeNextBlock(ctx context.Context,
	t *testing.T,
	state sm.State,
	signer types.PrivValidator,
	height int64,
	commit *types.Commit) (*types.Block, types.BlockID, *types.PartSet, *types.Commit) {
	return makeNextBlockWithParts(ctx, t, state, signer, height, commit, nil)
}

// makeNextBlockWithParts is makeNextBlock with the block committed with the
// parts blockParts returns.
func makeNextBlockWithParts(ctx context.Context,
	t *testing.T,
	state sm.State,
	signer types.PrivValidator,
	height int64,
	commit *types.Commit,
	blockParts blockPartsFunc) (*types.Block, types.BlockID, *types.PartSet, *types.Commit) {
	block, err := sf.MakeBlock(state, height, commit, 0)
	require.NoError(t, err)
	block.CoreChainLockedHeight = state.LastCoreChainLockedBlockHeight
	var partSet *types.PartSet
	if blockParts != nil {
		partSet = blockParts(t, height, block)
	}
	if partSet == nil {
		partSet, err = block.MakePartSet(types.BlockPartSizeBytes)
		require.NoError(t, err)
	}
	blockID := block.BlockID(partSet)

	// Simulate a commit for the current height
	vote, err := factory.MakeVote(
		ctx,
		signer,
		state.Validators,
		block.Header.ChainID,
		0,
		block.Header.Height,
		0,
		2,
		blockID,
	)
	require.NoError(t, err)
	seenCommit := types.NewCommit(
		vote.Height,
		vote.Round,
		blockID,
		vote.VoteExtensions,
		&types.CommitSigns{
			QuorumSigns: types.QuorumSigns{
				BlockSign:               vote.BlockSignature,
				VoteExtensionSignatures: vote.VoteExtensions.GetSignatures(),
			},
			QuorumHash: state.Validators.QuorumHash,
		},
	)
	return block, blockID, partSet, seenCommit
}

func (rts *reactorTestSuite) start(ctx context.Context, t *testing.T) {
	t.Helper()
	rts.network.Start(ctx, t)
	require.Len(t,
		rts.network.AnyNode().PeerManager.Peers(),
		len(rts.nodes)-1,
		"network does not have expected number of nodes")
}

func TestReactor_AbruptDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := config.ResetTestRoot(t.TempDir(), "block_sync_reactor_test")
	require.NoError(t, err)
	defer os.RemoveAll(cfg.RootDir)

	genDoc, privVals := factory.RandGenesisDoc(1, factory.ConsensusParams())
	maxBlockHeight := int64(64)

	rts := setup(ctx, t, cfg, genDoc, privVals[0], []int64{maxBlockHeight, 0})

	require.Equal(t, maxBlockHeight, rts.reactors[rts.nodes[0]].store.Height())

	rts.start(ctx, t)

	secondaryPool := rts.reactors[rts.nodes[1]].synchronizer

	require.Eventually(
		t,
		func() bool {
			height, _ := secondaryPool.GetStatus()
			return secondaryPool.MaxPeerHeight() > 0 && height > 0 && height < 10
		},
		10*time.Second,
		10*time.Millisecond,
		"expected node to be partially synced",
	)

	// Remove synced node from the syncing node which should not result in any
	// deadlocks or race conditions within the context of poolRoutine.
	rts.peerChans[rts.nodes[1]] <- p2p.PeerUpdate{
		Status: p2p.PeerStatusDown,
		NodeID: rts.nodes[0],
	}
	rts.network.Nodes[rts.nodes[1]].PeerManager.Disconnected(ctx, rts.nodes[0])
}

func TestReactor_SyncTime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := config.ResetTestRoot(t.TempDir(), t.Name())
	require.NoError(t, err)
	defer os.RemoveAll(cfg.RootDir)

	genDoc, privVals := factory.RandGenesisDoc(1, factory.ConsensusParams())
	maxBlockHeight := int64(199)

	// A small monitor interval makes the sync-rate computation fire early
	// (after ~10 blocks instead of the default 100), so the assertion below
	// is deterministic rather than racing the rate filter.
	rts := setup(ctx, t, cfg, genDoc, privVals[0], []int64{maxBlockHeight, 0},
		WithSynchronizerOptions(WithMonitorInterval(10)))
	require.Equal(t, maxBlockHeight, rts.reactors[rts.nodes[0]].store.Height())
	rts.start(ctx, t)

	require.Eventually(
		t,
		func() bool {
			node := rts.reactors[rts.nodes[1]]
			return node.GetRemainingSyncTime() > time.Nanosecond &&
				node.synchronizer.getLastSyncRate() > 0.001
		},
		10*time.Second,
		10*time.Millisecond,
		"expected node to be partially synced",
	)
}

func TestReactor_NoBlockResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := config.ResetTestRoot(t.TempDir(), "block_sync_reactor_test")
	require.NoError(t, err)
	defer os.RemoveAll(cfg.RootDir)

	genDoc, privVals := factory.RandGenesisDoc(1, factory.ConsensusParams())
	maxBlockHeight := int64(65)

	shortStatusUpdateInterval := 2 * time.Second

	rts := setup(
		ctx,
		t,
		cfg,
		genDoc,
		privVals[0],
		[]int64{maxBlockHeight, 0},
		WithStatusUpdateInterval(shortStatusUpdateInterval),
	)

	require.Equal(t, maxBlockHeight, rts.reactors[rts.nodes[0]].store.Height())

	rts.start(ctx, t)

	testCases := []struct {
		height   int64
		existent bool
	}{
		{maxBlockHeight + 2, false},
		{10, true},
		{1, true},
		{100, false},
	}

	reactor := rts.reactors[rts.nodes[1]]
	secondaryPool := reactor.synchronizer
	// Wait long enough to cover several status updates plus processing lag.
	waitForFullSync := 4 * reactor.statusUpdateInterval
	if waitForFullSync < 30*time.Second {
		waitForFullSync = 30 * time.Second
	}
	statusLogTicker := time.NewTicker(2 * time.Second)
	defer statusLogTicker.Stop()
	stopStatusLog := make(chan struct{})
	defer close(stopStatusLog)
	go func() {
		for {
			select {
			case <-statusLogTicker.C:
				height, pending := secondaryPool.GetStatus()
				reactor.logger.Info(
					"sync progress",
					"height", height,
					"pending_requests", pending,
					"max_peer_height", secondaryPool.MaxPeerHeight(),
					"caught_up", secondaryPool.IsCaughtUp(),
				)
			case <-stopStatusLog:
				return
			}
		}
	}()

	require.Eventually(
		t,
		func() bool { return secondaryPool.MaxPeerHeight() > 0 && secondaryPool.IsCaughtUp() },
		waitForFullSync,
		10*time.Millisecond,
		"expected node to be fully synced",
	)

	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			block := reactor.store.LoadBlock(tc.height)
			require.Equal(t, tc.existent, block != nil)
		})
	}
}

// TestReactor_SyncBlocksAsCommitted syncs blocks committed with bytes or parts
// that types.Block.MakePartSet does not reproduce from the decoded block. The
// commit's part set header is computed over the committed parts, so a peer can
// only serve such a block from the parts it stored, and the syncing node must
// rebuild the part set from the bytes it received.
func TestReactor_SyncBlocksAsCommitted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := config.ResetTestRoot(t.TempDir(), t.Name())
	require.NoError(t, err)
	defer os.RemoveAll(cfg.RootDir)

	genDoc, privVals := factory.RandGenesisDoc(1, factory.ConsensusParams())
	const (
		maxBlockHeight = int64(12)
		// a field that tendermint.types.Block does not define, which decoding
		// skips, appended to the block's bytes
		unknownFieldHeight = int64(3)
		// the block's bytes cut into parts shorter than types.BlockPartSizeBytes
		smallPartsHeight = int64(5)
		bothHeight       = int64(7)
		// one part, whose proof is of a tree of more leaves than the header
		// counts, so the header cannot be computed from the part
		largerTreeHeight = int64(9)
		smallPartSize    = 100
	)
	committedHeights := []int64{unknownFieldHeight, smallPartsHeight, bothHeight, largerTreeHeight}
	blockParts := func(t *testing.T, height int64, block *types.Block) *types.PartSet {
		switch height {
		case unknownFieldHeight:
			return types.NewPartSetFromData(withUnknownField(t, block), types.BlockPartSizeBytes)
		case smallPartsHeight:
			return types.NewPartSetFromData(blockBytes(t, block), smallPartSize)
		case bothHeight:
			return types.NewPartSetFromData(withUnknownField(t, block), smallPartSize)
		case largerTreeHeight:
			return withProofOfLargerTree(t, block)
		}
		return nil
	}
	rts := setupWithBlockParts(ctx, t, cfg, genDoc, privVals[0], []int64{maxBlockHeight, 0}, blockParts)
	source, syncing := rts.reactors[rts.nodes[0]], rts.reactors[rts.nodes[1]]
	require.Equal(t, maxBlockHeight, source.store.Height())
	for _, height := range committedHeights {
		require.NotEqual(t,
			source.store.LoadBlockMeta(height).BlockID.PartSetHeader,
			source.store.LoadBlock(height).BlockID(nil).PartSetHeader,
			"height %d must be committed with parts a re-encoding does not reproduce", height)
	}

	rts.start(ctx, t)

	require.Eventually(t,
		func() bool { return syncing.store.Height() > largerTreeHeight },
		30*time.Second,
		10*time.Millisecond,
		"expected node to sync the blocks committed with other bytes")
	for _, height := range committedHeights {
		require.Equal(t, source.store.LoadBlockMeta(height).BlockID, syncing.store.LoadBlockMeta(height).BlockID,
			"block ID at height %d", height)
		require.Equal(t, loadCommittedParts(source.store, height), loadCommittedParts(syncing.store, height),
			"stored parts at height %d", height)
	}
}

// withUnknownField returns the bytes of block with field 15 appended, a field
// tendermint.types.Block does not define and decoding skips.
func withUnknownField(t *testing.T, block *types.Block) []byte {
	// tag (15 << 3 | varint wire type), then the value 1
	return append(blockBytes(t, block), 15<<3, 1)
}

// withProofOfLargerTree returns a part set of one part with the bytes of block,
// whose root is that of a tree of two leaves. PartSet.AddPart checks a proof
// against the root and not against the header's total, so consensus accepts it.
func withProofOfLargerTree(t *testing.T, block *types.Block) *types.PartSet {
	bz := blockBytes(t, block)
	root, proofs := merkle.ProofsFromByteSlices([][]byte{bz, []byte("leaf the header does not count")})
	partSet := types.NewPartSetFromHeader(types.PartSetHeader{Total: 1, Hash: root})
	added, err := partSet.AddPart(&types.Part{Index: 0, Bytes: bz, Proof: *proofs[0]})
	require.NoError(t, err)
	require.True(t, added)
	return partSet
}

func TestReactor_BadBlockStopsPeer(t *testing.T) {
	// Ultimately, this should be refactored to be less integration test oriented
	// and more unit test oriented by simply testing channel sends and receives.
	// See: https://github.com/tendermint/tendermint/issues/6005
	t.SkipNow()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := config.ResetTestRoot(t.TempDir(), "block_sync_reactor_test")
	require.NoError(t, err)
	defer os.RemoveAll(cfg.RootDir)

	maxBlockHeight := int64(48)
	genDoc, privVals := factory.RandGenesisDoc(1, factory.ConsensusParams())

	rts := setup(ctx, t, cfg, genDoc, privVals[0], []int64{maxBlockHeight, 0, 0, 0, 0})

	require.Equal(t, maxBlockHeight, rts.reactors[rts.nodes[0]].store.Height())

	rts.start(ctx, t)

	require.Eventually(
		t,
		func() bool {
			caughtUp := true
			for _, id := range rts.nodes[1 : len(rts.nodes)-1] {
				if rts.reactors[id].synchronizer.MaxPeerHeight() == 0 || !rts.reactors[id].synchronizer.IsCaughtUp() {
					caughtUp = false
				}
			}

			return caughtUp
		},
		10*time.Minute,
		10*time.Millisecond,
		"expected all nodes to be fully synced",
	)

	for _, id := range rts.nodes[:len(rts.nodes)-1] {
		require.Len(t, rts.reactors[id].synchronizer.peerStore.Len(), 3)
	}

	// Mark testSuites[3] as an invalid peer which will cause newSuite to disconnect
	// from this peer.
	//
	// XXX: This causes a potential race condition.
	// See: https://github.com/tendermint/tendermint/issues/6005
	otherGenDoc, otherPrivVals := factory.RandGenesisDoc(1, factory.ConsensusParams())
	newNode := rts.network.MakeNode(ctx, t, nil, p2ptest.NodeOptions{
		MaxPeers:     uint16(len(rts.nodes) + 1),
		MaxConnected: uint16(len(rts.nodes) + 1),
		ChanDescr:    p2p.ChannelDescriptors(cfg),
	})
	rts.addNode(ctx, t, newNode.NodeID, otherGenDoc, otherPrivVals[0], maxBlockHeight)

	// add a fake peer just so we do not wait for the consensus ticker to timeout
	rts.reactors[newNode.NodeID].synchronizer.AddPeer(newPeerData("00ff", 10, 10))

	// wait for the new peer to catch up and become fully synced
	require.Eventually(
		t,
		func() bool {
			return rts.reactors[newNode.NodeID].synchronizer.MaxPeerHeight() > 0 && rts.reactors[newNode.NodeID].synchronizer.IsCaughtUp()
		},
		10*time.Minute,
		10*time.Millisecond,
		"expected new node to be fully synced",
	)

	require.Eventuallyf(
		t,
		func() bool { return rts.reactors[newNode.NodeID].synchronizer.peerStore.Len() < len(rts.nodes)-1 },
		10*time.Minute,
		10*time.Millisecond,
		"invalid number of peers; expected < %d, got: %d",
		len(rts.nodes)-1,
		rts.reactors[newNode.NodeID].synchronizer.peerStore.Len(),
	)
}
