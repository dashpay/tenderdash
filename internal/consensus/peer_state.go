package consensus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/jonboulle/clockwork"
	"github.com/rs/zerolog"

	"github.com/dashpay/tenderdash/config"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	"github.com/dashpay/tenderdash/libs/bits"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/math"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

var (
	ErrPeerStateHeightRegression = errors.New("peer state height regression")
	ErrPeerStateInvalidStartTime = errors.New("peer state invalid startTime")
	ErrPeerStateSetNilVote       = errors.New("peer state set a nil vote")
	ErrPeerStateInvalidVoteIndex = errors.New("peer sent a vote with an invalid vote index")
)

// peerStateStats holds internal statistics for a peer.
type peerStateStats struct {
	Votes      int `json:"votes,string"`
	BlockParts int `json:"block_parts,string"`
	Commits    int `json:"commits,string"`
}

func (pss peerStateStats) String() string {
	return fmt.Sprintf("peerStateStats{votes: %d, commits: %d, blockParts: %d}", pss.Votes, pss.Commits, pss.BlockParts)
}

// PeerState contains the known state of a peer, including its connection and
// threadsafe access to its PeerRoundState.
// NOTE: THIS GETS DUMPED WITH rpc/core/consensus.go.
// Be mindful of what you Expose.
type PeerState struct {
	peerID types.NodeID
	logger log.Logger
	// clock is the time source the majority-claim answer memory is dated
	// against; the wall clock unless a test overrides it.
	clock clockwork.Clock
	// maj23AnswerTTL is how long an answer keeps the same majority claim from
	// being answered again.
	maj23AnswerTTL time.Duration

	// NOTE: Modify below using setters, never directly.
	mtx     sync.RWMutex
	cancel  context.CancelFunc
	running bool
	PRS     cstypes.PeerRoundState `json:"round_state"`
	Stats   *peerStateStats        `json:"stats"`

	// answeredMaj23 records the majority claims this node has most recently
	// answered for the peer, and how many of our own votes each answer carried.
	// Together they decide whether repeating a claim can tell the peer anything
	// new.
	answeredMaj23     [maj23AnswerMemory]maj23Answer
	answeredMaj23Next int

	// ProTxHash is accessible only for the validator
	ProTxHash types.ProTxHash

	// connID is the generation of the connection this peer state currently stands
	// for, and laneSession the scheduler session it was admitted under. They are
	// set together at peer-up and read together, so a message is matched against
	// the connection that produced it (by the immutable generation the router
	// stamped on it) and, only if that connection is still live, admitted under
	// its session.
	connID      uint64
	laneSession uint64
}

// PeerStateOptionFunc overrides a default of a PeerState.
type PeerStateOptionFunc func(*PeerState)

// WithPeerStateClock sets the time source the peer state meters against. The
// default is the wall clock; a test injects a fake clock to advance time
// explicitly.
func WithPeerStateClock(clock clockwork.Clock) PeerStateOptionFunc {
	return func(ps *PeerState) {
		ps.clock = clock
	}
}

// WithMaj23AnswerTTL sets how long an answered majority claim keeps the same
// claim from being answered again, which is counted in the gossip passes the
// peer makes.
func WithMaj23AnswerTTL(ttl time.Duration) PeerStateOptionFunc {
	return func(ps *PeerState) {
		ps.maj23AnswerTTL = ttl
	}
}

// NewPeerState returns a new PeerState for the given node ID.
func NewPeerState(logger log.Logger, peerID types.NodeID, opts ...PeerStateOptionFunc) *PeerState {
	ps := &PeerState{
		peerID: peerID,
		logger: logger.With("peer", peerID),
		clock:  clockwork.NewRealClock(),
		PRS: cstypes.PeerRoundState{
			Round:              -1,
			ProposalPOLRound:   -1,
			LastCommitRound:    -1,
			CatchupCommitRound: -1,
		},
		Stats: &peerStateStats{},
	}
	for _, opt := range opts {
		opt(ps)
	}
	if ps.maj23AnswerTTL <= 0 {
		ps.maj23AnswerTTL = maj23AnswerTTLFor(0)
	}
	return ps
}

