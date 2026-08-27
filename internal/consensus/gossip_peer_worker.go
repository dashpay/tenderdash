package consensus

import (
	"context"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/dashpay/tenderdash/libs/log"
)

type gossipHandlerFunc func(ctx context.Context, appState StateData)

type gossipHandler struct {
	sleepDuration time.Duration
	handlerFunc   func(ctx context.Context, appState StateData)
	stoppedCh     chan struct{}
}

func newGossipHandler(fn gossipHandlerFunc, sleep time.Duration) gossipHandler {
	return gossipHandler{
		sleepDuration: sleep,
		handlerFunc:   fn,
		stoppedCh:     make(chan struct{}),
	}
}

type peerGossipWorker struct {
	clock          clockwork.Clock
	logger         log.Logger
	handlers       []gossipHandler
	running        atomic.Bool
	stateDataStore *StateDataStore
	stopCh         chan struct{}
}

func newPeerGossipWorker(
	logger log.Logger,
	ps *PeerState,
	state *State,
	msgSender *p2pMsgSender,
) *peerGossipWorker {
	clock := clockwork.NewRealClock()
	gossiper := msgGossiper{
		ps:         ps,
		blockStore: &blockRepository{BlockStore: state.blockStore, logger: logger},
		msgSender:  msgSender,
		logger:     logger,
		optimistic: true,
		clock:      clock,
	}
	return &peerGossipWorker{
		clock:          clock,
		logger:         logger,
		stopCh:         make(chan struct{}),
		stateDataStore: state.stateDataStore,
		handlers: []gossipHandler{
			newGossipHandler(
				votesAndCommitGossipHandler(ps, state.blockStore, &gossiper),
				state.config.PeerGossipSleepDuration,
			),
			newGossipHandler(
				dataGossipHandler(ps, logger, state.blockStore, &gossiper),
				state.config.PeerGossipSleepDuration,
			),
			newGossipHandler(
				queryMaj23GossipHandler(ps, &gossiper),
				state.config.PeerQueryMaj23SleepDuration,
			),
		},
	}
}

func (g *peerGossipWorker) IsRunning() bool {
	return g.running.Load()
}

func (g *peerGossipWorker) Start(ctx context.Context) error {
	g.running.Store(true)
	for _, handler := range g.handlers {
		go g.runHandler(ctx, handler)
	}
	return nil
}

func (g *peerGossipWorker) Stop() {
	if !g.running.Swap(false) {
		return
	}
	g.logger.Trace("peer gossip worker stopping")
	close(g.stopCh)
	g.Wait()
}

func (g *peerGossipWorker) Wait() {
	for _, hd := range g.handlers {
		<-hd.stoppedCh
		g.logger.Trace("peer gossip worker stopped")
	}
}

// runGossipHandler invokes a gossip handler, converting a panic into a logged
// error. Handlers load historical records at heights chosen by the peer they
// gossip to, and the block store panics rather than returning an error, so a
// single unreadable record would otherwise terminate the process on demand.
// Every other peer-facing path in the node recovers the same way.
func (g *peerGossipWorker) runGossipHandler(ctx context.Context, hd gossipHandler) {
	defer func() {
		if e := recover(); e != nil {
			g.logger.Error("panic in gossip handler",
				"err", e, "stack", string(debug.Stack()))
		}
	}()
	hd.handlerFunc(ctx, g.stateDataStore.Get())
}

func (g *peerGossipWorker) runHandler(ctx context.Context, hd gossipHandler) {
	timer := g.clock.NewTimer(0)
	defer timer.Stop()
	for {
		g.runGossipHandler(ctx, hd)
		timer.Reset(hd.sleepDuration)
		select {
		case <-timer.Chan():
		case <-g.stopCh:
			g.logger.Trace("peer gossip worker got stop signal")
			close(hd.stoppedCh)
			return
		case <-ctx.Done():
			g.logger.Trace("peer gossip worker got stop signal via context.Done")
			close(hd.stoppedCh)
			return
		}
	}
}
