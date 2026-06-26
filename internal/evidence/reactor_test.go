package evidence_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/dashpay/dashd-go/btcjson"
	"github.com/fortytw2/leaktest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/evidence"
	"github.com/dashpay/tenderdash/internal/evidence/mocks"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/p2ptest"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

var (
	numEvidence = 10

	rng = rand.New(rand.NewSource(time.Now().UnixNano()))
)

type reactorTestSuite struct {
	network          *p2ptest.Network
	logger           log.Logger
	reactors         map[types.NodeID]*evidence.Reactor
	pools            map[types.NodeID]*evidence.Pool
	evidenceChannels map[types.NodeID]p2p.Channel
	peerUpdates      map[types.NodeID]*p2p.PeerUpdates
	peerChans        map[types.NodeID]chan p2p.PeerUpdate
	nodes            []*p2ptest.Node
	numStateStores   int
}

func setup(ctx context.Context, t *testing.T, stateStores []sm.Store) *reactorTestSuite {
	t.Helper()

	pID := make([]byte, 16)
	_, err := rng.Read(pID)
	require.NoError(t, err)

	numStateStores := len(stateStores)
	rts := &reactorTestSuite{
		numStateStores: numStateStores,
		logger:         log.NewNopLogger().With("testCase", t.Name()),
		network:        p2ptest.MakeNetwork(ctx, t, p2ptest.NetworkOptions{NumNodes: numStateStores}, log.NewNopLogger()),
		reactors:       make(map[types.NodeID]*evidence.Reactor, numStateStores),
		pools:          make(map[types.NodeID]*evidence.Pool, numStateStores),
		peerUpdates:    make(map[types.NodeID]*p2p.PeerUpdates, numStateStores),
		peerChans:      make(map[types.NodeID]chan p2p.PeerUpdate, numStateStores),
	}

	// Use the production channel descriptor so the test channels match real
	// deployment (RecvBufferCapacity, Priority, Name, etc.).
	chDesc := evidence.GetChannelDescriptor()
	rts.evidenceChannels = rts.network.MakeChannelsNoCleanup(ctx, t, chDesc)
	require.Len(t, rts.network.AnyNode().PeerManager.Peers(), 0)

	idx := 0
	evidenceTime := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)

	for nodeID := range rts.network.Nodes {
		logger := rts.logger.With("validator", idx)
		evidenceDB := dbm.NewMemDB()
		blockStore := &mocks.BlockStore{}
		state, _ := stateStores[idx].Load()
		blockStore.On("Base").Return(int64(1))
		blockStore.On("LoadBlockMeta", mock.AnythingOfType("int64")).Return(func(h int64) *types.BlockMeta {
			if h <= state.LastBlockHeight {
				return makeBlockMeta(h, evidenceTime, state.Validators)
			}
			return nil
		})
		eventBus := eventbus.NewDefault(logger)
		err = eventBus.Start(ctx)
		require.NoError(t, err)

		rts.pools[nodeID] = evidence.NewPool(logger, evidenceDB, stateStores[idx], blockStore, evidence.NopMetrics(), eventBus)
		startPool(t, rts.pools[nodeID], stateStores[idx])

		require.NoError(t, err)

		rts.peerChans[nodeID] = make(chan p2p.PeerUpdate)
		pu := p2p.NewPeerUpdates(rts.peerChans[nodeID], 1, "evidence")
		rts.peerUpdates[nodeID] = pu
		rts.network.Nodes[nodeID].PeerManager.Register(ctx, pu)
		rts.nodes = append(rts.nodes, rts.network.Nodes[nodeID])

		chCreator := func(_ctx context.Context, _chDesc *p2p.ChannelDescriptor) (p2p.Channel, error) {
			return rts.evidenceChannels[nodeID], nil
		}

		rts.reactors[nodeID] = evidence.NewReactor(
			logger,
			chCreator,
			func(_ctx context.Context, _ string) *p2p.PeerUpdates { return pu },
			rts.pools[nodeID])

		require.NoError(t, rts.reactors[nodeID].Start(ctx))
		require.True(t, rts.reactors[nodeID].IsRunning())

		idx++
	}

	t.Cleanup(func() {
		for _, r := range rts.reactors {
			if r.IsRunning() {
				r.Stop()
				r.Wait()
				require.False(t, r.IsRunning())
			}
		}

	})
	t.Cleanup(leaktest.Check(t))

	return rts
}

