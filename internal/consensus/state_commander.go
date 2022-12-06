package consensus

import (
	"context"
)

type EventType int

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

type StateEvent struct {
	EventType EventType
	AppState  *AppState
	Data      any
}

type CommandHandler interface {
	Execute(ctx context.Context, behaviour *Behaviour, event StateEvent) (any, error)
}

type CommandExecutor struct {
	commands map[EventType]CommandHandler
}

func (c *CommandExecutor) Register(eventType EventType, handler CommandHandler) {
	c.commands[eventType] = handler
}

func (c *CommandExecutor) Execute(ctx context.Context, behaviour *Behaviour, event StateEvent) (any, error) {
	command, ok := c.commands[event.EventType]
	if !ok {
		panic("unknown command")
	}
	return command.Execute(ctx, behaviour, event)
}
