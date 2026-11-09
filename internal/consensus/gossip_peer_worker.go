package consensus

import (
	"context"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/libs/log"
)

// minGossipSleepDuration is the shortest cadence a gossip handler runs at,
// whatever the config says. A handler sleeping for zero runs back to back,
// spinning a goroutine per handler per peer, and config validation runs only
// on the CLI path, so the reactor holds a floor itself. It stops the spin and
// nothing more: well below the defaults the handlers still offer peers more
// than their state-channel allowance admits (see peerStateRateLimit), and the
// excess is dropped there. 1ms is the floor incoming-connection-window uses
// too; the test config runs at 5ms.
const minGossipSleepDuration = time.Millisecond

// gossipCadence is how often each peer's gossip handlers run, floored at
// minGossipSleepDuration. The reactor derives it from the config once and
// every consumer of these settings reads it from there.
type gossipCadence struct {
	// gossip is PeerGossipSleepDuration: votes, commits and block parts.
	gossip time.Duration
	// queryMaj23 is PeerQueryMaj23SleepDuration: majority claims, round state,
	// skip certificates and vote resends.
	queryMaj23 time.Duration
}

// flooredGossipCadence returns the configured cadences, each raised to
// minGossipSleepDuration if shorter, warning once per raised setting.
func flooredGossipCadence(logger log.Logger, cfg *config.ConsensusConfig) gossipCadence {
	return gossipCadence{
		gossip:     flooredGossipSleep(logger, "peer-gossip-sleep-duration", cfg.PeerGossipSleepDuration),
		queryMaj23: flooredGossipSleep(logger, "peer-query-maj23-sleep-duration", cfg.PeerQueryMaj23SleepDuration),
	}
}

func flooredGossipSleep(logger log.Logger, key string, configured time.Duration) time.Duration {
	if configured >= minGossipSleepDuration {
		return configured
	}
	logger.Warn("gossip sleep duration below the minimum, gossiping at the minimum instead",
		"key", key, "configured", configured, "minimum", minGossipSleepDuration)
	return minGossipSleepDuration
}

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
	cadence gossipCadence,
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
				cadence.gossip,
			),
			newGossipHandler(
				dataGossipHandler(ps, logger, state.blockStore, &gossiper),
				cadence.gossip,
			),
			newGossipHandler(
				queryMaj23GossipHandler(ps, &gossiper),
				cadence.queryMaj23,
			),
			newGossipHandler(
				roundStateGossipHandler(logger, msgSender.send),
				cadence.queryMaj23,
			),
			// A certificate is resent at the majority-claim cadence: a peer
			// behind waits for nothing else, and one that applied it stops
			// being sent it as soon as it announces its new round.
			newGossipHandler(
				roundSkipGossipHandler(ps, logger, msgSender.send),
				cadence.queryMaj23,
			),
			// So is a vote of its round, or of a later round +2/3 voted in, to
			// a peer that waits in this node's round, or is in a round this
			// node left: a vote lost after the channel took it is not sent
			// again, and the peer may have no timeout to fall back on. It
			// stops once the peer announces the propose step, or this node's
			// round.
			newGossipHandler(
				roundVoteResendGossipHandler(ps, logger, msgSender.send),
				cadence.queryMaj23,
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
