package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/rs/cors"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/blocksync"
	"github.com/dashpay/tenderdash/internal/consensus"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/eventlog"
	"github.com/dashpay/tenderdash/internal/libs/strings"
	"github.com/dashpay/tenderdash/internal/mempool"
	"github.com/dashpay/tenderdash/internal/p2p"
	tmpubsub "github.com/dashpay/tenderdash/internal/pubsub"
	"github.com/dashpay/tenderdash/internal/pubsub/query"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/state/indexer"
	"github.com/dashpay/tenderdash/internal/statesync"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
	"github.com/dashpay/tenderdash/rpc/coretypes"
	rpcserver "github.com/dashpay/tenderdash/rpc/jsonrpc/server"
	"github.com/dashpay/tenderdash/types"
)

const (
	// see README
	defaultPerPage = 30
	maxPerPage     = 100

	// SubscribeTimeout is the maximum time we wait to subscribe for an event.
	// must be less than the server's write timeout (see rpcserver.DefaultConfig)
	SubscribeTimeout = 5 * time.Second

	// genesisChunkSize is the maximum size, in bytes, of each
	// chunk in the genesis structure for the chunked API
	genesisChunkSize = 16 * 1024 * 1024 // 16
)

//----------------------------------------------
// These interfaces are used by RPC and must be thread safe

type peerManager interface {
	Peers() []types.NodeID
	Addresses(types.NodeID) []p2p.NodeAddress
}

// ----------------------------------------------
// Environment contains objects and interfaces used by the RPC. It is expected
// to be setup once during startup.
type Environment struct {
	// external, thread safe interfaces
	ProxyApp abciclient.Client

	// interfaces defined in types and above
	StateStore       sm.Store
	BlockStore       sm.BlockStore
	EvidencePool     sm.EvidencePool
	ConsensusState   *consensus.State
	ConsensusReactor *consensus.Reactor
	BlockSyncReactor *blocksync.Reactor

	IsListening bool
	Listeners   []string
	NodeInfo    types.NodeInfo

	// interfaces for new p2p interfaces
	PeerManager peerManager

	// objects
	ProTxHash         crypto.ProTxHash
	GenDoc            *types.GenesisDoc // cache the genesis structure
	EventSinks        []indexer.EventSink
	EventBus          *eventbus.EventBus // thread safe
	EventLog          *eventlog.Log
	Mempool           mempool.Mempool
	StateSyncMetricer statesync.Metricer

	Logger log.Logger

	Config config.RPCConfig

	asyncBroadcasts asyncBroadcasts
	rpcMu           sync.Mutex
	rpcService      *rpcService

	// cache of chunked genesis data.
	genChunks []string
}

//----------------------------------------------

func validatePage(pagePtr *int, perPage, totalCount int) (int, error) {
	// this can only happen if we haven't first run validatePerPage
	if perPage < 1 {
		panic(fmt.Errorf("%w (%d)", coretypes.ErrZeroOrNegativePerPage, perPage))
	}

	if pagePtr == nil { // no page parameter
		return 1, nil
	}

	pages := ((totalCount - 1) / perPage) + 1
	if pages == 0 {
		pages = 1 // one page (even if it's empty)
	}
	page := *pagePtr
	if page <= 0 || page > pages {
		return 1, fmt.Errorf("%w expected range: [1, %d], given %d", coretypes.ErrPageOutOfRange, pages, page)
	}

	return page, nil
}

func (env *Environment) validatePerPage(perPagePtr *int) int {
	if perPagePtr == nil { // no per_page parameter
		return defaultPerPage
	}

	perPage := *perPagePtr
	if perPage < 1 {
		return defaultPerPage
		// in unsafe mode there is no max on the page size but in safe mode
		// we cap it to maxPerPage
	} else if perPage > maxPerPage && !env.Config.Unsafe {
		return maxPerPage
	}
	return perPage
}

// InitGenesisChunks configures the environment and should be called on service
// startup.
func (env *Environment) InitGenesisChunks() error {
	if env.genChunks != nil {
		return nil
	}

	if env.GenDoc == nil {
		return nil
	}

	data, err := json.Marshal(env.GenDoc)
	if err != nil {
		return err
	}

	for i := 0; i < len(data); i += genesisChunkSize {
		end := i + genesisChunkSize

		if end > len(data) {
			end = len(data)
		}

		env.genChunks = append(env.genChunks, base64.StdEncoding.EncodeToString(data[i:end]))
	}

	return nil
}

func validateSkipCount(page, perPage int) int {
	skipCount := (page - 1) * perPage
	if skipCount < 0 {
		return 0
	}

	return skipCount
}

