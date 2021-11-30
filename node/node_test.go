package node

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	abciclient "github.com/tendermint/tendermint/abci/client"
	"github.com/tendermint/tendermint/abci/example/kvstore"
	"github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
	"github.com/tendermint/tendermint/crypto/ed25519"
	"github.com/tendermint/tendermint/crypto/tmhash"
	"github.com/tendermint/tendermint/dash/quorum"
	"github.com/tendermint/tendermint/internal/evidence"
	"github.com/tendermint/tendermint/internal/mempool"
	mempoolv0 "github.com/tendermint/tendermint/internal/mempool/v0"
	"github.com/tendermint/tendermint/internal/proxy"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/internal/state/indexer"
	"github.com/tendermint/tendermint/internal/store"
	"github.com/tendermint/tendermint/internal/test/factory"
	"github.com/tendermint/tendermint/libs/log"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	tmtime "github.com/tendermint/tendermint/libs/time"
	"github.com/tendermint/tendermint/privval"
	"github.com/tendermint/tendermint/types"
	dbm "github.com/tendermint/tm-db"
)

func TestNodeStartStop(t *testing.T) {
	cfg, err := config.ResetTestRoot("node_node_test")
	require.NoError(t, err)

	defer os.RemoveAll(cfg.RootDir)

	// create & start node
	ns, err := newDefaultNode(cfg, log.TestingLogger())
	require.NoError(t, err)
	require.NoError(t, ns.Start())

	n, ok := ns.(*nodeImpl)
	require.True(t, ok)

	// wait for the node to produce a block
	blocksSub, err := n.EventBus().
		Subscribe(context.Background(), "node_test", types.EventQueryNewBlock)
	require.NoError(t, err)
	select {
	case <-blocksSub.Out():
	case <-blocksSub.Canceled():
		t.Fatal("blocksSub was canceled")
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the node to produce a block")
	}

	// check if we can read node ID of this node
	va, err := types.ParseValidatorAddress(cfg.P2P.ListenAddress)
	assert.NoError(t, err)
	nodeAddress, err := quorum.NewTCPNodeIDResolver().Resolve(va)
	assert.Equal(t, n.nodeInfo.ID(), nodeAddress.NodeID)
	assert.NoError(t, err)

	// stop the node
	go func() {
		err = n.Stop()
		require.NoError(t, err)
	}()

	select {
	case <-n.Quit():
	case <-time.After(5 * time.Second):
		pid := os.Getpid()
		p, err := os.FindProcess(pid)
		if err != nil {
			panic(err)
		}
		err = p.Signal(syscall.SIGABRT)
		fmt.Println(err)
		t.Fatal("timed out waiting for shutdown")
	}
}

func getTestNode(t *testing.T, conf *config.Config, logger log.Logger) *nodeImpl {
	t.Helper()
	ns, err := newDefaultNode(conf, logger)
	require.NoError(t, err)

	n, ok := ns.(*nodeImpl)
	require.True(t, ok)
	return n
}

func TestNodeDelayedStart(t *testing.T) {
	cfg, err := config.ResetTestRoot("node_delayed_start_test")
	require.NoError(t, err)

	defer os.RemoveAll(cfg.RootDir)
	now := tmtime.Now()

	// create & start node
	n := getTestNode(t, cfg, log.TestingLogger())
	n.GenesisDoc().GenesisTime = now.Add(2 * time.Second)

	require.NoError(t, n.Start())
	defer n.Stop() //nolint:errcheck // ignore for tests

	startTime := tmtime.Now()
	assert.Equal(t, true, startTime.After(n.GenesisDoc().GenesisTime))
}

func TestNodeSetAppVersion(t *testing.T) {
	cfg, err := config.ResetTestRoot("node_app_version_test")
	require.NoError(t, err)
	defer os.RemoveAll(cfg.RootDir)

	// create node
	n := getTestNode(t, cfg, log.TestingLogger())

	// default config uses the kvstore app
	var appVersion uint64 = kvstore.ProtocolVersion

	// check version is set in state
	state, err := n.stateStore.Load()
	require.NoError(t, err)
	assert.Equal(t, state.Version.Consensus.App, appVersion)

	// check version is set in node info
	assert.Equal(t, n.nodeInfo.ProtocolVersion.App, appVersion)
}

