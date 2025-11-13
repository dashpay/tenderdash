package blocksync

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/consensus"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/store"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
	bcproto "github.com/dashpay/tenderdash/proto/tendermint/blocksync"
	"github.com/dashpay/tenderdash/types"
)

var _ service.Service = (*Reactor)(nil)

const (
	// ask for best height every 10s
	statusUpdateIntervalSeconds = 10

	// check if we should switch to consensus reactor
	switchToConsensusIntervalSeconds = 1

	// switch to consensus after this duration of inactivity
	syncTimeout = 60 * time.Second
)

type ReactorOption func(*Reactor)

type consensusReactor interface {
	// For when we switch from block sync reactor to the consensus
	// machine.
	SwitchToConsensus(ctx context.Context, state sm.State, skipWAL bool)
}

// Reactor handles long-term catchup syncing.
type Reactor struct {
	service.BaseService
	logger log.Logger

	// immutable
	initialState sm.State
	// store
	stateStore sm.Store

	blockExec     *sm.BlockExecutor
	store         sm.BlockStore
	synchronizer  *Synchronizer
	consReactor   consensusReactor
	blockSyncFlag *atomic.Bool

	p2pClient  *client.Client
	peerEvents p2p.PeerEventSubscriber

	metrics  *consensus.Metrics
	eventBus *eventbus.EventBus

	syncStartTime time.Time

	nodeProTxHash types.ProTxHash

	executor *blockApplier

	statusUpdateInterval time.Duration
}

// NewReactor returns new reactor instance.
func NewReactor(
	logger log.Logger,
	stateStore sm.Store,
	blockExec *sm.BlockExecutor,
	store *store.BlockStore,
	nodeProTxHash crypto.ProTxHash,
	consReactor consensusReactor,
	p2pClient *client.Client,
	peerEvents p2p.PeerEventSubscriber,
	blockSync bool,
	metrics *consensus.Metrics,
	eventBus *eventbus.EventBus,
	opts ...ReactorOption,
) *Reactor {
	r := &Reactor{
		logger:               logger,
		stateStore:           stateStore,
		blockExec:            blockExec,
		store:                store,
		consReactor:          consReactor,
		blockSyncFlag:        &atomic.Bool{},
		p2pClient:            p2pClient,
		peerEvents:           peerEvents,
		metrics:              metrics,
		eventBus:             eventBus,
		nodeProTxHash:        nodeProTxHash,
		statusUpdateInterval: statusUpdateIntervalSeconds * time.Second,
		executor: newBlockApplier(
			blockExec,
			store,
			applierWithMetrics(metrics),
			applierWithLogger(logger),
		),
	}
	for _, opt := range opts {
		opt(r)
	}
	if r.statusUpdateInterval <= 0 {
		r.statusUpdateInterval = statusUpdateIntervalSeconds * time.Second
	}
	r.blockSyncFlag.Store(blockSync)
	r.BaseService = *service.NewBaseService(logger, "BlockSync", r)
	return r
}

// WithStatusUpdateInterval overrides the interval used to poll peers for their status updates.
func WithStatusUpdateInterval(interval time.Duration) ReactorOption {
	return func(r *Reactor) {
		if interval > 0 {
			r.statusUpdateInterval = interval
		}
	}
}

// OnStart starts separate go routines for each p2p Channel and listens for
// envelopes on each. In addition, it also listens for peer updates and handles
// messages on that p2p channel accordingly. The caller must be sure to execute
// OnStop to ensure the outbound p2p Channels are closed.
//
// If blockSyncFlag is enabled, we also start the synchronizer
// If the synchronizer fails to start, an error is returned.
func (r *Reactor) OnStart(ctx context.Context) error {
	state, err := r.stateStore.Load()
	if err != nil {
		return err
	}
	r.initialState = state
	r.executor.state = state

	if state.LastBlockHeight != r.store.Height() {
		return fmt.Errorf("state (%v) and store (%v) height mismatch", state.LastBlockHeight, r.store.Height())
	}

	startHeight := r.store.Height() + 1
	if startHeight == 1 {
		startHeight = state.InitialHeight
	}

	r.synchronizer = NewSynchronizer(startHeight, r.p2pClient, r.executor, WithLogger(r.logger))
	if r.blockSyncFlag.Load() {
		if err := r.synchronizer.Start(ctx); err != nil {
			return err
		}
		go r.requestRoutine(ctx, r.p2pClient)
		go r.poolRoutine(ctx, false)
	}
	go func() {
		err := r.p2pClient.Consume(ctx, consumerHandler(r.logger, r.store, r.synchronizer))
		if err != nil {
			r.logger.Error("failed to consume p2p blocksync messages", "error", err)
		}
	}()
	go r.processPeerUpdates(ctx, r.peerEvents(ctx, "blocksync"), r.p2pClient)

	return nil
}

