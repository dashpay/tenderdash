package consensus

import (
	"context"
	"errors"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/libs/eventemitter"
)

// EventType is an integer representation of a transition event
type EventType int

// All possible event types
const (
	EnterNewRoundType EventType = iota
	EnterProposeType
	EnterPrevoteType
	EnterPrecommitType
	EnterCommitType
	TryAddCommitType
	AddCommitType
	TryFinalizeCommitType
	ApplyCommitType
	AddProposalBlockPartType
	ProposalCompletedType
	EnterPrevoteWaitType
	EnterPrecommitWaitType
	AddVoteType
)

var (
	errActionNotRegistered = errors.New("an action is not registered")
)

// StateEvent uses to execute an action handler
// EventType and StateData are required for a call
// Data is optional
type StateEvent struct {
	Ctrl      *Controller
	EventType EventType
	StateData *StateData
	Data      ActionEvent
}

type ActionEvent interface {
	GetType() EventType
}

// ActionHandler is an action handler interface
type ActionHandler interface {
	Execute(ctx context.Context, event StateEvent) error
}

// Controller is responsible for registering and dispatching an event to an action
type Controller struct {
	actions map[EventType]ActionHandler
}

// Register adds or overrides a action handler for an event-type
func (c *Controller) Register(eventType EventType, handler ActionHandler) {
	c.actions[eventType] = handler
}

// Get returns an action handler by an event-type, if the action is not existed then returns nil
func (c *Controller) Get(eventType EventType) ActionHandler {
	return c.actions[eventType]
}

// NewController returns a new instance of a controller with a set of all possible transitions (actions)
func NewController(cs *State, wal *wrapWAL, statsQueue *chanQueue[msgInfo], propler cstypes.Proposaler) *Controller {
	propUpdater := &proposalUpdater{
		logger:         cs.logger,
		eventPublisher: cs.eventPublisher,
	}
	ctrl := &Controller{}
	ctrl.actions = map[EventType]ActionHandler{
		EnterNewRoundType: &EnterNewRoundAction{
			logger:         cs.logger,
			config:         cs.config,
			scheduler:      cs.roundScheduler,
			eventPublisher: cs.eventPublisher,
		},
		EnterProposeType: &EnterProposeAction{
			logger:          cs.logger,
			wal:             wal,
			scheduler:       cs.roundScheduler,
			eventPublisher:  cs.eventPublisher,
			proposalCreator: propler,
			replayMode:      cs.replayMode,
		},
		AddProposalBlockPartType: &AddProposalBlockPartAction{
			logger:         cs.logger,
			metrics:        cs.metrics,
			blockExec:      cs.blockExecutor,
			eventPublisher: cs.eventPublisher,
			statsQueue:     statsQueue,
		},
		ProposalCompletedType: &ProposalCompletedAction{logger: cs.logger},
		AddVoteType:           newAddVoteAction(cs, ctrl, statsQueue),
		EnterCommitType: &EnterCommitAction{
			logger:          cs.logger,
			eventPublisher:  cs.eventPublisher,
			metrics:         cs.metrics,
			proposalUpdater: propUpdater,
		},
		EnterPrevoteType: &EnterPrevoteAction{
			logger:         cs.logger,
			eventPublisher: cs.eventPublisher,
			prevoter:       newPrevote(cs.logger, cs.voteSigner, cs.blockExecutor, cs.metrics),
		},
		EnterPrecommitType: &EnterPrecommitAction{
			logger:         cs.logger,
			eventPublisher: cs.eventPublisher,
			blockExec:      cs.blockExecutor,
			voteSigner:     cs.voteSigner,
		},
		TryAddCommitType: &TryAddCommitAction{
			logger:         cs.logger,
			blockExec:      cs.blockExecutor,
			eventPublisher: cs.eventPublisher,
		},
		AddCommitType: &AddCommitAction{
			eventPublisher:  cs.eventPublisher,
			statsQueue:      statsQueue,
			proposalUpdater: propUpdater,
		},
		ApplyCommitType: &ApplyCommitAction{
			logger:         cs.logger,
			blockStore:     cs.blockStore,
			blockExec:      cs.blockExecutor,
			wal:            wal,
			scheduler:      cs.roundScheduler,
			metrics:        cs.metrics,
			eventPublisher: cs.eventPublisher,
		},
		TryFinalizeCommitType: &TryFinalizeCommitAction{
			logger:     cs.logger,
			blockExec:  cs.blockExecutor,
			blockStore: cs.blockStore,
		},
		EnterPrevoteWaitType: &EnterPrevoteWaitAction{
			logger:         cs.logger,
			scheduler:      cs.roundScheduler,
			eventPublisher: cs.eventPublisher,
		},
		EnterPrecommitWaitType: &EnterPrecommitWaitAction{
			logger:         cs.logger,
			scheduler:      cs.roundScheduler,
			eventPublisher: cs.eventPublisher,
		},
	}
	subscribeActions(cs.emitter, ctrl.actions)
	return ctrl
}

// Dispatch dispatches an event to a handler
func (c *Controller) Dispatch(ctx context.Context, event ActionEvent, stateData *StateData) error {
	if int(event.GetType()) >= len(c.actions) {
		panic(errActionNotRegistered)
	}
	stateEvent := StateEvent{
		Ctrl:      c,
		EventType: event.GetType(),
		StateData: stateData,
		Data:      event,
	}
	return c.actions[event.GetType()].Execute(ctx, stateEvent)
}

func subscribeActions(emitter *eventemitter.EventEmitter, actions map[EventType]ActionHandler) {
	for _, action := range actions {
		sub, ok := action.(eventemitter.Subscriber)
		if ok {
			sub.Subscribe(emitter)
		}
	}
}