// latestHeight can be either latest committed or uncommitted (+1) height.
func (env *Environment) getHeight(latestHeight int64, heightPtr *int64) (int64, error) {
	if heightPtr != nil {
		height := *heightPtr
		if height <= 0 {
			return 0, fmt.Errorf("%w (requested height: %d)", coretypes.ErrZeroOrNegativeHeight, height)
		}
		if height > latestHeight {
			return 0, fmt.Errorf("%w (requested height: %d, blockchain height: %d)",
				coretypes.ErrHeightExceedsChainHead, height, latestHeight)
		}
		base := env.BlockStore.Base()
		if height < base {
			return 0, fmt.Errorf("%w (requested height: %d, base height: %d)", coretypes.ErrHeightNotAvailable, height, base)
		}
		return height, nil
	}
	return latestHeight, nil
}

func (env *Environment) latestUncommittedHeight() int64 {
	if env.ConsensusReactor != nil {
		// consensus reactor can be nil in inspect mode.

		nodeIsSyncing := env.ConsensusReactor.WaitSync()
		if nodeIsSyncing {
			return env.BlockStore.Height()
		}
	}
	return env.BlockStore.Height() + 1
}

// ListenRPC binds all configured RPC addresses without serving requests.
// On failure it closes all listeners opened by this call; otherwise the caller owns them.
func ListenRPC(conf *config.RPCConfig) ([]net.Listener, error) {
	addresses := strings.SplitAndTrimEmpty(conf.ListenAddress, ",", " ")
	listeners := make([]net.Listener, 0, len(addresses))
	for _, address := range addresses {
		listener, err := rpcserver.Listen(address, conf.MaxOpenConnections)
		if err != nil {
			for _, opened := range listeners {
				err = errors.Join(err, opened.Close())
			}
			return nil, err
		}
		listeners = append(listeners, listener)
	}
	return listeners, nil
}

// StartService serves RPC on already bound listeners until ctx is canceled.
// If startup fails, the caller remains responsible for closing the listeners.
func (env *Environment) StartService(ctx context.Context, conf *config.Config, listeners []net.Listener) error {
	env.rpcMu.Lock()
	if env.rpcService != nil {
		env.rpcMu.Unlock()
		return errors.New("RPC service already started")
	}
	ctx, cancel := context.WithCancel(ctx)
	owner := &rpcService{env: env, conf: conf, listeners: listeners, cancel: cancel, ready: make(chan struct{})}
	owner.BaseService = service.NewBaseService(env.Logger, "RPC", owner)
	env.rpcService = owner
	env.rpcMu.Unlock()
	err := owner.Start(ctx)
	close(owner.ready)
	if err != nil {
		cancel()
		env.rpcMu.Lock()
		if env.rpcService == owner {
			env.rpcService = nil
		}
		env.rpcMu.Unlock()
	}
	return err
}

// StopService cancels and joins RPC requests, websocket sessions, and event-log
// forwarding. Call it before releasing the dependencies used by RPC handlers.
func (env *Environment) StopService() {
	env.rpcMu.Lock()
	owner := env.rpcService
	env.rpcMu.Unlock()
	if owner == nil {
		return
	}
	owner.cancel()
	<-owner.ready
	owner.Stop()
	owner.Wait()
	env.rpcMu.Lock()
	if env.rpcService == owner {
		env.rpcService = nil
	}
	env.rpcMu.Unlock()
}

type rpcService struct {
	*service.BaseService
	env       *Environment
	conf      *config.Config
	listeners []net.Listener
	cancel    context.CancelFunc
	ready     chan struct{}
}

func (*rpcService) OnStop() {}

