package consensus

import (
	"bytes"
	"errors"
	"sync"

	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

const (
	// roundSkipVoteEventName is emitted with each skip vote this validator signs,
	// for the reactor to broadcast.
	roundSkipVoteEventName = "roundSkipVote"
	// roundSkipCertificateEventName is emitted with each certificate this node
	// applies, for the reactor to broadcast.
	roundSkipCertificateEventName = "roundSkipCertificate"

	// maxRoundSkipRoundsAhead is how many rounds past its own a node collects
	// skip votes for. Validators of one height can be a round or two apart, and
	// the bound keeps a peer from making the node hold votes for any round it
	// cares to name.
	maxRoundSkipRoundsAhead = 4
)

// errRoundSkipVoteWrongValidator is returned for a skip vote whose validator
// index and pro tx hash name different validators.
var errRoundSkipVoteWrongValidator = errors.New("round skip vote names a validator that is not at its index")

// roundSkipCollector gathers the skip votes of the current height and recovers
// the certificate for a round once validators holding +2/3 of the voting power
// have voted to skip it.
//
// It runs on the reactor's goroutine for votes from peers and on the consensus
// goroutine for this validator's own vote and for following the height and
// round (track). It holds no consensus state: a certificate it recovers goes to
// the state machine as a message, like one received from a peer.
type roundSkipCollector struct {
	logger log.Logger
	// budget is the node-wide verification budget a peer's skip vote draws on
	// before its signature is checked.
	budget types.VerificationBudget

	mtx sync.Mutex
	// enabled is whether the chain is at a consensus version that skips rounds.
	// Before that, no honest validator signs a skip vote, so none is collected.
	enabled bool
	height  int64
	round   int32
	chainID string
	valSet  *types.ValidatorSet
	rounds  map[int32]*roundSkipVotes
}

type roundSkipVotes struct {
	votes map[string]*types.RoundSkipVote
	power int64
	// recovered is set once a certificate was recovered from these votes, after
	// which more votes for the round add nothing.
	recovered bool
}

func newRoundSkipCollector(logger log.Logger, budget types.VerificationBudget) *roundSkipCollector {
	return &roundSkipCollector{
		logger: logger,
		budget: budget,
		rounds: make(map[int32]*roundSkipVotes),
	}
}

// track follows the height and round the node is in. Votes of an earlier height
// are dropped when the height changes.
func (c *roundSkipCollector) track(stateData *StateData) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	c.enabled = stateData.state.ConsensusParams.Version.ProposerLivenessEnabled()
	if c.height != stateData.Height || !sameQuorum(c.valSet, stateData.Validators) {
		c.height = stateData.Height
		c.chainID = stateData.state.ChainID
		c.rounds = make(map[int32]*roundSkipVotes)
	}
	c.valSet = stateData.Validators
	if c.round != stateData.Round {
		c.round = stateData.Round
		for round := range c.rounds {
			if round < c.round {
				delete(c.rounds, round)
			}
		}
	}
}

