package consensus

import (
	tmstrings "github.com/dashpay/tenderdash/internal/libs/strings"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

type proposalUpdater struct {
	logger         log.Logger
	eventPublisher *EventPublisher
}

func (u *proposalUpdater) updateStateData(stateData *StateData, blockID types.BlockID) error {
	stateData.replaceProposalBlockOnLockedBlock(blockID)
	if stateData.holdsProposalBlock(blockID) {
		return nil
	}
	u.logger.Debug(
		"retargeting proposal block state at the committed block",
		"proposal_block", tmstrings.LazyBlockHash(stateData.ProposalBlock),
		"commit_block", blockID.Hash,
		"part_set_header_matched", stateData.ProposalBlockParts.HasHeader(blockID.PartSetHeader),
	)
	stateData.retargetTo(blockID, retargetOnApplyCommit)
	err := stateData.Save()
	if err != nil {
		return err
	}
	u.eventPublisher.PublishValidBlockEvent(stateData.RoundState)
	return nil
}
