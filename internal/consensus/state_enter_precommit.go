package consensus

import (
	"context"
	"fmt"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	tmstrings "github.com/dashpay/tenderdash/internal/libs/strings"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

type EnterPrecommitEvent struct {
	Height int64
	Round  int32
}

// GetType returns EnterPrecommitType event-type
func (e *EnterPrecommitEvent) GetType() EventType {
	return EnterPrecommitType
}

// EnterPrecommitAction ...
// Enter: `timeoutPrevote` after any +2/3 prevotes.
// Enter: `timeoutPrecommit` after any +2/3 precommits.
// Enter: +2/3 precomits for block or nil.
// Lock & precommit the ProposalBlock if we have enough prevotes for it (a POL in this round)
// else, precommit nil otherwise.
type EnterPrecommitAction struct {
	logger         log.Logger
	eventPublisher *EventPublisher
	blockExec      *blockExecutor
	voteSigner     *voteSigner
}

// Execute ...
// Enter: `timeoutPrevote` after any +2/3 prevotes.
// Enter: `timeoutPrecommit` after any +2/3 precommits.
// Enter: +2/3 precomits for block or nil.
// Lock & precommit the ProposalBlock if we have enough prevotes for it (a POL in this round)
// else, precommit nil otherwise.
func (c *EnterPrecommitAction) Execute(ctx context.Context, stateEvent StateEvent) error {
	event := stateEvent.Data.(*EnterPrecommitEvent)
	stateData := stateEvent.StateData
	height := event.Height
	round := event.Round
	logger := c.logger.With("new_height", height, "new_round", round)

	if stateData.Height != height || round < stateData.Round || (stateData.Round == round && cstypes.RoundStepPrecommit <= stateData.Step) {
		logger.Trace("entering precommit step with invalid args",
			"height", stateData.Height,
			"round", stateData.Round,
			"step", stateData.Step)
		return nil
	}

	logger.Debug("entering precommit step",
		"height", stateData.Height,
		"round", stateData.Round,
		"step", stateData.Step)

	defer func() {
		// Done enterPrecommit:
		stateData.updateRoundStep(round, cstypes.RoundStepPrecommit)
	}()

	// check for a polka
	blockID, ok := stateData.Votes.Prevotes(round).TwoThirdsMajority()

	// If we don't have a polka, we must precommit nil.
	// From protocol perspective it's an error condition, so we log it on Error level.
	if !ok {
		if stateData.LockedBlock != nil {
			logger.Error("precommit step; no +2/3 prevotes during enterPrecommit while we are locked; precommitting nil")
		} else {
			logger.Error("precommit step; no +2/3 prevotes during enterPrecommit; precommitting nil")
		}

		c.voteSigner.signAddVote(ctx, stateData, tmproto.PrecommitType, types.BlockID{})
		return nil
	}

	// At this point +2/3 prevoted for a particular block or nil.
	c.eventPublisher.PublishPolkaEvent(stateData.RoundState)

	// the latest POLRound should be this round.
	polRound, _ := stateData.Votes.POLInfo()
	if polRound < round {
		panic(fmt.Sprintf("this POLRound should be %v but got %v", round, polRound))
	}

	// +2/3 prevoted nil. Precommit nil.
	if blockID.IsNil() {
		// From protocol perspective it's an error condition, so we log it on Error level.
		logger.Error("precommit step: +2/3 prevoted for nil; precommitting nil")
		c.voteSigner.signAddVote(ctx, stateData, tmproto.PrecommitType, types.BlockID{})
		return nil
	}
	// At this point, +2/3 prevoted for a particular block.

	// If we never received a proposal for this block, we must precommit nil
	if stateData.Proposal == nil || stateData.ProposalBlock == nil {
		logger.Debug("precommit step; did not receive proposal, precommitting nil")
		c.voteSigner.signAddVote(ctx, stateData, tmproto.PrecommitType, types.BlockID{})
		return nil
	}

	// If the proposal time does not match the block time, precommit nil.
	if !stateData.Proposal.Timestamp.Equal(stateData.ProposalBlock.Header.Time) {
		logger.Error("precommit step: proposal timestamp not equal; precommitting nil")
		c.voteSigner.signAddVote(ctx, stateData, tmproto.PrecommitType, types.BlockID{})
		return nil
	}

	// +2/3 prevoted a block other than the one proposed in this round.
	//
	// We precommit a block only once it is this round's proposal (Tendermint
	// paper, line 36: a PROPOSAL for v in round r and 2f+1 PREVOTEs for id(v) in
	// round r), including a block we are already locked on. A precommit for a
	// block carries our vote extensions, and the application extends a vote only
	// for the block it last ran ProcessProposal for, at this height and round:
	// Dash Drive refuses anything else and ExtendVote panics on the refusal. A
	// block locked in an earlier round was processed for that round, not this one,
	// and a proposal for another block in this round means its proposer
	// equivocated, since honest validators prevote only the proposal they
	// received.
	//
	// Precommitting nil is always safe and the lock is kept as it is. Honest
	// validators that received the proposal for the block precommit it, and a
	// later proposer re-proposes its valid block with this round as its POL round.
	proposalMatches := stateData.ProposalBlock.BlockID(stateData.ProposalBlockParts).Equals(blockID)
	if !proposalMatches && stateData.LockedBlock.HashesTo(blockID.Hash) {
		logger.Error("precommit step: +2/3 prevoted locked block, but this round's proposal is for another block; precommitting nil",
			"locked_block", blockID.Hash,
			"proposal_block", tmstrings.LazyBlockHash(stateData.ProposalBlock))
		c.voteSigner.signAddVote(ctx, stateData, tmproto.PrecommitType, types.BlockID{})
		return nil
	}

	// If greater than 2/3 of the voting power on the network prevoted for the
	// proposed block, lock on it -- or, if already locked on it, update the
	// locked round -- and precommit it.
	if proposalMatches {
		// Process the proposal for this round before precommitting it, since the
		// precommit's vote extensions are for this round. It has not run yet if the
		// block arrived after we prevoted, and a block we are locked on was
		// processed for the round it was locked in, not necessarily this one.
		c.blockExec.mustEnsureProcess(ctx, &stateData.RoundState, round)

		// Validate the block.
		c.blockExec.mustValidate(ctx, stateData)

		// Already locked on it: update the locked round. ValidBlock needs no update
		// here: the polka and the proposal block are both for this round, and
		// whichever arrived last already made the block valid for this round
		// (addVoteUpdateValidBlockMw or ProposalCompletedAction).
		if stateData.LockedBlock.HashesTo(blockID.Hash) {
			logger.Debug("precommit step: +2/3 prevoted locked block; relocking", "hash", blockID.Hash)
			stateData.LockedRound = round

			c.eventPublisher.PublishRelockEvent(stateData.RoundState)
			c.voteSigner.signAddVote(ctx, stateData, tmproto.PrecommitType, blockID)
			return nil
		}

		logger.Debug("precommit step: +2/3 prevoted proposal block; locking", "hash", blockID.Hash)
		stateData.updateLockedBlock()

		c.eventPublisher.PublishLockEvent(stateData.RoundState)
		c.voteSigner.signAddVote(ctx, stateData, tmproto.PrecommitType, blockID)

		if stateData.updateValidBlock() {
			c.eventPublisher.PublishValidBlockEvent(stateData.RoundState)
		}

		return nil
	}

	// There was a polka in this round for a block we don't have.
	// Fetch that block, and precommit nil.
	logger.Debug("precommit step: +2/3 prevotes for a block we do not have; voting nil", "block_id", blockID)

	stateData.retargetTo(blockID, retargetOnPrecommit)

	c.voteSigner.signAddVote(ctx, stateData, tmproto.PrecommitType, types.BlockID{})
	return nil
}
