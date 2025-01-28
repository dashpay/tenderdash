package consensus

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dashpay/tenderdash/config"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	selectproposer "github.com/dashpay/tenderdash/internal/consensus/versioned/selectproposer"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	"github.com/dashpay/tenderdash/types"
)

var (
	errPrevoteProposalBlockNil  = errors.New("proposal-block is nil")
	errPrevoteProposalNil       = errors.New("proposal is nil")
	errPrevoteTimestampNotEqual = errors.New("proposal timestamp not equal")
	errPrevoteProposalNotTimely = errors.New("proposal is not timely")
	errPrevoteInvalidChainLock  = errors.New("proposal-block chain lock is invalid")
)

// StateDataStore is a state-data store
type StateDataStore struct {
	mtx            sync.Mutex
	roundState     cstypes.RoundState
	committedState sm.State
	metrics        *Metrics
	logger         log.Logger
	config         *config.ConsensusConfig
	emitter        *eventemitter.EventEmitter
	replayMode     bool
	version        int64
}

// NewStateDataStore creates and returns a new state-data store
func NewStateDataStore(
	metrics *Metrics,
	logger log.Logger,
	cfg *config.ConsensusConfig,
	emitter *eventemitter.EventEmitter,
) *StateDataStore {
	return &StateDataStore{
		metrics: metrics,
		logger:  logger,
		config:  cfg,
		emitter: emitter,
	}
}

// Get returns the state-data
func (s *StateDataStore) Get() StateData {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	return s.load()
}

// Update updates state-data with candidate
func (s *StateDataStore) Update(candidate StateData) error {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	return s.update(candidate)
}

// UpdateAndGet updates state-data with a candidate and returns updated state-data
func (s *StateDataStore) UpdateAndGet(candidate StateData) (StateData, error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	err := s.update(candidate)
	if err != nil {
		return StateData{}, err
	}
	return s.load(), nil
}

// UpdateReplayMode safe updates replay-mode flag
func (s *StateDataStore) UpdateReplayMode(flag bool) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	s.replayMode = flag
}

func (s *StateDataStore) load() StateData {
	return StateData{
		config:     s.config,
		RoundState: s.roundState,
		state:      s.committedState,
		version:    s.version,
		metrics:    s.metrics,
		replayMode: s.replayMode,
		store:      s,
		logger:     s.logger,
	}
}

func (s *StateDataStore) update(candidate StateData) error {
	if candidate.version != s.version {
		return fmt.Errorf("mismatch state-data versions actual %d want %d", candidate.version, s.version)
	}
	s.roundState = candidate.RoundState
	if s.committedState.LastBlockHeight == 0 || s.committedState.LastBlockHeight < candidate.state.LastBlockHeight {
		// fires the event to update committed-state in those components that have a reference with this data
		s.emitter.Emit(committedStateUpdateEventName, candidate.state)
	}
	s.committedState = candidate.state
	s.version++
	return nil
}

func (s *StateDataStore) Subscribe(evsw *eventemitter.EventEmitter) {
	evsw.AddListener(setReplayModeEventName, func(obj eventemitter.EventData) error {
		s.UpdateReplayMode(obj.(bool))
		return nil
	})
}

// StateData is a copy of the current RoundState and state.State stored in the store
// Along with data, StateData provides some methods to check or update data inside
type StateData struct {
	config *config.ConsensusConfig
	cstypes.RoundState
	state      sm.State // State until height-1.
	logger     log.Logger
	metrics    *Metrics
	store      *StateDataStore
	version    int64
	replayMode bool
}

// Save persists the current state-data using store and updates state-data with the version inclusive
func (s *StateData) Save() error {
	stateData, err := s.store.UpdateAndGet(*s)
	if err != nil {
		return err
	}
	s.RoundState = stateData.RoundState
	s.state = stateData.state
	s.version = stateData.version
	return nil
}

func (s *StateData) isProposer(proTxHash types.ProTxHash) bool {
	prop, err := s.ProposerSelector.GetProposer(s.Height, s.Round)
	if err != nil {
		s.logger.Error("error getting proposer", "err", err, "height", s.Height, "round", s.Round)
		return false
	}
	return proTxHash != nil && bytes.Equal(prop.ProTxHash.Bytes(), proTxHash.Bytes())
}

func (s *StateData) isValidator(proTxHash types.ProTxHash) bool {
	return s.state.Validators.HasProTxHash(proTxHash)
}

// Returns true if the proposal block is complete &&
// (if POLRound was proposed, we have +2/3 prevotes from there).
func (s *StateData) isProposalComplete() bool {
	if s.Proposal == nil || s.ProposalBlock == nil {
		return false
	}
	// we have the proposal. if there's a POLRound,
	// make sure we have the prevotes from it too
	if s.Proposal.POLRound < 0 {
		return true
	}
	// if this is false the proposer is lying or we haven't received the POL yet
	return s.Votes.Prevotes(s.Proposal.POLRound).HasTwoThirdsMajority()
}

