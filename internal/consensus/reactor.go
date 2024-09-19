package consensus

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/gogo/protobuf/proto"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/eventbus"
	tmstrings "github.com/dashpay/tenderdash/internal/libs/strings"
	"github.com/dashpay/tenderdash/internal/p2p"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/bits"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

var (
	_ service.Service = (*Reactor)(nil)
	_ p2p.Wrapper     = (*tmcons.Message)(nil)
)

const (
	maxMsgSize = 1048576 // 1MB; NOTE: keep in sync with types.PartSet sizes.

	blocksToContributeToBecomeGoodPeer  = 10000
	votesToContributeToBecomeGoodPeer   = 10000
	commitsToContributeToBecomeGoodPeer = 10000
)

var errReactorClosed = errors.New("reactor is closed")

// NOTE: Temporary interface for switching to block sync, we should get rid of v0.
// See: https://github.com/tendermint/tendermint/issues/4595
type BlockSyncReactor interface {
	SwitchToBlockSync(context.Context, sm.State) error

	GetMaxPeerBlockHeight() int64

	// GetTotalSyncedTime returns the time duration since the blocksync starting.
	GetTotalSyncedTime() time.Duration

	// GetRemainingSyncTime returns the estimating time the node will be fully synced,
	// if will return 0 if the blocksync does not perform or the number of block synced is
	// too small (less than 100).
	GetRemainingSyncTime() time.Duration
}

// Reactor defines a reactor for the consensus service.
type Reactor struct {
	service.BaseService
	logger log.Logger

	state    *State
	eventBus *eventbus.EventBus
	Metrics  *Metrics

	mtx         sync.RWMutex
	peers       map[types.NodeID]*PeerState
	waitSync    bool
	readySignal chan struct{} // closed when the node is ready to start consensus

	peerEvents p2p.PeerEventSubscriber
	chCreator  p2p.ChannelCreator
}

// NewReactor returns a reference to a new consensus reactor, which implements
// the service.Service interface. It accepts a logger, consensus state, references
// to relevant p2p Channels and a channel to listen for peer updates on. The
// reactor will close all p2p Channels when stopping.
func NewReactor(
	logger log.Logger,
	cs *State,
	channelCreator p2p.ChannelCreator,
	peerEvents p2p.PeerEventSubscriber,
	eventBus *eventbus.EventBus,
	waitSync bool,
	metrics *Metrics,
) *Reactor {
	r := &Reactor{
		logger:      logger,
		state:       cs,
		waitSync:    waitSync,
		peers:       make(map[types.NodeID]*PeerState),
		eventBus:    eventBus,
		Metrics:     metrics,
		peerEvents:  peerEvents,
		chCreator:   channelCreator,
		readySignal: make(chan struct{}),
	}
	r.BaseService = *service.NewBaseService(logger, "Consensus", r)

	if !waitSync {
		close(r.readySignal)
	}

	return r
}

type channelBundle struct {
	state   p2p.Channel
	data    p2p.Channel
	vote    p2p.Channel
	voteSet p2p.Channel
}

