package blocksync

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/internal/consensus"
	"github.com/tendermint/tendermint/internal/eventbus"
	"github.com/tendermint/tendermint/internal/p2p"
	"github.com/tendermint/tendermint/internal/p2p/conn"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/internal/store"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/service"
	bcproto "github.com/tendermint/tendermint/proto/tendermint/blocksync"
	"github.com/tendermint/tendermint/types"
)

var _ service.Service = (*Reactor)(nil)

const (
	// BlockSyncChannel is a channel for blocks and status updates
	BlockSyncChannel = p2p.ChannelID(0x40)

	trySyncIntervalMS = 10

	// ask for best height every 10s
	statusUpdateIntervalSeconds = 10

	// check if we should switch to consensus reactor
	switchToConsensusIntervalSeconds = 1

	// switch to consensus after this duration of inactivity
	syncTimeout = 60 * time.Second
)

func GetChannelDescriptor() *p2p.ChannelDescriptor {
	return &p2p.ChannelDescriptor{
		ID:                  BlockSyncChannel,
		MessageType:         new(bcproto.Message),
		Priority:            5,
		SendQueueCapacity:   1000,
		RecvBufferCapacity:  1024,
		RecvMessageCapacity: MaxMsgSize,
		Name:                "blockSync",
	}
}

type consensusReactor interface {
	// For when we switch from block sync reactor to the consensus
	// machine.
	SwitchToConsensus(ctx context.Context, state sm.State, skipWAL bool)
}

type peerError struct {
	err    error
	peerID types.NodeID
}

func (e peerError) Error() string {
	return fmt.Sprintf("error with peer %v: %s", e.peerID, e.err.Error())
}

// Reactor handles long-term catchup syncing.
type Reactor struct {
	service.BaseService
	logger log.Logger

	// immutable
	initialState sm.State
	// store
	stateStore sm.Store

	blockExec   *sm.BlockExecutor
	store       sm.BlockStore
	pool        *BlockPool
	consReactor consensusReactor
	blockSync   *atomic.Bool

	chCreator  p2p.ChannelCreator
	peerEvents p2p.PeerEventSubscriber

	metrics  *consensus.Metrics
	eventBus *eventbus.EventBus

	syncStartTime time.Time

	nodeProTxHash types.ProTxHash

	executor *blockApplier
}

// NewReactor returns new reactor instance.
func NewReactor(
	logger log.Logger,
	stateStore sm.Store,
	blockExec *sm.BlockExecutor,
	store *store.BlockStore,
	nodeProTxHash crypto.ProTxHash,
	consReactor consensusReactor,
	channelCreator p2p.ChannelCreator,
	peerEvents p2p.PeerEventSubscriber,
	blockSync bool,
	metrics *consensus.Metrics,
	eventBus *eventbus.EventBus,
) *Reactor {
	r := &Reactor{
		logger:        logger,
		stateStore:    stateStore,
		blockExec:     blockExec,
		store:         store,
		consReactor:   consReactor,
		blockSync:     &atomic.Bool{},
		chCreator:     channelCreator,
		peerEvents:    peerEvents,
		metrics:       metrics,
		eventBus:      eventBus,
		nodeProTxHash: nodeProTxHash,
		executor: newBlockApplier(
			blockExec,
			store,
			applierWithMetrics(metrics),
			applierWithLogger(logger),
		),
	}
	r.blockSync.Store(blockSync)
	r.BaseService = *service.NewBaseService(logger, "BlockSync", r)
	return r
}

// OnStart starts separate go routines for each p2p Channel and listens for
// envelopes on each. In addition, it also listens for peer updates and handles
// messages on that p2p channel accordingly. The caller must be sure to execute
// OnStop to ensure the outbound p2p Channels are closed.
//
// If blockSync is enabled, we also start the pool and the pool processing
// goroutine. If the pool fails to start, an error is returned.
func (r *Reactor) OnStart(ctx context.Context) error {
	blockSyncCh, err := r.chCreator(ctx, GetChannelDescriptor())
	if err != nil {
		return err
	}
	r.chCreator = func(context.Context, *conn.ChannelDescriptor) (p2p.Channel, error) { return blockSyncCh, nil }

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

	blockSyncClient := NewChannel(blockSyncCh, ChannelWithLogger(r.logger))
	r.pool = NewBlockPool(startHeight, blockSyncClient, r.executor, WithLogger(r.logger))
	if r.blockSync.Load() {
		if err := r.pool.Start(ctx); err != nil {
			return err
		}
		go r.requestRoutine(ctx, blockSyncCh)

		go r.poolRoutine(ctx, false, blockSyncCh)
	}

	go r.processBlockSyncCh(ctx, blockSyncCh, blockSyncClient)
	go r.processPeerUpdates(ctx, r.peerEvents(ctx, "blocksync"), blockSyncCh)

	return nil
}

