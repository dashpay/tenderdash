package consensus

import (
	"bytes"
	"errors"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"

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
	// livenessRelayEventName is emitted with a livenessRelay for each proposer
	// heartbeat or round skip vote from a peer that was news to this node, for
	// the reactor to pass on to its other peers.
	livenessRelayEventName = "livenessRelay"

	// maxRoundSkipRoundsAhead is how many rounds past its own a node collects
	// skip votes for. Validators of one height can be a round or two apart, and
	// the bound keeps a peer from making the node hold votes for any round it
	// cares to name.
	maxRoundSkipRoundsAhead = 4
)

// livenessRelay is a proposer heartbeat or round skip vote from a peer, for the
// reactor to relay.
type livenessRelay struct {
	// msg is a *ProposerHeartbeatMessage or a *RoundSkipVoteMessage.
	msg Message
	// from is the peer it came from.
	from types.NodeID
}

// errRoundSkipVoteWrongValidator is returned for a skip vote whose validator
// index and pro tx hash name different validators.
var errRoundSkipVoteWrongValidator = errors.New("round skip vote names a validator that is not at its index")

// roundSkipCollector gathers the skip votes of the current height and recovers
// the certificate for a round once validators holding +2/3 of the voting power
// have voted to skip it.
//
// It runs on the consensus goroutine, which counts votes from peers as the peer
// scheduler hands them over, this validator's own vote, and follows the height
// and round (track). The reactor only screens votes from peers before queuing
// them (wanted), and asks which counted votes to relay again (relayAgain). It
// holds no consensus state: a certificate it recovers goes to the state machine
// as a message, like one received from a peer.
type roundSkipCollector struct {
	logger log.Logger
	// budget is the node-wide verification budget a peer's skip vote draws on
	// before its signature is checked.
	budget types.VerificationBudget
	// clock times relays (see relayAgain); the wall clock unless a test
	// overrides it.
	clock clockwork.Clock

	mtx sync.Mutex
	// enabled is whether the chain skips rounds (roundSkipping). Where it does
	// not, no honest validator signs a skip vote, so none is collected.
	enabled bool
	height  int64
	round   int32
	chainID string
	valSet  *types.ValidatorSet
	// timeouts are those of the height, which pace relays (see relayAgain).
	timeouts types.TimeoutParams
	rounds   map[int32]*roundSkipVotes
}

type roundSkipVotes struct {
	votes map[string]*types.RoundSkipVote
	// relayed holds when each vote was last passed on to peers, by validator.
	relayed map[string]time.Time
	power   int64
	// recovered is set while a certificate is being recovered from these
	// votes, and stays set once one was.
	recovered bool
	// certified is set once a certificate was recovered, after which more
	// votes for the round add nothing and are not even verified.
	certified bool
}

func newRoundSkipCollector(logger log.Logger, budget types.VerificationBudget) *roundSkipCollector {
	return &roundSkipCollector{
		logger: logger,
		budget: budget,
		clock:  clockwork.NewRealClock(),
		rounds: make(map[int32]*roundSkipVotes),
	}
}

// track follows the height and round the node is in. Votes of an earlier height
// are dropped when the height changes.
func (c *roundSkipCollector) track(stateData *StateData) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	c.enabled = roundSkipping(stateData)
	if c.height != stateData.Height || !sameQuorum(c.valSet, stateData.Validators) {
		c.height = stateData.Height
		c.chainID = stateData.state.ChainID
		c.rounds = make(map[int32]*roundSkipVotes)
	}
	c.valSet = stateData.Validators
	c.timeouts = stateData.state.ConsensusParams.Timeout.TimeoutParamsOrDefaults()
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
	check, worth, err := c.screen(vote, verify)
	if !worth {
		return false, nil, err
	}
	height, chainID, valSet, val := check.height, check.chainID, check.valSet, check.val
	if verify {
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
		votes = &roundSkipVotes{
			votes:   make(map[string]*types.RoundSkipVote),
			relayed: make(map[string]time.Time),
		}
		c.rounds[vote.Round] = votes
	}
	// A vote is relayed, or broadcast if it is this validator's own, as it is
	// counted.
	votes.votes[vote.ValidatorProTxHash.String()] = vote
	votes.relayed[vote.ValidatorProTxHash.String()] = c.clock.Now()
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
			c.mtx.Lock()
			votes.certified = true
			c.mtx.Unlock()
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