// OnStart starts separate go routines for each p2p Channel and listens for
// envelopes on each. In addition, it also listens for peer updates and handles
// messages on that p2p channel accordingly. The caller must be sure to execute
// OnStop to ensure the outbound p2p Channels are closed.
func (r *Reactor) OnStart(ctx context.Context) error {
	r.logger.Trace("consensus wait sync", "wait_sync", r.WaitSync())

	peerUpdates := r.peerEvents(ctx, "consensus")

	var chBundle channelBundle
	var err error

	chans := p2p.ConsensusChannelDescriptors()
	chBundle.state, err = r.chCreator(ctx, chans[p2p.ConsensusStateChannel])
	if err != nil {
		return err
	}

	chBundle.data, err = r.chCreator(ctx, chans[p2p.ConsensusDataChannel])
	if err != nil {
		return err
	}

	chBundle.vote, err = r.chCreator(ctx, chans[p2p.ConsensusVoteChannel])
	if err != nil {
		return err
	}

	chBundle.voteSet, err = r.chCreator(ctx, chans[p2p.VoteSetBitsChannel])
	if err != nil {
		return err
	}

	// start routine that computes peer statistics for evaluating peer quality
	//
	// TODO: Evaluate if we need this to be synchronized via WaitGroup as to not
	// leak the goroutine when stopping the reactor.
	go r.peerStatsRoutine(ctx, peerUpdates)

	r.subscribeToBroadcastEvents(ctx, chBundle.state)

	if !r.WaitSync() {
		if err := r.state.Start(ctx); err != nil {
			return err
		}
	} else if err := r.state.updateStateFromStore(); err != nil {
		return err
	}

	// Only state channel should be read during state sync.
	// Data, vote and vote set must wait.
	// We cannot skip waiting messages, as the peers might already have marked them as delivered.
	// XXX: this can lead to a deadlock, if so - we need additional buffer for (at least) Commits.
	go r.processMsgCh(ctx, chBundle.state, chBundle)
	go func() {
		select {
		case <-r.readySignal:
			go r.processMsgCh(ctx, chBundle.data, chBundle)
			go r.processMsgCh(ctx, chBundle.vote, chBundle)
			go r.processMsgCh(ctx, chBundle.voteSet, chBundle)
		case <-ctx.Done():
		}
	}()

	go r.processPeerUpdates(ctx, peerUpdates, chBundle)

	return nil
}

// OnStop stops the reactor by signaling to all spawned goroutines to exit and
// blocking until they all exit, as well as unsubscribing from events and stopping
// state.
func (r *Reactor) OnStop() {
	r.state.Stop()

	if !r.WaitSync() {
		r.state.Wait()
	}
}

// WaitSync returns whether the consensus reactor is waiting for state/block sync.
func (r *Reactor) WaitSync() bool {
	select {
	case <-r.readySignal:
		// channel closed
		return false
	default:
		// channel is still open, so we still wait
		return true
	}
}

// SwitchToConsensus switches from block-sync mode to consensus mode. It resets
// the state, turns off block-sync, and starts the consensus state-machine.
func (r *Reactor) SwitchToConsensus(ctx context.Context, state sm.State, skipWAL bool) {
	r.logger.Info("switching to consensus")

	stateData := r.state.GetStateData()
	// we have no votes, so reconstruct LastCommit from SeenCommit
	if state.LastBlockHeight > 0 {
		var err error
		stateData.LastCommit, err = r.state.loadLastCommit(state.LastBlockHeight)
		if err != nil {
			panic(err)
		}
	}

	// NOTE: The line below causes broadcastNewRoundStepRoutine() to broadcast a
	// NewRoundStepMessage.
	stateData.updateToState(state, nil, r.state.blockStore)
	err := r.state.stateDataStore.Update(stateData)
	if err != nil {
		panic(err)
	}
	r.state.eventPublisher.PublishNewRoundStepEvent(stateData.RoundState)

	if err := r.state.Start(ctx); err != nil {
		panic(fmt.Sprintf(`failed to start consensus state: %v

conS:
%+v

conR:
%+v`, err, r.state, r))
	}

	close(r.readySignal)

	r.Metrics.BlockSyncing.Set(0)
	r.Metrics.StateSyncing.Set(0)

	if skipWAL {
		r.state.doWALCatchup = false
	}

	d := types.EventDataBlockSyncStatus{Complete: true, Height: state.LastBlockHeight}
	if err := r.eventBus.PublishEventBlockSyncStatus(d); err != nil {
		r.logger.Error("failed to emit the blocksync complete event", "err", err)
	}
}

// String returns a string representation of the Reactor.
//
// NOTE: For now, it is just a hard-coded string to avoid accessing unprotected
// shared variables.
//
// TODO: improve!
func (r *Reactor) String() string {
	return "ConsensusReactor"
}

// GetPeerState returns PeerState for a given NodeID.
func (r *Reactor) GetPeerState(peerID types.NodeID) (*PeerState, bool) {
	r.mtx.RLock()
	defer r.mtx.RUnlock()

	ps, ok := r.peers[peerID]
	return ps, ok
}