// OnStop stops the reactor by signaling to all spawned goroutines to exit and
// blocking until they all exit.
func (r *Reactor) OnStop() {
	if r.blockSync.Load() {
		r.pool.Stop()
	}
}

// respondToPeer loads a block and sends it to the requesting peer, if we have it.
// Otherwise, we'll respond saying we do not have it.
func (r *Reactor) respondToPeer(ctx context.Context, msg *bcproto.BlockRequest, peerID types.NodeID, blockSyncCh p2p.Channel) error {
	block := r.store.LoadBlock(msg.Height)
	if block == nil {
		r.logger.Info("peer requesting a block we do not have", "peer", peerID, "height", msg.Height)
		return blockSyncCh.Send(ctx, p2p.Envelope{
			To:      peerID,
			Message: &bcproto.NoBlockResponse{Height: msg.Height},
		})
	}

	commit := r.store.LoadSeenCommitAt(msg.Height)
	if commit == nil {
		return fmt.Errorf("found block in store with no commit: %v", block)
	}

	blockProto, err := block.ToProto()
	if err != nil {
		return fmt.Errorf("failed to convert block to protobuf: %w", err)
	}

	return blockSyncCh.Send(ctx, p2p.Envelope{
		To: peerID,
		Message: &bcproto.BlockResponse{
			Block:  blockProto,
			Commit: commit.ToProto(),
		},
	})

}

// handleMessage handles an Envelope sent from a peer on a specific p2p Channel.
// It will handle errors and any possible panics gracefully. A caller can handle
// any error returned by sending a PeerError on the respective channel.
func (r *Reactor) handleMessage(ctx context.Context, envelope *p2p.Envelope, blockSyncCh p2p.Channel, channel *Channel) (err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("panic in processing message: %v", e)
			r.logger.Error(
				"recovering from processing message panic",
				"err", err,
				"stack", string(debug.Stack()),
			)
		}
	}()

	//r.logger.Debug("received message", "message", envelope.Message, "peer", envelope.From)

	if envelope.ChannelID != BlockSyncChannel {
		return fmt.Errorf("unknown channel ID (%d) for envelope (%v)", envelope.ChannelID, envelope)
	}

	switch msg := envelope.Message.(type) {
	case *bcproto.BlockRequest:
		return r.respondToPeer(ctx, msg, envelope.From, blockSyncCh)
	case *bcproto.BlockResponse:
		err = channel.Resolve(ctx, envelope)
		if err != nil {
			r.logger.Error("error", "error", err)
		}
	case *bcproto.StatusRequest:
		return blockSyncCh.Send(ctx, p2p.Envelope{
			To: envelope.From,
			Message: &bcproto.StatusResponse{
				Height: r.store.Height(),
				Base:   r.store.Base(),
			},
		})
	case *bcproto.StatusResponse:
		r.pool.SetPeerRange(newPeerData(envelope.From, msg.Base, msg.Height))

	case *bcproto.NoBlockResponse:
		r.logger.Debug("peer does not have the requested block",
			"peer", envelope.From,
			"height", msg.Height)

	default:
		return fmt.Errorf("received unknown message: %T", msg)
	}

	return nil
}

// processBlockSyncCh initiates a blocking process where we listen for and handle
// envelopes on the BlockSyncChannel and blockSyncOutBridgeCh. Any error encountered during
// message execution will result in a PeerError being sent on the BlockSyncChannel.
// When the reactor is stopped, we will catch the signal and close the p2p Channel
// gracefully.
func (r *Reactor) processBlockSyncCh(ctx context.Context, blockSyncCh p2p.Channel, channel *Channel) {
	iter := blockSyncCh.Receive(ctx)
	for iter.Next(ctx) {
		envelope := iter.Envelope()
		if err := r.handleMessage(ctx, envelope, blockSyncCh, channel); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}

			r.logger.Error("failed to process message", "ch_id", envelope.ChannelID, "envelope", envelope, "err", err)
			if serr := blockSyncCh.SendError(ctx, p2p.PeerError{
				NodeID: envelope.From,
				Err:    err,
			}); serr != nil {
				return
			}
		}
	}
}

// processPeerUpdate processes a PeerUpdate.
func (r *Reactor) processPeerUpdate(ctx context.Context, peerUpdate p2p.PeerUpdate, blockSyncCh p2p.Channel) {
	r.logger.Debug("received peer update", "peer", peerUpdate.NodeID, "status", peerUpdate.Status)

	// XXX: Pool#RedoRequest can sometimes give us an empty peer.
	if len(peerUpdate.NodeID) == 0 {
		return
	}

	switch peerUpdate.Status {
	case p2p.PeerStatusUp:
		// send a status update the newly added peer
		if err := blockSyncCh.Send(ctx, p2p.Envelope{
			To: peerUpdate.NodeID,
			Message: &bcproto.StatusResponse{
				Base:   r.store.Base(),
				Height: r.store.Height(),
			},
		}); err != nil {
			r.pool.RemovePeer(peerUpdate.NodeID)
			if err := blockSyncCh.SendError(ctx, p2p.PeerError{
				NodeID: peerUpdate.NodeID,
				Err:    err,
			}); err != nil {
				return
			}
		}

	case p2p.PeerStatusDown:
		r.pool.RemovePeer(peerUpdate.NodeID)
	}
}

