package statesync

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/config"
	dashcore "github.com/dashpay/tenderdash/dash/core"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/p2p"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/internal/store"
	"github.com/dashpay/tenderdash/libs/log"
	tmmath "github.com/dashpay/tenderdash/libs/math"
	"github.com/dashpay/tenderdash/libs/service"
	"github.com/dashpay/tenderdash/light/provider"
	ssproto "github.com/dashpay/tenderdash/proto/tendermint/statesync"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

var (
	_ service.Service = (*Reactor)(nil)
)

const (
	// SnapshotChannel exchanges snapshot metadata
	SnapshotChannel = p2p.ChannelID(0x60)

	// ChunkChannel exchanges chunk contents
	ChunkChannel = p2p.ChannelID(0x61)

	// LightBlockChannel exchanges light blocks
	LightBlockChannel = p2p.ChannelID(0x62)

	// ParamsChannel exchanges consensus params
	ParamsChannel = p2p.ChannelID(0x63)

	// recentSnapshots is the number of recent snapshots to send and receive per peer.
	recentSnapshots = 10

	// lightBlockResponseTimeout is how long the dispatcher waits for a peer to
	// return a light block
	lightBlockResponseTimeout = 10 * time.Second

	// initStateProviderTimeout is how long state provider initialization (including trusted block fetch/verify) can take
	initStateProviderTimeout = 2 * lightBlockResponseTimeout

	// initStateProviderRetries defines how many times state provider initialization will be retried
	initStateProviderRetries = 3

	// consensusParamsResponseTimeout is the time the p2p state provider waits
	// before performing a secondary call
	consensusParamsResponseTimeout = 5 * time.Second

	// maxLightBlockRequestRetries is the amount of retries acceptable before
	// the backfill process aborts
	maxLightBlockRequestRetries = 20

	// backfillSleepTime uses to sleep if no connected peers to fetch light blocks
	backfillSleepTime = 1 * time.Second

	// minPeers is the minimum number of peers required to start a state sync
	minPeers = 2
)

func getChannelDescriptors() map[p2p.ChannelID]*p2p.ChannelDescriptor {
	return p2p.StatesyncChannelDescriptors()
}

// Metricer defines an interface used for the rpc sync info query, please see statesync.metrics
// for the details.
type Metricer interface {
	TotalSnapshots() int64
	ChunkProcessAvgTime() time.Duration
	SnapshotHeight() int64
	SnapshotChunksCount() int64
	SnapshotChunksTotal() int64
	BackFilledBlocks() int64
	BackFillBlocksTotal() int64
}

// Reactor handles state sync, both restoring snapshots for the local node and
// serving snapshots for other nodes.
type Reactor struct {
	service.BaseService
	logger log.Logger

	chainID       string
	initialHeight int64
	cfg           config.StateSyncConfig
	stateStore    sm.Store
	blockStore    *store.BlockStore

	conn           abciclient.Client
	tempDir        string
	peerEvents     p2p.PeerEventSubscriber
	chCreator      p2p.ChannelCreator
	sendBlockError func(context.Context, p2p.PeerError) error
	postSyncHook   func(context.Context, sm.State) error

	// when true, the reactor will, during startup perform a
	// statesync for this node, and otherwise just provide
	// snapshots to other nodes.
	needsStateSync bool

	// Dispatcher is used to multiplex light block requests and responses over multiple
	// peers used by the p2p state provider and in reverse sync.
	dispatcher *Dispatcher
	peers      *peerList

	// These will only be set when a state sync is in progress. It is used to feed
	// received snapshots and chunks into the syncer and manage incoming and outgoing
	// providers.
	mtx               sync.RWMutex
	initSyncer        func() *syncer
	requestSnapshot   func() error
	syncer            *syncer // syncer is nil when sync is not in progress
	initStateProvider func(ctx context.Context, chainID string, initialHeight int64) error
	stateProvider     StateProvider

	eventBus           *eventbus.EventBus
	metrics            *Metrics
	backfillBlockTotal int64
	backfilledBlocks   int64

	dashCoreClient dashcore.Client

	csState ConsensusStateProvider
}

// ConsensusStateProvider is an interface that allows the state sync reactor to
// interact with the consensus state. It is defined to improve testability.
//
// Implemented by consensus.State
type ConsensusStateProvider interface {
	PublishCommitEvent(commit *types.Commit) error
	GetCurrentHeight() int64
}

// NewReactor returns a reference to a new state sync reactor, which implements
// the service.Service interface. It accepts a logger, connections for snapshots
// and querying, references to p2p Channels and a channel to listen for peer
// updates on. Note, the reactor will close all p2p Channels when stopping.
func NewReactor(
	chainID string,
	initialHeight int64,
	cfg config.StateSyncConfig,
	logger log.Logger,
	conn abciclient.Client,
	channelCreator p2p.ChannelCreator,
	peerEvents p2p.PeerEventSubscriber,
	stateStore sm.Store,
	blockStore *store.BlockStore,
	tempDir string,
	ssMetrics *Metrics,
	eventBus *eventbus.EventBus,
	postSyncHook func(context.Context, sm.State) error,
	needsStateSync bool,
	client dashcore.Client,
	csState ConsensusStateProvider,
) *Reactor {
	r := &Reactor{
		logger:         logger,
		chainID:        chainID,
		initialHeight:  initialHeight,
		cfg:            cfg,
		conn:           conn,
		chCreator:      channelCreator,
		peerEvents:     peerEvents,
		tempDir:        tempDir,
		stateStore:     stateStore,
		blockStore:     blockStore,
		peers:          newPeerList(),
		metrics:        ssMetrics,
		eventBus:       eventBus,
		postSyncHook:   postSyncHook,
		needsStateSync: needsStateSync,
		dashCoreClient: client,
		csState:        csState,
	}

	r.BaseService = *service.NewBaseService(logger, "StateSync", r)
	return r
}

