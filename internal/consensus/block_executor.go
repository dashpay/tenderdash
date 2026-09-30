package consensus

import (
	"context"
	"errors"
	"fmt"

	sync "github.com/sasha-s/go-deadlock"

	abci "github.com/dashpay/tenderdash/abci/types"
	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

type blockExecutor struct {
	mtx                sync.RWMutex
	logger             log.Logger
	privValidator      privValidator
	blockExec          sm.Executor
	proposedAppVersion uint64
	committedState     sm.State
	// expectations is touched only by the consensus goroutine; mtx does not
	// guard it.
	expectations commitExpectations
}

// Create the next block to propose and return it. Returns nil block upon error.
//
// We really only need to return the parts, but the block is returned for
// convenience so we can log the proposal block.
//
// NOTE: keep it side-effect free for clarity.
func (c *blockExecutor) create(ctx context.Context, rs *cstypes.RoundState, round int32) (*types.Block, error) {
	if c.privValidator.IsZero() {
		return nil, errors.New("cannot create proposal block on non-validator")
	}

	// TODO(sergio): wouldn't it be easier if CreateProposalBlock accepted cs.LastCommit directly?
	committedState := c.getCommittedState()
	var commit *types.Commit
	switch {
	case rs.Height == committedState.InitialHeight:
		// We're creating a proposal for the first block.
		// The commit is empty, but not nil.
		commit = types.NewCommit(0, 0, types.BlockID{}, nil, nil)
	case rs.LastCommit != nil:
		commit = rs.LastCommit

	default: // This shouldn't happen.
		c.logger.Error("propose step; cannot propose anything without commit for the previous block")
		return nil, nil
	}

	proposerProTxHash := c.privValidator.ProTxHash

	ret, uncommittedState, err := c.blockExec.CreateProposalBlock(ctx, rs.Height, round, committedState, commit, proposerProTxHash, c.proposedAppVersion)
	if err != nil {
		panic(err)
	}
	rs.CurrentRoundState = uncommittedState
	return ret, nil
}

// ensureProcess makes rs.CurrentRoundState the application's result of
// ProcessProposal for rs.ProposalBlock at round, calling it unless that is
// already the result held.
//
// Invariant: CurrentRoundState must match the application's latest
// ProcessProposal. An application may keep one execution context per height
// (Drive does) and finalize only the round it processed last, so a result for
// another round must never be reused without a new ProcessProposal. A repeat
// for the same block and round, as after a block sync handover, re-executes it.
func (c *blockExecutor) ensureProcess(ctx context.Context, rs *cstypes.RoundState, round int32) error {
	block := rs.ProposalBlock
	// Above the condition, not inside it: either operand can reach the block,
	// depending on whether the first short-circuits the second.
	if block == nil {
		return fmt.Errorf("%w: height %d, round %d", ErrProposalBlockNotSet, rs.Height, round)
	}
	if !processedFor(rs, round) {
		c.logger.Trace("CurrentRoundState is outdated, executing ProcessProposal", "crs", rs.CurrentRoundState)
		// consensus holds no proof for the block's LastCommit, so it is verified
		// in full
		uncommittedState, err := c.blockExec.ProcessProposal(ctx, block, round, c.getCommittedState(), true,
			types.VerifiedCommit{})
		if err != nil {
			return fmt.Errorf("ProcessProposal abci method: %w", err)
		}
		rs.CurrentRoundState = uncommittedState
	}
	return nil
}

// processedFor reports whether rs.CurrentRoundState is the application's result of
// ProcessProposal for rs.ProposalBlock at round, so that ensureProcess would
// not call it.
func processedFor(rs *cstypes.RoundState, round int32) bool {
	crs := &rs.CurrentRoundState
	return rs.ProposalBlock != nil && crs.Params.Source == sm.ProcessProposalSource &&
		crs.MatchesBlock(rs.ProposalBlock.Header, round)
}

// ensureOwnRound undoes a commit check that left the application processing
// the round's proposal block for another round: it has the block processed for
// rs.Round again. Applications keeping one execution context per height, like
// Drive, answer ExtendVote and VerifyVoteExtension from the round processed
// last, and Drive's ExtendVote fails for any other.
//
// It does nothing unless the proposal block is this round's proposal and was
// last processed for another round, so it never processes a block the round
// would not.
func (c *blockExecutor) ensureOwnRound(ctx context.Context, rs *cstypes.RoundState) error {
	crs := &rs.CurrentRoundState
	if rs.Proposal == nil || rs.ProposalBlock == nil || !rs.ProposalBlock.HashesTo(rs.Proposal.BlockID.Hash) ||
		crs.Params.Source != sm.ProcessProposalSource || crs.Round == rs.Round ||
		!crs.MatchesBlock(rs.ProposalBlock.Header, crs.Round) {
		return nil
	}
	return c.ensureProcess(ctx, rs, rs.Round)
}

// refuseUnprocessedCommit returns an error wrapping
// sm.ErrCommitExtensionsRejected when checking commit would need its block
// processed again for commit.Round, and the application's expectation for that
// block and round already refuses the commit's vector: it differs from the one
// expected, or is the one expected and was rejected. See commitExpectations.
//
// A commit for the round processed last is not refused here: checking it costs
// no ProcessProposal, and the application decides.
func (c *blockExecutor) refuseUnprocessedCommit(rs *cstypes.RoundState, commit *types.Commit) error {
	if processedFor(rs, commit.Round) {
		return nil
	}
	exp := c.expectations.lookup(commit)
	switch {
	case exp == nil:
		return nil
	case !exp.matches(commit):
		return fmt.Errorf("commit extensions at height %d round %d block %X differ from those the application expects: %w",
			commit.Height, commit.Round, commit.BlockID.Hash, sm.ErrCommitExtensionsRejected)
	case exp.rejected:
		return errRejectedBefore(commit)
	}
	return nil
}

// verifyCommitExtensions asks the application to accept commit's extension
// vector, unless that is the vector expected for commit's block and round and it
// was already rejected. The block must already be processed for commit.Round.
//
// On a rejection it learns the application's expectation for the block and
// round, if not yet known, from ExtendVote; see commitExpectations. ExtendVote
// panics on an ABCI error, as every other ABCI call on this path does.
func (c *blockExecutor) verifyCommitExtensions(ctx context.Context, commit *types.Commit) error {
	exp := c.expectations.lookup(commit)
	if exp != nil && exp.rejected && exp.matches(commit) {
		return errRejectedBefore(commit)
	}
	err := sm.VerifyCommitExtensions(ctx, c.blockExec, commit)
	if !errors.Is(err, sm.ErrCommitExtensionsRejected) {
		return err
	}
	if exp == nil {
		exp = c.expectations.learn(commit, c.expectedExtensions(ctx, commit))
	}
	if exp.matches(commit) {
		exp.rejected = true
	}
	return err
}

// expectedExtensions returns the threshold-recoverable extensions the
// application's ExtendVote returns for commit's block and round, as a commit
// carries them, without signatures. The block must be processed for
// commit.Round: that is the context the application answers from.
func (c *blockExecutor) expectedExtensions(ctx context.Context, commit *types.Commit) []*tmproto.VoteExtension {
	vote := &types.Vote{
		Type:    tmproto.PrecommitType,
		Height:  commit.Height,
		Round:   commit.Round,
		BlockID: commit.BlockID,
	}
	c.blockExec.ExtendVote(ctx, vote)
	return types.NewCommit(commit.Height, commit.Round, commit.BlockID, vote.VoteExtensions, nil).ThresholdVoteExtensions
}

func errRejectedBefore(commit *types.Commit) error {
	return fmt.Errorf("commit extensions at height %d round %d block %X already rejected: %w",
		commit.Height, commit.Round, commit.BlockID.Hash, sm.ErrCommitExtensionsRejected)
}

func (c *blockExecutor) mustEnsureProcess(ctx context.Context, rs *cstypes.RoundState, round int32) {
	err := c.ensureProcess(ctx, rs, round)
	if err != nil {
		panic(err)
	}
}

func (c *blockExecutor) finalize(ctx context.Context, stateData *StateData, commit *types.Commit) (sm.State, *abci.ResponseFinalizeBlock, error) {
	block := stateData.ProposalBlock
	blockParts := stateData.ProposalBlockParts
	return c.blockExec.FinalizeBlock(
		ctx,
		stateData.state.Copy(),
		stateData.CurrentRoundState,
		types.BlockID{
			Hash:          block.Hash(),
			PartSetHeader: blockParts.Header(),
			StateID:       block.StateID().Hash(),
		},
		block,
		commit,
		// consensus holds no proof for the block's LastCommit, so it is verified
		// in full
		types.VerifiedCommit{},
	)
}

func (c *blockExecutor) validate(ctx context.Context, stateData *StateData) error {
	// Validate the block. Consensus holds no proof for its LastCommit, so it is
	// verified in full.
	err := c.blockExec.ValidateBlockWithRoundState(ctx, stateData.state, stateData.CurrentRoundState,
		stateData.ProposalBlock, types.VerifiedCommit{})
	if err != nil {
		step := stateData.Step.String()
		return fmt.Errorf("invalid block %X (step %s): %w", step, stateData.CurrentRoundState.AppHash, err)
	}
	return nil
}

func (c *blockExecutor) mustValidate(ctx context.Context, stateData *StateData) {
	err := c.validate(ctx, stateData)
	if err != nil {
		panic(err)
	}
}

func (c *blockExecutor) setCommittedState(committedState sm.State) {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	c.committedState = committedState
}

func (c *blockExecutor) getCommittedState() sm.State {
	c.mtx.RLock()
	defer c.mtx.RUnlock()
	return c.committedState
}

func (c *blockExecutor) Subscribe(emitter *eventemitter.EventEmitter) {
	emitter.AddListener(setPrivValidatorEventName, func(obj eventemitter.EventData) error {
		c.privValidator = obj.(privValidator)
		return nil
	})
	emitter.AddListener(setProposedAppVersionEventName, func(obj eventemitter.EventData) error {
		c.proposedAppVersion = obj.(uint64)
		return nil
	})
	emitter.AddListener(committedStateUpdateEventName, func(obj eventemitter.EventData) error {
		c.setCommittedState(obj.(sm.State))
		return nil
	})
}
