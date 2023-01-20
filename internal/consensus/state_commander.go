package consensus

import (
	"context"
	"errors"
)

type EventType int

// All possible event types
const (
	EnterNewRoundType EventType = iota
	EnterProposeType
	SetProposalType
	DecideProposalType
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
	TryAddVoteType
	DoPrevoteType
)

var (
	errCommandNotRegistered = errors.New("a command is not registered")
)

// StateEvent uses to execute a command handler
// EventType and StateData are required for a call
// Data is optional
type StateEvent struct {
	FSM       *FSM
	EventType EventType
	StateData *StateData
	Data      FSMEvent
}

type FSMEvent interface {
	GetType() EventType
}

// CommandHandler is a command handler interface
type CommandHandler interface {
	Execute(ctx context.Context, event StateEvent) error
}

// FSM ...
type FSM struct {
	commands map[EventType]CommandHandler
}

// Register adds or overrides a command handler for an event-type
func (c *FSM) Register(eventType EventType, handler CommandHandler) {
	c.commands[eventType] = handler
}

// Get returns a command handler by an event-type, if command not is existed then returns nil
func (c *FSM) Get(eventType EventType) CommandHandler {
	return c.commands[eventType]
}

// NewFSM returns a new instance of finite-state-machine with a set of all possible transitions
func NewFSM(cs *State, wal *wrapWAL, statsQueue *chanQueue[msgInfo]) *FSM {
	propUpdater := &proposalUpdater{
		logger:         cs.logger,
		eventPublisher: cs.eventPublisher,
	}
	fsm := &FSM{}
	fsm.commands = map[EventType]CommandHandler{
		EnterNewRoundType: &EnterNewRoundCommand{
			logger:         cs.logger,
			config:         cs.config,
			scheduler:      cs.roundScheduler,
			eventPublisher: cs.eventPublisher,
		},
		EnterProposeType: &EnterProposeCommand{
			logger:         cs.logger,
			privValidator:  cs.privValidator,
			msgInfoQueue:   cs.msgInfoQueue,
			wal:            cs.wal,
			replayMode:     cs.replayMode,
			metrics:        cs.metrics,
			blockExec:      cs.blockExecutor,
			scheduler:      cs.roundScheduler,
			eventPublisher: cs.eventPublisher,
		},
		SetProposalType: &SetProposalCommand{
			logger:  cs.logger,
			metrics: cs.metrics,
		},
		DecideProposalType: &DecideProposalCommand{
			logger:        cs.logger,
			privValidator: cs.privValidator,
			msgInfoQueue:  cs.msgInfoQueue,
			wal:           cs.wal,
			metrics:       cs.metrics,
			blockExec:     cs.blockExecutor,
			replayMode:    cs.replayMode,
		},
		AddProposalBlockPartType: &AddProposalBlockPartCommand{
			logger:         cs.logger,
			metrics:        cs.metrics,
			blockExec:      cs.blockExecutor,
			eventPublisher: cs.eventPublisher,
			statsQueue:     statsQueue,
		},
		ProposalCompletedType: &ProposalCompletedCommand{logger: cs.logger},
		DoPrevoteType: &DoPrevoteCommand{
			logger:     cs.logger,
			voteSigner: cs.voteSigner,
			blockExec:  cs.blockExecutor,
			metrics:    cs.metrics,
			replayMode: cs.replayMode,
		},
		TryAddVoteType: &TryAddVoteCommand{
			evpool:         cs.evpool,
			logger:         cs.logger,
			privValidator:  cs.privValidator,
			eventPublisher: cs.eventPublisher,
			blockExec:      cs.blockExec,
			metrics:        cs.metrics,
			statsQueue:     statsQueue,
		},
		EnterCommitType: &EnterCommitCommand{
			logger:          cs.logger,
			eventPublisher:  cs.eventPublisher,
			metrics:         cs.metrics,
			proposalUpdater: propUpdater,
		},
		EnterPrevoteType: &EnterPrevoteCommand{
			logger:         cs.logger,
			eventPublisher: cs.eventPublisher,
		},
		EnterPrecommitType: &EnterPrecommitCommand{
			logger:         cs.logger,
			eventPublisher: cs.eventPublisher,
			blockExec:      cs.blockExecutor,
			voteSigner:     cs.voteSigner,
		},
		TryAddCommitType: &TryAddCommitCommand{
			logger:         cs.logger,
			blockExec:      cs.blockExecutor,
			eventPublisher: cs.eventPublisher,
		},
		AddCommitType: &AddCommitCommand{
			eventPublisher:  cs.eventPublisher,
			statsQueue:      statsQueue,
			proposalUpdater: propUpdater,
		},
		ApplyCommitType: &ApplyCommitCommand{
			logger:         cs.logger,
			blockStore:     cs.blockStore,
			blockExec:      cs.blockExecutor,
			wal:            wal,
			scheduler:      cs.roundScheduler,
			metrics:        cs.metrics,
			eventPublisher: cs.eventPublisher,
		},
		TryFinalizeCommitType: &TryFinalizeCommitCommand{
			logger:     cs.logger,
			blockExec:  cs.blockExecutor,
			blockStore: cs.blockStore,
		},
		EnterPrevoteWaitType: &EnterPrevoteWaitCommand{
			logger:         cs.logger,
			scheduler:      cs.roundScheduler,
			eventPublisher: cs.eventPublisher,
		},
		EnterPrecommitWaitType: &EnterPrecommitWaitCommand{
			logger:         cs.logger,
			scheduler:      cs.roundScheduler,
			eventPublisher: cs.eventPublisher,
		},
	}
	for _, command := range fsm.commands {
		sub, ok := command.(eventSwitchSubscriber)
		if ok {
			sub.subscribe(cs.evsw)
		}
	}
	return fsm
}

// Dispatch dispatches an event to a handler
func (c *FSM) Dispatch(ctx context.Context, event FSMEvent, stateData *StateData) error {
	stateEvent := StateEvent{
		FSM:       c,
		EventType: event.GetType(),
		StateData: stateData,
		Data:      event,
	}
	if int(event.GetType()) >= len(c.commands) {
		panic(errCommandNotRegistered)
	}
	stateEvent.FSM = c
	return c.commands[event.GetType()].Execute(ctx, stateEvent)
}
