package consensus

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/tendermint/tendermint/libs/log"
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
	gossiper := msgGossiper{
		ps:         ps,
		blockStore: &blockRepository{BlockStore: state.blockStore},
		msgSender:  msgSender,
		logger:     logger,
		optimistic: true,
	}
	return &peerGossipWorker{
		clock:          clockwork.NewRealClock(),
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

func (g *peerGossipWorker) isRunning() bool {
	return g.running.Load()
}

func (g *peerGossipWorker) start(ctx context.Context) {
	g.running.Store(true)
	for _, handler := range g.handlers {
		go g.runHandler(ctx, handler)
	}
}

func (g *peerGossipWorker) stop(ctx context.Context) {
	if !g.running.Swap(false) {
		return
	}
	g.logger.Debug("peer gossip worker stopping")
	close(g.stopCh)
	g.wait(ctx)
}

func (g *peerGossipWorker) wait(ctx context.Context) {
	for _, hd := range g.handlers {
		select {
		case <-ctx.Done():
			return
		case <-hd.stoppedCh:
			g.logger.Debug("peer gossip worker stopped")
		}
	}
}

func (g *peerGossipWorker) runHandler(ctx context.Context, hd gossipHandler) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		hd.handlerFunc(ctx, g.stateDataStore.Get())
		timer.Reset(hd.sleepDuration)
		select {
		case <-timer.C:
		case <-g.stopCh:
			g.logger.Debug("peer gossip worker got stop signal")
			close(hd.stoppedCh)
			return
		case <-ctx.Done():
			g.logger.Debug("peer gossip worker got stop signal via context.Done")
			close(hd.stoppedCh)
			return
		}
	}
}
