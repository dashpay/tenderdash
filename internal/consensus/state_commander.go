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
// EventType and AppState are required for a call
// Data is optional
type StateEvent struct {
	EventType EventType
	AppState  *AppState
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
