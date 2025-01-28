package statesync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/proxy"
	sm "github.com/dashpay/tenderdash/internal/state"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	tmmath "github.com/dashpay/tenderdash/libs/math"
	"github.com/dashpay/tenderdash/light"
	ssproto "github.com/dashpay/tenderdash/proto/tendermint/statesync"
	"github.com/dashpay/tenderdash/types"
)

const (
	// chunkTimeout is the timeout while waiting for the next chunk from the chunk queue.
	chunkTimeout = 2 * time.Minute

	// minimumDiscoveryTime is the lowest allowable time for a
	// SyncAny discovery time.
	minimumDiscoveryTime = 5 * time.Second

	dequeueChunkIDTimeoutDefault = 2 * time.Second
)

var (
	// errAbort is returned by Sync() when snapshot restoration is aborted.
	errAbort = errors.New("state sync aborted")
	// errRetrySnapshot is returned by Sync() when the snapshot should be retried.
	errRetrySnapshot = errors.New("retry snapshot")
	// errRejectSnapshot is returned by Sync() when the snapshot is rejected.
	errRejectSnapshot = errors.New("snapshot was rejected")
	// errRejectFormat is returned by Sync() when the snapshot format is rejected.
	errRejectFormat = errors.New("snapshot version was rejected")
	// errRejectSender is returned by Sync() when the snapshot sender is rejected.
	errRejectSender = errors.New("snapshot sender was rejected")
	// errVerifyFailed is returned by Sync() when app hash or last height
	// verification fails.
	errVerifyFailed = errors.New("verification with app failed")
	// errTimeout is returned by Sync() when we've waited too long to receive a chunk.
	errTimeout = errors.New("timed out waiting for chunk")
	// errNoSnapshots is returned by SyncAny() if no snapshots are found and discovery is disabled.
	errNoSnapshots            = errors.New("no suitable snapshots found")
	errStatesyncNotInProgress = errors.New("no state sync in progress")
)

// syncer runs a state sync against an ABCI app. Use either SyncAny() to automatically attempt to
// sync all snapshots in the pool (pausing to discover new ones), or Sync() to sync a specific
// snapshot. Snapshots and chunks are fed via AddSnapshot() and AddChunk() as appropriate.
type syncer struct {
	logger        log.Logger
	stateProvider StateProvider
	conn          abciclient.Client
	snapshots     *snapshotPool
	snapshotCh    p2p.Channel
	chunkCh       p2p.Channel
	tempDir       string
	fetchers      int
	retryTimeout  time.Duration

	dequeueChunkIDTimeout time.Duration

	mtx        sync.RWMutex
	chunkQueue *chunkQueue
	metrics    *Metrics

	avgChunkTime             int64
	lastSyncedSnapshotHeight int64
	processingSnapshot       *snapshot
}

// AddChunk adds a chunk to the chunk queue, if any. It returns false if the chunk has already
// been added to the queue, or an error if there's no sync in progress.
func (s *syncer) AddChunk(chunk *chunk) (bool, error) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	if s.chunkQueue == nil {
		return false, errStatesyncNotInProgress
	}
	keyVals := []any{
		"height", chunk.Height,
		"version", chunk.Version,
		"chunk", chunk.ID,
	}
	added, err := s.chunkQueue.Add(chunk)
	if err != nil {
		if errors.Is(err, errNilSnapshot) {
			s.logger.Error("Can't add a chunk because of a snapshot is nil", keyVals...)
			return false, nil
		}
		return false, err
	}
	if added {
		s.logger.Debug("Added chunk to queue", keyVals...)
	} else {
		s.logger.Debug("Ignoring duplicate chunk in requestQueue", keyVals...)
	}
	return added, nil
}

// AddSnapshot adds a snapshot to the snapshot pool. It returns true if a new, previously unseen
// snapshot was accepted and added.
func (s *syncer) AddSnapshot(peerID types.NodeID, snapshot *snapshot) (bool, error) {
	added, err := s.snapshots.Add(peerID, snapshot)
	if err != nil {
		return false, err
	}
	if added {
		s.metrics.TotalSnapshots.Add(1)
		s.logger.Info("Discovered new snapshot",
			"height", snapshot.Height,
			"version", snapshot.Version,
			"hash", snapshot.Hash.ShortString())
	} else {
		s.logger.Debug("snapshot not added", "height", snapshot.Height, "hash", snapshot.Hash)
	}
	return added, nil
}