// OnStop stops the reactor by signaling to all spawned goroutines to exit and
// blocking until they all exit.
func (r *Reactor) OnStop() {
	if r.blockSyncFlag.Load() {
		r.synchronizer.Stop()
	}
}

// processPeerUpdate processes a PeerUpdate.
func (r *Reactor) processPeerUpdate(ctx context.Context, peerUpdate p2p.PeerUpdate, client *client.Client) {
	r.logger.Trace("received peer update", "peer", peerUpdate.NodeID, "status", peerUpdate.Status)

	// XXX: Pool#RedoRequest can sometimes give us an empty peer.
	if len(peerUpdate.NodeID) == 0 {
		return
	}

	switch peerUpdate.Status {
	case p2p.PeerStatusUp:
		// send a status update the newly added peer
		err := client.Send(ctx, p2p.Envelope{
			To: peerUpdate.NodeID,
			Message: &bcproto.StatusResponse{
				Base:   r.store.Base(),
				Height: r.store.Height(),
			},
		})
		if err != nil {
			r.synchronizer.RemovePeer(peerUpdate.NodeID)
			if err := client.Send(ctx, p2p.PeerError{
				NodeID: peerUpdate.NodeID,
				Err:    err,
			}); err != nil {
				return
			}
		}

	case p2p.PeerStatusDown:
		r.synchronizer.RemovePeer(peerUpdate.NodeID)
	}
}

// processPeerUpdates initiates a blocking process where we listen for and handle
// PeerUpdate messages. When the reactor is stopped, we will catch the signal and
// close the p2p PeerUpdatesCh gracefully.
func (r *Reactor) processPeerUpdates(ctx context.Context, peerUpdates *p2p.PeerUpdates, client *client.Client) {
	for {
		select {
		case <-ctx.Done():
			return
		case peerUpdate := <-peerUpdates.Updates():
			r.processPeerUpdate(ctx, peerUpdate, client)
		}
	}
}

// SwitchToBlockSync is called by the state sync reactor when switching to fast
// sync.
func (r *Reactor) SwitchToBlockSync(ctx context.Context, state sm.State) error {
	r.blockSyncFlag.Store(true)
	r.initialState = state
	r.executor.state = state
	r.synchronizer.height = state.LastBlockHeight + 1

	if err := r.synchronizer.Start(ctx); err != nil {
		return err
	}

	r.syncStartTime = time.Now()

	go r.requestRoutine(ctx, r.p2pClient)
	go r.poolRoutine(ctx, true)

	if err := r.PublishStatus(types.EventDataBlockSyncStatus{
		Complete: false,
		Height:   state.LastBlockHeight,
	}); err != nil {
		return err
	}

	return nil
}

func (r *Reactor) requestRoutine(ctx context.Context, p2pClient *client.Client) {
	statusUpdateTicker := time.NewTicker(r.statusUpdateInterval)
	defer statusUpdateTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-statusUpdateTicker.C:
			err := p2pClient.GetSyncStatus(ctx)
			if err != nil {
				return
			}
		}
	}
}

// poolRoutine handles messages from the poolReactor telling the reactor what to
// do.
//
// NOTE: Don't sleep in the FOR_LOOP or otherwise slow it down!
func (r *Reactor) poolRoutine(ctx context.Context, stateSynced bool) {
	r.synchronizer.WaitForSync(ctx)
	r.synchronizer.Stop()
	r.blockSyncFlag.Store(false)
	if r.consReactor != nil {
		r.consReactor.SwitchToConsensus(ctx, r.executor.State(), r.synchronizer.IsCaughtUp() || stateSynced)
	}
}

func (r *Reactor) GetMaxPeerBlockHeight() int64 {
	return r.synchronizer.MaxPeerHeight()
}

func (r *Reactor) GetTotalSyncedTime() time.Duration {
	if !r.blockSyncFlag.Load() || r.syncStartTime.IsZero() {
		return time.Duration(0)
	}
	return time.Since(r.syncStartTime)
}

func (r *Reactor) GetRemainingSyncTime() time.Duration {
	if !r.blockSyncFlag.Load() {
		return time.Duration(0)
	}
	targetSyncs := r.synchronizer.targetSyncBlocks()
	currentSyncs := r.store.Height() - r.synchronizer.startHeight + 1
	lastSyncRate := r.synchronizer.getLastSyncRate()
	if currentSyncs < 0 || lastSyncRate < 0.001 {
		return time.Duration(0)
	}
	remainSyncs := targetSyncs - currentSyncs
	remain := float64(remainSyncs) / lastSyncRate
	return time.Duration(int64(remain * float64(time.Second)))
}

func (r *Reactor) PublishStatus(event types.EventDataBlockSyncStatus) error {
	if r.eventBus == nil {
		return errors.New("event bus is not configured")
	}
	return r.eventBus.PublishEventBlockSyncStatus(event)
}