// OnStart starts separate go routines for each p2p Channel and listens for
// envelopes on each. In addition, it also listens for peer updates and handles
// messages on that p2p channel accordingly. Note, we do not launch a go-routine to
// handle individual envelopes as to not have to deal with bounding workers or pools.
// The caller must be sure to execute OnStop to ensure the outbound p2p Channels are
// closed. No error is returned.
func (r *Reactor) OnStart(ctx context.Context) error {
	// construct channels
	chDesc := getChannelDescriptors()
	snapshotCh, err := r.chCreator(ctx, chDesc[SnapshotChannel])
	if err != nil {
		return err
	}
	chunkCh, err := r.chCreator(ctx, chDesc[ChunkChannel])
	if err != nil {
		return err
	}
	blockCh, err := r.chCreator(ctx, chDesc[LightBlockChannel])
	if err != nil {
		return err
	}
	paramsCh, err := r.chCreator(ctx, chDesc[ParamsChannel])
	if err != nil {
		return err
	}

	// define constructor and helper functions, that hold
	// references to these channels for use later. This is not
	// ideal.
	r.initSyncer = func() *syncer {
		return &syncer{
			logger:        r.logger,
			stateProvider: r.stateProvider,
			conn:          r.conn,
			snapshots:     newSnapshotPool(),
			snapshotCh:    snapshotCh,
			chunkCh:       chunkCh,
			tempDir:       r.tempDir,
			fetchers:      r.cfg.Fetchers,
			retryTimeout:  r.cfg.ChunkRequestTimeout,
			metrics:       r.metrics,
		}
	}
	r.dispatcher = NewDispatcher(blockCh, r.logger)
	r.requestSnapshot = func() error {
		// request snapshots from all currently connected peers
		return snapshotCh.Send(ctx, p2p.Envelope{
			Broadcast: true,
			Message:   &ssproto.SnapshotsRequest{},
		})
	}
	r.sendBlockError = blockCh.SendError

	r.initStateProvider = func(ctx context.Context, chainID string, initialHeight int64) error {
		spLogger := r.logger.With("module", "stateprovider")
		spLogger.Debug("initializing state sync state provider", "useP2P", r.cfg.UseP2P)

		if r.cfg.UseP2P {
			if err := r.waitForEnoughPeers(ctx, minPeers); err != nil {
				return err
			}

			peers := r.peers.All()
			providers := make([]provider.Provider, len(peers))
			for idx, p := range peers {
				providers[idx] = NewBlockProvider(p, chainID, r.dispatcher)
			}

			stateProvider, err := NewP2PStateProvider(ctx, chainID, initialHeight,
				providers, paramsCh, r.logger.With("module", "stateprovider"), r.dashCoreClient)
			if err != nil {
				return fmt.Errorf("failed to initialize P2P state provider: %w", err)
			}
			r.setStateProvider(stateProvider)
			return nil
		}

		stateProvider, err := NewRPCStateProvider(ctx, chainID, initialHeight, r.cfg.RPCServers, spLogger, r.dashCoreClient)
		if err != nil {
			return fmt.Errorf("failed to initialize RPC state provider: %w", err)
		}
		r.setStateProvider(stateProvider)
		return nil
	}

	go r.processChannels(ctx, map[p2p.ChannelID]p2p.Channel{
		SnapshotChannel:   snapshotCh,
		ChunkChannel:      chunkCh,
		LightBlockChannel: blockCh,
		ParamsChannel:     paramsCh,
	})
	go r.processPeerUpdates(ctx, r.peerEvents(ctx, "statesync"))

	if r.needsStateSync {
		r.logger.Info("starting state sync")
		if _, err := r.Sync(ctx); err != nil {
			if errors.Is(err, errNoSnapshots) && r.postSyncHook != nil {
				r.logger.Warn("no snapshots available; falling back to block sync", "err", err)

				state, err := r.stateStore.Load()
				if err != nil {
					return fmt.Errorf("failed to load state: %w", err)
				}

				if err := r.postSyncHook(ctx, state); err != nil {
					return fmt.Errorf("post sync failed: %w", err)
				}
			} else {
				r.logger.Error("state sync failed; shutting down this node", "err", err)
				return err
			}
		}
	}

	return nil
}

// OnStop stops the reactor by signaling to all spawned goroutines to exit and
// blocking until they all exit.
func (r *Reactor) OnStop() {
	// tell the dispatcher to stop sending any more requests
	r.dispatcher.Close()
}

// Sync runs a state sync, fetching snapshots and providing chunks to the
// application. At the close of the operation, Sync will bootstrap the state
// store and persist the commit at that height so that either consensus or
// blocksync can commence. It will then proceed to backfill the necessary amount
// of historical blocks before participating in consensus
func (r *Reactor) Sync(ctx context.Context) (sm.State, error) {
	if r.eventBus != nil {
		if err := r.eventBus.PublishEventStateSyncStatus(types.EventDataStateSyncStatus{
			Complete: false,
			Height:   r.initialHeight,
		}); err != nil {
			return sm.State{}, err
		}
	}

	// We need at least two peers (for cross-referencing of light blocks) before we can
	// begin state sync
	if err := r.waitForEnoughPeers(ctx, minPeers); err != nil {
		return sm.State{}, fmt.Errorf("wait for peers: %w", err)
	}

	// We init syncer early so that it can be used as part of PeerUp and PeerDown logic.
	// State provider initialization can take a few mins, risking loss of these events.
	// We'll need to set r.syncer.stateProvider once it's also initialized

	if err := r.startSyncer(); err != nil {
		return sm.State{}, err
	}
	defer r.syncComplete()

	if err := r.startStateProvider(ctx); err != nil {
		return sm.State{}, err
	}
	r.getSyncer().SetStateProvider(r.stateProvider)

	state, commit, err := r.syncer.SyncAny(ctx, r.cfg.DiscoveryTime, r.cfg.Retries, r.requestSnapshot)
	if err != nil {
		return sm.State{}, fmt.Errorf("sync any: %w", err)
	}

	err = r.publishCommitEvent(commit)
	if err != nil {
		return state, fmt.Errorf("publish commit: %w", err)
	}

	if err := r.stateStore.Bootstrap(state); err != nil {
		return sm.State{}, fmt.Errorf("failed to bootstrap node with new state: %w", err)
	}

	if err := r.blockStore.SaveSeenCommit(commit); err != nil {
		return sm.State{}, fmt.Errorf("failed to store last seen commit: %w", err)
	}

	if err := r.Backfill(ctx, state); err != nil {
		r.logger.Error("backfill failed. Proceeding optimistically...", "error", err)
	}

	if r.eventBus != nil {
		if err := r.eventBus.PublishEventStateSyncStatus(types.EventDataStateSyncStatus{
			Complete: true,
			Height:   state.LastBlockHeight,
		}); err != nil {
			return sm.State{}, fmt.Errorf("publish state sync status event: %w", err)
		}
	}

	if r.postSyncHook != nil {
		if err := r.postSyncHook(ctx, state); err != nil {
			return sm.State{}, fmt.Errorf("post sync: %w", err)
		}
	}

	return state, nil
}

