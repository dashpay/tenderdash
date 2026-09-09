package blocksync

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"

	"github.com/cosmos/gogoproto/proto"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	bcproto "github.com/dashpay/tenderdash/proto/tendermint/blocksync"
	"github.com/dashpay/tenderdash/types"
)

// maxPlausiblePeerHeight bounds a peer-reported height so that downstream
// height arithmetic (MaxHeight()+1, target counts) cannot overflow.
const maxPlausiblePeerHeight = int64(1) << 60

const (
	maxInFlightBlockResponses = 2
	maxQueuedBlockResponses   = 128
)

var errStoredBlockMissingCommit = errors.New("stored block has no commit")

type (
	response func(ctx context.Context, msg proto.Message) error

	blockResponseJob struct {
		ctx          context.Context
		envelope     p2p.Envelope
		resp         response
		deliveryResp response
	}

	blockP2PMessageHandler struct {
		logger     log.Logger
		store      sm.BlockStore
		peerAdder  PeerAdder
		ctx        context.Context
		cancel     context.CancelFunc
		workers    sync.WaitGroup
		deliveries sync.WaitGroup

		responseMtx     sync.Mutex
		responseQueues  map[types.NodeID][]blockResponseJob
		activePeers     map[types.NodeID]bool
		readyPeers      []types.NodeID
		responseWake    chan struct{}
		queuedResponses int
		activeResponses int
		peerConnections map[types.NodeID]peerConnectionState
		// PeerManager emits globally ordered, monotonically increasing connection IDs.
		lastConnID uint64
	}

	peerConnectionState struct {
		connID uint64
		live   bool
	}
)

func consumerHandler(
	ctx context.Context,
	logger log.Logger,
	store sm.BlockStore,
	peerAdder PeerAdder,
) (client.ConsumerParams, *blockP2PMessageHandler) {
	handler := newBlockP2PMessageHandler(ctx, logger, store, peerAdder)
	return client.ConsumerParams{
		ReadChannels: []p2p.ChannelID{p2p.BlockSyncChannel},
		Handler: client.HandlerWithMiddlewares(
			handler,
			client.WithValidateMessageHandler([]p2p.ChannelID{p2p.BlockSyncChannel}),
			client.WithErrorLoggerMiddleware(logger),
			client.WithRecoveryMiddleware(logger),
		),
	}, handler
}

func newBlockP2PMessageHandler(
	ctx context.Context,
	logger log.Logger,
	store sm.BlockStore,
	peerAdder PeerAdder,
) *blockP2PMessageHandler {
	workerCtx, cancel := context.WithCancel(ctx)
	h := &blockP2PMessageHandler{
		logger:          logger,
		store:           store,
		peerAdder:       peerAdder,
		ctx:             workerCtx,
		cancel:          cancel,
		responseQueues:  make(map[types.NodeID][]blockResponseJob),
		activePeers:     make(map[types.NodeID]bool),
		responseWake:    make(chan struct{}, 1),
		peerConnections: make(map[types.NodeID]peerConnectionState),
	}
	for range maxInFlightBlockResponses {
		h.workers.Add(1)
		go func() {
			defer h.workers.Done()
			h.runBlockResponseWorker(workerCtx)
		}()
	}
	return h
}

func (h *blockP2PMessageHandler) stop() {
	h.cancel()
	h.workers.Wait()
	h.deliveries.Wait()
}

// Handle handles a message from a block-sync message set
func (h *blockP2PMessageHandler) Handle(ctx context.Context, p2pClient *client.Client, envelope *p2p.Envelope) error {
	resp := client.ResponseFuncFromEnvelope(p2pClient, envelope)
	switch msg := envelope.Message.(type) {
	case *bcproto.BlockRequest:
		h.enqueueBlockResponse(blockResponseJob{
			ctx:          h.ctx,
			envelope:     *envelope,
			resp:         resp,
			deliveryResp: client.DeliveryResponseFuncFromEnvelope(p2pClient, envelope),
		})
		return nil
	case *bcproto.StatusRequest:
		return resp(ctx, &bcproto.StatusResponse{
			Height: h.store.Height(),
			Base:   h.store.Base(),
		})
	case *bcproto.StatusResponse:
		if msg.Base < 0 || msg.Height < msg.Base || msg.Height > maxPlausiblePeerHeight {
			h.logger.Debug("ignoring implausible peer status",
				"peer", envelope.From,
				"base", msg.Base,
				"height", msg.Height)
			return nil
		}
		h.peerAdder.AddPeer(newPeerData(envelope.From, msg.Base, msg.Height))
	case *bcproto.NoBlockResponse:
		h.logger.Debug("peer does not have the requested block",
			"peer", envelope.From,
			"height", msg.Height)
	default:
		return fmt.Errorf("received unknown message: %T", msg)
	}
	return nil
}

