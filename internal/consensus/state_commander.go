package consensus

import (
	"context"
	"errors"
)

type EventType int

// All possible event types
const (
	EnterNewRoundType EventType = iota + 1
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
	EventType EventType
	StateData *StateData
	Data      any
}

// CommandHandler is a command handler interface
type CommandHandler interface {
	Execute(ctx context.Context, behavior *Behavior, event StateEvent) (any, error)
}

type CommandExecutor struct {
	commands map[EventType]CommandHandler
}

// Register adds or overrides a command handler for an event-type
func (c *CommandExecutor) Register(eventType EventType, handler CommandHandler) {
	c.commands[eventType] = handler
}

// Get returns a command handler by an event-type, if command not is existed then returns nil
func (c *CommandExecutor) Get(eventType EventType) CommandHandler {
	return c.commands[eventType]
}

// Execute executes a command for a given state-event
// panic if a command is not registered
func (c *CommandExecutor) Execute(ctx context.Context, behavior *Behavior, event StateEvent) (any, error) {
	command, ok := c.commands[event.EventType]
	if !ok {
		panic(errCommandNotRegistered)
	}
	return command.Execute(ctx, behavior, event)
}

// NewCommandExecutor ...
func NewCommandExecutor(cs *State, eventPublisher *EventPublisher, wal *wrapWAL) *CommandExecutor {
	return &CommandExecutor{
		commands: map[EventType]CommandHandler{
			EnterNewRoundType: &EnterNewRoundCommand{
				logger:         cs.logger,
				config:         cs.config,
				eventPublisher: eventPublisher,
			},
			EnterProposeType: &EnterProposeCommand{
				logger:        cs.logger,
				privValidator: cs.privValidator,
				msgInfoQueue:  cs.msgInfoQueue,
				wal:           cs.wal,
				replayMode:    cs.replayMode,
				metrics:       cs.metrics,
				blockExec:     cs.blockExecutor,
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
				eventPublisher: eventPublisher,
			},
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
				eventPublisher: eventPublisher,
				blockExec:      cs.blockExec,
				metrics:        cs.metrics,
			},
			EnterCommitType: &EnterCommitCommand{
				logger:         cs.logger,
				eventPublisher: eventPublisher,
				metrics:        cs.metrics,
				evsw:           cs.evsw,
			},
			EnterPrevoteType: &EnterPrevoteCommand{
				logger: cs.logger,
			},
			EnterPrecommitType: &EnterPrecommitCommand{
				logger:         cs.logger,
				eventPublisher: eventPublisher,
				blockExec:      cs.blockExecutor,
				voteSigner:     cs.voteSigner,
			},
			TryAddCommitType: &TryAddCommitCommand{
				logger:         cs.logger,
				blockExec:      cs.blockExecutor,
				eventPublisher: eventPublisher,
			},
			AddCommitType: &AddCommitCommand{
				eventPublisher: eventPublisher,
			},
			ApplyCommitType: &ApplyCommitCommand{
				logger:     cs.logger,
				blockStore: cs.blockStore,
				blockExec:  cs.blockExecutor,
				wal:        wal,
			},
			TryFinalizeCommitType: &TryFinalizeCommitCommand{
				logger:     cs.logger,
				blockExec:  cs.blockExecutor,
				blockStore: cs.blockStore,
			},
			EnterPrevoteWaitType: &EnterPrevoteWaitCommand{
				logger: cs.logger,
			},
			EnterPrecommitWaitType: &EnterPrecommitWaitCommand{
				logger: cs.logger,
			},
		},
	}
}