func (r *Reactor) startSyncer() error {
	r.mtx.Lock()
	defer r.mtx.Unlock()

	if r.syncer != nil {
		return errors.New("a state sync is already in progress")
	}
	r.syncer = r.initSyncer()

	return nil
}

func (r *Reactor) startStateProvider(ctx context.Context) error {

	var err error
	for retry := 0; retry < initStateProviderRetries; retry++ {
		initCtx, cancel := context.WithTimeout(ctx, initStateProviderTimeout)
		err = r.initStateProvider(initCtx, r.chainID, r.initialHeight)
		cancel()

		if err == nil { // success
			return nil
		}
		r.logger.Error("failed to init state provider, retrying", "retry", retry, "error", err)
		// let's wait before next attempt
		time.Sleep(time.Second)
	}

	return fmt.Errorf("init state provider: %w", err)
}

func (r *Reactor) syncComplete() {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	// reset syncing objects at the close of Sync
	r.syncer = nil
	r.stateProvider = nil
}

func (r *Reactor) publishCommitEvent(commit *types.Commit) error {
	if r.csState == nil {
		return nil
	}
	return r.csState.PublishCommitEvent(commit)
}

// Backfill sequentially fetches, verifies and stores light blocks in reverse
// order. It does not stop verifying blocks until reaching a block with a height
// and time that is less or equal to the stopHeight and stopTime. The
// trustedBlockID should be of the header at startHeight.
func (r *Reactor) Backfill(ctx context.Context, state sm.State) error {
	params := state.ConsensusParams.Evidence
	stopHeight := state.LastBlockHeight - params.MaxAgeNumBlocks
	stopTime := state.LastBlockTime.Add(-params.MaxAgeDuration)
	// To make tests on mainnet faster, we can use:
	// stopHeight := state.LastBlockHeight - 500
	// stopTime := state.LastBlockTime.Add(-24 * time.Hour)
	// ensure that stop height doesn't go below the initial height
	if stopHeight < state.InitialHeight {
		stopHeight = state.InitialHeight
		// this essentially makes stop time a void criteria for termination
		stopTime = state.LastBlockTime
	}
	return r.backfill(
		ctx,
		state.ChainID,
		state.LastBlockHeight,
		stopHeight,
		state.InitialHeight,
		state.LastBlockID,
		stopTime,
		backfillSleepTime,
		lightBlockResponseTimeout,
	)
}

// peerSeverity says whether withdrawing a peer from a backfill run should also
// disconnect it. Disconnection is not this reactor's alone to spend: it severs the
// connection for consensus, mempool and evidence too, so it is reserved for a peer
// this node can show is faulty rather than merely incompatible with it.
type peerSeverity bool

const (
	// disconnectPeer is for a response that no build of this software could have
	// produced honestly, so nothing is lost by ending the connection.
	disconnectPeer peerSeverity = true

	// penalizePeer is for a response this build cannot use, which is not the same
	// claim: light blocks are refused during decoding by rules that are relative to
	// the build doing the decoding, not fixed by the wire format. Header.ValidateBasic
	// requires Version.Block to equal this build's version.BlockProtocol exactly, a
	// constant that has been bumped several times, and Commit.ValidateBasic refuses
	// any vote-extension type this build has no conversion for, which the type's own
	// rollback note calls out as a consequence of adding one. Either would make an
	// otherwise honest peer on a neighboring release undecodable here, and a rolling
	// upgrade puts such peers on the network by construction. Score the peer down and
	// keep the connection; withdrawing it from the run is what protects the run.
	penalizePeer peerSeverity = false
)

// maxThresholdVoteExtensions bounds the peer-supplied vote-extension list that
// VerifyCommit walks, spending one BLS pairing (~3ms) per entry and never short-
// circuiting. A genuine commit carries one extension per threshold-recoverable
// request - an application-fixed handful - whereas the 10MB LightBlockChannel
// message limit alone would otherwise buy tens of thousands of them, minutes of
// single-threaded CPU, for one response.
const maxThresholdVoteExtensions = 256