// SetLaneAdmission records the connection generation and scheduler session this
// peer's current connection was admitted under, together, so a later message can
// be matched against the connection that produced it.
func (ps *PeerState) SetLaneAdmission(connID, session uint64) {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	ps.connID = connID
	ps.laneSession = session
}

// laneSessionForConn returns the scheduler session under which to admit a message
// that its connection stamped with connID, and whether that connection is still
// this peer's live one.
//
// A message carrying the generation currently admitted resolves to that
// connection's session; a message an earlier connection left in flight carries an
// older generation and is refused, so a reconnect under the same NodeID cannot
// let it inherit the standing of the connection that replaced it. Because the
// generation and the session are read together, the session returned is always
// the one the matched connection was admitted under, never a later reconnect's.
//
// A message with no generation (connID == 0) — a path that predates ingress
// stamping, or this node's own work — matches a peer that likewise has none,
// preserving the former admission behaviour.
func (ps *PeerState) laneSessionForConn(connID uint64) (uint64, bool) {
	ps.mtx.RLock()
	defer ps.mtx.RUnlock()

	if connID != ps.connID {
		return 0, false
	}
	return ps.laneSession, true
}

// SetRunning sets the running state of the peer.
func (ps *PeerState) SetRunning(v bool) {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	ps.running = v
}

// IsRunning returns true if a PeerState is considered running where multiple
// broadcasting goroutines exist for the peer.
func (ps *PeerState) IsRunning() bool {
	ps.mtx.RLock()
	defer ps.mtx.RUnlock()

	return ps.running
}

// GetRoundState returns a shallow copy of the PeerRoundState. There's no point
// in mutating it since it won't change PeerState.
func (ps *PeerState) GetRoundState() *cstypes.PeerRoundState {
	ps.mtx.RLock()
	defer ps.mtx.RUnlock()

	prs := ps.PRS.Copy()
	return &prs
}

// UpdateRoundState ensures that the update function is called using the blocking mechanism
func (ps *PeerState) UpdateRoundState(fn func(prs *cstypes.PeerRoundState)) {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()
	fn(&ps.PRS)
}

// UpdateProposalBlockParts updates peer-round-state with proposal block parts
func (ps *PeerState) UpdateProposalBlockParts(partSet *types.PartSet) {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()
	header := partSet.Header()
	ps.PRS.ProposalBlockPartSetHeader = header
	ps.PRS.ProposalBlockParts = bits.NewBitArray(int(header.Total))
}

// ToJSON returns a json of PeerState.
func (ps *PeerState) ToJSON() ([]byte, error) {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()
	return json.Marshal(ps)
}

// GetHeight returns an atomic snapshot of the PeerRoundState's height used by
// the mempool to ensure peers are caught up before broadcasting new txs.
func (ps *PeerState) GetHeight() int64 {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	return ps.PRS.Height
}

// SetHasProposal sets the given proposal as known for the peer.
func (ps *PeerState) SetHasProposal(proposal *types.Proposal) {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	if ps.PRS.Height != proposal.Height || ps.PRS.Round != proposal.Round {
		return
	}

	if ps.PRS.Proposal {
		return
	}

	ps.PRS.Proposal = true

	// ps.PRS.ProposalBlockParts is set due to NewValidBlockMessage
	if ps.PRS.ProposalBlockParts != nil {
		return
	}

	ps.PRS.ProposalBlockPartSetHeader = proposal.BlockID.PartSetHeader
	ps.PRS.ProposalBlockParts = bits.NewBitArray(int(proposal.BlockID.PartSetHeader.Total))
	ps.PRS.ProposalPOLRound = proposal.POLRound
	ps.PRS.ProposalPOL = nil // Nil until ProposalPOLMessage received.
}

