package consensus

import (
	"context"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/log"
)

func dataGossipHandler(ps *PeerState, logger log.Logger, blockStore sm.BlockStore, gossiper *msgGossiper) gossipHandlerFunc {
	return func(ctx context.Context, appState AppState) {
		isValidator := appState.isValidator(ps.GetProTxHash())
		rs := appState.RoundState
		prs := ps.GetRoundState()

		// Send proposal Block parts?
		if shouldBlockPartsBeGossiped(rs, prs, isValidator) {
			if !isValidator && prs.HasCommit && prs.ProposalBlockParts == nil {
				// We can assume if they have the commit then they should have the same part set header
				ps.UpdateProposalBlockParts(rs.ProposalBlockParts)
			}
			_, ok := rs.ProposalBlockParts.BitArray().Sub(prs.ProposalBlockParts.Copy()).PickRandom()
			if ok {
				gossiper.gossipProposalBlockParts(ctx, rs, prs)
				return
			}
		}

		// if the peer is on a previous height that we have, help catch up
		blockStoreBase := blockStore.Base()
		if shouldPeerBeCaughtUp(rs, prs, blockStoreBase) {
			if prs.ProposalBlockParts != nil {
				gossiper.gossipBlockPartsAndCommitForCatchup(ctx, rs, prs)
				return
			}
			// If we never received the commit message from the peer, the block parts
			// will not be initialized.
			blockMeta := blockStore.LoadBlockMeta(prs.Height)
			if blockMeta != nil {
				ps.InitProposalBlockParts(blockMeta.BlockID.PartSetHeader)
				// Stop this handling iteration since prs is a copy and not effected by this
				// initialization.
				return
			}
			logger.Error(
				"failed to load block meta",
				"height", prs.Height,
				"blockstor_base", blockStoreBase,
				"blockstore_height", blockStore.Height(),
			)
			return
		}

		// if height and round don't match
		if (rs.Height != prs.Height) || (rs.Round != prs.Round) {
			return
		}

		// By here, height and round match.
		// Proposal block parts were already matched and sent if any were wanted.
		// (These can match on hash so the round doesn't matter)
		// Now consider sending other things, like the Proposal itself.

		// Send Proposal && ProposalPOL BitArray?
		if shouldProposalBeGossiped(rs, prs, isValidator) {
			gossiper.gossipProposal(ctx, rs, prs)
		}
	}
}

func shouldProposalBeGossiped(rs cstypes.RoundState, prs *cstypes.PeerRoundState, isValidator bool) bool {
	return rs.Proposal != nil && !prs.Proposal && isValidator
}

func shouldBlockPartsBeGossiped(rs cstypes.RoundState, prs *cstypes.PeerRoundState, isValidator bool) bool {
	if isValidator && rs.ProposalBlockParts.HasHeader(prs.ProposalBlockPartSetHeader) {
		return true
	}
	return prs.HasCommit && rs.ProposalBlockParts != nil
}

func shouldPeerBeCaughtUp(rs cstypes.RoundState, prs *cstypes.PeerRoundState, blockStoreBase int64) bool {
	return blockStoreBase > 0 && 0 < prs.Height && prs.Height < rs.Height && prs.Height >= blockStoreBase
}