func (h *blockP2PMessageHandler) prepareBlockResponse(job blockResponseJob) (proto.Message, bool, error) {
	envelope := &job.envelope
	msg := envelope.Message.(*bcproto.BlockRequest)
	block := h.store.LoadBlock(msg.Height)
	if !h.connectionIsCurrent(envelope.From, envelope.ConnID) {
		return nil, false, nil
	}
	if block == nil {
		h.logger.Debug("peer requesting a block we do not have", "peer", envelope.From, "height", msg.Height)
		return &bcproto.NoBlockResponse{Height: msg.Height}, false, nil
	}
	commit := h.store.LoadSeenCommitAt(msg.Height)
	if commit == nil {
		return nil, false, fmt.Errorf("%w at height %d", errStoredBlockMissingCommit, msg.Height)
	}
	blockProto, err := block.ToProto()
	if err != nil {
		return nil, false, fmt.Errorf("failed to convert block to protobuf: %w", err)
	}
	return &bcproto.BlockResponse{
		Block:  blockProto,
		Commit: commit.ToProto(),
	}, true, nil
}

func (h *blockP2PMessageHandler) enqueueBlockResponse(job blockResponseJob) {
	peerID := job.envelope.From
	h.responseMtx.Lock()
	if job.envelope.ConnID == 0 || (job.envelope.ConnID <= h.lastConnID &&
		!h.connectionIsCurrentLocked(peerID, job.envelope.ConnID)) {
		h.responseMtx.Unlock()
		h.logger.Debug("dropping block request without a current or pending peer connection", "peer", peerID)
		return
	}
	peerOutstanding := len(h.responseQueues[peerID])
	if h.activePeers[peerID] {
		peerOutstanding++
	}
	if h.queuedResponses >= maxQueuedBlockResponses && peerOutstanding == 0 {
		h.evictExcessQueuedResponseLocked()
	}
	if peerOutstanding >= maxPendingRequestsPerPeer || h.queuedResponses >= maxQueuedBlockResponses {
		h.responseMtx.Unlock()
		h.logger.Debug("dropping block request because the response queue is full", "peer", peerID)
		return
	}
	h.responseQueues[peerID] = append(h.responseQueues[peerID], job)
	h.queuedResponses++
	sort.SliceStable(h.responseQueues[peerID], func(i, j int) bool {
		return h.responseQueues[peerID][i].envelope.ConnID < h.responseQueues[peerID][j].envelope.ConnID
	})
	h.schedulePeerLocked(peerID)
	h.responseMtx.Unlock()
}

func (h *blockP2PMessageHandler) runBlockResponseWorker(ctx context.Context) {
	for {
		peerID, job, ok := h.nextBlockResponse()
		if ok {
			if job.ctx.Err() == nil && h.connectionIsCurrent(peerID, job.envelope.ConnID) {
				message, waitForDelivery, err := h.prepareBlockResponse(job)
				if err != nil {
					h.logBlockResponseError(peerID, err)
				} else if message != nil && waitForDelivery && job.ctx.Err() == nil {
					h.deliveries.Add(1)
					go h.deliverBlockResponse(peerID, job, message)
					continue
				} else if message != nil {
					h.logBlockResponseError(peerID, job.resp(job.ctx, message))
				}
			}
			h.finishBlockResponse(peerID)
			continue
		}
		select {
		case <-h.responseWake:
		case <-ctx.Done():
			return
		}
	}
}

func (h *blockP2PMessageHandler) deliverBlockResponse(
	peerID types.NodeID,
	job blockResponseJob,
	message proto.Message,
) {
	defer h.deliveries.Done()
	defer h.finishBlockResponse(peerID)
	h.logBlockResponseError(peerID, job.deliveryResp(job.ctx, message))
}

func (h *blockP2PMessageHandler) logBlockResponseError(peerID types.NodeID, err error) {
	if err == nil {
		return
	}
	switch {
	case errors.Is(err, errStoredBlockMissingCommit):
		h.logger.Error("cannot serve block because its commit is missing", "peer", peerID, "err", err)
	case errors.Is(err, client.ErrDeliveryStalled):
		h.logger.Warn("stopped serving a peer after outbound delivery stalled", "peer", peerID, "err", err)
	default:
		h.logger.Debug("failed to serve block request", "peer", peerID, "err", err)
	}
}