// InitProposalBlockParts initializes the peer's proposal block parts header
// and bit array.
func (ps *PeerState) InitProposalBlockParts(partSetHeader types.PartSetHeader) {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	if ps.PRS.ProposalBlockParts != nil {
		return
	}

	ps.PRS.ProposalBlockPartSetHeader = partSetHeader
	ps.PRS.ProposalBlockParts = bits.NewBitArray(int(partSetHeader.Total))
}

// SetHasProposalBlockPart sets the given block part index as known for the peer.
func (ps *PeerState) SetHasProposalBlockPart(height int64, round int32, index int) {
	prs := ps.GetRoundState()
	if prs.Height != height || prs.Round != round {
		ps.logger.Debug("SetHasProposalBlockPart height/round mismatch",
			"height", height, "round", round, "peer_height", prs.Height, "peer_round", prs.Round)
		return
	}
	ps.mtx.Lock()
	ps.PRS.ProposalBlockParts.SetIndex(index, true)
	ps.mtx.Unlock()
}

// PickVoteToSend picks a vote to send to the peer. It will return true if a
// vote was picked.
//
// NOTE: `votes` must be the correct Size() for the Height().
func (ps *PeerState) PickVoteToSend(votes types.VoteSetReader) (*types.Vote, bool) {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	if votes.Size() == 0 {
		return nil, false
	}

	var (
		height    = votes.GetHeight()
		round     = votes.GetRound()
		votesType = tmproto.SignedMsgType(votes.Type())
		size      = votes.Size()
	)

	// lazily set data using 'votes'
	if votes.IsCommit() {
		ps.ensureCatchupCommitRound(height, round, size)
	}

	ps.ensureVoteBitArrays(height, size)

	psVotes := ps.getVoteBitArray(height, round, votesType)
	if psVotes == nil {
		return nil, false // not something worth sending
	}

	if index, ok := votes.BitArray().Sub(psVotes).PickRandom(); ok {
		idx, err := math.SafeConvertInt32(int64(index))
		if err != nil {
			panic(fmt.Errorf("failed to convert index to int32: %w", err))
		}
		vote := votes.GetByIndex(idx)
		if vote != nil {
			return vote, true
		}
	}

	return nil, false
}

func (ps *PeerState) getVoteBitArray(height int64, round int32, votesType tmproto.SignedMsgType) *bits.BitArray {
	if !types.IsVoteTypeValid(votesType) {
		return nil
	}

	if ps.PRS.Height == height {
		if ps.PRS.Round == round {
			switch votesType {
			case tmproto.PrevoteType:
				return ps.PRS.Prevotes

			case tmproto.PrecommitType:
				return ps.PRS.Precommits
			}
		}

		if ps.PRS.CatchupCommitRound == round {
			switch votesType {
			case tmproto.PrevoteType:
				return nil

			case tmproto.PrecommitType:
				return ps.PRS.CatchupCommit
			}
		}

		if ps.PRS.ProposalPOLRound == round {
			switch votesType {
			case tmproto.PrevoteType:
				return ps.PRS.ProposalPOL

			case tmproto.PrecommitType:
				return nil
			}
		}

		return nil
	}
	if ps.PRS.Height == height+1 {
		if ps.PRS.LastCommitRound == round {
			switch votesType {
			case tmproto.PrevoteType:
				return nil

			case tmproto.PrecommitType:
				return ps.PRS.Precommits
			}
		}

		return nil
	}

	return nil
}

// 'round': A round for which we have a +2/3 commit.
func (ps *PeerState) ensureCatchupCommitRound(height int64, round int32, numValidators int) {
	if ps.PRS.Height != height {
		return
	}

	/*
		NOTE: This is wrong, 'round' could change.
		e.g. if orig round is not the same as block LastCommit round.
		if ps.CatchupCommitRound != -1 && ps.CatchupCommitRound != round {
			panic(fmt.Sprintf(
				"Conflicting CatchupCommitRound. Height: %v,
				Orig: %v,
				New: %v",
				height,
				ps.CatchupCommitRound,
				round))
		}
	*/

	if ps.PRS.CatchupCommitRound == round {
		return // Nothing to do!
	}

	ps.PRS.CatchupCommitRound = round
	if round == ps.PRS.Round {
		ps.PRS.CatchupCommit = ps.PRS.Precommits
	} else {
		ps.PRS.CatchupCommit = bits.NewBitArray(numValidators)
	}
}