func (s *StateData) updateRoundStep(round int32, step cstypes.RoundStepType) {
	if !s.replayMode {
		if round != s.Round || round == 0 && step == cstypes.RoundStepNewRound {
			s.metrics.MarkRound(s.Round, s.StartTime)
		}
		if s.Step != step {
			s.metrics.MarkStep(s.Step)
		}
	}
	s.Round = round
	s.Step = step

	if err := s.ProposerSelector.UpdateHeightRound(s.Height, round); err != nil {
		s.logger.Error("error updating proposer scores",
			"height", s.Height, "round", round,
			"err", err)
	}
}

// Updates State and increments height to match that of state.
// The round becomes 0 and cs.Step becomes cstypes.RoundStepNewHeight.
func (s *StateData) updateToState(state sm.State, commit *types.Commit, blockStore selectproposer.BlockStore) {
	if s.CommitRound > -1 && 0 < s.Height && s.Height != state.LastBlockHeight {
		panic(fmt.Sprintf(
			"updateToState() expected state height of %v but found %v",
			s.Height, state.LastBlockHeight,
		))
	}

	if !s.state.IsEmpty() {
		if s.state.LastBlockHeight > 0 && s.state.LastBlockHeight+1 != s.Height {
			// This might happen when someone else is mutating cs.state.
			// Someone forgot to pass in state.Copy() somewhere?!
			panic(fmt.Sprintf(
				"inconsistent committedState.LastBlockHeight+1 %v vs cs.Height %v",
				s.state.LastBlockHeight+1, s.Height,
			))
		}
		if s.state.LastBlockHeight > 0 && s.Height == s.state.InitialHeight {
			panic(fmt.Sprintf(
				"inconsistent committedState.LastBlockHeight %v, expected 0 for initial height %v",
				s.state.LastBlockHeight, s.state.InitialHeight,
			))
		}

		// If state isn't further out than cs.state, just ignore.
		// This happens when SwitchToConsensus() is called in the reactor.
		// We don't want to reset e.g. the Votes, but we still want to
		// signal the new round step, because other services (eg. txNotifier)
		// depend on having an up-to-date peer state!
		if state.LastBlockHeight < s.state.LastBlockHeight {
			//s.logger.Debug(
			//	"ignoring updateToState()",
			//	"new_height", state.LastBlockHeight+1,
			//	"old_height", s.state.LastBlockHeight+1)
			//s.newStep()
			return
		}
	}

	// Reset fields based on state.

	switch {
	case state.LastBlockHeight == 0: // Very first commit should be empty.
		s.LastCommit = (*types.Commit)(nil)
	case s.CommitRound > -1 && s.Votes != nil && commit == nil: // Otherwise, use cs.Votes
		if !s.Votes.Precommits(s.CommitRound).HasTwoThirdsMajority() {
			panic(fmt.Sprintf(
				"wanted to form a commit, but precommits (H/R: %d/%d) didn't have 2/3+: %v",
				state.LastBlockHeight, s.CommitRound, s.Votes.Precommits(s.CommitRound),
			))
		}
		precommits := s.Votes.Precommits(s.CommitRound)
		s.LastCommit = precommits.MakeCommit()
	case commit != nil:
		s.LastCommit = commit
	case s.LastCommit == nil:
		// NOTE: when Tendermint starts, it has no votes. reconstructLastCommit
		// must be called to reconstruct LastPrecommits from SeenCommit.
		panic(fmt.Sprintf(
			"last commit cannot be empty after initial block (H:%d)",
			state.LastBlockHeight+1,
		))
	}

	// Next desired block height
	height := state.LastBlockHeight + 1
	if height == 1 {
		height = state.InitialHeight
	}

	// state.Validators contain validator set at (state.LastBlockHeight+1, 0)
	validators := state.Validators

	if s.Validators == nil || !bytes.Equal(s.Validators.QuorumHash, validators.QuorumHash) {
		s.logger.Info("Updating validators", "from", s.Validators.BasicInfoString(),
			"to", validators.BasicInfoString())
	}

	s.Validators = validators
	var err error

	s.ProposerSelector, err = selectproposer.NewProposerSelector(
		state.ConsensusParams,
		s.Validators,
		height,
		0,
		blockStore,
		s.logger,
	)
	if err != nil {
		s.logger.Error("error creating proposer selector", "height", height, "round", 0, "validators", s.Validators, "err", err)
		panic(fmt.Sprintf("error creating proposer selector: %v", err))
	}

	s.logger.Trace("updating state height", "newHeight", height)

	if s.CommitTime.IsZero() {
		// "Now" makes it easier to sync up dev nodes.
		s.StartTime = tmtime.Now()
	} else {
		s.StartTime = s.CommitTime
	}

	s.Proposal = nil
	s.ProposalReceiveTime = time.Time{}
	s.ProposalBlock = nil
	s.ProposalBlockParts = nil
	s.LockedRound = -1
	s.LockedBlock = nil
	s.LockedBlockParts = nil
	s.ValidRound = -1
	s.ValidBlock = nil
	s.ValidBlockParts = nil
	s.Commit = nil
	s.Votes = cstypes.NewHeightVoteSet(state.ChainID, height, validators)
	s.CommitRound = -1
	s.LastValidators = state.LastValidators
	s.TriggeredTimeoutPrecommit = false

	s.state = state

	// RoundState fields
	s.updateHeight(height)
	s.updateRoundStep(0, cstypes.RoundStepNewHeight)
}

