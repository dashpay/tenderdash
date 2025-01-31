package mempool

import (
	"context"
	"runtime/debug"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/libs/clist"
	tmstrings "github.com/dashpay/tenderdash/internal/libs/strings"
	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/internal/p2p/client"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
	"github.com/dashpay/tenderdash/types"
)

var (
	_ service.Service = (*Reactor)(nil)
)

// Reactor implements a service that contains mempool of txs that are broadcasted
// amongst peers. It maintains a map from peer ID to counter, to prevent gossiping
// txs to the peers you received it from.
type Reactor struct {
	service.BaseService
	logger log.Logger

	cfg     *config.MempoolConfig
	mempool *TxMempool
	ids     *IDs // Peer IDs assigned for peers

	peerEvents p2p.PeerEventSubscriber
	p2pClient  *client.Client

	// observePanic is a function for observing panics that were recovered in methods on
	// Reactor. observePanic is called with the recovered value.
	observePanic func(interface{})

	mtx          sync.Mutex
	peerRoutines map[types.NodeID]context.CancelFunc
}

// NewReactor returns a reference to a new reactor.
func NewReactor(
	logger log.Logger,
	cfg *config.MempoolConfig,
	txmp *TxMempool,
	p2pClient *client.Client,
	peerEvents p2p.PeerEventSubscriber,
) *Reactor {
	r := &Reactor{
		logger:       logger,
		cfg:          cfg,
		mempool:      txmp,
		ids:          NewMempoolIDs(),
		p2pClient:    p2pClient,
		peerEvents:   peerEvents,
		peerRoutines: make(map[types.NodeID]context.CancelFunc),
		observePanic: func(_i interface{}) {},
	}

	r.BaseService = *service.NewBaseService(logger, "Mempool", r)
	return r
}

// OnStart starts separate go routines for each p2p Channel and listens for
// envelopes on each. In addition, it also listens for peer updates and handles
// messages on that p2p channel accordingly. The caller must be sure to execute
// OnStop to ensure the outbound p2p Channels are closed.
func (r *Reactor) OnStart(ctx context.Context) error {
	if !r.cfg.Broadcast {
		r.logger.Info("tx broadcasting is disabled")
	}
	go func() {
		err := r.p2pClient.Consume(ctx, consumerHandler(ctx, r.logger, r.mempool.config, r.mempool, r.ids))
		if err != nil {
			r.logger.Error("failed to consume p2p checker messages", "error", err)
		}
	}()
	go r.processPeerUpdates(ctx, r.peerEvents(ctx, "checker"))

	return nil
}

// OnStop stops the reactor by signaling to all spawned goroutines to exit and
// blocking until they all exit.
func (r *Reactor) OnStop() {}

// processPeerUpdate processes a PeerUpdate. For added peers, PeerStatusUp, we
// check if the reactor is running and if we've already started a tx broadcasting
// goroutine or not. If not, we start one for the newly added peer. For down or
// removed peers, we remove the peer from the mempool peer ID set and signal to
// stop the tx broadcasting goroutine.
func (r *Reactor) processPeerUpdate(ctx context.Context, peerUpdate p2p.PeerUpdate) {
	r.logger.Trace("received peer update", "peer", peerUpdate.NodeID, "status", peerUpdate.Status)

	r.mtx.Lock()
	defer r.mtx.Unlock()

	switch peerUpdate.Status {
	case p2p.PeerStatusUp:
		// Do not allow starting new tx broadcast loops after reactor shutdown
		// has been initiated. This can happen after we've manually closed all
		// peer broadcast, but the router still sends in-flight peer updates.
		if !r.IsRunning() {
			return
		}

		if r.cfg.Broadcast {
			// Check if we've already started a goroutine for this peer, if not we create
			// a new done channel so we can explicitly close the goroutine if the peer
			// is later removed, we increment the waitgroup so the reactor can stop
			// safely, and finally start the goroutine to broadcast txs to that peer.
			_, ok := r.peerRoutines[peerUpdate.NodeID]
			if !ok {
				pctx, pcancel := context.WithCancel(ctx)
				r.peerRoutines[peerUpdate.NodeID] = pcancel

				r.ids.ReserveForPeer(peerUpdate.NodeID)

				// start a broadcast routine ensuring all txs are forwarded to the peer
				go r.broadcastTxRoutine(pctx, peerUpdate.NodeID)
			}
		}

	case p2p.PeerStatusDown:
		r.ids.Reclaim(peerUpdate.NodeID)

		// Check if we've started a tx broadcasting goroutine for this peer.
		// If we have, we signal to terminate the goroutine via the channel's closure.
		// This will internally decrement the peer waitgroup and remove the peer
		// from the map of peer tx broadcasting goroutines.
		closer, ok := r.peerRoutines[peerUpdate.NodeID]
		if ok {
			closer()
		}
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

// sendTxs sends the given txs to the given peer.
//
// Sending txs to a peer is rate limited to prevent spamming the network.
// Each peer has its own rate limiter.
//
// As we will wait for confirmation of the txs being delivered, it is generally safe to
// drop the txs if the send fails.
func (r *Reactor) sendTxs(ctx context.Context, peerID types.NodeID, txs ...types.Tx) error {
	return r.p2pClient.SendTxs(ctx, peerID, txs...)
}

func (r *Reactor) broadcastTxRoutine(ctx context.Context, peerID types.NodeID) {
	peerMempoolID := r.ids.GetForPeer(peerID)
	var nextGossipTx *clist.CElement

	// remove the peer ID from the map of routines and mark the waitgroup as done
	defer func() {
		r.mtx.Lock()
		delete(r.peerRoutines, peerID)
		r.mtx.Unlock()

		if e := recover(); e != nil {
			r.observePanic(e)
			r.logger.Error(
				"recovering from broadcasting mempool loop",
				"err", e,
				"stack", string(debug.Stack()),
			)
		}
	}()

	for {
		if !r.IsRunning() || ctx.Err() != nil {
			return
		}

		// This happens because the CElement we were looking at got garbage
		// collected (removed). That is, .NextWait() returned nil. Go ahead and
		// start from the beginning.
		if nextGossipTx == nil {
			select {
			case <-ctx.Done():
				return
			case <-r.mempool.TxsWaitChan(): // wait until a tx is available
				if nextGossipTx = r.mempool.TxsFront(); nextGossipTx == nil {
					continue
				}
			}
		}

		memTx := nextGossipTx.Value.(*WrappedTx)

		// We expect the peer to send tx back once it gets it, and that's
		// when we will mark it as seen.
		// NOTE: Transaction batching was disabled due to:
		// https://github.com/tendermint/tendermint/issues/5796
		if !memTx.HasPeer(peerMempoolID) {
			// Send the mempool tx to the corresponding peer. Note, the peer may be
			// behind and thus would not be able to process the mempool tx correctly.
			err := r.sendTxs(ctx, peerID, memTx.tx)
			if err != nil {
				r.logger.Error("failed to gossip transaction", "peerID", peerID, "error", err)
				return
			}

			r.logger.Debug("gossiped tx to peer",
				"tx", tmstrings.LazySprintf("%X", memTx.tx.Hash()),
				"peer", peerID,
			)
		}

		select {
		case <-nextGossipTx.NextWaitChan():
			nextGossipTx = nextGossipTx.Next()
		case <-ctx.Done():
			return
		}
	}
}