// AddPeer adds a peer to the pool. For now we just keep it simple and send a
// single request to discover snapshots, later we may want to do retries and stuff.
func (s *syncer) AddPeer(ctx context.Context, peerID types.NodeID) error {
	s.logger.Debug("Requesting snapshots from peer", "peer", peerID)

	return s.snapshotCh.Send(ctx, p2p.Envelope{
		To:      peerID,
		Message: &ssproto.SnapshotsRequest{},
	})
}

// RemovePeer removes a peer from the pool.
func (s *syncer) RemovePeer(peerID types.NodeID) {
	s.logger.Debug("Removing peer from sync", "peer", peerID)
	s.snapshots.RemovePeer(peerID)
}

// SyncAny tries to sync any of the snapshots in the snapshot pool, waiting to discover further
// snapshots if none were found and discoveryTime > 0. It returns the latest state and block commit
// which the caller must use to bootstrap the node.
func (s *syncer) SyncAny(
	ctx context.Context,
	discoveryTime time.Duration,
	retries int,
	requestSnapshots func() error,
) (sm.State, *types.Commit, error) {
	if discoveryTime != 0 && discoveryTime < minimumDiscoveryTime {
		discoveryTime = minimumDiscoveryTime
	}

	timer := time.NewTimer(discoveryTime)
	defer timer.Stop()

	// The app may ask us to retry a snapshot restoration, in which case we need to reuse
	// the snapshot and chunk queue from the previous loop iteration.
	var (
		snapshot *snapshot
		queue    *chunkQueue
		err      error
		iters    int
	)

	for {
		if retries > 0 && snapshot == nil && iters > retries {
			return sm.State{}, nil, errNoSnapshots
		}

		iters++
		// If not nil, we're going to retry restoration of the same snapshot.
		if snapshot == nil {
			snapshot = s.snapshots.Best()
			queue = nil
		}
		if snapshot == nil {
			if discoveryTime == 0 {
				return sm.State{}, nil, errNoSnapshots
			}
			// we re-request snapshots
			if err := requestSnapshots(); err != nil {
				return sm.State{}, nil, err
			}
			s.logger.Info("discovering snapshots",
				"iterations", iters,
				"interval", discoveryTime)
			timer.Reset(discoveryTime)
			select {
			case <-ctx.Done():
				return sm.State{}, nil, ctx.Err()
			case <-timer.C:
				continue
			}
		}
		if queue == nil {
			queue, err = newChunkQueue(snapshot, s.tempDir, s.fetchers)
			if err != nil {
				return sm.State{}, nil, fmt.Errorf("failed to create chunk queue: %w", err)
			}
			defer queue.Close() // in case we forget to close it elsewhere
		}

		queue.Enqueue(snapshot.Hash)
		s.processingSnapshot = snapshot

		newState, commit, err := s.Sync(ctx, snapshot, queue)
		switch {
		case err == nil:
			s.metrics.SnapshotHeight.Set(float64(snapshot.Height))
			s.lastSyncedSnapshotHeight = tmmath.MustConvertInt64(snapshot.Height)
			return newState, commit, nil

		case errors.Is(err, errAbort):
			return sm.State{}, nil, err

		case errors.Is(err, errRetrySnapshot):
			queue.RetryAll()
			s.logger.Info("Retrying snapshot",
				"height", snapshot.Height,
				"version", snapshot.Version,
				"hash", snapshot.Hash)
			continue

		case errors.Is(err, errTimeout):
			s.snapshots.Reject(snapshot)
			s.logger.Error("Timed out waiting for snapshot chunks, rejected snapshot",
				"height", snapshot.Height,
				"version", snapshot.Version,
				"hash", snapshot.Hash)

		case errors.Is(err, errRejectSnapshot):
			s.snapshots.Reject(snapshot)
			s.logger.Info("Snapshot rejected",
				"height", snapshot.Height,
				"version", snapshot.Version,
				"hash", snapshot.Hash)

		case errors.Is(err, errRejectFormat):
			s.snapshots.RejectVersion(snapshot.Version)
			s.logger.Info("Snapshot version rejected", "version", snapshot.Version)

		case errors.Is(err, errRejectSender):
			s.logger.Info("Snapshot senders rejected",
				"height", snapshot.Height,
				"version", snapshot.Version,
				"hash", snapshot.Hash)
			for _, peer := range s.snapshots.GetPeers(snapshot) {
				s.snapshots.RejectPeer(peer)
				s.logger.Info("Snapshot sender rejected", "peer", peer)
			}

		default:
			return sm.State{}, nil, fmt.Errorf("snapshot restoration failed: %w", err)
		}

		// Discard snapshot and chunks for next iteration
		err = queue.Close()
		if err != nil {
			s.logger.Error("Failed to clean up chunk queue", "err", err)
		}
		snapshot = nil
		queue = nil
		s.processingSnapshot = nil
	}
}

