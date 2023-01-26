package types

import (
	"context"
	"time"

	"github.com/tendermint/tendermint/types"
)

type ProposalSetter interface {
	Set(proposal *types.Proposal, receivedAt time.Time, rs *RoundState) error
}

type ProposalDecider interface {
	Decide(ctx context.Context, height int64, round int32, rs *RoundState) error
}

type Proposaler interface {
	ProposalDecider
	ProposalSetter
}
