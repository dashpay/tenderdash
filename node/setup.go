package node

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/dashpay/dashd-go/btcjson"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/crypto"
	dashcore "github.com/dashpay/tenderdash/dash/core"
	"github.com/dashpay/tenderdash/internal/consensus"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/evidence"
	tmstrings "github.com/dashpay/tenderdash/internal/libs/strings"
	tmsync "github.com/dashpay/tenderdash/internal/libs/sync"

	"github.com/dashpay/tenderdash/internal/mempool"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/internal/p2p/conn"
	"github.com/dashpay/tenderdash/internal/p2p/pex"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/state/indexer"
	"github.com/dashpay/tenderdash/internal/statesync"
	"github.com/dashpay/tenderdash/internal/store"
	"github.com/dashpay/tenderdash/libs/log"
	tmnet "github.com/dashpay/tenderdash/libs/net"
	"github.com/dashpay/tenderdash/libs/service"
	"github.com/dashpay/tenderdash/privval"
	tmgrpc "github.com/dashpay/tenderdash/privval/grpc"
	"github.com/dashpay/tenderdash/types"
	"github.com/dashpay/tenderdash/version"

	"net/http"
	_ "net/http/pprof" //nolint: gosec // securely exposed on separate, optional port
)

const (
	// httpReadHeaderTimeout is set to address linter issue:
	//   G112: Potential Slowloris Attack because ReadHeaderTimeout
	//   is not configured in the http.Server (gosec).
	httpReadHeaderTimeout = 10 * time.Second
)

type closer func() error

func makeCloser(cs []closer) closer {
	return func() error {
		errs := make([]string, 0, len(cs))
		for _, cl := range cs {
			if err := cl(); err != nil {
				errs = append(errs, err.Error())
			}
		}
		if len(errs) >= 0 {
			return errors.New(strings.Join(errs, "; "))
		}
		return nil
	}
}

func convertCancelCloser(cancel context.CancelFunc) closer {
	return func() error { cancel(); return nil }
}

func combineCloseError(err error, cl closer) error {
	if err == nil {
		return cl()
	}

	clerr := cl()
	if clerr == nil {
		return err
	}

	return fmt.Errorf("error=%q closerError=%q", err.Error(), clerr.Error())
}

func initDBs(
	cfg *config.Config,
	dbProvider config.DBProvider,
) (*store.BlockStore, dbm.DB, closer, error) {

	blockStoreDB, err := dbProvider(&config.DBContext{ID: "blockstore", Config: cfg})
	if err != nil {
		return nil, nil, func() error { return nil }, fmt.Errorf("unable to initialize blockstore: %w", err)
	}
	closers := []closer{}
	blockStore := store.NewBlockStore(blockStoreDB)
	closers = append(closers, blockStoreDB.Close)

	stateDB, err := dbProvider(&config.DBContext{ID: "state", Config: cfg})
	if err != nil {
		return nil, nil, makeCloser(closers), fmt.Errorf("unable to initialize statestore: %w", err)
	}

	closers = append(closers, stateDB.Close)

	return blockStore, stateDB, makeCloser(closers), nil
}

func logNodeStartupInfo(state sm.State, proTxHash crypto.ProTxHash, logger log.Logger, mode string) {
	// Log the version info.
	logger.Info("Version info",
		"tmVersion", version.TMCoreSemVer,
		"block", version.BlockProtocol,
		"p2p", version.P2PProtocol,
		"mode", mode,
	)

	// If the state and software differ in block version, at least log it.
	if state.Version.Consensus.Block != version.BlockProtocol {
		logger.Info("Software and state have different block protocols",
			"software", version.BlockProtocol,
			"state", state.Version.Consensus.Block,
		)
	}

	switch mode {
	case config.ModeFull:
		logger.Info("This node is a fullnode")
	case config.ModeValidator:
		// Log whether this node is a validator or an observer
		if state.Validators.HasProTxHash(proTxHash) {
			logger.Info("This node is a validator",
				"proTxHash", proTxHash.ShortString(),
			)
		} else {
			logger.Info("This node is a validator (NOT in the active validator set)",
				"proTxHash", proTxHash.ShortString(),
			)
		}
	}
}

func onlyValidatorIsUs(state sm.State, proTxHash types.ProTxHash) bool {
	return state.Validators.Size() == 1 && state.Validators.HasProTxHash(proTxHash)
}