// EnsureVoteBitArrays ensures the bit-arrays have been allocated for tracking
// what votes this peer has received.
// NOTE: It's important to make sure that numValidators actually matches
// what the node sees as the number of validators for height.
func (ps *PeerState) EnsureVoteBitArrays(height int64, numValidators int) {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	ps.ensureVoteBitArrays(height, numValidators)
}

func (ps *PeerState) ensureVoteBitArrays(height int64, numValidators int) {
	if ps.PRS.Height == height {
		if ps.PRS.Prevotes == nil {
			ps.PRS.Prevotes = bits.NewBitArray(numValidators)
		}
		if ps.PRS.Precommits == nil {
			ps.PRS.Precommits = bits.NewBitArray(numValidators)
		}
		if ps.PRS.CatchupCommit == nil {
			ps.PRS.CatchupCommit = bits.NewBitArray(numValidators)
		}
		if ps.PRS.ProposalPOL == nil {
			ps.PRS.ProposalPOL = bits.NewBitArray(numValidators)
		}
	}
}

// RecordVote increments internal votes related statistics for this peer.
// It returns the total number of added votes.
func (ps *PeerState) RecordVote() int {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	ps.Stats.Votes++

	return ps.Stats.Votes
}

// VotesSent returns the number of blocks for which peer has been sending us
// votes.
func (ps *PeerState) VotesSent() int {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	return ps.Stats.Votes
}

// RecordCommit safely increments the commit counter and returns the new value
func (ps *PeerState) RecordCommit() int {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	ps.Stats.Commits++

	return ps.Stats.Commits
}

// CommitsSent safely returns a last value of a commit counter
func (ps *PeerState) CommitsSent() int {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	return ps.Stats.Commits
}

// RecordBlockPart increments internal block part related statistics for this peer.
// It returns the total number of added block parts.
func (ps *PeerState) RecordBlockPart() int {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	ps.Stats.BlockParts++
	return ps.Stats.BlockParts
}

// BlockPartsSent returns the number of useful block parts the peer has sent us.
func (ps *PeerState) BlockPartsSent() int {
	ps.mtx.RLock()
	defer ps.mtx.RUnlock()

	return ps.Stats.BlockParts
}

func (ps *PeerState) SetProTxHash(proTxHash types.ProTxHash) {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	ps.ProTxHash = proTxHash.Copy()
}

// GetProTxHash returns a copy of peer's pro-tx-hash
func (ps *PeerState) GetProTxHash() types.ProTxHash {
	ps.mtx.RLock()
	defer ps.mtx.RUnlock()

	return ps.ProTxHash.Copy()
}

// SetHasVote sets the given vote as known by the peer
func (ps *PeerState) SetHasVote(vote *types.Vote) error {
	// sanity check
	if vote == nil {
		return ErrPeerStateSetNilVote
	}
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	return ps.setHasVote(vote.Height, vote.Round, vote.Type, vote.ValidatorIndex)
}

// setHasVote will return an error when the index exceeds the bitArray length
func (ps *PeerState) setHasVote(height int64, round int32, voteType tmproto.SignedMsgType, index int32) error {
	ps.logger.Trace(
		"peerState setHasVote",
		"peer", ps.peerID,
		"height", height,
		"round", round,
		"peer_height", ps.PRS.Height,
		"peer_round", ps.PRS.Round,
		"type", voteType,
		"index", index,
		"peerVotes", ps.Stats.Votes,
	)

	// NOTE: some may be nil BitArrays -> no side effects
	psVotes := ps.getVoteBitArray(height, round, voteType)
	if psVotes != nil {
		if ok := psVotes.SetIndex(int(index), true); !ok {
			// https://github.com/tendermint/tendermint/issues/2871
			return ErrPeerStateInvalidVoteIndex
		}
	}
	return nil
}