// validateThresholdVoteExtensions rejects a peer-supplied extension list that
// exceeds the bound or repeats an entry. Repetition is the sharper of the two:
// the list's multiplicity is not authenticated, so a commit whose genuine
// extensions are duplicated verifies successfully, buying verification work the
// sender is never punished for.
func validateThresholdVoteExtensions(extensions tmproto.VoteExtensions) error {
	if len(extensions) > maxThresholdVoteExtensions {
		return fmt.Errorf("too many threshold vote extensions: %d, at most %d are accepted",
			len(extensions), maxThresholdVoteExtensions)
	}
	seen := make(map[string]struct{}, len(extensions))
	for i, extension := range extensions {
		key := thresholdVoteExtensionKey(extension)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate threshold vote extension at index %d", i)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// thresholdVoteExtensionKey identifies an extension by the fields its signature
// actually covers, so that entries verifying against the same signature collide
// here rather than being treated as distinct.
//
// Only THRESHOLD_RECOVER_RAW carries the request ID into its sign hash; for the
// other types it is dropped during canonicalization. Keying on it unconditionally
// would let one genuine extension be repeated under distinct request IDs, each
// copy still verifying against the single signature it was cloned from.
//
// The signature is deliberately excluded: an entry repeated under a different
// signature fails verification anyway, and is punished there.
func thresholdVoteExtensionKey(extension *tmproto.VoteExtension) string {
	if extension.Type == tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW {
		return fmt.Sprintf("%d|%x|%x", extension.Type, extension.GetSignRequestId(), extension.Extension)
	}
	return fmt.Sprintf("%d|%x", extension.Type, extension.Extension)
}

func (r *Reactor) backfill(
	ctx context.Context,
	chainID string,
	startHeight, stopHeight, initialHeight int64,
	trustedBlockID types.BlockID,
	stopTime time.Time,
	sleepTime time.Duration,
	lightBlockResponseTimeout time.Duration,
) error {
	r.logger.Info("starting backfill process...",
		"startHeight", startHeight,
		"stopHeight", stopHeight,
		"stopTime", stopTime,
		"trustedBlockID", trustedBlockID)

	r.backfillBlockTotal = startHeight - stopHeight + 1
	r.metrics.BackFillBlocksTotal.Set(float64(r.backfillBlockTotal))

	var (
		lastValidatorSet *types.ValidatorSet
		lastChangeHeight = startHeight
	)

	queue := newBlockQueue(startHeight, stopHeight, initialHeight, stopTime, maxLightBlockRequestRetries)

	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	// Peer-dispatch gate for this run: it quarantines peers that supply light
	// blocks this node cannot accept and reports when no peer is left to serve.
	dispatch := newBackfillDispatch(r.peers)
	failIfStalled := func() {
		if reason := dispatch.stalled(); reason != nil {
			queue.fail(reason)
		}
	}

	// withdrawPeer retires a peer that supplied a light block this node refuses, and
	// reschedules the height for somebody else. What a peer sends is its own choice,
	// so none of the work is charged to the retry budget: that budget bounds transient
	// network failure on behalf of every peer, and one of them must not be able to
	// spend the share belonging to honest ones. Fetching runs ahead of the serialized
	// verify loop, so whatever else the peer already supplied is dropped with it.
	//
	// The reason is remembered as the run's failure cause, which is what a run that
	// turns out to have had no other peer able to serve reports instead of a bare
	// retry-budget message.
	//
	// Severity decides only whether the peer is also disconnected; withdrawal from
	// the run happens either way, and that is what keeps the run healthy.
	//
	// It returns the error from reporting the peer; each caller resolves that in its
	// own control flow.
	withdrawPeer := func(peer types.NodeID, height int64, reason error, severity peerSeverity) error {
		dispatch.quarantine(peer, reason)
		queue.discardPeer(peer)
		queue.requeue(height)
		err := r.sendBlockError(ctx, p2p.PeerError{
			NodeID: peer,
			Err:    reason,
			Fatal:  bool(severity),
		})
		failIfStalled()
		return err
	}

	// fetch light blocks across four workers. The aim with deploying concurrent
	// workers is to equate the network messaging time with the verification
	// time. Ideally we want the verification process to never have to be
	// waiting on blocks. If it takes 4s to retrieve a block and 1s to verify
	// it, then steady state involves four workers.
	for i := 0; i < r.cfg.Fetchers; i++ {
		go func() {
			for {
				select {
				case <-ctxWithCancel.Done():
					return
				case height := <-queue.nextHeight():
					// pop the next peer of the list to send a request to
					peer := r.peers.Pop(ctxWithCancel)
					if peer == "" {
						// a peer can be empty only if context is done
						return
					}
					if !dispatch.acquire(peer) {
						// The peer supplied an unverifiable commit earlier in this run.
						// Drop it rather than dispatch to it; no fetch was attempted, so
						// the retry budget shared with honest peers is not charged.
						queue.requeue(height)
						failIfStalled()
						continue
					}

					// stop reports whether the fetcher goroutine should exit.
					stop := func() bool {
						// reusePeer stays false for a peer that answered with nothing or
						// failed: only a peer that served a block is offered again.
						reusePeer := false
						defer func() {
							dispatch.release(peer, reusePeer)
							failIfStalled()
						}()

						r.logger.Debug("fetching next block", "height", height, "peer", peer)
						// request the light block with a timeout
						subCtx, subCtxCancel := context.WithTimeout(ctxWithCancel, lightBlockResponseTimeout)
						lb, err := r.dispatcher.LightBlock(subCtx, height, peer)
						subCtxCancel()

						if errors.Is(err, errMalformedLightBlock) {
							// The peer answered, and what it answered with is not a light
							// block this node can accept. That is the peer's doing, so it is
							// withdrawn rather than retried against: reading it as a slow
							// peer instead would charge the shared budget, hand the peer
							// straight back to the pool and leave the run reporting the
							// network for what one peer chose to send.
							//
							// Withdrawn, but not disconnected: decoding refuses some
							// responses for reasons that hold only against this build, so a
							// peer failing here has not been shown to be faulty.
							r.logger.Info("backfill: received a light block that could not be decoded",
								"height", height, "peer", peer, "error", err)
							if serr := withdrawPeer(peer, height, err, penalizePeer); serr != nil {
								r.logger.Error("backfill: failed to report peer supplying a malformed light block",
									"height", height, "error", serr)
								return true
							}
							return false
						}
						if err != nil {
							queue.retry(height)
							if errors.Is(err, errNoConnectedPeers) {
								r.logger.Info("backfill: no connected peers to fetch light blocks from; sleeping...",
									"sleepTime", sleepTime)
								time.Sleep(sleepTime)
							} else if errors.Is(err, context.DeadlineExceeded) {
								// we don't punish the peer as it might just have not responded in time
								// In future, we might want to consider a backoff strategy
								r.logger.Debug("backfill: peer didn't respond on time",
									"height", height, "peer", peer, "error", err)
								reusePeer = true
							} else {
								r.logger.Info("backfill: error fetching light block",
									"height", height,
									"error", err)
							}
							return false
						}
						if lb == nil {
							r.logger.Info("backfill: peer didn't have block, fetching from another peer", "height", height, "peers_outstanding", r.peers.Len())
							queue.retry(height)
							// As we are fetching blocks backwards, if this node doesn't have the block it likely doesn't
							// have any prior ones, thus we remove it from the peer list.
							return false
						}
						// the peer returned a value, so it is worth using again - unless it
						// is quarantined by the time this fetch releases it
						reusePeer = true

						// run a validate basic. This checks the validator set and commit
						// hashes line up
						err = lb.ValidateBasic(chainID)
						if err != nil || lb.Height != height {
							r.logger.Info("backfill: fetched light block failed validate basic, reporting peer...",
								"height", height,
								"error", err)
							queue.retry(height)
							if serr := r.sendBlockError(ctx, p2p.PeerError{
								NodeID: peer,
								Err:    fmt.Errorf("received invalid light block: %w", err),
							}); serr != nil {
								r.logger.Error("backfill: failed to send block error", "error", serr)
								return true
							}
							return false
						}

						// add block to queue to be verified
						queue.add(lightBlockResponse{
							block: lb,
							peer:  peer,
						})
						r.logger.Debug("backfill: added light block to processing queue", "height", height)
						return false
					}()
					if stop {
						return
					}

				case <-queue.done():
					return
				}
			}
		}()
	}

	// verify all light blocks
	for {
		select {
		case <-ctx.Done():
			queue.close()
			// Not nil: a run that stopped with heights left unfilled must not be
			// reported to the caller as a completed backfill.
			return fmt.Errorf("backfill interrupted: %w", ctx.Err())
		case resp := <-queue.verifyNext():
			// validate the header hash. We take the last block id of the previous
			// header (i.e. one height above) as the trusted hash which we equate to.
			// ValidateBasic has already cross-checked the validator set hash against
			// the header's ValidatorsHash and tied Commit.BlockID.Hash to Header.Hash();
			// it authenticates neither the commit signature nor the vote extensions.
			if w, g := trustedBlockID.Hash, resp.block.Hash(); !bytes.Equal(w, g) {
				r.logger.Info("received invalid light block. header hash doesn't match trusted LastBlockID",
					"trustedHash", w, "receivedHash", g, "height", resp.block.Height)
				if err := r.sendBlockError(ctx, p2p.PeerError{
					NodeID: resp.peer,
					Err:    fmt.Errorf("received invalid light block. Expected hash %v, got: %v", w, g),
				}); err != nil {
					r.logger.Error("backfill: failed to report peer supplying a mismatched header",
						"height", resp.block.Height, "error", err)
					return fmt.Errorf("backfill aborted: failed to report peer supplying a mismatched header: %w", err)
				}
				queue.retry(resp.block.Height)
				continue
			}

			// Authenticate the commit itself: neither the header hash chain nor
			// ValidateBasic covers the threshold signature or the vote extensions. Pass
			// the commit's own BlockID, as light.Client does (light/client.go). Each
			// extension present has its content authenticated; the list's completeness,
			// order and multiplicity are not.
			//
			// Both checks must precede VerifyCommit, which is where the peer-supplied
			// values become expensive or unsafe: the BLS sign-hash path
			// (MustConvertUint8) panics on a QuorumType outside a uint8, and the
			// vote-extension list costs one BLS pairing an entry.
			//
			// The vote-extension bound has no other enforcer, so it fires here for
			// real. The quorum type is already bounded by the ValidatorSet.ValidateBasic
			// that decoding the response runs, which is the gate a peer actually meets;
			// it is repeated here so that anything reaching this loop by another route
			// still cannot carry a value the sign-hash path would panic on.
			var rejectErr error
			if qerr := resp.block.ValidatorSet.QuorumType.Validate(); qerr != nil {
				rejectErr = fmt.Errorf("received light block with invalid commit -- unsupported quorum type: %w", qerr)
			} else if xerr := validateThresholdVoteExtensions(resp.block.Commit.ThresholdVoteExtensions); xerr != nil {
				rejectErr = fmt.Errorf("received light block with invalid commit -- %w", xerr)
			} else if verr := resp.block.ValidatorSet.VerifyCommit(
				chainID, resp.block.Commit.BlockID, resp.block.Height, resp.block.Commit,
			); verr != nil {
				rejectErr = fmt.Errorf("received light block with invalid commit: %w", verr)
			}
			if rejectErr != nil {
				r.logger.Info("backfill: received light block with an unverifiable commit",
					"height", resp.block.Height, "error", rejectErr)
				// Unlike the transient failures above, an unverifiable commit is
				// unambiguous evidence of a bad peer, so the peer is withdrawn and
				// disconnected. Evicting it locally as well matters because the router's
				// own eviction is asynchronous and lags behind the fetch loop.
				if serr := withdrawPeer(resp.peer, resp.block.Height, rejectErr, disconnectPeer); serr != nil {
					r.logger.Error("backfill: failed to report peer supplying an unverifiable commit",
						"height", resp.block.Height, "error", serr)
					return fmt.Errorf("backfill aborted: failed to report peer supplying an unverifiable commit: %w", serr)
				}
				continue
			}

			// save the signed headers
			if err := r.blockStore.SaveSignedHeader(resp.block.SignedHeader, trustedBlockID); err != nil {
				return err
			}

			// check if there has been a change in the validator set
			//
			// ValidatorsHash covers only the threshold public key and the quorum hash,
			// so the member list, proposer index and VotingPowerThreshold persisted here
			// are not authenticated by the header chain.
			if lastValidatorSet != nil && !bytes.Equal(resp.block.Header.ValidatorsHash, resp.block.Header.NextValidatorsHash) {
				// save all the heights that the last validator set was the same
				if err := r.stateStore.SaveValidatorSets(resp.block.Height+1, lastChangeHeight, lastValidatorSet); err != nil {
					return err
				}

				// update the lastChangeHeight
				lastChangeHeight = resp.block.Height
			}

			trustedBlockID = resp.block.LastBlockID
			queue.success()
			r.logger.Info("backfill: verified and stored light block", "height", resp.block.Height)

			lastValidatorSet = resp.block.ValidatorSet

			r.backfilledBlocks++
			r.metrics.BackFilledBlocks.Add(1)

			// The block height might be less than the stopHeight because of the stopTime condition
			// hasn't been fulfilled.
			if resp.block.Height < stopHeight {
				r.backfillBlockTotal++
				r.metrics.BackFillBlocksTotal.Set(float64(r.backfillBlockTotal))
			}

		case <-queue.done():
			if err := queue.error(); err != nil {
				return err
			}

			// save the final batch of validators
			if err := r.stateStore.SaveValidatorSets(queue.terminal.Height, lastChangeHeight, lastValidatorSet); err != nil {
				return err
			}

			r.logger.Info("successfully completed backfill process", "endHeight", queue.terminal.Height)
			return nil
		}
	}
}

// handleSnapshotMessage handles envelopes sent from peers on the
// SnapshotChannel. It returns an error only if the Envelope.Message is unknown
// for this channel. This should never be called outside of handleMessage.
func (r *Reactor) handleSnapshotMessage(ctx context.Context, envelope *p2p.Envelope, snapshotCh p2p.Channel) error {
	logger := r.logger.With("peer", envelope.From)

	switch msg := envelope.Message.(type) {
	case *ssproto.SnapshotsRequest:
		snapshots, err := r.recentSnapshots(ctx, recentSnapshots)
		if err != nil {
			logger.Error("failed to fetch snapshots", "error", err)
			return nil
		}

		for _, snapshot := range snapshots {
			logger.Info(
				"advertising snapshot",
				"height", snapshot.Height,
				"version", snapshot.Version,
				"peer", envelope.From,
			)

			if err := snapshotCh.Send(ctx, p2p.Envelope{
				To: envelope.From,
				Message: &ssproto.SnapshotsResponse{
					Height:   snapshot.Height,
					Version:  snapshot.Version,
					Hash:     snapshot.Hash,
					Metadata: snapshot.Metadata,
				},
			}); err != nil {
				return err
			}
		}

	case *ssproto.SnapshotsResponse:
		syncer := r.getSyncer()
		if syncer == nil {
			logger.Debug("received unexpected snapshot; no state sync in progress")
			return nil
		}

		logger.Info("received snapshot",
			"height", msg.Height,
			"version", msg.Version)
		_, err := syncer.AddSnapshot(envelope.From, &snapshot{
			Height:   msg.Height,
			Version:  msg.Version,
			Hash:     msg.Hash,
			Metadata: msg.Metadata,
		})
		if err != nil {
			logger.Error(
				"failed to add snapshot",
				"height", msg.Height,
				"version", msg.Version,
				"channel", envelope.ChannelID,
				"error", err,
			)
			return nil
		}
		logger.Info("added snapshot",
			"height", msg.Height,
			"version", msg.Version)

	default:
		return fmt.Errorf("handleSnapshotMessage received unknown message: %T", msg)
	}

	return nil
}

// handleChunkMessage handles envelopes sent from peers on the ChunkChannel.
// It returns an error only if the Envelope.Message is unknown for this channel.
// This should never be called outside of handleMessage.
func (r *Reactor) handleChunkMessage(ctx context.Context, envelope *p2p.Envelope, chunkCh p2p.Channel) error {
	switch msg := envelope.Message.(type) {
	case *ssproto.ChunkRequest:
		r.logger.Debug("received chunk request",
			"height", msg.Height,
			"version", msg.Version,
			"chunkID", hex.EncodeToString(msg.ChunkId),
			"peer", envelope.From)
		resp, err := r.conn.LoadSnapshotChunk(ctx, &abci.RequestLoadSnapshotChunk{
			Height:  msg.Height,
			Version: msg.Version,
			ChunkId: msg.ChunkId,
		})
		if err != nil {
			r.logger.Error("failed to load chunk",
				"height", msg.Height,
				"version", msg.Version,
				"chunkID", hex.EncodeToString(msg.ChunkId),
				"peer", envelope.From,
				"error", err)
			return nil
		}

		r.logger.Debug("sending chunk",
			"height", msg.Height,
			"version", msg.Version,
			"chunkID", hex.EncodeToString(msg.ChunkId),
			"peer", envelope.From)
		if err := chunkCh.Send(ctx, p2p.Envelope{
			To: envelope.From,
			Message: &ssproto.ChunkResponse{
				Height:  msg.Height,
				Version: msg.Version,
				ChunkId: msg.ChunkId,
				Chunk:   resp.Chunk,
				Missing: resp.Chunk == nil,
			},
		}); err != nil {
			return err
		}

	case *ssproto.ChunkResponse:
		syncer := r.getSyncer()
		if syncer == nil {
			r.logger.Debug("received unexpected chunk; no state sync in progress", "peer", envelope.From)
			return nil
		}

		r.logger.Debug("received chunk; adding to sync",
			"height", msg.Height,
			"version", msg.Version,
			"chunkID", hex.EncodeToString(msg.ChunkId),
			"chunkLen", len(msg.Chunk),
			"peer", envelope.From)
		_, err := syncer.AddChunk(&chunk{
			Height:  msg.Height,
			Version: msg.Version,
			ID:      msg.ChunkId,
			Chunk:   msg.Chunk,
			Sender:  envelope.From,
		})
		if err != nil {
			r.logger.Error("failed to add chunk",
				"height", msg.Height,
				"version", msg.Version,
				"chunkID", hex.EncodeToString(msg.ChunkId),
				"peer", envelope.From,
				"error", err)
			return nil
		}

	default:
		return fmt.Errorf("handleChunkMessage received unknown message: %T", msg)
	}

	return nil
}

func (r *Reactor) handleLightBlockMessage(ctx context.Context, envelope *p2p.Envelope, blockCh p2p.Channel) error {
	switch msg := envelope.Message.(type) {
	case *ssproto.LightBlockRequest:
		r.logger.Info("received light block request", "height", msg.Height)
		lb, err := r.fetchLightBlock(msg.Height)
		if err != nil {
			r.logger.Error("failed to retrieve light block",
				"height", msg.Height,
				"error", err)
			return err
		}
		if lb == nil {
			if err := blockCh.Send(ctx, p2p.Envelope{
				To: envelope.From,
				Message: &ssproto.LightBlockResponse{
					LightBlock: nil,
				},
			}); err != nil {
				return err
			}
			return nil
		}

		lbproto, err := lb.ToProto()
		if err != nil {
			r.logger.Error("marshaling light block to proto", "error", err)
			return nil
		}

		// NOTE: If we don't have the light block we will send a nil light block
		// back to the requested node, indicating that we don't have it.
		if err := blockCh.Send(ctx, p2p.Envelope{
			To: envelope.From,
			Message: &ssproto.LightBlockResponse{
				LightBlock: lbproto,
			},
		}); err != nil {
			return err
		}
	case *ssproto.LightBlockResponse:
		var height int64
		if msg.LightBlock != nil {
			height = msg.LightBlock.SignedHeader.Header.Height
		}
		r.logger.Info("received light block response", "peer", envelope.From, "height", height)
		if err := r.dispatcher.Respond(ctx, msg.LightBlock, envelope.From); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			r.logger.Error("error processing light block response",
				"height", height,
				"error", err)
		}

	default:
		return fmt.Errorf("handleLightBlockMessage received unknown message: %T", msg)
	}

	return nil
}

func (r *Reactor) handleParamsMessage(ctx context.Context, envelope *p2p.Envelope, paramsCh p2p.Channel) error {
	switch msg := envelope.Message.(type) {
	case *ssproto.ParamsRequest:
		r.logger.Debug("received consensus params request", "height", msg.Height)
		cp, err := r.stateStore.LoadConsensusParams(tmmath.MustConvertInt64(msg.Height))
		if err != nil {
			r.logger.Error("failed to fetch requested consensus params",
				"height", msg.Height,
				"error", err)
			return nil
		}

		cpproto := cp.ToProto()
		if err := paramsCh.Send(ctx, p2p.Envelope{
			To: envelope.From,
			Message: &ssproto.ParamsResponse{
				Height:          msg.Height,
				ConsensusParams: cpproto,
			},
		}); err != nil {
			return err
		}
	case *ssproto.ParamsResponse:
		r.mtx.RLock()
		defer r.mtx.RUnlock()
		r.logger.Debug("received consensus params response", "height", msg.Height)

		cp := types.ConsensusParamsFromProto(msg.ConsensusParams)

		if sp, ok := r.stateProvider.(*stateProviderP2P); ok {
			select {
			case sp.paramsRecvCh <- cp:
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
				return errors.New("failed to send consensus params, stateprovider not ready for response")
			}
		} else {
			r.logger.Debug("received unexpected params response; using RPC state provider", "peer", envelope.From)
		}

	default:
		return fmt.Errorf("handleParamsMessage received unknown message: %T", msg)
	}

	return nil
}

// handleMessage handles an Envelope sent from a peer on a specific p2p Channel.
// It will handle errors and any possible panics gracefully. A caller can handle
// any error returned by sending a PeerError on the respective channel.
func (r *Reactor) handleMessage(ctx context.Context, envelope *p2p.Envelope, chans map[p2p.ChannelID]p2p.Channel) (err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("panic in processing message: %v", e)
			r.logger.Error(
				"recovering from processing message panic",
				"error", err,
				"stack", string(debug.Stack()),
			)
		}
	}()

	//r.logger.Debug("received message", "msg", reflect.TypeOf(envelope.Message), "peer", envelope.From)

	switch envelope.ChannelID {
	case SnapshotChannel:
		err = r.handleSnapshotMessage(ctx, envelope, chans[SnapshotChannel])
	case ChunkChannel:
		err = r.handleChunkMessage(ctx, envelope, chans[ChunkChannel])
	case LightBlockChannel:
		err = r.handleLightBlockMessage(ctx, envelope, chans[LightBlockChannel])
	case ParamsChannel:
		err = r.handleParamsMessage(ctx, envelope, chans[ParamsChannel])
	default:
		err = fmt.Errorf("unknown channel ID (%d) for envelope (%v)", envelope.ChannelID, envelope)
	}

	return err
}