// Sync executes a sync for a specific snapshot, returning the latest state and block commit which
// the caller must use to bootstrap the node.
func (s *syncer) Sync(ctx context.Context, snapshot *snapshot, queue *chunkQueue) (sm.State, *types.Commit, error) {
	s.mtx.Lock()
	if s.chunkQueue != nil {
		s.mtx.Unlock()
		return sm.State{}, nil, errors.New("a state sync is already in progress")
	}
	s.chunkQueue = queue
	s.mtx.Unlock()
	defer func() {
		s.mtx.Lock()
		s.chunkQueue = nil
		s.mtx.Unlock()
	}()

	hctx, hcancel := context.WithTimeout(ctx, 30*time.Second)
	defer hcancel()

	// Fetch the app hash corresponding to the snapshot
	appHash, err := s.getStateProvider().AppHash(hctx, snapshot.Height)
	if err != nil {
		// check if the main context was triggered
		if ctx.Err() != nil {
			return sm.State{}, nil, ctx.Err()
		}
		// catch the case where all the light client providers have been exhausted
		if err == light.ErrNoWitnesses {
			return sm.State{}, nil,
				fmt.Errorf("failed to get app hash at height %d. No witnesses remaining", snapshot.Height)
		}
		s.logger.Info("failed to get and verify tendermint state. Dropping snapshot and trying again",
			"error", err,
			"height", snapshot.Height)
		return sm.State{}, nil, errRejectSnapshot
	}
	snapshot.trustedAppHash = appHash

	// Offer snapshot to ABCI app.
	err = s.offerSnapshot(ctx, snapshot)
	if err != nil {
		s.logger.Error("Snapshot wasn't accepted",
			"height", snapshot.Height,
			"version", snapshot.Version,
			"hash", snapshot.Hash,
			"error", err)
		return sm.State{}, nil, err
	}

	// Spawn chunk fetchers. They will terminate when the chunk queue is closed or context canceled.
	fetchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	fetchStartTime := time.Now()

	// TODO: this approach of creating will be deprecated in favor of new design
	// This epic https://dashpay.atlassian.net/browse/TD-161 contains all the tasks for refactoring
	for i := 0; i < s.fetchers; i++ {
		go s.fetchChunks(fetchCtx, snapshot, queue)
	}

	pctx, pcancel := context.WithTimeout(ctx, 1*time.Minute)
	defer pcancel()

	// Optimistically build new state, so we don't discover any light client failures at the end.
	state, err := s.getStateProvider().State(pctx, snapshot.Height)
	if err != nil {
		// check if the main context was triggered
		if ctx.Err() != nil {
			return sm.State{}, nil, ctx.Err()
		}
		if errors.Is(err, light.ErrNoWitnesses) {
			return sm.State{}, nil,
				fmt.Errorf("failed to get tendermint state at height %d. No witnesses remaining", snapshot.Height)
		}
		s.logger.Info("failed to get and verify tendermint state. Dropping snapshot and trying again",
			"err", err, "height", snapshot.Height)
		return sm.State{}, nil, errRejectSnapshot
	}
	commit, err := s.getStateProvider().Commit(pctx, snapshot.Height)
	if err != nil {
		// check if the provider context exceeded the 10 second deadline
		if ctx.Err() != nil {
			return sm.State{}, nil, ctx.Err()
		}
		if err == light.ErrNoWitnesses {
			return sm.State{}, nil,
				fmt.Errorf("failed to get commit at height %d. No witnesses remaining", snapshot.Height)
		}
		s.logger.Error("failed to get and verify light block. Dropping snapshot and trying again",
			"err", err, "height", snapshot.Height)
		return sm.State{}, nil, errRejectSnapshot
	}

	// Restore snapshot
	err = s.applyChunks(ctx, queue, fetchStartTime)
	if err != nil {
		return sm.State{}, nil, err
	}

	// Verify app and app version
	if err := s.verifyApp(ctx, snapshot, state.Version.Consensus.App); err != nil {
		return sm.State{}, nil, err
	}

	// Done! 🎉
	s.logger.Info("Snapshot restored",
		"height", snapshot.Height,
		"version", snapshot.Version,
		"hash", snapshot.Hash)

	return state, commit, nil
}