func TestNodeSetPrivValTCP(t *testing.T) {
	addr := "tcp://" + testFreeAddr(t)

	cfg, err := config.ResetTestRoot("node_priv_val_tcp_test")
	require.NoError(t, err)
	defer os.RemoveAll(cfg.RootDir)
	cfg.PrivValidator.ListenAddr = addr

	dialer := privval.DialTCPFn(addr, 100*time.Millisecond, ed25519.GenPrivKey())
	dialerEndpoint := privval.NewSignerDialerEndpoint(
		log.TestingLogger(),
		dialer,
	)
	privval.SignerDialerEndpointTimeoutReadWrite(100 * time.Millisecond)(dialerEndpoint)

	// We need to get the quorum hash used in config to set up the node
	pv, err := privval.LoadOrGenFilePV(cfg.PrivValidator.KeyFile(), cfg.PrivValidator.StateFile())
	require.NoError(t, err)
	quorumHash, err := pv.GetFirstQuorumHash(context.Background())
	require.NoError(t, err)

	signerServer := privval.NewSignerServer(
		dialerEndpoint,
		cfg.ChainID(),
		types.NewMockPVForQuorum(quorumHash),
	)

	go func() {
		err := signerServer.Start()
		if err != nil {
			panic(err)
		}
	}()
	defer signerServer.Stop() //nolint:errcheck // ignore for tests

	n := getTestNode(t, cfg, log.TestingLogger())
	assert.IsType(t, &privval.RetrySignerClient{}, n.PrivValidator())
}

// address without a protocol must result in error
func TestPrivValidatorListenAddrNoProtocol(t *testing.T) {
	addrNoPrefix := testFreeAddr(t)

	cfg, err := config.ResetTestRoot("node_priv_val_tcp_test")
	require.NoError(t, err)
	defer os.RemoveAll(cfg.RootDir)
	cfg.PrivValidator.ListenAddr = addrNoPrefix

	_, err = newDefaultNode(cfg, log.TestingLogger())
	assert.Error(t, err)
}

func TestNodeSetPrivValIPC(t *testing.T) {
	tmpfile := "/tmp/kms." + tmrand.Str(6) + ".sock"
	defer os.Remove(tmpfile) // clean up

	cfg, err := config.ResetTestRoot("node_priv_val_tcp_test")
	require.NoError(t, err)
	defer os.RemoveAll(cfg.RootDir)
	cfg.PrivValidator.ListenAddr = "unix://" + tmpfile

	dialer := privval.DialUnixFn(tmpfile)
	dialerEndpoint := privval.NewSignerDialerEndpoint(
		log.TestingLogger(),
		dialer,
	)
	privval.SignerDialerEndpointTimeoutReadWrite(100 * time.Millisecond)(dialerEndpoint)

	// We need to get the quorum hash used in config to set up the node
	pv, err := privval.LoadOrGenFilePV(cfg.PrivValidator.KeyFile(), cfg.PrivValidator.StateFile())
	require.NoError(t, err)
	quorumHash, err := pv.GetFirstQuorumHash(context.Background())
	require.NoError(t, err)

	pvsc := privval.NewSignerServer(
		dialerEndpoint,
		cfg.ChainID(),
		types.NewMockPVForQuorum(quorumHash),
	)

	go func() {
		err := pvsc.Start()
		require.NoError(t, err)
	}()
	defer pvsc.Stop() //nolint:errcheck // ignore for tests
	n := getTestNode(t, cfg, log.TestingLogger())
	assert.IsType(t, &privval.RetrySignerClient{}, n.PrivValidator())
}