// processPeerUpdates initiates a blocking process where we listen for and handle
// PeerUpdate messages. When the reactor is stopped, we will catch the signal and
// close the p2p PeerUpdatesCh gracefully.
func (r *Reactor) processPeerUpdates(ctx context.Context, peerUpdates *p2p.PeerUpdates, blockSyncCh p2p.Channel) {
	for {
		select {
		case <-ctx.Done():
			return
		case peerUpdate := <-peerUpdates.Updates():
			r.processPeerUpdate(ctx, peerUpdate, blockSyncCh)
		}
	}
}

// SwitchToBlockSync is called by the state sync reactor when switching to fast
// sync.
func (r *Reactor) SwitchToBlockSync(ctx context.Context, state sm.State) error {
	r.blockSync.Store(true)
	r.initialState = state
	r.executor.state = state
	r.pool.height = state.LastBlockHeight + 1

	if err := r.pool.Start(ctx); err != nil {
		return err
	}

	r.syncStartTime = time.Now()

	bsCh, err := r.chCreator(ctx, GetChannelDescriptor())
	if err != nil {
		return err
	}

	go r.requestRoutine(ctx, bsCh)
	go r.poolRoutine(ctx, true, bsCh)

	if err := r.PublishStatus(types.EventDataBlockSyncStatus{
		Complete: false,
		Height:   state.LastBlockHeight,
	}); err != nil {
		return err
	}

	return nil
}

func (r *Reactor) requestRoutine(ctx context.Context, blockSyncCh p2p.Channel) {
	statusUpdateTicker := time.NewTicker(statusUpdateIntervalSeconds * time.Second)
	defer statusUpdateTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-statusUpdateTicker.C:
			if err := blockSyncCh.Send(ctx, p2p.Envelope{
				Broadcast: true,
				Message:   &bcproto.StatusRequest{},
			}); err != nil {
				return
			}
		}
	}
}

// poolRoutine handles messages from the poolReactor telling the reactor what to
// do.
//
// NOTE: Don't sleep in the FOR_LOOP or otherwise slow it down!
func (r *Reactor) poolRoutine(ctx context.Context, stateSynced bool, blockSyncCh p2p.Channel) {
	switchToConsensusTicker := time.NewTicker(switchToConsensusIntervalSeconds * time.Second)
	defer switchToConsensusTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-switchToConsensusTicker.C:
			var (
				height, numPending = r.pool.GetStatus()
				lastAdvance        = r.pool.LastAdvance()
				isCaughtUp         = r.pool.IsCaughtUp()
			)

			r.logger.Debug("consensus ticker",
				"num_pending", numPending,
				"height", height)

			switch {

			case isCaughtUp:
				r.logger.Info("switching to consensus reactor", "height", height)

			case time.Since(lastAdvance) > syncTimeout:
				r.logger.Error("no progress since last advance", "last_advance", lastAdvance)

			default:
				r.logger.Info(
					"not caught up yet",
					"height", height,
					"max_peer_height", r.pool.MaxPeerHeight(),
					"timeout_in", syncTimeout-time.Since(lastAdvance),
				)
				continue
			}

			r.pool.Stop()

			r.blockSync.Store(false)

			if r.consReactor != nil {
				r.consReactor.SwitchToConsensus(ctx, r.executor.State(), isCaughtUp || stateSynced)
			}

			return
		}
	}
}

func (r *Reactor) GetMaxPeerBlockHeight() int64 {
	return r.pool.MaxPeerHeight()
}

func (r *Reactor) GetTotalSyncedTime() time.Duration {
	if !r.blockSync.Load() || r.syncStartTime.IsZero() {
		return time.Duration(0)
	}
	return time.Since(r.syncStartTime)
}

func (r *Reactor) GetRemainingSyncTime() time.Duration {
	if !r.blockSync.Load() {
		return time.Duration(0)
	}

	targetSyncs := r.pool.targetSyncBlocks()
	currentSyncs := r.store.Height() - r.pool.startHeight + 1
	lastSyncRate := r.pool.getLastSyncRate()
	if currentSyncs < 0 || lastSyncRate < 0.001 {
		return time.Duration(0)
	}

	remain := float64(targetSyncs-currentSyncs) / lastSyncRate

	return time.Duration(int64(remain * float64(time.Second)))
}

func (r *Reactor) PublishStatus(event types.EventDataBlockSyncStatus) error {
	if r.eventBus == nil {
		return errors.New("event bus is not configured")
	}
	return r.eventBus.PublishEventBlockSyncStatus(event)
}