func (rts *reactorTestSuite) start(ctx context.Context, t *testing.T) {
	rts.network.Start(ctx, t)
	require.Len(t,
		rts.network.AnyNode().PeerManager.Peers(),
		rts.numStateStores-1,
		"network does not have expected number of nodes")
}

func (rts *reactorTestSuite) waitForEvidence(t *testing.T, evList types.EvidenceList, ids ...types.NodeID) {
	t.Helper()

	// fn polls pool until it holds all expected evidence or 30s elapses.
	// Returns nil on success, an error on timeout or mismatch.
	// Safe to call from any goroutine — no t.FailNow inside.
	fn := func(pool *evidence.Pool) error {
		var localEvList []types.Evidence

		// wait till we have at least the amount of evidence that we expect;
		// if there's more local evidence the assertion below will catch it
		deadline := time.Now().Add(30 * time.Second)
		for len(localEvList) < len(evList) {
			if time.Now().After(deadline) {
				return fmt.Errorf("timed out waiting for evidence: have %d, want %d",
					len(localEvList), len(evList))
			}
			var size int64
			localEvList, size = pool.PendingEvidence(int64(len(evList) * 1000))
			t.Log("current wait status:", "|",
				"local", len(localEvList), "|",
				"waitlist", len(evList), "|",
				"size", size)
			time.Sleep(100 * time.Millisecond)
		}

		// put the reaped evidence in a map so we can quickly check we got everything
		evMap := make(map[string]types.Evidence)
		for _, e := range localEvList {
			evMap[string(e.Hash())] = e
		}

		for i, expectedEv := range evList {
			gotEv := evMap[string(expectedEv.Hash())]
			if !reflect.DeepEqual(expectedEv, gotEv) {
				return fmt.Errorf("evidence %d mismatch: got %v, expected %v", i, gotEv, expectedEv)
			}
		}
		return nil
	}

	if len(ids) == 1 {
		// special case: single pool, run directly on the test goroutine so
		// the stack trace is clearer on failure.
		require.NoError(t, fn(rts.pools[ids[0]]))
		return
	}

	// Collect the pool IDs we need to wait for.
	poolIDs := make([]types.NodeID, 0)
	for id := range rts.pools {
		if len(ids) > 0 && !p2ptest.NodeInSlice(id, ids) {
			// if an ID list is specified, then we only
			// want to wait for those pools that are
			// specified in the list, otherwise, wait for
			// all pools.
			continue
		}
		poolIDs = append(poolIDs, id)
	}

	// Fan out to goroutines; collect errors via channel to avoid calling
	// t.FailNow from a non-test goroutine (prohibited by Go's testing package).
	errCh := make(chan error, len(poolIDs))
	wg := sync.WaitGroup{}
	for _, id := range poolIDs {
		wg.Add(1)
		go func(id types.NodeID) {
			defer wg.Done()
			errCh <- fn(rts.pools[id])
		}(id)
	}
	wg.Wait()
	close(errCh)

	// Assert on the test goroutine after all workers finish.
	for err := range errCh {
		require.NoError(t, err)
	}
}

func createEvidenceList(
	ctx context.Context,
	t *testing.T,
	pool *evidence.Pool,
	val types.PrivValidator,
	numEvidence int,
) types.EvidenceList {
	t.Helper()

	evList := make([]types.Evidence, numEvidence)

	vals := pool.State().Validators
	for i := 0; i < numEvidence; i++ {
		ev, err := types.NewMockDuplicateVoteEvidenceWithValidator(
			ctx,
			int64(i+1),
			time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
			val,
			evidenceChainID,
			vals.QuorumType,
			vals.QuorumHash,
		)
		require.NoError(t, err)
		err = pool.AddEvidence(ctx, ev)
		require.NoError(t, err,
			"adding evidence it#%d of %d to pool with height %d",
			i, numEvidence, pool.State().LastBlockHeight)
		evList[i] = ev
	}

	return evList
}

func TestReactorMultiDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quorumHash := crypto.RandQuorumHash()
	val := types.NewMockPVForQuorum(quorumHash)
	height := int64(numEvidence) + 10

	stateDB1 := initializeValidatorState(ctx, t, val, height, btcjson.LLMQType_5_60, quorumHash)
	stateDB2 := initializeValidatorState(ctx, t, val, height, btcjson.LLMQType_5_60, quorumHash)

	rts := setup(ctx, t, []sm.Store{stateDB1, stateDB2})
	primary := rts.nodes[0]
	secondary := rts.nodes[1]

	_ = createEvidenceList(ctx, t, rts.pools[primary.NodeID], val, numEvidence)

	require.Equal(t, primary.PeerManager.Status(secondary.NodeID), p2p.PeerStatusDown)

	rts.start(ctx, t)

	require.Equal(t, primary.PeerManager.Status(secondary.NodeID), p2p.PeerStatusUp)
	// Ensure "disconnecting" the secondary peer from the primary more than once
	// is handled gracefully.

	primary.PeerManager.Disconnected(ctx, secondary.NodeID)
	require.Equal(t, primary.PeerManager.Status(secondary.NodeID), p2p.PeerStatusDown)
	_, err := primary.PeerManager.TryEvictNext()
	require.NoError(t, err)
	primary.PeerManager.Disconnected(ctx, secondary.NodeID)

	require.Equal(t, primary.PeerManager.Status(secondary.NodeID), p2p.PeerStatusDown)
	require.Equal(t, secondary.PeerManager.Status(primary.NodeID), p2p.PeerStatusUp)

}

// TestReactorBroadcastEvidence creates an environment of multiple peers that
// are all at the same height. One peer, designated as a primary, gossips all
// evidence to the remaining peers.
func TestReactorBroadcastEvidence(t *testing.T) {
	numPeers := 7

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// create a stateDB for all test suites (nodes)
	stateDBs := make([]sm.Store, numPeers)
	quorumHash := crypto.RandQuorumHash()
	val := types.NewMockPVForQuorum(quorumHash)

	// We need all validators saved for heights at least as high as we have
	// evidence for.
	height := int64(numEvidence) + 10
	for i := 0; i < numPeers; i++ {
		stateDBs[i] = initializeValidatorState(ctx, t, val, height, btcjson.LLMQType_5_60, quorumHash)
	}

	rts := setup(ctx, t, stateDBs)

	rts.start(ctx, t)

	// Create a series of fixtures where each suite contains a reactor and
	// evidence pool. In addition, we mark a primary suite and the rest are
	// secondaries where each secondary is added as a peer via a PeerUpdate to the
	// primary. As a result, the primary will gossip all evidence to each secondary.

	primary := rts.network.AnyNode()
	secondaries := make([]*p2ptest.Node, 0, len(rts.network.NodeIDs())-1)
	secondaryIDs := make([]types.NodeID, 0, cap(secondaries))
	for id := range rts.network.Nodes {
		if id == primary.NodeID {
			continue
		}

		secondaries = append(secondaries, rts.network.Nodes[id])
		secondaryIDs = append(secondaryIDs, id)
	}

	evList := createEvidenceList(ctx, t, rts.pools[primary.NodeID], val, numEvidence)

	// Add each secondary suite (node) as a peer to the primary suite (node). This
	// will cause the primary to gossip all evidence to the secondaries.
	for _, suite := range secondaries {
		rts.peerChans[primary.NodeID] <- p2p.PeerUpdate{
			Status: p2p.PeerStatusUp,
			NodeID: suite.NodeID,
		}
	}

	// Wait till all secondary suites (reactor) received all evidence from the
	// primary suite (node).
	rts.waitForEvidence(t, evList, secondaryIDs...)

	for _, pool := range rts.pools {
		require.Equal(t, numEvidence, int(pool.Size()))
	}

}

// TestReactorSelectiveBroadcast tests a context where we have two reactors
// connected to one another but are at different heights. Reactor 1 which is
// ahead receives a list of evidence.
func TestReactorBroadcastEvidence_Lagging(t *testing.T) {
	quorumHash := crypto.RandQuorumHash()
	val := types.NewMockPVForQuorum(quorumHash)
	height1 := int64(numEvidence) + 10
	height2 := int64(numEvidence) / 2

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// stateDB1 is ahead of stateDB2, where stateDB1 has all heights (1-20) and
	// stateDB2 only has heights 1-5.
	stateDB1 := initializeValidatorState(ctx, t, val, height1, btcjson.LLMQType_5_60, quorumHash)
	stateDB2 := initializeValidatorState(ctx, t, val, height2, btcjson.LLMQType_5_60, quorumHash)

	rts := setup(ctx, t, []sm.Store{stateDB1, stateDB2})
	rts.start(ctx, t)

	primary := rts.nodes[0]
	secondary := rts.nodes[1]

	// Send a list of valid evidence to the first reactor's, the one that is ahead,
	// evidence pool.
	evList := createEvidenceList(ctx, t, rts.pools[primary.NodeID], val, numEvidence)

	// Add each secondary suite (node) as a peer to the primary suite (node). This
	// will cause the primary to gossip all evidence to the secondaries.
	rts.peerChans[primary.NodeID] <- p2p.PeerUpdate{
		Status: p2p.PeerStatusUp,
		NodeID: secondary.NodeID,
	}

	// only ones less than the peers height should make it through
	rts.waitForEvidence(t, evList[:height2], secondary.NodeID)

	require.Equal(t, numEvidence, int(rts.pools[primary.NodeID].Size()))
	require.Equal(t, int(height2), int(rts.pools[secondary.NodeID].Size()))
}