func (h *blockP2PMessageHandler) nextBlockResponse() (types.NodeID, blockResponseJob, bool) {
	h.responseMtx.Lock()
	defer h.responseMtx.Unlock()
	for len(h.readyPeers) > 0 {
		peerID := h.readyPeers[0]
		h.readyPeers = h.readyPeers[1:]
		queue := h.responseQueues[peerID]
		if len(queue) == 0 || h.activePeers[peerID] || !h.connectionIsCurrentLocked(peerID, queue[0].envelope.ConnID) {
			continue
		}
		job := queue[0]
		h.responseQueues[peerID] = queue[1:]
		h.queuedResponses--
		h.activePeers[peerID] = true
		h.activeResponses++
		if len(h.readyPeers) > 0 {
			h.wakeBlockResponseWorkerLocked()
		}
		return peerID, job, true
	}
	return "", blockResponseJob{}, false
}

func (h *blockP2PMessageHandler) finishBlockResponse(peerID types.NodeID) {
	h.responseMtx.Lock()
	defer h.responseMtx.Unlock()
	h.activePeers[peerID] = false
	h.activeResponses--
	if len(h.responseQueues[peerID]) == 0 {
		delete(h.responseQueues, peerID)
		delete(h.activePeers, peerID)
	} else {
		h.schedulePeerLocked(peerID)
	}
}

func (h *blockP2PMessageHandler) blockResponsesIdle() bool {
	h.responseMtx.Lock()
	defer h.responseMtx.Unlock()
	return h.queuedResponses == 0 && h.activeResponses == 0
}

func (h *blockP2PMessageHandler) handlePeerUpdate(update p2p.PeerUpdate) {
	h.responseMtx.Lock()
	defer h.responseMtx.Unlock()

	switch update.Status {
	case p2p.PeerStatusUp:
		if update.ConnID == 0 {
			return
		}
		h.lastConnID = max(h.lastConnID, update.ConnID)
		h.purgeQueuedResponsesBeforeLocked(update.NodeID, update.ConnID)
		h.peerConnections[update.NodeID] = peerConnectionState{connID: update.ConnID, live: true}
		h.schedulePeerLocked(update.NodeID)
	case p2p.PeerStatusDown:
		state := h.peerConnections[update.NodeID]
		h.purgeQueuedResponsesBeforeLocked(update.NodeID, state.connID+1)
		delete(h.peerConnections, update.NodeID)
	}
}

func (h *blockP2PMessageHandler) connectionIsCurrent(peerID types.NodeID, connID uint64) bool {
	h.responseMtx.Lock()
	defer h.responseMtx.Unlock()
	return h.connectionIsCurrentLocked(peerID, connID)
}

func (h *blockP2PMessageHandler) connectionIsCurrentLocked(peerID types.NodeID, connID uint64) bool {
	if connID == 0 {
		return false
	}
	state, known := h.peerConnections[peerID]
	if !known {
		return false
	}
	return state.live && state.connID == connID
}

func (h *blockP2PMessageHandler) purgeQueuedResponsesBeforeLocked(peerID types.NodeID, connID uint64) {
	queue := h.responseQueues[peerID]
	kept := slices.DeleteFunc(queue, func(job blockResponseJob) bool { return job.envelope.ConnID < connID })
	queued := len(queue) - len(kept)
	if len(kept) == 0 {
		delete(h.responseQueues, peerID)
	} else {
		h.responseQueues[peerID] = kept
	}
	if !h.activePeers[peerID] {
		delete(h.activePeers, peerID)
	}
	h.queuedResponses -= queued
	ready := h.readyPeers[:0]
	for _, readyPeerID := range h.readyPeers {
		if readyPeerID != peerID {
			ready = append(ready, readyPeerID)
		}
	}
	h.readyPeers = ready
}

// Requests may precede their ordered peer updates; keep them bounded and wait
// for PeerStatusUp before reading blocks or scheduling a response.
func (h *blockP2PMessageHandler) schedulePeerLocked(peerID types.NodeID) {
	queue := h.responseQueues[peerID]
	if len(queue) == 0 || h.activePeers[peerID] || slices.Contains(h.readyPeers, peerID) ||
		!h.connectionIsCurrentLocked(peerID, queue[0].envelope.ConnID) {
		return
	}
	h.readyPeers = append(h.readyPeers, peerID)
	h.wakeBlockResponseWorkerLocked()
}

func (h *blockP2PMessageHandler) wakeBlockResponseWorkerLocked() {
	select {
	case h.responseWake <- struct{}{}:
	default:
	}
}

// evictExcessQueuedResponseLocked reserves admission for a peer without an
// outstanding request by replacing the tail of a peer that already has more
// than one queued response. The ready-peer schedule remains valid because the
// removed entry was not at the head.
func (h *blockP2PMessageHandler) evictExcessQueuedResponseLocked() {
	var victim types.NodeID
	maxQueued := 1
	for peerID, queue := range h.responseQueues {
		if len(queue) > maxQueued {
			victim = peerID
			maxQueued = len(queue)
		}
	}
	if maxQueued > 1 {
		h.responseQueues[victim] = h.responseQueues[victim][:maxQueued-1]
		h.queuedResponses--
	}
}
