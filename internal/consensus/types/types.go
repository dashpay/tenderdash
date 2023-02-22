package types

import (
	"context"
	"time"

	"github.com/tendermint/tendermint/types"
)

// ProposalSetter updates a proposal for the round if all conditions are met
type ProposalSetter interface {
	Set(proposal *types.Proposal, receivedAt time.Time, rs *RoundState) error
}

// ProposalCreator creates and updates RoundState with a new proposal for a round if a validator is the proposer
// and the proposal wasn't created yet
type ProposalCreator interface {
	Create(ctx context.Context, height int64, round int32, rs *RoundState) error
}

// Proposaler is the interface that groups the ProposalSetter and ProposalCreator interfaces
type Proposaler interface {
	ProposalCreator
	ProposalSetter
}