// offerSnapshot offers a snapshot to the app. It returns various errors depending on the app's
// response, or nil if the snapshot was accepted.
func (s *syncer) offerSnapshot(ctx context.Context, snapshot *snapshot) error { //nolint:dupl
	s.logger.Info("Offering snapshot to ABCI app",
		"height", snapshot.Height,
		"version", snapshot.Version,
		"hash", snapshot.Hash)
	resp, err := s.conn.OfferSnapshot(ctx, &abci.RequestOfferSnapshot{
		Snapshot: &abci.Snapshot{
			Height:   snapshot.Height,
			Version:  snapshot.Version,
			Hash:     snapshot.Hash,
			Metadata: snapshot.Metadata,
		},
		AppHash: snapshot.trustedAppHash,
	})
	if err != nil {
		return fmt.Errorf("failed to offer snapshot: %w", err)
	}
	switch resp.Result {
	case abci.ResponseOfferSnapshot_ACCEPT:
		s.logger.Info("Snapshot accepted, restoring",
			"height", snapshot.Height,
			"version", snapshot.Version,
			"hash", snapshot.Hash)
		return nil
	case abci.ResponseOfferSnapshot_ABORT:
		return errAbort
	case abci.ResponseOfferSnapshot_REJECT:
		return errRejectSnapshot
	case abci.ResponseOfferSnapshot_REJECT_FORMAT:
		return errRejectFormat
	case abci.ResponseOfferSnapshot_REJECT_SENDER:
		return errRejectSender
	default:
		return fmt.Errorf("unknown ResponseOfferSnapshot result %v", resp.Result)
	}
}

// applyChunks applies chunks to the app. It returns various errors depending on the app's
// response, or nil once the snapshot is fully restored.
func (s *syncer) applyChunks(ctx context.Context, queue *chunkQueue, start time.Time) error {
	for {
		chunk, err := queue.Next()
		if err != nil {
			return fmt.Errorf("failed to fetch chunk: %w", err)
		}

		resp, err := s.conn.ApplySnapshotChunk(ctx, &abci.RequestApplySnapshotChunk{
			ChunkId: chunk.ID,
			Chunk:   chunk.Chunk,
			Sender:  string(chunk.Sender),
		})
		if err != nil {
			return fmt.Errorf("failed to apply chunkID %x: %w", chunk.ID, err)
		}
		s.logger.Info("applied snapshot chunk to ABCI app",
			"height", chunk.Height,
			"version", chunk.Version,
			"chunkID", chunk.ID.String())

		// Discard and refetch any chunks as requested by the app
		for _, chunkID := range resp.RefetchChunks {
			err := queue.Discard(chunkID)
			if err != nil {
				return fmt.Errorf("failed to discard chunkID %x: %w", chunkID, err)
			}
			queue.Enqueue(chunkID)
		}

		// Reject any senders as requested by the app
		for _, sender := range resp.RejectSenders {
			if sender != "" {
				peerID := types.NodeID(sender)
				s.snapshots.RejectPeer(peerID)

				if err := queue.DiscardSender(peerID); err != nil {
					return fmt.Errorf("failed to reject sender: %w", err)
				}
			}
		}

		s.logger.Debug("snapshot chunk applied",
			"result", resp.Result.String(),
			"chunkID", chunk.ID.String())

		switch resp.Result {
		case abci.ResponseApplySnapshotChunk_ACCEPT:
			queue.Enqueue(resp.NextChunks...)
			s.acceptChunk(queue, start)
		case abci.ResponseApplySnapshotChunk_COMPLETE_SNAPSHOT:
			s.acceptChunk(queue, start)
			return nil
		case abci.ResponseApplySnapshotChunk_ABORT:
			return errAbort
		case abci.ResponseApplySnapshotChunk_RETRY:
			queue.Retry(chunk.ID)
		case abci.ResponseApplySnapshotChunk_RETRY_SNAPSHOT:
			return errRetrySnapshot
		case abci.ResponseApplySnapshotChunk_REJECT_SNAPSHOT:
			return errRejectSnapshot
		default:
			return fmt.Errorf("unknown ResponseApplySnapshotChunk result %v", resp.Result)
		}
	}
}