func createMempoolReactor(
	logger log.Logger,
	cfg *config.Config,
	appClient abciclient.Client,
	store sm.Store,
	memplMetrics *mempool.Metrics,
	peerEvents p2p.PeerEventSubscriber,
	p2pClient *client.Client,
) (service.Service, mempool.Mempool) {
	logger = logger.With("module", "mempool")

	mp := mempool.NewTxMempool(
		logger,
		cfg.Mempool,
		appClient,
		mempool.WithMetrics(memplMetrics),
		mempool.WithPreCheck(sm.TxPreCheckFromStore(store)),
		mempool.WithPostCheck(sm.TxPostCheckFromStore(store)),
	)

	reactor := mempool.NewReactor(
		logger,
		cfg.Mempool,
		mp,
		p2pClient,
		peerEvents,
	)

	if cfg.Consensus.WaitForTxs() {
		mp.EnableTxsAvailable()
	}

	return reactor, mp
}

func createEvidenceReactor(
	logger log.Logger,
	cfg *config.Config,
	dbProvider config.DBProvider,
	store sm.Store,
	blockStore *store.BlockStore,
	peerEvents p2p.PeerEventSubscriber,
	chCreator p2p.ChannelCreator,
	metrics *evidence.Metrics,
	eventBus *eventbus.EventBus,
) (*evidence.Reactor, *evidence.Pool, closer, error) {
	evidenceDB, err := dbProvider(&config.DBContext{ID: "evidence", Config: cfg})
	if err != nil {
		return nil, nil, func() error { return nil }, fmt.Errorf("unable to initialize evidence db: %w", err)
	}

	logger = logger.With("module", "evidence")

	evidencePool := evidence.NewPool(logger, evidenceDB, store, blockStore, metrics, eventBus)
	evidenceReactor := evidence.NewReactor(logger, chCreator, peerEvents, evidencePool)

	return evidenceReactor, evidencePool, evidenceDB.Close, nil
}

func createPeerManager(
	ctx context.Context,
	cfg *config.Config,
	dbProvider config.DBProvider,
	nodeID types.NodeID,
	metrics *p2p.Metrics,
	logger log.Logger,
) (*p2p.PeerManager, closer, error) {

	selfAddr, err := p2p.ParseNodeAddress(nodeID.AddressString(cfg.P2P.ExternalAddress))
	if err != nil {
		return nil, func() error { return nil }, fmt.Errorf("couldn't parse ExternalAddress %q: %w", cfg.P2P.ExternalAddress, err)
	}

	privatePeerIDs := make(map[types.NodeID]struct{})
	for _, id := range tmstrings.SplitAndTrimEmpty(cfg.P2P.PrivatePeerIDs, ",", " ") {
		privatePeerIDs[types.NodeID(id)] = struct{}{}
	}

	var maxConns uint16

	switch {
	case cfg.P2P.MaxConnections > 0:
		maxConns = cfg.P2P.MaxConnections
	default:
		maxConns = 64
	}

	var maxOutgoingConns uint16
	switch {
	case cfg.P2P.MaxOutgoingConnections > 0:
		maxOutgoingConns = cfg.P2P.MaxOutgoingConnections
	default:
		maxOutgoingConns = maxConns / 2
	}

	maxUpgradeConns := uint16(4)

	options := p2p.PeerManagerOptions{
		SelfAddress:               selfAddr,
		MaxConnected:              maxConns,
		MaxOutgoingConnections:    maxOutgoingConns,
		MaxIncomingConnectionTime: cfg.P2P.MaxIncomingConnectionTime,
		MaxConnectedUpgrade:       maxUpgradeConns,
		DisconnectCooldownPeriod:  2 * time.Second,
		MaxPeers:                  maxUpgradeConns + 4*maxConns,
		MinRetryTime:              250 * time.Millisecond,
		MaxRetryTime:              30 * time.Minute,
		MaxRetryTimePersistent:    5 * time.Minute,
		RetryTimeJitter:           5 * time.Second,
		PrivatePeers:              privatePeerIDs,
		Metrics:                   metrics,
	}

	peers := []p2p.NodeAddress{}
	for _, p := range tmstrings.SplitAndTrimEmpty(cfg.P2P.PersistentPeers, ",", " ") {
		address, err := p2p.ParseNodeAddress(p)
		if err != nil {
			return nil, func() error { return nil }, fmt.Errorf("invalid peer address %q: %w", p, err)
		}

		peers = append(peers, address)
		options.PersistentPeers = append(options.PersistentPeers, address.NodeID)
	}

	for _, p := range tmstrings.SplitAndTrimEmpty(cfg.P2P.BootstrapPeers, ",", " ") {
		address, err := p2p.ParseNodeAddress(p)
		if err != nil {
			return nil, func() error { return nil }, fmt.Errorf("invalid peer address %q: %w", p, err)
		}
		peers = append(peers, address)
	}

	peerDB, err := dbProvider(&config.DBContext{ID: "peerstore", Config: cfg})
	if err != nil {
		return nil, func() error { return nil }, fmt.Errorf("unable to initialize peer store: %w", err)
	}

	peerManager, err := p2p.NewPeerManager(ctx, nodeID, peerDB, options)
	if err != nil {
		return nil, peerDB.Close, fmt.Errorf("failed to create peer manager: %w", err)
	}
	peerManager.SetLogger(logger.With("module", "peermanager"))
	closer := func() error {
		peerManager.Close()
		return peerDB.Close()
	}
	for _, peer := range peers {
		if _, err := peerManager.Add(peer); err != nil {
			return nil, closer, fmt.Errorf("failed to add peer %q: %w", peer, err)
		}
	}

	return peerManager, closer, nil
}