// subscribeToBroadcastEvents subscribes for new round steps and votes using the
// internal pubsub defined in the consensus state to broadcast them to peers
// upon receiving.
func (r *Reactor) subscribeToBroadcastEvents(ctx context.Context, stateCh p2p.Channel) {
	onStopCh := r.state.getOnStopCh()

	r.state.emitter.AddListener(
		types.EventNewRoundStepValue,
		func(data eventemitter.EventData) error {
			rs := data.(*cstypes.RoundState)
			err := r.broadcast(ctx, stateCh, rs.NewRoundStepMessage())
			if err != nil {
				return err
			}
			r.logResult(err, r.logger, "broadcasting round step message", "height", rs.Height, "round", rs.Round)
			select {
			case onStopCh <- data.(*cstypes.RoundState):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			default:
				return nil
			}
		},
	)

	r.state.emitter.AddListener(
		types.EventValidBlockValue,
		func(data eventemitter.EventData) error {
			rs := data.(*cstypes.RoundState)
			err := r.broadcast(ctx, stateCh, rs.NewValidBlockMessage())
			r.logResult(err, r.logger, "broadcasting new valid block message", "height", rs.Height, "round", rs.Round)
			return err
		},
	)

	r.state.emitter.AddListener(
		types.EventVoteValue,
		func(data eventemitter.EventData) error {
			vote := data.(*types.Vote)
			err := r.broadcast(ctx, stateCh, vote.HasVoteMessage())
			r.logResult(err, r.logger, "broadcasting HasVote message", "height", vote.Height, "round", vote.Round)
			return err
		},
	)

	r.state.emitter.AddListener(types.EventCommitValue,
		func(data eventemitter.EventData) error {
			commit := data.(*types.Commit)
			err := r.broadcast(ctx, stateCh, commit.HasCommitMessage())
			r.logResult(err, r.logger, "broadcasting HasVote message", "height", commit.Height, "round", commit.Round)
			return err
		},
	)
}

// broadcast sends a broadcast message to all peers connected to the `channel`.
func (r *Reactor) broadcast(ctx context.Context, channel p2p.Channel, msg proto.Message) error {
	select {
	case <-ctx.Done():
		return errReactorClosed
	default:
		return channel.Send(ctx, p2p.Envelope{
			Broadcast: true,
			Message:   msg,
		})
	}
}

// logResult creates a log that depends on value of err
func (r *Reactor) logResult(err error, logger log.Logger, message string, keyvals ...interface{}) bool {
	if err != nil {
		logger.Error(message+" error", append(keyvals, "error", err))
		return false
	}
	logger.Trace(message+" success", keyvals...)
	return true
}

// processPeerUpdate process a peer update message. For new or reconnected peers,
// we create a peer state if one does not exist for the peer, which should always
// be the case, and we spawn all the relevant goroutine to broadcast messages to
// the peer. During peer removal, we remove the peer for our set of peers and
// signal to all spawned goroutines to gracefully exit in a non-blocking manner.
func (r *Reactor) processPeerUpdate(ctx context.Context, peerUpdate p2p.PeerUpdate, chans channelBundle) {
	r.logger.Trace("received peer update", "peer", peerUpdate.NodeID, "status", peerUpdate.Status,
		"peer_proTxHash", peerUpdate.ProTxHash.ShortString())

	switch peerUpdate.Status {
	case p2p.PeerStatusUp:
		// Do not allow starting new broadcasting goroutines after reactor shutdown
		// has been initiated. This can happen after we've manually closed all
		// peer goroutines, but the router still sends in-flight peer updates.
		if !r.IsRunning() {
			return
		}
		r.peerUp(ctx, peerUpdate, 3, chans)
	case p2p.PeerStatusDown:
		r.peerDown(ctx, peerUpdate, chans)
	}
}

