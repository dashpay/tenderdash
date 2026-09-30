package consensus

import (
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// commitExpectation is what the application expects of a commit for one block
// at one round: the threshold-recoverable extensions its ExtendVote returns for
// them, and whether it has rejected a commit carrying exactly that vector.
type commitExpectation struct {
	extensions []*tmproto.VoteExtension
	rejected   bool
}

// matches reports whether commit carries the expected vector.
func (exp *commitExpectation) matches(commit *types.Commit) bool {
	return types.SameVoteExtensionShape(commit.ThresholdVoteExtensions, exp.extensions)
}

// roundBlock identifies a block and the round it is processed for.
type roundBlock struct {
	round   int32
	blockID string
}

func commitRoundBlock(commit *types.Commit) roundBlock {
	return roundBlock{round: commit.Round, blockID: commit.BlockID.Key()}
}

// commitExpectations keeps, for one height, the application's expectation for
// every block and round a commit was rejected at, so that a commit which would
// need the block processed again for its round is refused without that work
// when its vector cannot be accepted.
//
// Why this bounds re-execution: the shape of a commit's extension list (order,
// count, duplication, the sign request ID of a THRESHOLD_RECOVER entry, and any
// app-supplied sign request ID, which binds no height or round) is not
// authenticated, so one genuine commit yields any number of signature-valid
// vectors, and a cache of verdicts on vectors can always be outrun. The block
// and round are authenticated, by the threshold block signature, and ExtendVote
// returns the one vector the application accepts for them. An entry therefore
// exists only for a block and round the quorum signed, and per height, commit
// checks force ProcessProposal at most:
//
//   - once per foreign round, to learn its expectation on the first rejection;
//   - once per foreign round for a commit carrying the expected vector, which
//     either finalizes the height or, from an application that rejects the
//     vector its own ExtendVote returned, is remembered as rejected;
//   - once per return to the node's own round after each of those.
//
// With G foreign rounds holding a commit for the held block, that is 2·G plus
// the execution that finalizes, from an application keeping the ExtendVote
// contract, whatever the number of peers and vectors; a contract-breaking
// application adds at most 2·G more.
//
// Expectations are learned only on a rejection, so an accepted commit costs no
// ExtendVote. Only the consensus goroutine touches this, like commitCandidates,
// so it needs no lock of its own.
type commitExpectations struct {
	height  int64
	byRound map[roundBlock]*commitExpectation
}

// lookup returns the expectation for commit's block and round, or nil.
func (e *commitExpectations) lookup(commit *types.Commit) *commitExpectation {
	if commit.Height != e.height {
		return nil
	}
	return e.byRound[commitRoundBlock(commit)]
}

// learn records extensions as the expectation for commit's block and round,
// dropping every expectation of another height first, and returns it.
func (e *commitExpectations) learn(commit *types.Commit, extensions []*tmproto.VoteExtension) *commitExpectation {
	if commit.Height != e.height || e.byRound == nil {
		e.height = commit.Height
		e.byRound = make(map[roundBlock]*commitExpectation)
	}
	exp := &commitExpectation{extensions: extensions}
	e.byRound[commitRoundBlock(commit)] = exp
	return exp
}