func TestReactorBroadcastEvidence_Pending(t *testing.T) {
	quorumHash := crypto.RandQuorumHash()
	val := types.NewMockPVForQuorum(quorumHash)
	height := int64(10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stateDB1 := initializeValidatorState(ctx, t, val, height, btcjson.LLMQType_5_60, quorumHash)
	stateDB2 := initializeValidatorState(ctx, t, val, height, btcjson.LLMQType_5_60, quorumHash)

	rts := setup(ctx, t, []sm.Store{stateDB1, stateDB2})
	primary := rts.nodes[0]
	secondary := rts.nodes[1]

	evList := createEvidenceList(ctx, t, rts.pools[primary.NodeID], val, numEvidence)

	// Manually add half the evidence to the secondary which will mark them as
	// pending.
	for i := 0; i < numEvidence/2; i++ {
		err := rts.pools[secondary.NodeID].AddEvidence(ctx, evList[i])
		require.NoError(t, err)
	}

	// the secondary should have half the evidence as pending
	require.Equal(t, numEvidence/2, int(rts.pools[secondary.NodeID].Size()))

	rts.start(ctx, t)

	// The secondary reactor should have received all the evidence ignoring the
	// already pending evidence.
	rts.waitForEvidence(t, evList, secondary.NodeID)

	// check to make sure that all of the evidence has
	// propogated
	require.Len(t, rts.pools, 2)
	assert.EqualValues(t, numEvidence, rts.pools[primary.NodeID].Size(),
		"primary node should have all the evidence")
	assert.EqualValues(t, numEvidence, rts.pools[secondary.NodeID].Size(),
		"secondary nodes should have caught up")
}

func TestReactorBroadcastEvidence_Committed(t *testing.T) {
	quorumHash := crypto.RandQuorumHash()
	val := types.NewMockPVForQuorum(quorumHash)
	height := int64(10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stateDB1 := initializeValidatorState(ctx, t, val, height, btcjson.LLMQType_5_60, quorumHash)
	stateDB2 := initializeValidatorState(ctx, t, val, height, btcjson.LLMQType_5_60, quorumHash)

	rts := setup(ctx, t, []sm.Store{stateDB1, stateDB2})

	primary := rts.nodes[0]
	secondary := rts.nodes[1]

	// add all evidence to the primary reactor
	evList := createEvidenceList(ctx, t, rts.pools[primary.NodeID], val, numEvidence)

	// Manually add half the evidence to the secondary which will mark them as
	// pending.
	for i := 0; i < numEvidence/2; i++ {
		err := rts.pools[secondary.NodeID].AddEvidence(ctx, evList[i])
		require.NoError(t, err)
	}

	// the secondary should have half the evidence as pending
	require.Equal(t, numEvidence/2, int(rts.pools[secondary.NodeID].Size()))

	state, err := stateDB2.Load()
	require.NoError(t, err)

	// update the secondary's pool such that all pending evidence is committed
	state.LastBlockHeight++
	rts.pools[secondary.NodeID].Update(ctx, state, evList[:numEvidence/2])

	// the secondary should have half the evidence as committed
	require.Equal(t, 0, int(rts.pools[secondary.NodeID].Size()))

	// start the network and ensure it's configured
	rts.start(ctx, t)

	// The secondary reactor should have received all the evidence ignoring the
	// already committed evidence.
	rts.waitForEvidence(t, evList[numEvidence/2:], secondary.NodeID)

	require.Len(t, rts.pools, 2)
	assert.EqualValues(t, numEvidence, rts.pools[primary.NodeID].Size(),
		"primary node should have all the evidence")
	assert.EqualValues(t, numEvidence/2, rts.pools[secondary.NodeID].Size(),
		"secondary nodes should have caught up")
}

func TestReactorBroadcastEvidence_FullyConnected(t *testing.T) {
	numPeers := 7

	// create a stateDB for all test suites (nodes)
	stateDBs := make([]sm.Store, numPeers)
	quorumHash := crypto.RandQuorumHash()
	val := types.NewMockPVForQuorum(quorumHash)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// We need all validators saved for heights at least as high as we have
	// evidence for.
	height := int64(numEvidence) + 10
	for i := 0; i < numPeers; i++ {
		stateDBs[i] = initializeValidatorState(ctx, t, val, height, btcjson.LLMQType_5_60, quorumHash)
	}

	rts := setup(ctx, t, stateDBs)
	rts.start(ctx, t)

	evList := createEvidenceList(ctx, t, rts.pools[rts.network.AnyNode().NodeID], val, numEvidence)

	// every suite (reactor) connects to every other suite (reactor)
	for outerID, outerChan := range rts.peerChans {
		for innerID := range rts.peerChans {
			if outerID != innerID {
				outerChan <- p2p.PeerUpdate{
					Status: p2p.PeerStatusUp,
					NodeID: innerID,
				}
			}
		}
	}

	// wait till all suites (reactors) received all evidence from other suites (reactors)
	rts.waitForEvidence(t, evList)

	for _, pool := range rts.pools {
		require.Equal(t, numEvidence, int(pool.Size()))

		// commit state so we do not continue to repeat gossiping the same evidence
		state := pool.State()
		state.LastBlockHeight++
		pool.Update(ctx, state, evList)
	}
}

func TestEvidenceListSerialization(t *testing.T) {
	exampleVote := func(msgType byte) *types.Vote {
		return &types.Vote{
			Type:   tmproto.SignedMsgType(msgType),
			Height: 3,
			Round:  2,
			BlockID: types.BlockID{
				Hash: crypto.Checksum([]byte("blockID_hash")),
				PartSetHeader: types.PartSetHeader{
					Total: 1000000,
					Hash:  crypto.Checksum([]byte("blockID_part_set_header_hash")),
				},
			},
			ValidatorProTxHash: crypto.ProTxHashFromSeedBytes([]byte("validator_pro_tx_hash")),
			ValidatorIndex:     56789,
		}
	}

	privKey := bls12381.GenPrivKey()
	val := &types.Validator{
		PubKey:      privKey.PubKey(),
		VotingPower: types.DefaultDashVotingPower,
		ProTxHash:   crypto.ProTxHashFromSeedBytes([]byte("validator_pro_tx_hash")),
	}

	valSet := types.NewValidatorSet(
		[]*types.Validator{val},
		val.PubKey,
		btcjson.LLMQType_5_60,
		crypto.RandQuorumHash(),
		true,
		nil,
	)

	dupl, err := types.NewDuplicateVoteEvidence(
		exampleVote(1),
		exampleVote(2),
		defaultEvidenceTime,
		valSet,
	)
	require.NoError(t, err)

	testCases := map[string]struct {
		evidenceList []types.Evidence
		expBytes     string
	}{
		"DuplicateVoteEvidence": {
			[]types.Evidence{dupl},
			"0a83020a80020a78080210031802224a0a208b01023386c371778ecb6368573e539afc3cc860ec3a2f614e54fe5652f4fc80122608c0843d122072db3d959635dff1bb567bedaa70573392c5159666a3f8caf11e413aac52207a2a20959a8f5ef2be68d0ed3a07ed8cff85991ee7995c2ac17030f742c135f9729fbe30d5bb031278080110031802224a0a208b01023386c371778ecb6368573e539afc3cc860ec3a2f614e54fe5652f4fc80122608c0843d122072db3d959635dff1bb567bedaa70573392c5159666a3f8caf11e413aac52207a2a20959a8f5ef2be68d0ed3a07ed8cff85991ee7995c2ac17030f742c135f9729fbe30d5bb03186420642a060880dbaae105",
		},
	}

	for name, tc := range testCases {
		tc := tc

		t.Run(name, func(t *testing.T) {
			protoEv := make([]tmproto.Evidence, len(tc.evidenceList))
			for i := 0; i < len(tc.evidenceList); i++ {
				ev, err := types.EvidenceToProto(tc.evidenceList[i])
				require.NoError(t, err)
				protoEv[i] = *ev
			}

			epl := tmproto.EvidenceList{
				Evidence: protoEv,
			}

			bz, err := epl.Marshal()
			require.NoError(t, err)

			require.Equal(t, tc.expBytes, hex.EncodeToString(bz))
		})
	}
}

// gateChannel is a p2p.Channel stub that lets a test pause a syncEvidence
// goroutine in the middle of its pool walk. Each Send announces itself on
// `entered` by handing the test a personal `release` channel, then blocks until
// the test closes that channel (or the shutdown safety-net fires). This gives
// the test precise control over goroutine interleaving without sleeps.
//
// Note: the park select deliberately does NOT watch ctx.Done(); a goroutine
// must stay frozen through a PeerStatusDown cancellation so the test can install
// a replacement goroutine before the stale one is allowed to run its deferred
// cleanup — that is exactly the Down→Up flap race being reproduced.
type gateChannel struct {
	entered  chan chan struct{}
	shutdown chan struct{} // closed at cleanup to free any goroutine still parked
}

func newGateChannel() *gateChannel {
	return &gateChannel{
		entered:  make(chan chan struct{}),
		shutdown: make(chan struct{}),
	}
}

func (g *gateChannel) Send(ctx context.Context, _ p2p.Envelope) error {
	release := make(chan struct{})
	// Announce entry. Watch ctx here only so a goroutine canceled before it
	// parks cannot deadlock the handshake; the test always reads `entered` on
	// the happy path.
	select {
	case g.entered <- release:
	case <-ctx.Done():
		return ctx.Err()
	}
	// Park until explicitly released. Intentionally not tied to ctx (see type doc).
	select {
	case <-release:
	case <-g.shutdown:
	}
	return nil
}

func (g *gateChannel) Err() error                                         { return nil }
func (g *gateChannel) SendError(_ context.Context, _ p2p.PeerError) error { return nil }
func (g *gateChannel) Receive(_ context.Context) p2p.ChannelIterator      { return nopIterator{} }
func (g *gateChannel) String() string                                     { return "gateChannel" }

// TestReactorPeerFlapNoGoroutineEviction is the deterministic regression guard
// for the per-peer sync-identity fix (PR #1366).
//
// It reproduces the exact Down→Up flap race the identity guards against:
//
//  1. PeerStatusUp(P) starts sync goroutine G1; G1 is frozen mid-walk inside Send.
//  2. PeerStatusDown(P) cancels G1 and deletes P's map entry — but G1 is still
//     alive (frozen) and has NOT yet run its deferred cleanup.
//  3. PeerStatusUp(P) starts a fresh goroutine G2 and installs its map entry.
//  4. G1 is released; its deferred cleanup runs while G2's entry is in the map.
//
// The deferred cleanup deletes P's entry only when the map's current id ==
// G1's own id. Each install assigns the next value of the reactor's monotonic
// nextSyncID counter, so G1's id differs from G2's and G1 leaves G2's entry
// untouched.
//
// # Why it fails on a broken guard and passes when correct (deterministic)
//
// If the install path ever handed two goroutines the same id (e.g. a refactor
// that stopped incrementing the counter, or derived the id from a non-unique
// source), G1 and G2 would share an id:
//   - The PeerRoutineID(P) distinctness assertion (id1 != id2) fails directly.
//   - G1's deferred cleanup sees current.id == its own id and evicts G2, so
//     PeerRoutineActive(P) flips to false and the require.Never trips.
//
// Both effects are deterministic: the gate channel forces step 4 to occur
// strictly after step 3, so G1's cleanup always observes G2's installed entry.
// With the monotonic counter the ids are distinct and both assertions hold.
func TestReactorPeerFlapNoGoroutineEviction(t *testing.T) {
	const (
		// Ticker effectively never fires, so each goroutine walks the pool exactly
		// once and then parks — keeping the interleaving fully under test control.
		syncInterval = time.Hour
		numEv        = 1

		settleWait = 5 * time.Second        // bound for observing a map mutation
		settleTick = 5 * time.Millisecond   // poll cadence for the bound above
		neverWin   = 300 * time.Millisecond // window over which the entry must survive
		neverTick  = 10 * time.Millisecond
	)

	restore := evidence.SetEvidenceSyncIntervalForTesting(syncInterval)
	defer restore()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Leak check first so it runs last (t.Cleanup is LIFO): a stale goroutine
	// that survives the flap would be caught here.
	t.Cleanup(leaktest.Check(t))

	// Minimal evidence pool with one pending item so the walk reaches Send.
	quorumHash := crypto.RandQuorumHash()
	val := types.NewMockPVForQuorum(quorumHash)
	height := int64(numEv) + 10
	stateDB := initializeValidatorState(ctx, t, val, height, btcjson.LLMQType_5_60, quorumHash)

	evidenceDB := dbm.NewMemDB()
	blockStore := &mocks.BlockStore{}
	state, err := stateDB.Load()
	require.NoError(t, err)
	evidenceTime := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	blockStore.On("Base").Return(int64(1))
	blockStore.On("LoadBlockMeta", mock.AnythingOfType("int64")).Return(
		func(h int64) *types.BlockMeta {
			if h <= state.LastBlockHeight {
				return makeBlockMeta(h, evidenceTime, state.Validators)
			}
			return nil
		},
	)
	eventBus := eventbus.NewDefault(log.NewNopLogger())
	require.NoError(t, eventBus.Start(ctx))

	pool := evidence.NewPool(log.NewNopLogger(), evidenceDB, stateDB, blockStore, evidence.NopMetrics(), eventBus)
	startPool(t, pool, stateDB)
	_ = createEvidenceList(ctx, t, pool, val, numEv)

	gate := newGateChannel()
	t.Cleanup(func() { close(gate.shutdown) }) // free any goroutine left parked on a failure path

	peerChan := make(chan p2p.PeerUpdate)
	pu := p2p.NewPeerUpdates(peerChan, 1, "evidence")
	reactor := evidence.NewReactor(
		log.NewNopLogger(),
		func(_ context.Context, _ *p2p.ChannelDescriptor) (p2p.Channel, error) { return gate, nil },
		func(_ context.Context, _ string) *p2p.PeerUpdates { return pu },
		pool,
	)
	require.NoError(t, reactor.Start(ctx))
	t.Cleanup(func() {
		reactor.Stop()
		reactor.Wait()
	})

	peerID := types.NodeID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	// Step 1: Up(P) → goroutine G1. Wait until G1 reaches Send (proves the map
	// entry is installed and G1 is now frozen mid-walk).
	peerChan <- p2p.PeerUpdate{Status: p2p.PeerStatusUp, NodeID: peerID}
	g1Release := <-gate.entered

	id1 := reactor.PeerRoutineID(peerID)
	require.NotZero(t, id1, "G1 must have an installed map entry while frozen in Send")
	require.True(t, reactor.PeerRoutineActive(peerID))
	require.Equal(t, 1, reactor.PeerRoutineCount())

	// Step 2: Down(P) → cancels G1 and deletes the map entry synchronously. G1
	// stays frozen in Send (the gate ignores ctx on the park). Observe the
	// deletion through the mutex-guarded accessor to establish happens-before.
	peerChan <- p2p.PeerUpdate{Status: p2p.PeerStatusDown, NodeID: peerID}
	require.Eventually(t, func() bool {
		return !reactor.PeerRoutineActive(peerID)
	}, settleWait, settleTick, "PeerStatusDown must delete the peer's map entry synchronously")

	// Step 3: Up(P) → fresh goroutine G2 with a new map entry. Wait until G2
	// reaches Send.
	peerChan <- p2p.PeerUpdate{Status: p2p.PeerStatusUp, NodeID: peerID}
	g2Release := <-gate.entered

	id2 := reactor.PeerRoutineID(peerID)
	require.NotZero(t, id2, "G2 must have an installed map entry while frozen in Send")
	require.True(t, reactor.PeerRoutineActive(peerID))
	require.Equal(t, 1, reactor.PeerRoutineCount())

	// Core invariant: a freshly created goroutine must receive an id distinct
	// from the prior goroutine's. The monotonic nextSyncID counter guarantees
	// this; a non-incrementing regression would yield equal ids here.
	require.NotEqual(t, id1, id2,
		"each sync goroutine must get a unique identity; equal ids defeat the flap guard")

	// Step 4: release G1. Its pctx is canceled, so once Send returns it exits and
	// runs its deferred cleanup while G2's entry is in the map.
	close(g1Release)

	// G1's stale cleanup must NOT evict G2: P's entry must survive and exactly one
	// routine must remain. On a broken guard, G1's defer sees a matching id and
	// deletes G2's entry within microseconds, tripping this assertion.
	require.Never(t, func() bool {
		return !reactor.PeerRoutineActive(peerID) || reactor.PeerRoutineCount() != 1
	}, neverWin, neverTick,
		"stale goroutine's deferred cleanup evicted the fresh sync routine's map entry")

	// Release G2 so it can finish its walk and park on the ticker select, where
	// ctx cancellation at cleanup can stop it cleanly (verified by leaktest).
	close(g2Release)
}

// TestReactorNoRebroadcastOnNonInvalidEvidenceError locks the bundled behavior
// change in handleEvidenceMessage (PR #1366): when AddEvidence fails with a
// plain (non-ErrInvalidEvidence) error — e.g. we lack the block needed to
// verify the evidence because we are behind — the reactor must NOT re-gossip
// the evidence. Re-broadcasting unverifiable evidence on every receipt would
// amplify across the mesh without ever converging; eventual delivery of any
// evidence that does enter the pending set is handled by the syncEvidence
// ticker instead.
func TestReactorNoRebroadcastOnNonInvalidEvidenceError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Leak check first so it runs last (t.Cleanup is LIFO).
	t.Cleanup(leaktest.Check(t))

	quorumHash := crypto.RandQuorumHash()
	val := types.NewMockPVForQuorum(quorumHash)
	height := int64(20)
	stateDB := initializeValidatorState(ctx, t, val, height, btcjson.LLMQType_5_60, quorumHash)

	// blockStore returns nil for every height, so verify() fails at the
	// missing-block check with a plain error (NOT ErrInvalidEvidence) — the "we
	// are behind" path that must be swallowed without disconnecting the peer.
	evidenceDB := dbm.NewMemDB()
	blockStore := &mocks.BlockStore{}
	blockStore.On("Base").Return(int64(1))
	blockStore.On("LoadBlockMeta", mock.AnythingOfType("int64")).Return(
		func(int64) *types.BlockMeta { return nil },
	)

	eventBus := eventbus.NewDefault(log.NewNopLogger())
	require.NoError(t, eventBus.Start(ctx))

	pool := evidence.NewPool(log.NewNopLogger(), evidenceDB, stateDB, blockStore, evidence.NopMetrics(), eventBus)
	startPool(t, pool, stateDB)

	// recordingChannel captures any broadcast; nopIterator makes the receive
	// loop a clean no-op so Start has nothing to process.
	rc := &recordingChannel{}
	peerChan := make(chan p2p.PeerUpdate)
	pu := p2p.NewPeerUpdates(peerChan, 1, "evidence")
	reactor := evidence.NewReactor(
		log.NewNopLogger(),
		func(_ context.Context, _ *p2p.ChannelDescriptor) (p2p.Channel, error) { return rc, nil },
		func(_ context.Context, _ string) *p2p.PeerUpdates { return pu },
		pool,
	)
	require.NoError(t, reactor.Start(ctx))
	t.Cleanup(func() {
		reactor.Stop()
		reactor.Wait()
	})

	// Build valid DuplicateVoteEvidence for a height we have no block for, so
	// verify() fails before adding anything to the pending set.
	vals := pool.State().Validators
	ev, err := types.NewMockDuplicateVoteEvidenceWithValidator(
		ctx, height-1, time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
		val, evidenceChainID, vals.QuorumType, vals.QuorumHash)
	require.NoError(t, err)
	evProto, err := types.EvidenceToProto(ev)
	require.NoError(t, err)

	peerID := types.NodeID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	err = reactor.HandleEvidenceMessageForTest(ctx, &p2p.Envelope{
		ChannelID: evidence.EvidenceChannel,
		From:      peerID,
		Message:   evProto,
	})

	// The non-ErrInvalidEvidence error is swallowed (nil return) so the router
	// does not disconnect the peer...
	require.NoError(t, err)
	// ...the evidence never entered the pending set...
	require.Zero(t, pool.Size(), "unverifiable evidence must not be added to the pool")
	// ...and crucially it was NOT re-broadcast. Any recorded send is the old
	// fall-through-to-broadcastEvidence regression.
	require.Zero(t, rc.sendCount(), "unverifiable evidence must not be re-broadcast")
}