func createRouter(
	logger log.Logger,
	p2pMetrics *p2p.Metrics,
	nodeInfoProducer func() *types.NodeInfo,
	nodeKey types.NodeKey,
	peerManager *p2p.PeerManager,
	cfg *config.Config,
	appClient abciclient.Client,
) (*p2p.Router, error) {

	p2pLogger := logger.With("module", "p2p")

	transportConf := conn.DefaultMConnConfig()
	transportConf.FlushThrottle = cfg.P2P.FlushThrottleTimeout
	transportConf.SendRate = cfg.P2P.SendRate
	transportConf.RecvRate = cfg.P2P.RecvRate
	transportConf.MaxPacketMsgPayloadSize = cfg.P2P.MaxPacketMsgPayloadSize
	transport := p2p.NewMConnTransport(
		p2pLogger, transportConf, []*p2p.ChannelDescriptor{},
		p2p.MConnTransportOptions{
			MaxAcceptedConnections: uint32(cfg.P2P.MaxConnections),
		},
	)

	ep, err := p2p.NewEndpoint(nodeKey.ID.AddressString(cfg.P2P.ListenAddress))
	if err != nil {
		return nil, err
	}

	return p2p.NewRouter(
		p2pLogger,
		p2pMetrics,
		nodeKey.PrivKey,
		peerManager,
		nodeInfoProducer,
		transport,
		ep,
		getRouterConfig(cfg, appClient),
	)
}

func makeNodeInfo(
	cfg *config.Config,
	nodeKey types.NodeKey,
	proTxHash crypto.ProTxHash,
	eventSinks []indexer.EventSink,
	genDoc *types.GenesisDoc,
	versionInfo version.Consensus,
) (types.NodeInfo, error) {

	txIndexerStatus := "off"

	if indexer.IndexingEnabled(eventSinks) {
		txIndexerStatus = "on"
	}

	nodeInfo := types.NodeInfo{
		ProtocolVersion: types.ProtocolVersion{
			P2P:   version.P2PProtocol, // global
			Block: versionInfo.Block,
			App:   versionInfo.App,
		},
		NodeID:  nodeKey.ID,
		Network: genDoc.ChainID,
		Version: version.TMCoreSemVer,
		Channels: tmsync.NewConcurrentSlice[conn.ChannelID](
			p2p.BlockSyncChannel,
			p2p.ConsensusStateChannel,
			p2p.ConsensusDataChannel,
			p2p.ConsensusVoteChannel,
			p2p.VoteSetBitsChannel,
			p2p.MempoolChannel,
			evidence.EvidenceChannel,
			statesync.SnapshotChannel,
			statesync.ChunkChannel,
			statesync.LightBlockChannel,
			statesync.ParamsChannel,
			pex.PexChannel,
		),
		Moniker: cfg.Moniker,
		Other: types.NodeInfoOther{
			TxIndex:    txIndexerStatus,
			RPCAddress: cfg.RPC.ListenAddress,
		},
		ProTxHash: proTxHash.Copy(),
	}

	nodeInfo.ListenAddr = cfg.P2P.ExternalAddress
	if nodeInfo.ListenAddr == "" {
		nodeInfo.ListenAddr = cfg.P2P.ListenAddress
	}

	return nodeInfo, nodeInfo.Validate()
}