func (s *syncer) acceptChunk(queue *chunkQueue, start time.Time) {
	s.metrics.SnapshotChunk.Add(1)
	s.avgChunkTime = time.Since(start).Nanoseconds() / int64(queue.DoneChunksCount())
	s.metrics.ChunkProcessAvgTime.Set(float64(s.avgChunkTime))
}

// fetchChunks requests chunks from peers, receiving allocations from the chunk queue. Chunks
// will be received from the reactor via syncer.AddChunks() to queue.Add().
func (s *syncer) fetchChunks(ctx context.Context, snapshot *snapshot, queue *chunkQueue) {
	ticker := time.NewTicker(s.retryTimeout)
	defer ticker.Stop()
	dequeueChunkIDTimeout := s.dequeueChunkIDTimeout
	if dequeueChunkIDTimeout == 0 {
		dequeueChunkIDTimeout = dequeueChunkIDTimeoutDefault
	}
	for {
		if queue.IsRequestQueueEmpty() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(dequeueChunkIDTimeout):
				continue
			}
		}
		ID, err := queue.Dequeue()
		if errors.Is(err, errQueueEmpty) {
			continue
		}
		s.logger.Info("Fetching snapshot chunk",
			"height", snapshot.Height,
			"version", snapshot.Version,
			"chunk", ID)
		ticker.Reset(s.retryTimeout)
		if err := s.requestChunk(ctx, snapshot, ID); err != nil {
			return
		}
		select {
		case <-queue.WaitFor(ID):
			// do nothing
		case <-ticker.C:
			s.chunkQueue.Enqueue(ID)
		case <-ctx.Done():
			return
		}
	}
}

// requestChunk requests a chunk from a peer.
//
// returns nil if there are no peers for the given snapshot or the
// request is successfully made and an error if the request cannot be
// completed
func (s *syncer) requestChunk(ctx context.Context, snapshot *snapshot, chunkID tmbytes.HexBytes) error {
	peer := s.snapshots.GetPeer(snapshot)
	if peer == "" {
		s.logger.Error("No valid peers found for snapshot",
			"height", snapshot.Height,
			"version", snapshot.Version,
			"hash", snapshot.Hash)
		return nil
	}

	s.logger.Debug("Requesting snapshot chunk",
		"height", snapshot.Height,
		"version", snapshot.Version,
		"chunkID", chunkID.String(),
		"peer", peer)

	msg := p2p.Envelope{
		To: peer,
		Message: &ssproto.ChunkRequest{
			Height:  snapshot.Height,
			Version: snapshot.Version,
			ChunkId: chunkID,
		},
	}

	return s.chunkCh.Send(ctx, msg)
}

// verifyApp verifies the sync, checking the app hash, last block height and app version
func (s *syncer) verifyApp(ctx context.Context, snapshot *snapshot, appVersion uint64) error {
	resp, err := s.conn.Info(ctx, &proxy.RequestInfo)
	if err != nil {
		return fmt.Errorf("failed to query ABCI app for appHash: %w", err)
	}

	// sanity check that the app version in the block matches the application's own record
	// of its version
	if resp.AppVersion != appVersion {
		// An error here most likely means that the app hasn't implemented state sync
		// or the Info call correctly
		return fmt.Errorf("app version mismatch. Expected: %d, got: %d",
			appVersion, resp.AppVersion)
	}

	if !bytes.Equal(snapshot.trustedAppHash, resp.LastBlockAppHash) {
		s.logger.Error("appHash verification failed",
			"expected", snapshot.trustedAppHash,
			"actual", tmbytes.HexBytes(resp.LastBlockAppHash))
		return errVerifyFailed
	}

	if uint64(resp.LastBlockHeight) != snapshot.Height {
		s.logger.Error(
			"ABCI app reported unexpected last block height",
			"expected", snapshot.Height,
			"actual", resp.LastBlockHeight,
		)
		return errVerifyFailed
	}

	s.logger.Info("Verified ABCI app", "height", snapshot.Height, "appHash", snapshot.trustedAppHash)
	return nil
}

func (s *syncer) getStateProvider() StateProvider {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	return s.stateProvider
}

func (s *syncer) SetStateProvider(sp StateProvider) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	s.stateProvider = sp
}