// processCh routes state sync messages to their respective handlers. Any error
// encountered during message execution will result in a PeerError being sent on
// the respective channel. When the reactor is stopped, we will catch the signal
// and close the p2p Channel gracefully.
func (r *Reactor) processChannels(ctx context.Context, chanTable map[p2p.ChannelID]p2p.Channel) {
	// make sure tht the iterator gets cleaned up in case of error
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	chs := make([]p2p.Channel, 0, len(chanTable))
	for key := range chanTable {
		chs = append(chs, chanTable[key])
	}

	iter := p2p.MergedChannelIterator(ctx, chs...)
	for iter.Next(ctx) {
		envelope := iter.Envelope()
		if err := r.handleMessage(ctx, envelope, chanTable); err != nil {
			ch, ok := chanTable[envelope.ChannelID]
			if !ok {
				r.logger.Error("received impossible message",
					"envelope_from", envelope.From,
					"envelope_ch", envelope.ChannelID,
					"num_chs", len(chanTable),
					"error", err,
				)
				return
			}
			r.logger.Error("failed to process message",
				"channel", ch.String(),
				"ch_id", envelope.ChannelID,
				"envelope", envelope,
				"error", err)
			if serr := ch.SendError(ctx, p2p.PeerError{
				NodeID: envelope.From,
				Err:    err,
			}); serr != nil {
				return
			}
		}
	}
}

