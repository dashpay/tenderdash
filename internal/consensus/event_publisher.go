package consensus

import (
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// EventPublisher is event message sender to event-bus and event-switch
// this component provides some methods for sending events
// event-bus is used to interact internally and between the modules
// event-switch is mostly used in tests
type EventPublisher struct {
	// synchronous pubsub between consensus state and reactor.
	// state only emits EventNewRoundStep, EventValidBlock, and EventVote
	emitter *eventemitter.EventEmitter
	// we use eventBus to trigger msg broadcasts in the reactor,
	// and to notify external subscribers, eg. through a websocket
	eventBus *eventbus.EventBus
	wal      WALWriter
	logger   log.Logger
}

// PublishValidBlockEvent ...
func (p *EventPublisher) PublishValidBlockEvent(rs cstypes.RoundState) {
	err := p.eventBus.PublishEventValidBlock(rs.RoundStateEvent())
	if err != nil {
		p.logger.Error("failed publishing valid block", "err", err)
	}
	p.emitter.Emit(types.EventValidBlockValue, &rs)
}

// PublishCommitEvent ...
func (p *EventPublisher) PublishCommitEvent(commit *types.Commit) error {
	p.logger.Debug("publish commit event", "commit", commit)
	if err := p.eventBus.PublishEventCommit(types.EventDataCommit{Commit: commit}); err != nil {
		return err
	}
	p.emitter.Emit(types.EventCommitValue, commit)
	return nil
}

// PublishPolkaEvent ...
func (p *EventPublisher) PublishPolkaEvent(rs cstypes.RoundState) {
	err := p.eventBus.PublishEventPolka(rs.RoundStateEvent())
	if err != nil {
		p.logger.Error("failed publishing polka", "err", err)
	}
}

// PublishRelockEvent ...
func (p *EventPublisher) PublishRelockEvent(rs cstypes.RoundState) {
	err := p.eventBus.PublishEventRelock(rs.RoundStateEvent())
	if err != nil {
		p.logger.Error("precommit step: failed publishing stateEvent relock", "err", err)
	}
}

// PublishLockEvent ...
func (p *EventPublisher) PublishLockEvent(rs cstypes.RoundState) {
	err := p.eventBus.PublishEventLock(rs.RoundStateEvent())
	if err != nil {
		p.logger.Error("precommit step: failed publishing stateEvent lock", "err", err)
	}
}

func (p *EventPublisher) PublishCompleteProposalEvent(event types.EventDataCompleteProposal) {
	err := p.eventBus.PublishEventCompleteProposal(event)
	if err != nil {
		p.logger.Error("failed publishing event complete proposal", "err", err)
	}
}

func (p *EventPublisher) PublishNewRoundEvent(event types.EventDataNewRound) {
	err := p.eventBus.PublishEventNewRound(event)
	if err != nil {
		p.logger.Error("failed publishing new round", "err", err)
	}
}

func (p *EventPublisher) PublishVoteEvent(vote *types.Vote) error {
	err := p.eventBus.PublishEventVote(types.EventDataVote{Vote: vote})
	if err != nil {
		return err
	}
	p.emitter.Emit(types.EventVoteValue, vote)
	return nil
}

func (p *EventPublisher) PublishNewRoundStepEvent(rs cstypes.RoundState) {
	event := rs.RoundStateEvent()
	if err := p.wal.Write(event); err != nil {
		p.logger.Error("failed writing to WAL", "err", err)
	}
	if err := p.eventBus.PublishEventNewRoundStep(rs.RoundStateEvent()); err != nil {
		p.logger.Error("failed publishing new round step", "err", err)
	}
	p.emitter.Emit(types.EventNewRoundStepValue, &rs)
}
