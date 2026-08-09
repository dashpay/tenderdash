package types

import (
	"context"
	"time"

	"github.com/dashpay/tenderdash/types"
)

// ProposalSetter updates a proposal for the round if all conditions are met.
//
// The context carries the permit the proposal's signature check is drawn
// against, so a caller that received the proposal from a peer can bound the
// verification work it forces.
type ProposalSetter interface {
	Set(ctx context.Context, proposal *types.Proposal, receivedAt time.Time, rs *RoundState) error
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