// processPeerUpdate processes a PeerUpdate, returning an error upon failing to
// handle the PeerUpdate or if a panic is recovered.
func (r *Reactor) processPeerUpdate(ctx context.Context, peerUpdate p2p.PeerUpdate) {
	r.logger.Trace("received peer update", "peer", peerUpdate.NodeID, "status", peerUpdate.Status)

	switch peerUpdate.Status {
	case p2p.PeerStatusUp:
		r.processPeerUp(ctx, peerUpdate)
	case p2p.PeerStatusDown:
		r.processPeerDown(ctx, peerUpdate)
	}

	r.logger.Trace("processed peer update", "peer", peerUpdate.NodeID, "status", peerUpdate.Status)
}

func (r *Reactor) processPeerUp(ctx context.Context, peerUpdate p2p.PeerUpdate) {

	if peerUpdate.Channels.Contains(SnapshotChannel) &&
		peerUpdate.Channels.Contains(ChunkChannel) &&
		peerUpdate.Channels.Contains(LightBlockChannel) &&
		peerUpdate.Channels.Contains(ParamsChannel) {

		r.peers.Append(peerUpdate.NodeID)
	} else {
		r.logger.Warn("could not use peer for statesync", "peer", peerUpdate.NodeID)
	}
	newProvider := NewBlockProvider(peerUpdate.NodeID, r.chainID, r.dispatcher)

	stateProvider := r.getStateProvider()
	if stateProvider != nil {
		if sp, ok := stateProvider.(*stateProviderP2P); ok {
			// we do this in a separate routine to not block whilst waiting for the light client to finish
			// whatever call it's currently executing
			go sp.addProvider(newProvider)
		}
	}

	syncer := r.getSyncer()
	if syncer != nil {
		if err := syncer.AddPeer(ctx, peerUpdate.NodeID); err != nil {
			r.logger.Error("error adding peer to syncer", "error", err)
			return
		}
	}
}