// SetHasCommit sets the given vote as known by the peer
func (ps *PeerState) SetHasCommit(commit *types.Commit) {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	ps.setHasCommit(commit.Height, commit.Round)

	if ps.PRS.Height < commit.Height || (ps.PRS.Height == commit.Height && ps.PRS.Round < commit.Round) {
		ps.PRS.ProposalBlockPartSetHeader = commit.BlockID.PartSetHeader
		ps.PRS.ProposalBlockParts = bits.NewBitArray(int(commit.BlockID.PartSetHeader.Total))
		ps.PRS.ProposalPOLRound = -1
		ps.PRS.ProposalPOL = nil
	}
}

func (ps *PeerState) setHasCommit(height int64, round int32) {
	ps.logger.Trace(
		"setHasCommit",
		"height", height,
		"round", round,
		"peer_height", ps.PRS.Height,
		"peer_round", ps.PRS.Round,
	)

	if ps.PRS.Height < height || (ps.PRS.Height == height && ps.PRS.Round <= round) {
		ps.PRS.Height = height
		ps.PRS.Round = round
		ps.PRS.Step = cstypes.RoundStepPropose // shouldn't matter
		ps.PRS.HasCommit = true
	}
}

// ApplyNewRoundStepMessage updates the peer state for the new round.
func (ps *PeerState) ApplyNewRoundStepMessage(msg *NewRoundStepMessage) {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	ps.logger.Trace("apply new round step message", "peer", ps.peerID, "msg", msg.String())

	// ignore duplicates or decreases
	if CompareHRS(msg.Height, msg.Round, msg.Step, ps.PRS.Height, ps.PRS.Round, ps.PRS.Step) <= 0 {
		return
	}

	var (
		psHeight             = ps.PRS.Height
		psRound              = ps.PRS.Round
		psCatchupCommitRound = ps.PRS.CatchupCommitRound
		psCatchupCommit      = ps.PRS.CatchupCommit
		startTime            = time.Now().Add(-1 * time.Duration(msg.SecondsSinceStartTime) * time.Second)
	)

	ps.PRS.Height = msg.Height
	ps.PRS.Round = msg.Round
	ps.PRS.Step = msg.Step
	ps.PRS.StartTime = startTime

	if psHeight != msg.Height || psRound != msg.Round {
		ps.PRS.Proposal = false
		ps.PRS.ProposalBlockPartSetHeader = types.PartSetHeader{}
		ps.PRS.ProposalBlockParts = nil
		ps.PRS.ProposalPOLRound = -1
		ps.PRS.ProposalPOL = nil

		// we'll update the BitArray capacity later
		ps.PRS.Prevotes = nil
		ps.PRS.Precommits = nil
		ps.PRS.HasCommit = false
	}

	if psHeight == msg.Height && psRound != msg.Round && msg.Round == psCatchupCommitRound {
		// Peer caught up to CatchupCommitRound.
		// Preserve psCatchupCommit!
		// NOTE: We prefer to use prs.Precommits if
		// pr.Round matches pr.CatchupCommitRound.
		ps.PRS.Precommits = psCatchupCommit
	}

	if psHeight != msg.Height {
		// Shift Precommits to LastPrecommits.
		if psHeight+1 == msg.Height && psRound == msg.LastCommitRound {
			ps.PRS.LastCommitRound = msg.LastCommitRound
		} else {
			ps.PRS.LastCommitRound = msg.LastCommitRound
		}

		// we'll update the BitArray capacity later
		ps.PRS.CatchupCommitRound = -1
		ps.PRS.CatchupCommit = nil
	}
}

// ApplyNewValidBlockMessage updates the peer state for the new valid block.
func (ps *PeerState) ApplyNewValidBlockMessage(msg *NewValidBlockMessage) {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	if ps.PRS.Height != msg.Height {
		return
	}

	if ps.PRS.Round != msg.Round && !msg.IsCommit {
		return
	}

	ps.PRS.ProposalBlockPartSetHeader = msg.BlockPartSetHeader
	ps.PRS.ProposalBlockParts = msg.BlockParts
}

