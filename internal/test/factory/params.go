package factory

import (
	"time"

	"github.com/dashpay/tenderdash/types"
)

// ConsensusParams returns a default set of ConsensusParams that are suitable
// for use in testing
func ConsensusParams(opts ...func(*types.ConsensusParams)) *types.ConsensusParams {
	c := types.DefaultConsensusParams()
	c.Timeout = types.TimeoutParams{
		Propose:      40 * time.Millisecond,
		ProposeDelta: 1 * time.Millisecond,
		Vote:         10 * time.Millisecond,
		VoteDelta:    1 * time.Millisecond,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}