func (r *Reactor) processPeerDown(_ctx context.Context, peerUpdate p2p.PeerUpdate) {
	r.peers.Remove(peerUpdate.NodeID)
	syncer := r.getSyncer()
	if syncer != nil {
		syncer.RemovePeer(peerUpdate.NodeID)
	}
}

// processPeerUpdates initiates a blocking process where we listen for and handle
// PeerUpdate messages. When the reactor is stopped, we will catch the signal and
// close the p2p PeerUpdatesCh gracefully.
func (r *Reactor) processPeerUpdates(ctx context.Context, peerUpdates *p2p.PeerUpdates) {
	for {
		select {
		case <-ctx.Done():
			return
		case peerUpdate := <-peerUpdates.Updates():
			r.processPeerUpdate(ctx, peerUpdate)
		}
	}
}

// recentSnapshots fetches the n most recent snapshots from the app
func (r *Reactor) recentSnapshots(ctx context.Context, n uint32) ([]*snapshot, error) {
	// if we don't have current state, we don't return any snapshots
	if r.csState == nil {
		return []*snapshot{}, nil
	}
	currentHeight := r.csState.GetCurrentHeight()

	resp, err := r.conn.ListSnapshots(ctx, &abci.RequestListSnapshots{})
	if err != nil {
		return nil, err
	}

	sort.Slice(resp.Snapshots, func(i, j int) bool {
		a := resp.Snapshots[i]
		b := resp.Snapshots[j]

		switch {
		case a.Height > b.Height:
			return true
		case a.Height == b.Height && a.Version > b.Version:
			return true
		default:
			return false
		}
	})

	snapshots := make([]*snapshot, 0, n)
	for i, s := range resp.Snapshots {
		if i >= recentSnapshots {
			break
		}

		// we only accept snapshots where next block is already finalized, that is we are voting
		// for `height + 2` or higher, because we need to be able to fetch light block containing
		// commit for `height` from block store (which is stored in block `height+1`)
		if tmmath.MustConvertInt64(s.Height) >= currentHeight-2 {
			r.logger.Debug("snapshot too new, skipping", "height", s.Height, "state_height", currentHeight)
			continue
		}

		snapshots = append(snapshots, &snapshot{
			Height:   s.Height,
			Version:  s.Version,
			Hash:     s.Hash,
			Metadata: s.Metadata,
		})
	}

	return snapshots, nil
}