// add takes a skip vote. A vote from a peer is verified first; this
// validator's own vote is not. It reports whether the vote was new, so it should
// be relayed, and returns the certificate if the vote completed +2/3 for its
// round. It returns an error only for a vote no honest peer would have sent.
func (c *roundSkipCollector) add(vote *types.RoundSkipVote, verify bool) (bool, *types.RoundSkipCertificate, error) {
	c.mtx.Lock()
	enabled, height, round, chainID, valSet := c.enabled, c.height, c.round, c.chainID, c.valSet
	known := c.knownLocked(vote)
	c.mtx.Unlock()

	if !enabled {
		return false, nil, nil
	}
	// Validators of one height are not all in the same round at once, so a
	// vote for a round this node has left, or is too far from, says nothing
	// wrong about its sender.
	if valSet == nil || vote.Height != height || vote.Round < round || vote.Round > round+maxRoundSkipRoundsAhead {
		return false, nil, nil
	}
	if known {
		return false, nil, nil
	}
	val := valSet.GetByIndex(vote.ValidatorIndex)
	if val == nil || !val.ProTxHash.Equal(vote.ValidatorProTxHash) {
		return false, nil, errRoundSkipVoteWrongValidator
	}
	if verify {
		// A node outside the validator set does not hold the members' key
		// shares and cannot check the vote, so it neither counts nor relays it.
		if val.PubKey == nil {
			return false, nil, nil
		}
		// A signature verification like a vote's draws on the node-wide
		// budget. A vote this node cannot afford now is dropped: every
		// validator that counts it relays it, so another copy follows.
		if c.budget != nil && !c.budget.Allow(baseMessageCost) {
			return false, nil, nil
		}
		if err := vote.Verify(chainID, valSet.QuorumType, valSet.QuorumHash, val.PubKey); err != nil {
			return false, nil, err
		}
	}

	c.mtx.Lock()
	if c.height != height || !sameQuorum(c.valSet, valSet) || c.knownLocked(vote) {
		c.mtx.Unlock()
		return false, nil, nil
	}
	votes := c.rounds[vote.Round]
	if votes == nil {
		votes = &roundSkipVotes{votes: make(map[string]*types.RoundSkipVote)}
		c.rounds[vote.Round] = votes
	}
	votes.votes[vote.ValidatorProTxHash.String()] = vote
	votes.power += val.VotingPower
	if votes.recovered || votes.power < types.RoundSkipThresholdPower(valSet) {
		c.mtx.Unlock()
		return true, nil, nil
	}
	// Set before recovering, so a vote counted meanwhile does not start a
	// second recovery, and cleared again if this one fails.
	votes.recovered = true
	collected := votes.collect()
	c.mtx.Unlock()

	for {
		certificate, err := types.RecoverRoundSkipCertificate(chainID, valSet, collected)
		if err == nil {
			return true, certificate, nil
		}
		c.logger.Error("cannot recover round skip certificate", "height", vote.Height, "round", vote.Round, "err", err)

		c.mtx.Lock()
		next, retry := c.afterFailedRecoveryLocked(vote.Round, votes, collected)
		c.mtx.Unlock()
		if !retry {
			return true, nil, nil
		}
		collected = next
	}
}

// afterFailedRecoveryLocked decides what a recovery from collected that failed
// does next. Votes counted while it ran did not try a recovery of their own,
// so if there are any it tries again with them. Otherwise it leaves the round
// to the next vote, which tries again with one more share.
func (c *roundSkipCollector) afterFailedRecoveryLocked(
	round int32,
	votes *roundSkipVotes,
	collected []*types.RoundSkipVote,
) ([]*types.RoundSkipVote, bool) {
	if len(votes.votes) == len(collected) || c.rounds[round] != votes {
		votes.recovered = false
		return nil, false
	}
	return votes.collect(), true
}

// collect returns the votes counted so far. The caller holds the collector's
// lock.
func (v *roundSkipVotes) collect() []*types.RoundSkipVote {
	collected := make([]*types.RoundSkipVote, 0, len(v.votes))
	for _, vote := range v.votes {
		collected = append(collected, vote)
	}
	return collected
}

// voteOf returns the skip vote the validator with proTxHash cast for height and
// round, if it was counted.
func (c *roundSkipCollector) voteOf(height int64, round int32, proTxHash types.ProTxHash) *types.RoundSkipVote {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	if c.height != height {
		return nil
	}
	votes := c.rounds[round]
	if votes == nil {
		return nil
	}
	return votes.votes[proTxHash.String()]
}

// sameQuorum reports whether a and b are the validator set of one quorum.
func sameQuorum(a, b *types.ValidatorSet) bool {
	if a == nil || b == nil {
		return a == b
	}
	return bytes.Equal(a.QuorumHash, b.QuorumHash)
}

func (c *roundSkipCollector) knownLocked(vote *types.RoundSkipVote) bool {
	votes := c.rounds[vote.Round]
	if votes == nil {
		return false
	}
	_, ok := votes.votes[vote.ValidatorProTxHash.String()]
	return ok
}