// skipVoteCheck is what screen found out about a skip vote: the height and
// validator set the node collects votes for, and the validator that cast it.
type skipVoteCheck struct {
	height  int64
	chainID string
	valSet  *types.ValidatorSet
	val     *types.Validator
}

// screen runs the checks on a skip vote that need no signature verification.
// It reports whether the vote is worth counting, and returns an error only for
// one no honest peer would have sent. With verify set, as for a vote from a
// peer, that includes whether this node can check the vote at all.
func (c *roundSkipCollector) screen(vote *types.RoundSkipVote, verify bool) (skipVoteCheck, bool, error) {
	c.mtx.Lock()
	check := skipVoteCheck{height: c.height, chainID: c.chainID, valSet: c.valSet}
	enabled, round := c.enabled, c.round
	known := c.knownLocked(vote) || c.certifiedLocked(vote.Round)
	c.mtx.Unlock()

	if !enabled {
		return check, false, nil
	}
	// Validators of one height are not all in the same round at once, so a
	// vote for a round this node has left, or is too far from, says nothing
	// wrong about its sender.
	if check.valSet == nil || vote.Height != check.height ||
		vote.Round < round || vote.Round > round+maxRoundSkipRoundsAhead {
		return check, false, nil
	}
	if known {
		return check, false, nil
	}
	check.val = check.valSet.GetByIndex(vote.ValidatorIndex)
	if check.val == nil || !check.val.ProTxHash.Equal(vote.ValidatorProTxHash) {
		return check, false, errRoundSkipVoteWrongValidator
	}
	// A node outside the validator set does not hold the members' key shares
	// and cannot check the vote, so it neither counts nor relays it.
	if verify && check.val.PubKey == nil {
		return check, false, nil
	}
	return check, true, nil
}

// wanted reports whether a skip vote from a peer is worth verifying (see
// screen). The reactor asks before queuing one for the consensus goroutine, so
// a vote that cannot count takes no place in its sender's lane.
func (c *roundSkipCollector) wanted(vote *types.RoundSkipVote) (bool, error) {
	_, worth, err := c.screen(vote, true)
	return worth, err
}

// relayAgain returns the vote this node counted for vote's validator and round
// when it is due to be relayed again, and nil otherwise.
//
// A node relays a vote as it counts it, and that relay can be dropped on its
// way out. Every validator sends its own vote again each silence timeout while
// its round stays silent, but to a node that counted it those copies are not
// news, so without this a node bridging two parts of the validator set would
// never pass the vote on again. A copy that arrives at least half a silence
// timeout after the last relay has the counted vote relayed once more, until
// the round is certified. That is at most two relays per vote per silence
// timeout, whoever sends the copies, and none needs a signature check: what is
// relayed is the vote this node verified, not the copy.
func (c *roundSkipCollector) relayAgain(vote *types.RoundSkipVote) *types.RoundSkipVote {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	if !c.enabled || vote.Height != c.height {
		return nil
	}
	votes := c.rounds[vote.Round]
	if votes == nil || votes.certified {
		return nil
	}
	key := vote.ValidatorProTxHash.String()
	counted := votes.votes[key]
	if counted == nil {
		return nil
	}
	now := c.clock.Now()
	if now.Sub(votes.relayed[key]) < c.timeouts.ProposerSilenceTimeout(vote.Round)/2 {
		return nil
	}
	votes.relayed[key] = now
	return counted
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

// certifiedLocked reports whether a certificate was recovered for round.
func (c *roundSkipCollector) certifiedLocked(round int32) bool {
	votes := c.rounds[round]
	return votes != nil && votes.certified
}

func (c *roundSkipCollector) knownLocked(vote *types.RoundSkipVote) bool {
	votes := c.rounds[vote.Round]
	if votes == nil {
		return false
	}
	_, ok := votes.votes[vote.ValidatorProTxHash.String()]
	return ok
}