// peerUp starts the peer. It recursively retries up to `retries` times if the peer is already closing.
func (r *Reactor) peerUp(ctx context.Context, peerUpdate p2p.PeerUpdate, retries int, chans channelBundle) {
	if retries < 1 {
		r.logger.Error("peer up failed: max retries exceeded", "peer", peerUpdate.NodeID)
		return
	}

	r.mtx.Lock()
	defer r.mtx.Unlock()

	ps, ok := r.peers[peerUpdate.NodeID]
	if !ok {
		ps = NewPeerState(r.logger, peerUpdate.NodeID)
		ps.SetProTxHash(peerUpdate.ProTxHash)
		r.peers[peerUpdate.NodeID] = ps
	} else if len(peerUpdate.ProTxHash) > 0 {
		ps.SetProTxHash(peerUpdate.ProTxHash)
	}

	logger := r.logger.With(
		"peer", ps.peerID,
		"peer_proTxHash", ps.GetProTxHash().ShortString(),
	)
	// TODO needs to register this gossip worker, to be able to stop it once a peer will be down
	msgSender := p2pMsgSender{logger: logger, ps: ps, chans: chans}
	pgw := newPeerGossipWorker(logger, ps, r.state, &msgSender)

	select {
	case <-ctx.Done():
		// Hmm, someone is closing this peer right now, let's wait and retry
		// Note: we run this in a goroutine to not block main goroutine in ps.broadcastWG.Wait()
		go func() {
			time.Sleep(r.state.config.PeerGossipSleepDuration)
			r.peerUp(ctx, peerUpdate, retries-1, chans)
		}()
		return
	default:
	}

	if !ps.IsRunning() {
		// Set the peer state's closer to signal to all spawned goroutines to exit
		// when the peer is removed. We also set the running state to ensure we
		// do not spawn multiple instances of the same goroutines and finally we
		// set the waitgroup counter so we know when all goroutines have exited.
		ps.SetRunning(true)
		ctx, ps.cancel = context.WithCancel(ctx)

		go func() {
			select {
			case <-ctx.Done():
				return
			case <-r.readySignal:
			}
			// do nothing if the peer has
			// stopped while we've been waiting.
			if !ps.IsRunning() {
				return
			}
			// start goroutines for this peer
			_ = pgw.Start(ctx)

			// Send our state to the peer. If we're block-syncing, broadcast a
			// RoundStepMessage later upon SwitchToConsensus().
			if !r.WaitSync() {
				go func() {
					rs := r.state.GetRoundState()
					err := msgSender.send(ctx, rs.NewRoundStepMessage())
					r.logResult(err, r.logger, "sending round step msg", "height", rs.Height, "round", rs.Round)
				}()
			}
		}()
	}
}

func (r *Reactor) peerDown(_ context.Context, peerUpdate p2p.PeerUpdate, _chans channelBundle) {
	r.mtx.RLock()
	ps, ok := r.peers[peerUpdate.NodeID]
	r.mtx.RUnlock()

	if ok && ps.IsRunning() {
		// signal to all spawned goroutines for the peer to gracefully exit
		go func() {
			r.mtx.Lock()
			delete(r.peers, peerUpdate.NodeID)
			r.mtx.Unlock()

			ps.SetRunning(false)
			ps.cancel()
		}()
	}
}

