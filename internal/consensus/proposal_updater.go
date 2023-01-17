package consensus

import (
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

type proposalUpdater struct {
	logger         log.Logger
	eventPublisher *EventPublisher
}

func (u *proposalUpdater) updateStateData(stateData *StateData, blockID types.BlockID) error {
	stateData.replaceProposalBlockOnLockedBlock(blockID)
	if stateData.ProposalBlock.HashesTo(blockID.Hash) || stateData.ProposalBlockParts.HasHeader(blockID.PartSetHeader) {
		return nil
	}
	// If we don't have the block being committed, set up to get it.
	u.logger.Info(
		"commit is for a block we do not know about; set ProposalBlock=nil",
		"proposal", stateData.ProposalBlock.Hash(),
		"commit", blockID.Hash,
	)
	// We're getting the wrong block.
	// Set up ProposalBlockParts and keep waiting.
	stateData.ProposalBlock = nil
	stateData.metrics.MarkBlockGossipStarted()
	stateData.ProposalBlockParts = types.NewPartSetFromHeader(blockID.PartSetHeader)
	err := stateData.Save()
	if err != nil {
		return err
	}
	u.eventPublisher.PublishValidBlockEvent(stateData.RoundState)
	return nil
}