func (owner *rpcService) OnStart(ctx context.Context) error {
	env, conf, listeners := owner.env, owner.conf, owner.listeners
	if err := env.InitGenesisChunks(); err != nil {
		return err
	}

	env.Listeners = []string{
		fmt.Sprintf("Listener(@%v)", conf.P2P.ExternalAddress),
	}

	routes := NewRoutesMap(env, &RouteOptions{
		Unsafe: conf.RPC.Unsafe,
	})

	cfg := rpcserver.DefaultConfig()
	cfg.MaxBodyBytes = conf.RPC.MaxBodyBytes
	cfg.MaxHeaderBytes = conf.RPC.MaxHeaderBytes
	cfg.MaxOpenConnections = conf.RPC.MaxOpenConnections
	// If necessary adjust global WriteTimeout to ensure it's greater than
	// TimeoutBroadcastTxCommit.
	// See https://github.com/dashpay/tenderdash/issues/3435
	// Note we don't need to adjust anything if the timeout is already unlimited.
	if cfg.WriteTimeout > 0 && cfg.WriteTimeout <= conf.RPC.TimeoutBroadcastTxCommit {
		cfg.WriteTimeout = conf.RPC.TimeoutBroadcastTxCommit + 1*time.Second
	}

	// If the event log is enabled, subscribe to all events published to the
	// event bus, and forward them to the event log.
	if lg := env.EventLog; lg != nil {
		// TODO(creachadair): This is kind of a hack, ideally we'd share the
		// observer with the indexer, but it's tricky to plumb them together.
		// For now, use a "normal" subscription with a big buffer allowance.
		// The event log should always be able to keep up.
		const subscriberID = "event-log-subscriber"
		sub, err := env.EventBus.SubscribeWithArgs(ctx, tmpubsub.SubscribeArgs{
			ClientID: subscriberID,
			Query:    query.All,
			Limit:    1 << 16, // essentially "no limit"
		})
		if err != nil {
			return fmt.Errorf("event log subscribe: %w", err)
		}
		if !owner.Go(ctx, func(ctx context.Context) {
			// N.B. Use background for unsubscribe, ctx is already terminated.
			defer env.EventBus.UnsubscribeAll(context.Background(), subscriberID) //nolint:errcheck
			for {
				msg, err := sub.Next(ctx)
				if err != nil {
					env.Logger.Error("Subscription terminated", "err", err)
					return
				}
				etype, ok := eventlog.FindType(msg.Events())
				if ok {
					_ = lg.Add(etype, msg.Data())
				}
			}
		}) {
			_ = env.EventBus.UnsubscribeAll(context.Background(), subscriberID)
			return errors.New("RPC service is stopping")
		}

		env.Logger.Info("Event log subscription enabled")
	}

	// We may expose the RPC over both TCP and a Unix-domain socket.
	for _, listener := range listeners {
		mux := http.NewServeMux()
		rpcLogger := env.Logger.With("module", "rpc-server")
		rpcserver.RegisterRPCFuncs(mux, routes, rpcLogger)

		if conf.RPC.ExperimentalDisableWebsocket {
			rpcLogger.Info("Disabling websocket endpoints (experimental-disable-websocket=true)")
		} else {
			rpcLogger.Info("WARNING: Websocket RPC access is deprecated and will be removed " +
				"in Tendermint v0.37. See https://tinyurl.com/adr075 for more information.")
			wmLogger := rpcLogger.With("protocol", "websocket")
			wm := rpcserver.NewWebsocketManager(wmLogger, routes,
				rpcserver.OnDisconnect(func(remoteAddr string) {
					err := env.EventBus.UnsubscribeAll(context.Background(), remoteAddr)
					if err != nil && err != tmpubsub.ErrSubscriptionNotFound {
						wmLogger.Error("Failed to unsubscribe addr from events", "addr", remoteAddr, "err", err)
					}
				}),
				rpcserver.ReadLimit(cfg.MaxBodyBytes),
			)
			wm.CheckOrigin = rpcserver.OriginChecker(wmLogger, conf.RPC.CORSAllowedOrigins)
			mux.HandleFunc("/websocket", wm.WebsocketHandler)
		}

		var rootHandler http.Handler = mux
		if conf.RPC.IsCorsEnabled() {
			corsMiddleware := cors.New(cors.Options{
				AllowedOrigins: conf.RPC.CORSAllowedOrigins,
				AllowedMethods: conf.RPC.CORSAllowedMethods,
				AllowedHeaders: conf.RPC.CORSAllowedHeaders,
			})
			rootHandler = corsMiddleware.Handler(mux)
		}
		if conf.RPC.IsTLSEnabled() {
			if !owner.Go(ctx, func(ctx context.Context) {
				if err := rpcserver.ServeTLS(
					ctx,
					listener,
					rootHandler,
					conf.RPC.CertFile(),
					conf.RPC.KeyFile(),
					rpcLogger,
					cfg,
				); err != nil {
					env.Logger.Error("error serving server with TLS", "err", err)
				}
			}) {
				return errors.New("RPC service is stopping")
			}
		} else {
			if !owner.Go(ctx, func(ctx context.Context) {
				if err := rpcserver.Serve(
					ctx,
					listener,
					rootHandler,
					rpcLogger,
					cfg,
				); err != nil {
					env.Logger.Error("error serving server", "err", err)
				}
			}) {
				return errors.New("RPC service is stopping")
			}
		}
	}

	return nil
}