// fetchLightBlock works out whether the node has a light block at a particular
// height and if so returns it so it can be gossiped to peers
//
// INTENTIONAL(no-read-side-reverification): pre-upgrade backfilled data isn't
// re-verified on read — mainnet data is healthy pre-upgrade, and on-disk tampering
// risk is low relative to key compromise, so read-side hardening is out of scope here.
func (r *Reactor) fetchLightBlock(height uint64) (*types.LightBlock, error) {
	h := tmmath.MustConvertInt64(height)

	blockMeta := r.blockStore.LoadBlockMeta(h)
	if blockMeta == nil {
		return nil, nil
	}

	commit := r.blockStore.LoadBlockCommit(h)
	if commit == nil {
		return nil, nil
	}

	vals, err := r.stateStore.LoadValidators(h, r.blockStore)
	if err != nil {
		return nil, err
	}
	if vals == nil {
		return nil, nil
	}

	return &types.LightBlock{
		SignedHeader: &types.SignedHeader{
			Header: &blockMeta.Header,
			Commit: commit,
		},
		ValidatorSet: vals,
	}, nil
}

func (r *Reactor) waitForEnoughPeers(ctx context.Context, numPeers int) error {
	startAt := time.Now()
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	logT := time.NewTicker(time.Minute)
	defer logT.Stop()
	var iter int
	for r.peers.Len() < numPeers {
		iter++
		select {
		case <-ctx.Done():
			return fmt.Errorf("operation canceled while waiting for peers after %.2fs [%d/%d]",
				time.Since(startAt).Seconds(), r.peers.Len(), numPeers)
		case <-t.C:
			continue
		case <-logT.C:
			r.logger.Info("waiting for sufficient peers to start statesync",
				"duration", time.Since(startAt).String(),
				"target", numPeers,
				"peers", r.peers.Len(),
				"iters", iter,
			)
			continue
		}
	}
	return nil
}

func (r *Reactor) TotalSnapshots() int64 {
	r.mtx.RLock()
	defer r.mtx.RUnlock()

	if r.syncer != nil && r.syncer.snapshots != nil {
		return int64(len(r.syncer.snapshots.snapshots))
	}
	return 0
}

func (r *Reactor) ChunkProcessAvgTime() time.Duration {
	r.mtx.RLock()
	defer r.mtx.RUnlock()

	if r.syncer != nil {
		return time.Duration(r.syncer.avgChunkTime)
	}
	return time.Duration(0)
}

func (r *Reactor) SnapshotHeight() int64 {
	r.mtx.RLock()
	defer r.mtx.RUnlock()

	if r.syncer != nil {
		return r.syncer.lastSyncedSnapshotHeight
	}
	return 0
}

func (r *Reactor) BackFilledBlocks() int64 {
	r.mtx.RLock()
	defer r.mtx.RUnlock()

	return r.backfilledBlocks
}

func (r *Reactor) BackFillBlocksTotal() int64 {
	r.mtx.RLock()
	defer r.mtx.RUnlock()

	return r.backfillBlockTotal
}

func (r *Reactor) getSyncer() *syncer {
	r.mtx.RLock()
	defer r.mtx.RUnlock()
	return r.syncer
}

func (r *Reactor) getStateProvider() StateProvider {
	r.mtx.RLock()
	defer r.mtx.RUnlock()
	return r.stateProvider
}

func (r *Reactor) setStateProvider(sp StateProvider) {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	r.stateProvider = sp
}