// handleStateMessage handles envelopes sent from peers on the StateChannel.
// An error is returned if the message is unrecognized or if validation fails.
// If we fail to find the peer state for the envelope sender, we perform a no-op
// and return. This can happen when we process the envelope after the peer is
// removed.
func (r *Reactor) handleStateMessage(ctx context.Context, envelope *p2p.Envelope, msgI Message, voteSetCh p2p.Channel) error {
	ps, ok := r.GetPeerState(envelope.From)
	if !ok || ps == nil {
		r.logger.Debug("failed to find peer state", "peer", envelope.From, "ch_id", "StateChannel")
		return nil
	}

	switch msg := envelope.Message.(type) {
	case *tmcons.NewRoundStep:
		stateData := r.state.GetStateData()
		initialHeight := stateData.InitialHeight()

		if err := msgI.(*NewRoundStepMessage).ValidateHeight(initialHeight); err != nil {
			r.logger.Error("peer sent us an invalid msg", "msg", msg, "err", err)
			return err
		}

		ps.ApplyNewRoundStepMessage(msgI.(*NewRoundStepMessage))

	case *tmcons.NewValidBlock:
		ps.ApplyNewValidBlockMessage(msgI.(*NewValidBlockMessage))

	case *tmcons.HasCommit:
		ps.ApplyHasCommitMessage(msgI.(*HasCommitMessage))

	case *tmcons.HasVote:
		if err := ps.ApplyHasVoteMessage(msgI.(*HasVoteMessage)); err != nil {
			r.logger.Error("applying HasVote message failed", "msg", msg, "err", err)
			return err
		}
	case *tmcons.VoteSetMaj23:
		stateData := r.state.GetStateData()
		height, votes := stateData.HeightVoteSet()

		if height != msg.Height {
			r.logger.Debug("vote set height does not match msg height", "height", height, "msg", msg)
			return nil
		}
		vsmMsg := msgI.(*VoteSetMaj23Message)

		// peer claims to have a maj23 for some BlockID at <H,R,S>
		err := votes.SetPeerMaj23(msg.Height, msg.Round, msg.Type, ps.peerID, vsmMsg.BlockID)
		if err != nil {
			return err
		}

		// Respond with a VoteSetBitsMessage showing which votes we have and
		// consequently shows which we don't have.
		var ourVotes *bits.BitArray
		switch vsmMsg.Type {
		case tmproto.PrevoteType:
			ourVotes = votes.Prevotes(msg.Round).BitArrayByBlockID(vsmMsg.BlockID)

		case tmproto.PrecommitType:
			ourVotes = votes.Precommits(msg.Round).BitArrayByBlockID(vsmMsg.BlockID)

		default:
			panic("bad VoteSetBitsMessage field type; forgot to add a check in ValidateBasic?")
		}

		eMsg := &tmcons.VoteSetBits{
			Height:  msg.Height,
			Round:   msg.Round,
			Type:    msg.Type,
			BlockID: msg.BlockID,
		}

		if votesProto := ourVotes.ToProto(); votesProto != nil {
			eMsg.Votes = *votesProto
		}

		if err := voteSetCh.Send(ctx, p2p.Envelope{
			To:      envelope.From,
			Message: eMsg,
		}); err != nil {
			return err
		}

	default:
		return fmt.Errorf("received unknown message on StateChannel: %T", msg)
	}

	return nil
}

// handleDataMessage handles envelopes sent from peers on the DataChannel. If we
// fail to find the peer state for the envelope sender, we perform a no-op and
// return. This can happen when we process the envelope after the peer is
// removed.
func (r *Reactor) handleDataMessage(ctx context.Context, envelope *p2p.Envelope, msgI Message) error {
	logger := r.logger.With("peer", envelope.From, "ch_id", "DataChannel")

	ps, ok := r.GetPeerState(envelope.From)
	if !ok || ps == nil {
		r.logger.Debug("failed to find peer state")
		return nil
	}

	if r.WaitSync() {
		logger.Debug("ignoring message received during sync", "msg", tmstrings.LazySprintf("%T", msgI))
		return nil
	}

	logger.Trace("data channel processing", "msg", envelope.Message, "type", fmt.Sprintf("%T", envelope.Message))

	switch msg := envelope.Message.(type) {
	case *tmcons.Proposal:
		pMsg := msgI.(*ProposalMessage)

		ps.SetHasProposal(pMsg.Proposal)
		return r.state.sendMessage(ctx, pMsg, envelope.From)
	case *tmcons.ProposalPOL:
		ps.ApplyProposalPOLMessage(msgI.(*ProposalPOLMessage))
	case *tmcons.BlockPart:
		bpMsg := msgI.(*BlockPartMessage)

		ps.SetHasProposalBlockPart(bpMsg.Height, bpMsg.Round, int(bpMsg.Part.Index))
		r.Metrics.BlockParts.With("peer_id", string(envelope.From)).Add(1)
		return r.state.sendMessage(ctx, bpMsg, envelope.From)
	default:
		return fmt.Errorf("received unknown message on DataChannel: %T", msg)
	}

	return nil
}