// testFreeAddr claims a free port so we don't block on listener being ready.
func testFreeAddr(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	return fmt.Sprintf("127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)
}

// create a proposal block using real and full
// mempool and evidence pool and validate it.
func TestCreateProposalBlock(t *testing.T) {
	cfg, err := config.ResetTestRoot("node_create_proposal")
	require.NoError(t, err)
	defer os.RemoveAll(cfg.RootDir)
	cc := abciclient.NewLocalCreator(kvstore.NewApplication())
	proxyApp := proxy.NewAppConns(cc, proxy.NopMetrics())
	err = proxyApp.Start()
	require.Nil(t, err)
	defer proxyApp.Stop() //nolint:errcheck // ignore for tests

	logger := log.TestingLogger()

	const height int64 = 1
	state, stateDB, privVals := state(1, height)
	stateStore := sm.NewStore(stateDB)
	maxBytes := 16568
	var partSize uint32 = 256
	maxEvidenceBytes := int64(maxBytes / 2)
	state.ConsensusParams.Block.MaxBytes = int64(maxBytes)
	state.ConsensusParams.Evidence.MaxBytes = maxEvidenceBytes
	proposerProTxHash, _ := state.Validators.GetByIndex(0)

	// Make Mempool
	mp := mempoolv0.NewCListMempool(
		cfg.Mempool,
		proxyApp.Mempool(),
		state.LastBlockHeight,
		mempoolv0.WithMetrics(mempool.NopMetrics()),
		mempoolv0.WithPreCheck(sm.TxPreCheck(state)),
		mempoolv0.WithPostCheck(sm.TxPostCheck(state)),
	)
	mp.SetLogger(logger)

	// Make EvidencePool
	evidenceDB := dbm.NewMemDB()
	blockStore := store.NewBlockStore(dbm.NewMemDB())
	evidencePool, err := evidence.NewPool(logger, evidenceDB, stateStore, blockStore)
	require.NoError(t, err)

	// fill the evidence pool with more evidence
	// than can fit in a block
	for currentBytes := 0; int64(currentBytes) <= maxEvidenceBytes; {
		ev, err := types.NewMockDuplicateVoteEvidenceWithValidator(
			height,
			time.Now(),
			privVals[0],
			"test-chain",
			state.Validators.QuorumType,
			state.Validators.QuorumHash,
		)
		require.NoError(t, err)
		currentBytes += len(ev.Bytes())
		evidencePool.ReportConflictingVotes(ev.VoteA, ev.VoteB)
	}

	evList, size := evidencePool.PendingEvidence(state.ConsensusParams.Evidence.MaxBytes)
	require.Less(t, size, state.ConsensusParams.Evidence.MaxBytes+1)
	evData := &types.EvidenceData{Evidence: evList}
	require.EqualValues(t, size, evData.ByteSize())

	// fill the mempool with more txs
	// than can fit in a block
	txLength := 100
	for i := 0; i <= maxBytes/txLength; i++ {
		tx := tmrand.Bytes(txLength)
		err := mp.CheckTx(context.Background(), tx, nil, mempool.TxInfo{})
		assert.NoError(t, err)
	}

	blockExec := sm.NewBlockExecutor(
		stateStore,
		logger,
		proxyApp.Consensus(),
		proxyApp.Query(),
		mp,
		evidencePool,
		blockStore,
		nil,
	)

	commit := types.NewCommit(height-1, 0, types.BlockID{}, types.StateID{}, nil, nil, nil)

	proposedAppVersion := uint64(1)

	block, _ := blockExec.CreateProposalBlock(
		height,
		state,
		commit,
		proposerProTxHash,
		proposedAppVersion,
	)

	// check that the part set does not exceed the maximum block size
	partSet := block.MakePartSet(partSize)
	assert.Less(t, partSet.ByteSize(), int64(maxBytes))

	partSetFromHeader := types.NewPartSetFromHeader(partSet.Header())
	for partSetFromHeader.Count() < partSetFromHeader.Total() {
		added, err := partSetFromHeader.AddPart(partSet.GetPart(int(partSetFromHeader.Count())))
		require.NoError(t, err)
		require.True(t, added)
	}
	assert.EqualValues(t, partSetFromHeader.ByteSize(), partSet.ByteSize())

	err = blockExec.ValidateBlock(state, block)
	assert.NoError(t, err)

	assert.EqualValues(t, block.Header.ProposedAppVersion, proposedAppVersion)
}

func TestMaxTxsProposalBlockSize(t *testing.T) {
	cfg, err := config.ResetTestRoot("node_create_proposal")
	require.NoError(t, err)
	defer os.RemoveAll(cfg.RootDir)
	cc := abciclient.NewLocalCreator(kvstore.NewApplication())
	proxyApp := proxy.NewAppConns(cc, proxy.NopMetrics())
	err = proxyApp.Start()
	require.Nil(t, err)
	defer proxyApp.Stop() //nolint:errcheck // ignore for tests

	logger := log.TestingLogger()

	const height int64 = 1
	state, stateDB, _ := state(1, height)
	stateStore := sm.NewStore(stateDB)
	blockStore := store.NewBlockStore(dbm.NewMemDB())
	const maxBytes int64 = 16384
	const partSize uint32 = 256
	state.ConsensusParams.Block.MaxBytes = maxBytes
	proposerProTxHash, _ := state.Validators.GetByIndex(0)

	// Make Mempool
	mp := mempoolv0.NewCListMempool(
		cfg.Mempool,
		proxyApp.Mempool(),
		state.LastBlockHeight,
		mempoolv0.WithMetrics(mempool.NopMetrics()),
		mempoolv0.WithPreCheck(sm.TxPreCheck(state)),
		mempoolv0.WithPostCheck(sm.TxPostCheck(state)),
	)
	mp.SetLogger(logger)

	// fill the mempool with one txs just below the maximum size
	txLength := int(types.MaxDataBytesNoEvidence(maxBytes))
	tx := tmrand.Bytes(txLength - 4) // to account for the varint
	err = mp.CheckTx(context.Background(), tx, nil, mempool.TxInfo{})
	assert.NoError(t, err)

	blockExec := sm.NewBlockExecutor(
		stateStore,
		logger,
		proxyApp.Consensus(),
		proxyApp.Query(),
		mp,
		sm.EmptyEvidencePool{},
		blockStore,
		nil,
	)

	commit := types.NewCommit(height-1, 0, types.BlockID{}, types.StateID{}, nil, nil, nil)
	block, _ := blockExec.CreateProposalBlock(
		height,
		state,
		commit,
		proposerProTxHash,
		0,
	)

	pb, err := block.ToProto()
	require.NoError(t, err)
	assert.Less(t, int64(pb.Size()), maxBytes)

	// check that the part set does not exceed the maximum block size
	partSet := block.MakePartSet(partSize)
	assert.EqualValues(t, partSet.ByteSize(), int64(pb.Size()))
}

func TestMaxProposalBlockSize(t *testing.T) {
	cfg, err := config.ResetTestRoot("node_create_proposal")
	require.NoError(t, err)
	defer os.RemoveAll(cfg.RootDir)
	cc := abciclient.NewLocalCreator(kvstore.NewApplication())
	proxyApp := proxy.NewAppConns(cc, proxy.NopMetrics())
	err = proxyApp.Start()
	require.Nil(t, err)
	defer proxyApp.Stop() //nolint:errcheck // ignore for tests

	logger := log.TestingLogger()

	state, stateDB, _ := state(100, int64(1))
	stateStore := sm.NewStore(stateDB)
	blockStore := store.NewBlockStore(dbm.NewMemDB())
	const maxBytes int64 = 1024 * 1024 * 2
	state.ConsensusParams.Block.MaxBytes = maxBytes
	state.LastCoreChainLockedBlockHeight = math.MaxUint32 - 1
	proposerProTxHash, _ := state.Validators.GetByIndex(0)

	// Make Mempool
	mp := mempoolv0.NewCListMempool(
		cfg.Mempool,
		proxyApp.Mempool(),
		state.LastBlockHeight,
		mempoolv0.WithMetrics(mempool.NopMetrics()),
		mempoolv0.WithPreCheck(sm.TxPreCheck(state)),
		mempoolv0.WithPostCheck(sm.TxPostCheck(state)),
	)
	mp.SetLogger(logger)

	//// fill the mempool with one txs just below the maximum size
	txLength := cfg.Mempool.MaxTxBytes - 6
	tx := tmrand.Bytes(txLength) // to account for the varint
	err = mp.CheckTx(context.Background(), tx, nil, mempool.TxInfo{})
	assert.NoError(t, err)
	// now produce more txs than what a normal block can hold with 10 smaller txs
	// At the end of the test, only the single big tx should be added
	for i := 0; i < 10; i++ {
		tx := tmrand.Bytes(10)
		err = mp.CheckTx(context.Background(), tx, nil, mempool.TxInfo{})
		assert.NoError(t, err)
	}

	coreChainLock := types.CoreChainLock{
		CoreBlockHeight: math.MaxUint32,
		CoreBlockHash:   crypto.CRandBytes(32),
		Signature:       crypto.CRandBytes(96),
	}

	blockExec := sm.NewBlockExecutor(
		stateStore,
		logger,
		proxyApp.Consensus(),
		proxyApp.Query(),
		mp,
		sm.EmptyEvidencePool{},
		blockStore,
		&coreChainLock,
	)

	blockID := types.BlockID{
		Hash: tmhash.Sum([]byte("blockID_hash")),
		PartSetHeader: types.PartSetHeader{
			Total: math.MaxInt32,
			Hash:  tmhash.Sum([]byte("blockID_part_set_header_hash")),
		},
	}

	stateID := types.StateID{
		Height:      math.MaxInt64 - 1,
		LastAppHash: tmhash.Sum([]byte("app_hash")),
	}

	timestamp := time.Date(math.MaxInt64, 0, 0, 0, 0, 0, math.MaxInt64, time.UTC)
	// change state in order to produce the largest accepted header
	state.LastBlockID = blockID
	state.LastBlockHeight = math.MaxInt64 - 1
	state.LastBlockTime = timestamp
	state.LastResultsHash = tmhash.Sum([]byte("last_results_hash"))
	state.AppHash = tmhash.Sum([]byte("app_hash"))
	state.Version.Consensus.Block = math.MaxInt64
	state.Version.Consensus.App = math.MaxInt64
	maxChainID := ""
	for i := 0; i < types.MaxChainIDLen; i++ {
		maxChainID += "𠜎"
	}
	state.ChainID = maxChainID

	commit := &types.Commit{
		Height:                  math.MaxInt64,
		Round:                   math.MaxInt32,
		BlockID:                 blockID,
		StateID:                 stateID,
		QuorumHash:              crypto.RandQuorumHash(),
		ThresholdBlockSignature: crypto.CRandBytes(bls12381.SignatureSize),
		ThresholdStateSignature: crypto.CRandBytes(bls12381.SignatureSize),
	}

	block, partSet := blockExec.CreateProposalBlock(
		math.MaxInt64,
		state, commit,
		proposerProTxHash, 0,
	)

	// this ensures that the header is at max size
	block.Header.Time = timestamp

	pb, err := block.ToProto()
	require.NoError(t, err)

	// require that the header and commit be the max possible size
	require.Equal(t, types.MaxHeaderBytes, int64(pb.Header.Size()))
	require.Equal(t, types.MaxCommitSize, int64(pb.LastCommit.Size()))
	// make sure that the block is less than the max possible size
	assert.Equal(t, int64(1292+cfg.Mempool.MaxTxBytes), int64(pb.Size()))
	// because of the proto overhead we expect the part set bytes to be equal or
	// less than the pb block size
	assert.LessOrEqual(t, partSet.ByteSize(), int64(pb.Size()))

}

func TestNodeNewSeedNode(t *testing.T) {
	cfg, err := config.ResetTestRoot("node_new_node_custom_reactors_test")
	require.NoError(t, err)
	cfg.Mode = config.ModeSeed
	defer os.RemoveAll(cfg.RootDir)

	nodeKey, err := types.LoadOrGenNodeKey(cfg.NodeKeyFile())
	require.NoError(t, err)

	ns, err := makeSeedNode(cfg,
		config.DefaultDBProvider,
		nodeKey,
		defaultGenesisDocProviderFunc(cfg),
		log.TestingLogger(),
	)
	require.NoError(t, err)
	n, ok := ns.(*nodeImpl)
	require.True(t, ok)

	err = n.Start()
	require.NoError(t, err)

	assert.True(t, n.pexReactor.IsRunning())
}

func TestNodeSetEventSink(t *testing.T) {
	cfg, err := config.ResetTestRoot("node_app_version_test")
	require.NoError(t, err)

	defer os.RemoveAll(cfg.RootDir)

	logger := log.TestingLogger()
	setupTest := func(t *testing.T, conf *config.Config) []indexer.EventSink {
		eventBus, err := createAndStartEventBus(logger)
		require.NoError(t, err)

		genDoc, err := types.GenesisDocFromFile(cfg.GenesisFile())
		require.NoError(t, err)

		indexService, eventSinks, err := createAndStartIndexerService(cfg,
			config.DefaultDBProvider, eventBus, logger, genDoc.ChainID,
			indexer.NopMetrics())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, indexService.Stop()) })
		return eventSinks
	}

	eventSinks := setupTest(t, cfg)
	assert.Equal(t, 1, len(eventSinks))
	assert.Equal(t, indexer.KV, eventSinks[0].Type())

	cfg.TxIndex.Indexer = []string{"null"}
	eventSinks = setupTest(t, cfg)

	assert.Equal(t, 1, len(eventSinks))
	assert.Equal(t, indexer.NULL, eventSinks[0].Type())

	cfg.TxIndex.Indexer = []string{"null", "kv"}
	eventSinks = setupTest(t, cfg)

	assert.Equal(t, 1, len(eventSinks))
	assert.Equal(t, indexer.NULL, eventSinks[0].Type())

	cfg.TxIndex.Indexer = []string{"kvv"}
	ns, err := newDefaultNode(cfg, logger)
	assert.Nil(t, ns)
	assert.Equal(t, errors.New("unsupported event sink type"), err)

	cfg.TxIndex.Indexer = []string{}
	eventSinks = setupTest(t, cfg)

	assert.Equal(t, 1, len(eventSinks))
	assert.Equal(t, indexer.NULL, eventSinks[0].Type())

	cfg.TxIndex.Indexer = []string{"psql"}
	ns, err = newDefaultNode(cfg, logger)
	assert.Nil(t, ns)
	assert.Equal(t, errors.New("the psql connection settings cannot be empty"), err)

	var psqlConn = "test"

	cfg.TxIndex.Indexer = []string{"psql"}
	cfg.TxIndex.PsqlConn = psqlConn
	eventSinks = setupTest(t, cfg)

	assert.Equal(t, 1, len(eventSinks))
	assert.Equal(t, indexer.PSQL, eventSinks[0].Type())

	cfg.TxIndex.Indexer = []string{"psql", "kv"}
	cfg.TxIndex.PsqlConn = psqlConn
	eventSinks = setupTest(t, cfg)

	assert.Equal(t, 2, len(eventSinks))
	// we use map to filter the duplicated sinks, so it's not guarantee the order when append sinks.
	if eventSinks[0].Type() == indexer.KV {
		assert.Equal(t, indexer.PSQL, eventSinks[1].Type())
	} else {
		assert.Equal(t, indexer.PSQL, eventSinks[0].Type())
		assert.Equal(t, indexer.KV, eventSinks[1].Type())
	}

	cfg.TxIndex.Indexer = []string{"kv", "psql"}
	cfg.TxIndex.PsqlConn = psqlConn
	eventSinks = setupTest(t, cfg)

	assert.Equal(t, 2, len(eventSinks))
	if eventSinks[0].Type() == indexer.KV {
		assert.Equal(t, indexer.PSQL, eventSinks[1].Type())
	} else {
		assert.Equal(t, indexer.PSQL, eventSinks[0].Type())
		assert.Equal(t, indexer.KV, eventSinks[1].Type())
	}

	var e = errors.New("found duplicated sinks, please check the tx-index section in the config.toml")
	cfg.TxIndex.Indexer = []string{"psql", "kv", "Kv"}
	cfg.TxIndex.PsqlConn = psqlConn
	_, err = newDefaultNode(cfg, logger)
	require.Error(t, err)
	assert.Equal(t, e, err)

	cfg.TxIndex.Indexer = []string{"Psql", "kV", "kv", "pSql"}
	cfg.TxIndex.PsqlConn = psqlConn
	_, err = newDefaultNode(cfg, logger)
	require.Error(t, err)
	assert.Equal(t, e, err)
}

