package consensus

import (
	"context"
	"fmt"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	"github.com/tendermint/tendermint/libs/log"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

type EnterPrecommitEvent struct {
	Height int64
	Round  int32
}

// GetType returns EnterPrecommitType event-type
func (e *EnterPrecommitEvent) GetType() EventType {
	return EnterPrecommitType
}

// EnterPrecommitCommand ...
// Enter: `timeoutPrevote` after any +2/3 prevotes.
// Enter: `timeoutPrecommit` after any +2/3 precommits.
// Enter: +2/3 precomits for block or nil.
// Lock & precommit the ProposalBlock if we have enough prevotes for it (a POL in this round)
// else, precommit nil otherwise.
type EnterPrecommitCommand struct {
	logger         log.Logger
	eventPublisher *EventPublisher
	blockExec      *blockExecutor
	voteSigner     *VoteSigner
}

// Execute ...
// Enter: `timeoutPrevote` after any +2/3 prevotes.
// Enter: `timeoutPrecommit` after any +2/3 precommits.
// Enter: +2/3 precomits for block or nil.
// Lock & precommit the ProposalBlock if we have enough prevotes for it (a POL in this round)
// else, precommit nil otherwise.
func (c *EnterPrecommitCommand) Execute(ctx context.Context, stateEvent StateEvent) error {
	event := stateEvent.Data.(*EnterPrecommitEvent)
	stateData := stateEvent.StateData
	height := event.Height
	round := event.Round
	logger := c.logger.With("new_height", height, "new_round", round)

	if stateData.Height != height || round < stateData.Round || (stateData.Round == round && cstypes.RoundStepPrecommit <= stateData.Step) {
		logger.Debug("entering precommit step with invalid args",
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
	if !ok {
		if stateData.LockedBlock != nil {
			logger.Debug("precommit step; no +2/3 prevotes during enterPrecommit while we are locked; precommitting nil")
		} else {
			logger.Debug("precommit step; no +2/3 prevotes during enterPrecommit; precommitting nil")
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
		logger.Debug("precommit step: +2/3 prevoted for nil; precommitting nil")
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
		logger.Debug("precommit step: proposal timestamp not equal; precommitting nil")
		c.voteSigner.signAddVote(ctx, stateData, tmproto.PrecommitType, types.BlockID{})
		return nil
	}

	// If we're already locked on that block, precommit it, and update the LockedRound
	if stateData.LockedBlock.HashesTo(blockID.Hash) {
		logger.Debug("precommit step: +2/3 prevoted locked block; relocking")
		stateData.LockedRound = round

		c.eventPublisher.PublishRelockEvent(stateData.RoundState)
		c.voteSigner.signAddVote(ctx, stateData, tmproto.PrecommitType, blockID)
		return nil
	}

	// If greater than 2/3 of the voting power on the network prevoted for
	// the proposed block, update our locked block to this block and issue a
	// precommit vote for it.
	if stateData.ProposalBlock.HashesTo(blockID.Hash) {
		logger.Debug("precommit step: +2/3 prevoted proposal block; locking", "hash", blockID.Hash)

		// we got precommit but we didn't process proposal yet
		c.blockExec.mustProcess(ctx, stateData, round)

		// Validate the block.
		c.blockExec.mustValidate(ctx, stateData)

		stateData.LockedRound = round
		stateData.LockedBlock = stateData.ProposalBlock
		stateData.LockedBlockParts = stateData.ProposalBlockParts

		c.eventPublisher.PublishLockEvent(stateData.RoundState)
		c.voteSigner.signAddVote(ctx, stateData, tmproto.PrecommitType, blockID)
		return nil
	}

	// There was a polka in this round for a block we don't have.
	// Fetch that block, and precommit nil.
	logger.Debug("precommit step: +2/3 prevotes for a block we do not have; voting nil", "block_id", blockID)

	if !stateData.ProposalBlockParts.HasHeader(blockID.PartSetHeader) {
		stateData.ProposalBlock = nil
		stateData.metrics.MarkBlockGossipStarted()
		stateData.ProposalBlockParts = types.NewPartSetFromHeader(blockID.PartSetHeader)
	}

	c.voteSigner.signAddVote(ctx, stateData, tmproto.PrecommitType, types.BlockID{})
	return nil
}