func (s *StateData) updateHeight(height int64) {
	s.metrics.Height.Set(float64(height))
	s.Height = height
}

// InitialHeight returns an initial height
func (s *StateData) InitialHeight() int64 {
	return s.state.InitialHeight
}

func (s *StateData) HeightVoteSet() (int64, *cstypes.HeightVoteSet) {
	return s.Height, s.Votes
}

// proposalIsTimely returns an error if the proposal is not timely
func (s *StateData) proposalIsTimely() error {
	if s.Height == s.state.InitialHeight {
		// by definition, initial block must have genesis time
		if !s.Proposal.Timestamp.Equal(s.state.LastBlockTime) {
			return fmt.Errorf(
				"%w: initial block must have genesis time: height %d, round %d, proposal time %v, genesis time %v",
				errPrevoteProposalNotTimely, s.Height, s.Round, s.Proposal.Timestamp, s.state.LastBlockTime,
			)
		}

		return nil
	}

	sp := s.state.ConsensusParams.Synchrony.SynchronyParamsOrDefaults()
	switch s.Proposal.CheckTimely(s.ProposalReceiveTime, sp, s.Round) {
	case 0:
		return nil
	case -1: // too early
		return fmt.Errorf(
			"%w: received too early: height %d, round %d, delay %s",
			errPrevoteProposalNotTimely, s.Height, s.Round,
			s.ProposalReceiveTime.Sub(s.Proposal.Timestamp).String(),
		)
	case 1: // too late
		return fmt.Errorf(
			"%w: received too late: height %d, round %d, delay %s",
			errPrevoteProposalNotTimely, s.Height, s.Round,
			s.ProposalReceiveTime.Sub(s.Proposal.Timestamp).String(),
		)
	default:
		panic("unexpected return value from isTimely")
	}
}

// Updates ValidBlock to current proposal.
// Returns true if the block was updated.
func (s *StateData) updateValidBlock() bool {
	s.ValidRound = s.Round
	// we only update valid block if it's not set already; otherwise we might overwrite the recv time
	if !s.ValidBlock.HashesTo(s.ProposalBlock.Hash()) {
		s.ValidBlock = s.ProposalBlock
		s.ValidBlockRecvTime = s.ProposalReceiveTime
		s.ValidBlockParts = s.ProposalBlockParts

		return true
	}

	s.logger.Debug("valid block is already up to date, not updating",
		"proposal_block", s.ProposalBlock.Hash(),
		"proposal_round", s.Round,
		"valid_block", s.ValidBlock.Hash(),
		"valid_block_round", s.ValidRound,
	)

	return false
}

// Locks the proposed block.
// You might also need to call updateValidBlock().
func (s *StateData) updateLockedBlock() {
	s.LockedRound = s.Round
	s.LockedBlock = s.ProposalBlock
	s.LockedBlockParts = s.ProposalBlockParts
}