func state(nVals int, height int64) (sm.State, dbm.DB, []types.PrivValidator) {
	vals, privVals := types.RandValidatorSet(nVals)
	genVals := types.MakeGenesisValsFromValidatorSet(vals)
	for i := 0; i < nVals; i++ {
		genVals[i].Name = fmt.Sprintf("test%d", i)
	}
	s, _ := sm.MakeGenesisState(&types.GenesisDoc{
		ChainID:            "test-chain",
		Validators:         genVals,
		ThresholdPublicKey: vals.ThresholdPublicKey,
		QuorumHash:         vals.QuorumHash,
		AppHash:            nil,
	})

	// save validators to db for 2 heights
	stateDB := dbm.NewMemDB()
	stateStore := sm.NewStore(stateDB)
	if err := stateStore.Save(s); err != nil {
		panic(err)
	}

	for i := 1; i < int(height); i++ {
		s.LastBlockHeight++
		s.LastValidators = s.Validators.Copy()
		if err := stateStore.Save(s); err != nil {
			panic(err)
		}
	}
	return s, stateDB, privVals
}

func TestLoadStateFromGenesis(t *testing.T) {
	_ = loadStatefromGenesis(t)
}

func loadStatefromGenesis(t *testing.T) sm.State {
	t.Helper()

	stateDB := dbm.NewMemDB()
	stateStore := sm.NewStore(stateDB)
	cfg, err := config.ResetTestRoot("load_state_from_genesis")
	require.NoError(t, err)

	loadedState, err := stateStore.Load()
	require.NoError(t, err)
	require.True(t, loadedState.IsEmpty())

	genDoc, _ := factory.RandGenesisDoc(cfg, 10, 0)

	state, err := loadStateFromDBOrGenesisDocProvider(
		stateStore,
		genDoc,
	)
	require.NoError(t, err)
	require.NotNil(t, state)

	return state
}