func makeSeedNodeInfo(
	cfg *config.Config,
	nodeKey types.NodeKey,
	genDoc *types.GenesisDoc,
	state sm.State,
) (types.NodeInfo, error) {
	nodeInfo := types.NodeInfo{
		ProtocolVersion: types.ProtocolVersion{
			P2P:   version.P2PProtocol, // global
			Block: state.Version.Consensus.Block,
			App:   state.Version.Consensus.App,
		},
		NodeID:   nodeKey.ID,
		Network:  genDoc.ChainID,
		Version:  version.TMCoreSemVer,
		Channels: tmsync.NewConcurrentSlice[conn.ChannelID](pex.PexChannel),
		Moniker:  cfg.Moniker,
		Other: types.NodeInfoOther{
			TxIndex:    "off",
			RPCAddress: cfg.RPC.ListenAddress,
		},
	}

	nodeInfo.ListenAddr = cfg.P2P.ExternalAddress
	if nodeInfo.ListenAddr == "" {
		nodeInfo.ListenAddr = cfg.P2P.ListenAddress
	}

	return nodeInfo, nodeInfo.Validate()
}

func createAndStartPrivValidatorSocketClient(
	ctx context.Context,
	listenAddr, chainID string,
	quorumHash crypto.QuorumHash,
	logger log.Logger,
) (types.PrivValidator, error) {

	pve, err := privval.NewSignerListener(listenAddr, logger)
	if err != nil {
		return nil, fmt.Errorf("starting validator listener: %w", err)
	}

	pvsc, err := privval.NewSignerClient(ctx, pve, chainID)
	if err != nil {
		return nil, fmt.Errorf("starting validator client: %w", err)
	}

	// try to get a pubkey from private validate first time
	_, err = pvsc.GetPubKey(ctx, quorumHash)
	if err != nil {
		return nil, fmt.Errorf("can't get pubkey: %w", err)
	}

	const (
		timeout = 100 * time.Millisecond
		maxTime = 5 * time.Second
		retries = int(maxTime / timeout)
	)
	pvscWithRetries := privval.NewRetrySignerClient(pvsc, retries, timeout)

	return pvscWithRetries, nil
}

func createAndStartPrivValidatorGRPCClient(
	ctx context.Context,
	cfg *config.Config,
	chainID string,
	quorumHash crypto.QuorumHash,
	logger log.Logger,
) (types.PrivValidator, error) {
	pvsc, err := tmgrpc.DialRemoteSigner(
		ctx,
		cfg.PrivValidator,
		chainID,
		logger,
		cfg.Instrumentation.Prometheus,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to start private validator: %w", err)
	}

	// try to get a pubkey from private validate first time
	_, err = pvsc.GetPubKey(ctx, quorumHash)
	if err != nil {
		return nil, fmt.Errorf("can't get pubkey: %w", err)
	}

	return pvsc, nil
}

func makeDefaultPrivval(conf *config.Config) (*privval.FilePV, error) {
	if conf.Mode == config.ModeValidator {
		pval, err := privval.LoadOrGenFilePV(conf.PrivValidator.KeyFile(), conf.PrivValidator.StateFile())
		if err != nil {
			return nil, err
		}
		return pval, nil
	}

	return nil, nil
}