// handleVoteMessage handles envelopes sent from peers on the VoteChannel. If we
// fail to find the peer state for the envelope sender, we perform a no-op and
// return. This can happen when we process the envelope after the peer is
// removed.
func (r *Reactor) handleVoteMessage(ctx context.Context, envelope *p2p.Envelope, msgI Message) error {
	logger := r.logger.With("peer", envelope.From, "ch_id", "VoteChannel")

	ps, ok := r.GetPeerState(envelope.From)
	if !ok || ps == nil {
		logger.Debug("failed to find peer state")
		return nil
	}

	if r.WaitSync() {
		logger.Debug("ignoring message received during sync", "msg", msgI)
		return nil
	}

	logger.Trace("vote channel processing", "msg", envelope.Message, "type", fmt.Sprintf("%T", envelope.Message))

	switch msg := envelope.Message.(type) {
	case *tmcons.Commit:
		c, err := types.CommitFromProto(msg.Commit)
		if err != nil {
			return err
		}
		ps.SetHasCommit(c)

		cMsg := msgI.(*CommitMessage)
		err = r.state.sendMessage(ctx, cMsg, envelope.From)
		if err != nil {
			return err
		}
	case *tmcons.Vote:
		stateData := r.state.stateDataStore.Get()
		isValidator := stateData.isValidator(r.state.privValidator.ProTxHash)
		height, valSize := stateData.Height, stateData.Validators.Size()
		lastValSize := len(stateData.LastValidators.Validators)

		if isValidator { // ignore votes on non-validator nodes; TODO don't even send it
			vMsg := msgI.(*VoteMessage)

			if err := vMsg.Vote.ValidateBasic(); err != nil {
				return fmt.Errorf("invalid vote received from %s: %w", envelope.From, err)
			}

			ps.EnsureVoteBitArrays(height, valSize)
			ps.EnsureVoteBitArrays(height-1, lastValSize)
			if err := ps.SetHasVote(vMsg.Vote); err != nil {
				return err
			}
			return r.state.sendMessage(ctx, vMsg, envelope.From)
		}
	default:
		return fmt.Errorf("received unknown message on VoteChannel: %T", msg)
	}

	return nil
}

// handleVoteSetBitsMessage handles envelopes sent from peers on the
// VoteSetBitsChannel. If we fail to find the peer state for the envelope sender,
// we perform a no-op and return. This can happen when we process the envelope
// after the peer is removed.
func (r *Reactor) handleVoteSetBitsMessage(_ context.Context, envelope *p2p.Envelope, msgI Message) error {
	logger := r.logger.With("peer", envelope.From, "ch_id", "VoteSetBitsChannel")

	ps, ok := r.GetPeerState(envelope.From)
	if !ok || ps == nil {
		r.logger.Debug("failed to find peer state")
		return nil
	}

	if r.WaitSync() {
		logger.Debug("ignoring message received during sync", "msg", msgI)
		return nil
	}

	switch msg := envelope.Message.(type) {
	case *tmcons.VoteSetBits:
		stateData := r.state.GetStateData()
		height, votes := stateData.Height, stateData.Votes

		vsbMsg := msgI.(*VoteSetBitsMessage)

		if height == msg.Height {
			var ourVotes *bits.BitArray

			switch msg.Type {
			case tmproto.PrevoteType:
				ourVotes = votes.Prevotes(msg.Round).BitArrayByBlockID(vsbMsg.BlockID)

			case tmproto.PrecommitType:
				ourVotes = votes.Precommits(msg.Round).BitArrayByBlockID(vsbMsg.BlockID)

			default:
				panic("bad VoteSetBitsMessage field type; forgot to add a check in ValidateBasic?")
			}

			ps.ApplyVoteSetBitsMessage(vsbMsg, ourVotes)
		} else {
			ps.ApplyVoteSetBitsMessage(vsbMsg, nil)
		}

	default:
		return fmt.Errorf("received unknown message on VoteSetBitsChannel: %T", msg)
	}

	return nil
}