// ApplyProposalPOLMessage updates the peer state for the new proposal POL.
func (ps *PeerState) ApplyProposalPOLMessage(msg *ProposalPOLMessage) {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	if ps.PRS.Height != msg.Height {
		return
	}
	if ps.PRS.ProposalPOLRound != msg.ProposalPOLRound {
		return
	}

	// TODO: Merge onto existing ps.PRS.ProposalPOL?
	// We might have sent some prevotes in the meantime.
	ps.PRS.ProposalPOL = msg.ProposalPOL
}

// ApplyHasVoteMessage updates the peer state for the new vote.
func (ps *PeerState) ApplyHasVoteMessage(msg *HasVoteMessage) error {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	if ps.PRS.Height != msg.Height {
		return nil
	}

	return ps.setHasVote(msg.Height, msg.Round, msg.Type, msg.Index)
}

// ApplyHasCommitMessage updates the peer state for the new commit.
func (ps *PeerState) ApplyHasCommitMessage(msg *HasCommitMessage) {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()
	if ps.PRS.Height != msg.Height {
		return
	}
	ps.setHasCommit(msg.Height, msg.Round)
}

// maj23AnswerMemory is how many distinct majority claims per peer are
// remembered as answered.
//
// A peer's gossip loop offers up to four claims on every tick — a prevote
// majority for its round, one for the proposal's POL round, a precommit
// majority, and a catch-up commit — so remembering only the last would be
// overwritten before the same claim came round again and would suppress
// nothing in steady state.
const maj23AnswerMemory = 4

// maj23AnswerTicks is how many majority-claim gossip passes an answer keeps the
// same claim from the same peer from being answered again.
//
// The answer is not only a report of our own vote set — it is the only thing
// that corrects the claimant's picture of what we hold. A claimant records a
// vote as delivered the moment it hands it to the router, and offers it once;
// nothing inside a round restores it to the offer except our answer. So when a
// vote is dropped between us, the claimant learns of it only by being told
// again which votes we lack — and that answer necessarily carries an unchanged
// count, because the vote is still missing. An answer withheld for being
// unchanged is therefore withheld exactly when it is needed.
//
// Counting the peer's own passes rather than fixing a duration is what makes
// that trade the same wherever the cadence is tuned: every second repeat costs
// nothing, and the one after it reopens the repair. A fixed duration would be
// most of a round at a fast cadence, and longer than any repeat at a slow one,
// where it would suppress nothing at all.
const maj23AnswerTicks = 2

// maj23AnswerTTLFor returns how long an answer suppresses the same claim, given
// the cadence at which a gossip loop repeats it.
func maj23AnswerTTLFor(sleep time.Duration) time.Duration {
	if sleep <= 0 {
		// Nothing throttles the gossip loop, so there is no cadence to count
		// passes of. The default is what the rest of these ceilings are sized
		// against.
		sleep = config.DefaultConsensusConfig().PeerQueryMaj23SleepDuration
	}
	return maj23AnswerTicks * sleep
}

// maj23Answer is one majority claim this node has answered, the number of our
// own votes the answer carried, and when it went out.
type maj23Answer struct {
	key  string
	bits int
	at   time.Time
}