func (s *StateData) verifyCommit(commit *types.Commit, peerID types.NodeID, ignoreProposalBlock bool) (verified bool, err error) {
	// Lets first do some basic commit validation before more complicated commit verification
	if err := commit.ValidateBasic(); err != nil {
		return false, fmt.Errorf("error validating commit: %v", err)
	}

	rs := s.RoundState
	stateHeight := s.Height

	// A commit for the previous height?
	// These come in while we wait timeoutCommit
	if commit.Height+1 == stateHeight {
		s.logger.Trace("old commit ignored", "commit", commit)
		return false, nil
	}

	s.logger.Trace(
		"verifying commit from remote",
		"commit_height", commit.Height,
		"cs_height", s.Height,
	)

	// Height mismatch is ignored.
	// Not necessarily a bad peer, but not favorable behavior.
	if commit.Height != stateHeight {
		s.logger.Debug(
			"commit ignored and not added",
			"commit_height",
			commit.Height,
			"cs_height",
			stateHeight,
			"peer",
			peerID,
		)
		return false, nil
	}

	if rs.Proposal == nil || ignoreProposalBlock {
		if ignoreProposalBlock {
			s.logger.Debug("Commit verified for future round", "height", commit.Height, "round", commit.Round)
		} else {
			s.logger.Debug("Commit came in before proposal", "height", commit.Height, "round", commit.Round)
		}

		// We need to verify that it was properly signed
		// This generally proves that the commit is correct
		if err := s.Validators.VerifyCommit(s.state.ChainID, commit.BlockID, s.Height, commit); err != nil {
			return false, fmt.Errorf("error verifying commit: %w", err)
		}

		if !s.ProposalBlockParts.HasHeader(commit.BlockID.PartSetHeader) {
			s.logger.Debug("setting proposal block parts from commit", "partSetHeader", commit.BlockID.PartSetHeader)
			s.ProposalBlockParts = types.NewPartSetFromHeader(commit.BlockID.PartSetHeader)
		}

		s.Commit = commit

		if ignoreProposalBlock {
			// If we are verifying the commit for a future round we just need to know if the commit was properly signed
			// so we can go to the next round
			return true, nil
		}
		// We don't need to go to the next round, when we get the proposal in the commit will be set and the proposal
		// block will be executed
		return false, nil
	}

	// Lets verify that the threshold signature matches the current validator set
	if err := s.Validators.VerifyCommit(s.state.ChainID, rs.Proposal.BlockID, s.Height, commit); err != nil {
		return false, fmt.Errorf("error verifying commit: %w", err)
	}

	return true, nil
}

func (s *StateData) isLockedBlockEqual(blockID types.BlockID) bool {
	return s.LockedBlock.HashesTo(blockID.Hash)
}

func (s *StateData) replaceProposalBlockOnLockedBlock(blockID types.BlockID) {
	// The Locked* fields no longer matter.
	// Move them over to ProposalBlock if they match the commit hash,
	// otherwise they'll be cleared in updateToState.
	if !s.isLockedBlockEqual(blockID) {
		return
	}
	s.ProposalBlock = s.LockedBlock
	s.ProposalBlockParts = s.LockedBlockParts
	s.logger.Trace("commit is for a locked block; set ProposalBlock=LockedBlock", "block_hash", blockID.Hash)
}

func (s *StateData) proposeTimeout(round int32) time.Duration {
	tp := s.state.ConsensusParams.Timeout.TimeoutParamsOrDefaults()
	p := tp.Propose
	if s.config.UnsafeProposeTimeoutOverride != 0 {
		p = s.config.UnsafeProposeTimeoutOverride
	}
	pd := tp.ProposeDelta
	if s.config.UnsafeProposeTimeoutDeltaOverride != 0 {
		pd = s.config.UnsafeProposeTimeoutDeltaOverride
	}
	return time.Duration(
		p.Nanoseconds()+pd.Nanoseconds()*int64(round),
	) * time.Nanosecond
}

func (s *StateData) voteTimeout(round int32) time.Duration {
	tp := s.state.ConsensusParams.Timeout.TimeoutParamsOrDefaults()
	v := tp.Vote
	if s.config.UnsafeVoteTimeoutOverride != 0 {
		v = s.config.UnsafeVoteTimeoutOverride
	}
	vd := tp.VoteDelta
	if s.config.UnsafeVoteTimeoutDeltaOverride != 0 {
		vd = s.config.UnsafeVoteTimeoutDeltaOverride
	}
	return time.Duration(
		v.Nanoseconds()+vd.Nanoseconds()*int64(round),
	) * time.Nanosecond
}

func (s *StateData) isValidForPrevote() error {
	// Check that a proposed block was not received within this round (and thus executing this from a timeout).
	if s.ProposalBlock == nil {
		return errPrevoteProposalBlockNil
	}
	if s.Proposal == nil {
		return errPrevoteProposalNil
	}
	if !s.Proposal.Timestamp.Equal(s.ProposalBlock.Header.Time) {
		return errPrevoteTimestampNotEqual
	}

	// if this block was not validated yet, we check if it's timely
	if !s.replayMode && !s.ProposalBlock.HashesTo(s.ValidBlock.Hash()) {
		if err := s.proposalIsTimely(); err != nil {
			return err
		}
	}

	// Validate proposal core chain lock
	if err := sm.ValidateBlockChainLock(s.state, s.ProposalBlock); err != nil {
		return errPrevoteInvalidChainLock
	}
	return nil
}