// handleMessage handles an Envelope sent from a peer on a specific p2p Channel.
// It will handle errors and any possible panics gracefully. A caller can handle
// any error returned by sending a PeerError on the respective channel.
//
// NOTE: We process these messages even when we're block syncing. Messages affect
// either a peer state or the consensus state. Peer state updates can happen in
// parallel, but processing of proposals, block parts, and votes are ordered by
// the p2p channel.
//
// NOTE: We block on consensus state for proposals, block parts, and votes.
func (r *Reactor) handleMessage(ctx context.Context, envelope *p2p.Envelope, chans channelBundle) (err error) {
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

	msg, err := MsgFromProto(envelope.Message)
	if err != nil {
		return err
	}

	switch envelope.ChannelID {
	case p2p.ConsensusStateChannel:
		err = r.handleStateMessage(ctx, envelope, msg, chans.voteSet)
	case p2p.ConsensusDataChannel:
		err = r.handleDataMessage(ctx, envelope, msg)
	case p2p.ConsensusVoteChannel:
		err = r.handleVoteMessage(ctx, envelope, msg)
	case p2p.VoteSetBitsChannel:
		err = r.handleVoteSetBitsMessage(ctx, envelope, msg)
	default:
		err = fmt.Errorf("unknown channel ID (%d) for envelope (%v)", envelope.ChannelID, envelope)
	}

	return err
}

// processMsgCh initiates a blocking process where we listen for and handle
// envelopes on the StateChannel or DataChannel or VoteChannel or VoteSetBitsChannel.
// Any error encountered during message execution will result in a PeerError being sent
// on the StateChannel or DataChannel or VoteChannel or VoteSetBitsChannel.
// When the reactor is stopped, we will catch the signal and close the p2p Channel gracefully.
func (r *Reactor) processMsgCh(ctx context.Context, msgCh p2p.Channel, chBundle channelBundle) {
	iter := msgCh.Receive(ctx)
	for iter.Next(ctx) {
		envelope := iter.Envelope()
		if err := r.handleMessage(ctx, envelope, chBundle); err != nil {
			r.logger.Error("failed to process message", "ch_id", envelope.ChannelID, "envelope", envelope, "err", err)
			if serr := msgCh.SendError(ctx, p2p.PeerError{
				NodeID: envelope.From,
				Err:    err,
			}); serr != nil {
				return
			}
		}
	}
}

// processPeerUpdates initiates a blocking process where we listen for and handle
// PeerUpdate messages. When the reactor is stopped, we will catch the signal and
// close the p2p PeerUpdatesCh gracefully.
func (r *Reactor) processPeerUpdates(ctx context.Context, peerUpdates *p2p.PeerUpdates, chans channelBundle) {
	for {
		select {
		case <-ctx.Done():
			return
		case peerUpdate := <-peerUpdates.Updates():
			r.processPeerUpdate(ctx, peerUpdate, chans)
		}
	}
}

func (r *Reactor) peerStatsRoutine(ctx context.Context, peerUpdates *p2p.PeerUpdates) {
	for {
		if !r.IsRunning() {
			r.logger.Trace("stopping peerStatsRoutine")
			return
		}

		select {
		case msg := <-r.state.statsMsgQueue.ch:
			ps, ok := r.GetPeerState(msg.PeerID)
			if !ok || ps == nil {
				// it's quite common to happen when a peer is removed
				r.logger.Trace("attempt to update stats for non-existent peer", "peer", msg.PeerID)
				continue
			}

			switch msg.Msg.(type) {
			case *CommitMessage:
				if numCommits := ps.RecordCommit(); numCommits%commitsToContributeToBecomeGoodPeer == 0 {
					peerUpdates.SendUpdate(ctx, p2p.PeerUpdate{
						NodeID: msg.PeerID,
						Status: p2p.PeerStatusGood,
					})
				}

			case *VoteMessage:
				if numVotes := ps.RecordVote(); numVotes%votesToContributeToBecomeGoodPeer == 0 {
					peerUpdates.SendUpdate(ctx, p2p.PeerUpdate{
						NodeID: msg.PeerID,
						Status: p2p.PeerStatusGood,
					})
				}

			case *BlockPartMessage:
				if numParts := ps.RecordBlockPart(); numParts%blocksToContributeToBecomeGoodPeer == 0 {
					peerUpdates.SendUpdate(ctx, p2p.PeerUpdate{
						NodeID: msg.PeerID,
						Status: p2p.PeerStatusGood,
					})
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

func (r *Reactor) GetConsensusState() *State {
	return r.state
}

func (r *Reactor) SetStateSyncingMetrics(v float64) {
	r.Metrics.StateSyncing.Set(v)
}

func (r *Reactor) SetBlockSyncingMetrics(v float64) {
	r.Metrics.BlockSyncing.Set(v)
}