// ShouldAnswerVoteSetMaj23 reports whether a majority claim from this peer is
// worth answering.
//
// A peer's gossip loop repeats the same claims on every tick for as long as
// they hold, and the answer — which votes we have for that block — is drawn
// from our own vote set alone. Answering every copy costs a bit array over
// every validator, on the goroutine that serves every peer's state messages,
// so a repeat that has just been answered is passed over.
//
// A grown vote count reopens the answer at once, since it is new information
// the peer is waiting for. An unchanged count does not mean the answer is
// worthless: it is what the claimant has to hear to notice that a vote it
// believes it delivered never arrived. That is what maj23AnswerTTL bounds —
// the repeats cost nothing for a while, and then the repair runs again.
//
// It saves this node work in normal gossip. It is not what bounds a sender who
// wants an answer: a claim naming a round we track no votes for is a new key
// every time, and rounds are free to name. The ceilings on the channel are what
// bound that.
func (ps *PeerState) ShouldAnswerVoteSetMaj23(
	height int64,
	round int32,
	voteType tmproto.SignedMsgType,
	blockID types.BlockID,
	ourVotes *bits.BitArray,
) bool {
	key := maj23Key(height, round, voteType, blockID)
	count := countTrueBits(ourVotes)
	now := ps.clock.Now()

	ps.mtx.RLock()
	defer ps.mtx.RUnlock()

	for _, answered := range ps.answeredMaj23 {
		if answered.key == key && answered.bits == count && now.Sub(answered.at) < ps.maj23AnswerTTL {
			return false
		}
	}
	return true
}

// RecordVoteSetMaj23Answer notes that this peer has been told what votes we
// have for a majority claim, so repeating the claim is not answered again until
// the answer would differ.
//
// It is recorded only once the answer has actually gone out. Recording an
// answer this node then failed to deliver would suppress the peer's next ask
// and leave it waiting for votes it was never told about.
func (ps *PeerState) RecordVoteSetMaj23Answer(
	height int64,
	round int32,
	voteType tmproto.SignedMsgType,
	blockID types.BlockID,
	ourVotes *bits.BitArray,
) {
	answer := maj23Answer{
		key:  maj23Key(height, round, voteType, blockID),
		bits: countTrueBits(ourVotes),
		at:   ps.clock.Now(),
	}

	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	for i, answered := range ps.answeredMaj23 {
		if answered.key == answer.key {
			// The same claim, answered again.
			ps.answeredMaj23[i] = answer
			return
		}
	}
	ps.answeredMaj23[ps.answeredMaj23Next] = answer
	ps.answeredMaj23Next = (ps.answeredMaj23Next + 1) % maj23AnswerMemory
}

func maj23Key(height int64, round int32, voteType tmproto.SignedMsgType, blockID types.BlockID) string {
	return fmt.Sprintf("%d/%d/%d/%s", height, round, voteType, blockID.Key())
}

func countTrueBits(bA *bits.BitArray) int {
	if bA == nil {
		return 0
	}
	return bA.CountTrueBits()
}

// ApplyVoteSetBitsMessage updates the peer state for the bit-array of votes
// it claims to have for the corresponding BlockID.
//
// `ourVotes` is a BitArray of votes we have for msg.BlockID
// NOTE: if ourVotes is nil (e.g. msg.Height < rs.Height),
// we conservatively overwrite ps's votes w/ msg.Votes.
func (ps *PeerState) ApplyVoteSetBitsMessage(msg *VoteSetBitsMessage, ourVotes *bits.BitArray) {
	ps.mtx.Lock()
	defer ps.mtx.Unlock()

	votes := ps.getVoteBitArray(msg.Height, msg.Round, msg.Type)
	if votes != nil {
		if ourVotes == nil {
			votes.Update(msg.Votes)
		} else {
			otherVotes := votes.Sub(ourVotes)
			hasVotes := otherVotes.Or(msg.Votes)
			votes.Update(hasVotes)
		}
	}
}

// String returns a string representation of the PeerState
func (ps *PeerState) String() string {
	return ps.StringIndented("")
}

// StringIndented returns a string representation of the PeerState
func (ps *PeerState) StringIndented(indent string) string {
	ps.mtx.RLock()
	defer ps.mtx.RUnlock()
	return fmt.Sprintf(`PeerState{
%s  Key        %v
%s  RoundState %v
%s  Stats      %v
%s}`,
		indent, ps.peerID,
		indent, ps.PRS.StringIndented(indent+"  "),
		indent, ps.Stats,
		indent,
	)
}

// MarshalZerologObject formats this object for logging purposes
func (ps *PeerState) MarshalZerologObject(e *zerolog.Event) {
	e.Str("peer_state", ps.String())
}
