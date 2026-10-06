package consensus

import (
	"context"
	"runtime/debug"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
)

type gossipHandlerFunc func(ctx context.Context, appState StateData)

type gossipHandler struct {
	sleepDuration time.Duration
	handlerFunc   func(ctx context.Context, appState StateData)
}

func newGossipHandler(fn gossipHandlerFunc, sleep time.Duration) gossipHandler {
	return gossipHandler{
		sleepDuration: sleep,
		handlerFunc:   fn,
	}
}

type peerGossipWorker struct {
	service.BaseService
	clock          clockwork.Clock
	logger         log.Logger
	handlers       []gossipHandler
	stateDataStore *StateDataStore
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
	worker := &peerGossipWorker{
		clock:          clock,
		logger:         logger,
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
	worker.BaseService = *service.NewBaseService(logger, "PeerGossipWorker", worker)
	return worker
}

func (g *peerGossipWorker) OnStart(ctx context.Context) error {
	for _, handler := range g.handlers {
		g.Go(ctx, func(ctx context.Context) { g.runHandler(ctx, handler) })
	}
	return nil
}

func (g *peerGossipWorker) OnStop() {}

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
		case <-ctx.Done():
			g.logger.Trace("peer gossip worker got stop signal via context.Done")
			return
		}
	}
}