// createPrivval creates and returns new PrivVal based on provided config.
func createPrivval(ctx context.Context, logger log.Logger, conf *config.Config, genDoc *types.GenesisDoc) (types.PrivValidator, error) {
	switch {
	case conf.PrivValidator.ListenAddr != "": // Generic tendermint privval
		protocol, _ := tmnet.ProtocolAndAddress(conf.PrivValidator.ListenAddr)
		// FIXME: we should return un-started services and
		// then start them later.
		if protocol == "grpc" {
			privValidator, err := createAndStartPrivValidatorGRPCClient(ctx, conf, genDoc.ChainID, genDoc.QuorumHash, logger)
			if err != nil {
				return nil, fmt.Errorf("error with private validator grpc client: %w", err)
			}
			return privValidator, nil
		}

		privValidator, err := createAndStartPrivValidatorSocketClient(
			ctx,
			conf.PrivValidator.ListenAddr,
			genDoc.ChainID,
			genDoc.QuorumHash,
			logger,
		)
		if err != nil {
			return nil, fmt.Errorf("error with private validator socket client: %w", err)
		}
		return privValidator, nil

	case conf.PrivValidator.CoreRPCHost != "": // DASH Core Privval
		if conf.Mode != config.ModeValidator {
			return nil, fmt.Errorf("cannot initialize PrivValidator: this node is NOT a validator")
		}

		logger.Info("Initializing Dash Core PrivValidator")

		dashCoreRPCClient, err := DefaultDashCoreRPCClient(conf, logger.With("module", dashcore.ModuleName))
		if err != nil {
			return nil, fmt.Errorf("failed to create Dash Core RPC client: %w", err)
		}

		logger.Info("Waiting for Dash Core RPC Client to be ready")
		err = dashcore.WaitForMNReady(dashCoreRPCClient, time.Second)
		if err != nil {
			return nil, fmt.Errorf("failed to wait for masternode status 'ready': %w", err)
		}
		logger.Info("Dash Core RPC Client is ready")

		// If a local port is provided for Dash Core rpc into the service to sign.
		privValidator, err := createAndStartPrivValidatorDashCoreClient(
			genDoc.QuorumType,
			dashCoreRPCClient,
			logger,
		)
		if err != nil {
			return nil, fmt.Errorf("error with private validator RPC client: %w", err)
		}
		proTxHash, err := privValidator.GetProTxHash(ctx)
		if err != nil {
			return nil, fmt.Errorf("can't get proTxHash using dash core signing: %w", err)
		}
		logger.Info("Connected to Core RPC Masternode", "proTxHash", proTxHash.String())

		return privValidator, nil
	default:
		return makeDefaultPrivval(conf)
	}
}

func createAndStartPrivValidatorDashCoreClient(
	defaultQuorumType btcjson.LLMQType,
	dashCoreRPCClient dashcore.Client,
	logger log.Logger,
) (types.PrivValidator, error) {
	pvsc, err := privval.NewDashCoreSignerClient(dashCoreRPCClient, defaultQuorumType, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to start private validator: %w", err)
	}

	// try to ping Core from private validator first time to make sure connection works
	err = pvsc.Ping()
	if err != nil {
		return nil, fmt.Errorf(
			"can't ping core server when starting private validator rpc client: %w",
			err,
		)
	}

	return pvsc, nil
}

func createBlockReplayer(n *nodeImpl) *consensus.BlockReplayer {
	logger := n.logger.With("module", "replayer")
	blockExec := consensus.NewReplayBlockExecutor(
		n.rpcEnv.ProxyApp,
		n.stateStore,
		n.blockStore,
		n.rpcEnv.EventBus,
		sm.BlockExecWithLogger(logger),
	)
	return consensus.NewBlockReplayer(
		n.rpcEnv.ProxyApp,
		n.stateStore,
		n.blockStore,
		n.genesisDoc,
		n.rpcEnv.EventBus,
		blockExec,
		consensus.ReplayerWithLogger(logger),
		consensus.ReplayerWithProTxHash(n.rpcEnv.ProTxHash),
	)
}

// startPrometheusServer starts a Prometheus HTTP server, listening for metrics
// collectors on addr.
func startPrometheusServer(ctx context.Context, cfg config.InstrumentationConfig) *http.Server {
	addr := cfg.PrometheusListenAddr
	srv := &http.Server{
		Addr:              addr,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		Handler: promhttp.InstrumentMetricHandler(
			prometheus.DefaultRegisterer, promhttp.HandlerFor(
				prometheus.DefaultGatherer,
				promhttp.HandlerOpts{MaxRequestsInFlight: cfg.MaxOpenConnections},
			),
		),
	}

	signal := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			sctx, scancel := context.WithTimeout(context.Background(), time.Second)
			defer scancel()
			_ = srv.Shutdown(sctx)
		case <-signal:
		}
	}()

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			close(signal)
		}
	}()

	return srv
}

// startPProfServer creates a new pprof server
// FIXME: implement as a Service
func startPProfServer(ctx context.Context, cfg config.RPCConfig) {
	signal := make(chan struct{})
	srv := &http.Server{
		Addr:              cfg.PprofListenAddress,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		Handler:           nil,
	}
	go func() {
		select {
		case <-ctx.Done():
			sctx, scancel := context.WithTimeout(context.Background(), time.Second)
			defer scancel()
			_ = srv.Shutdown(sctx)
		case <-signal:
		}
	}()

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			close(signal)
		}
	}()
}
