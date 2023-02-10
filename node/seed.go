package node

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/internal/p2p"
	"github.com/tendermint/tendermint/internal/p2p/pex"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/service"
	tmtime "github.com/tendermint/tendermint/libs/time"
	"github.com/tendermint/tendermint/types"
)

type seedNodeImpl struct {
	service.BaseService
	logger log.Logger

	// config
	config     *config.Config
	genesisDoc *types.GenesisDoc // initial validator set

	// network
	peerManager *p2p.PeerManager
	router      *p2p.Router
	nodeInfo    types.NodeInfo
	nodeKey     types.NodeKey // our node privkey

	// services
	pexReactor    service.Service // for exchanging peer addresses
	prometheusSrv *http.Server

	shutdownOps closer
}

// makeSeedNode returns a new seed node, containing only p2p, pex reactor
func makeSeedNode(
	ctx context.Context,
	logger log.Logger,
	cfg *config.Config,
	dbProvider config.DBProvider,
	nodeKey types.NodeKey,
	genesisDocProvider genesisDocProvider,
) (service.Service, error) {
	genDoc, err := genesisDocProvider()
	if err != nil {
		return nil, err
	}

	state, err := sm.MakeGenesisState(genDoc)
	if err != nil {
		return nil, err
	}

	// Setup Transport and Switch.
	p2pMetrics := p2p.PrometheusMetrics(cfg.Instrumentation.Namespace, "chain_id", genDoc.ChainID)

	peerManager, closer, err := createPeerManager(ctx, cfg, dbProvider, nodeKey.ID, p2pMetrics, logger)
	if err != nil {
		return nil, combineCloseError(
			fmt.Errorf("failed to create peer manager: %w", err),
			closer)
	}

	node := &seedNodeImpl{
		config:     cfg,
		logger:     logger,
		genesisDoc: genDoc,

		nodeKey:     nodeKey,
		peerManager: peerManager,

		shutdownOps: closer,
	}

	node.nodeInfo, err = makeSeedNodeInfo(cfg, nodeKey, genDoc, state)
	if err != nil {
		return nil, err
	}

	node.router, err = createRouter(logger, p2pMetrics, node.NodeInfo, nodeKey, peerManager, cfg, nil)
	if err != nil {
		return nil, combineCloseError(
			fmt.Errorf("failed to create router: %w", err),
			closer)
	}
	node.pexReactor = pex.NewReactor(logger, peerManager, node.router.OpenChannel, peerManager.Subscribe)

	node.BaseService = *service.NewBaseService(logger, "SeedNode", node)

	return node, nil
}

// OnStart starts the Seed Node. It implements service.Service.
func (n *seedNodeImpl) OnStart(ctx context.Context) error {
	if n.config.RPC.PprofListenAddress != "" {
		startPProfServer(ctx, *n.config.RPC)
	}

	now := tmtime.Now()
	genTime := n.genesisDoc.GenesisTime
	if genTime.After(now) {
		n.logger.Info("Genesis time is in the future. Sleeping until then...", "genTime", genTime)
		time.Sleep(genTime.Sub(now))
	}

	if n.config.Instrumentation.Prometheus && n.config.Instrumentation.PrometheusListenAddr != "" {
		n.prometheusSrv = startPrometheusServer(ctx, *n.config.Instrumentation)
	}

	// Start the transport.
	if err := n.router.Start(ctx); err != nil {
		return err
	}

	return n.pexReactor.Start(ctx)
}

// OnStop stops the Seed Node. It implements service.Service.
func (n *seedNodeImpl) OnStop() {
	n.logger.Info("Stopping Node")

	n.pexReactor.Wait()
	n.router.Wait()

	if err := n.shutdownOps(); err != nil {
		if strings.TrimSpace(err.Error()) != "" {
			n.logger.Error("problem shutting down additional services", "err", err)
		}
	}
}

func (n *seedNodeImpl) NodeInfo() *types.NodeInfo {
	return &n.nodeInfo
}
