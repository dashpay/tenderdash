package consensus

import (
	"context"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/types"
)

func queryMaj23GossipHandler(ps *PeerState, gossiper Gossiper) gossipHandlerFunc {
	return func(ctx context.Context, appState AppState) {
		// If peer is not a validator, we do nothing
		if !appState.isValidator(ps.GetProTxHash()) {
			return
		}
		rs := appState.RoundState
		prs := ps.GetRoundState()
		gossiper.GossipVoteSetMaj23(ctx, rs, prs)
	}
}

func votesAndCommitGossipHandler(
	ps *PeerState,
	blockStore sm.BlockStore,
	gossiper Gossiper,
) gossipHandlerFunc {
	return func(ctx context.Context, appState AppState) {
		rs := appState.RoundState
		prs := ps.GetRoundState()
		isValidator := appState.isValidator(ps.GetProTxHash())
		//	If there are lastCommits to send
		if shouldCommitBeGossiped(rs, prs) {
			gossiper.GossipCommit(ctx, rs, prs)
			return
		}
		// if height matches, then send LastCommit, Prevotes, and Precommits
		if shouldVoteBeGossiped(rs, prs, isValidator) {
			gossiper.GossipVote(ctx, rs, prs)
			return
		}
		// catchup logic -- if peer is lagging by more than 1, syncProposalBlockPart Commit
		// note that peer can ignore a commit if it doesn't have a complete block,
		// so we might need to resend it until it notifies us that it's all right
		if shouldCommitBeGossipedForCatchup(rs, prs, blockStore.Base()) {
			gossiper.GossipCommit(ctx, rs, prs)
		}
	}
}

func dataGossipHandler(ps *PeerState, logger log.Logger, blockStore sm.BlockStore, gossiper Gossiper) gossipHandlerFunc {
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
				gossiper.GossipProposalBlockParts(ctx, rs, prs)
				return
			}
		}

		// if the peer is on a previous height that we have, help catch up
		blockStoreBase := blockStore.Base()
		if shouldPeerBeCaughtUp(rs, prs, blockStoreBase) {
			if prs.ProposalBlockParts != nil {
				gossiper.GossipBlockPartsAndCommitForCatchup(ctx, rs, prs)
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
			gossiper.GossipProposal(ctx, rs, prs)
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

func shouldCommitBeGossipedForCatchup(rs cstypes.RoundState, prs *cstypes.PeerRoundState, blockStoreBase int64) bool {
	return rs.Height >= prs.Height+2 && prs.Height >= blockStoreBase && !prs.HasCommit
}

func shouldCommitBeGossiped(rs cstypes.RoundState, prs *cstypes.PeerRoundState) bool {
	return prs.Height > 0 && prs.Height+1 == rs.Height && !prs.HasCommit
}

func shouldVoteBeGossiped(rs cstypes.RoundState, prs *cstypes.PeerRoundState, isValidator bool) bool {
	return isValidator && rs.Height == prs.Height
}

func getVoteSetForGossip(rs cstypes.RoundState, prs *cstypes.PeerRoundState) *types.VoteSet {
	// if there are lastPrecommits to send
	if prs.Step == cstypes.RoundStepNewHeight {
		return rs.LastPrecommits
	}
	// if there are POL prevotes to send
	if prs.Step <= cstypes.RoundStepPropose && prs.Round != -1 && prs.Round <= rs.Round && prs.ProposalPOLRound != -1 {
		return rs.Votes.Prevotes(prs.ProposalPOLRound)
	}
	// if there are prevotes to send
	if prs.Step <= cstypes.RoundStepPrevoteWait && prs.Round != -1 && prs.Round <= rs.Round {
		return rs.Votes.Prevotes(prs.Round)
	}
	// if there are precommits to send
	if prs.Step <= cstypes.RoundStepPrecommitWait && prs.Round != -1 && prs.Round <= rs.Round {
		return rs.Votes.Precommits(prs.Round)
	}
	// if there are prevotes to send (which are needed because of validBlock mechanism)
	if prs.Round != -1 && prs.Round <= rs.Round {
		return rs.Votes.Prevotes(prs.Round)
	}
	// if there are POLPrevotes to send
	if prs.ProposalPOLRound != -1 {
		return rs.Votes.Prevotes(prs.ProposalPOLRound)
	}
	return nil
}
