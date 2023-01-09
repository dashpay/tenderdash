package consensus

import (
	"context"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/types"
)

func votesAndCommitGossipHandler(
	ps *PeerState,
	blockStore sm.BlockStore,
	gossiper *msgGossiper,
) gossipHandlerFunc {
	return func(ctx context.Context, appState AppState) {
		rs := appState.RoundState
		prs := ps.GetRoundState()
		isValidator := appState.isValidator(ps.GetProTxHash())
		//	If there are lastCommits to send
		if shouldCommitBeGossiped(rs, prs) {
			gossiper.gossipCommit(ctx, rs, prs)
			return
		}
		// if height matches, then send LastCommit, Prevotes, and Precommits
		if shouldVoteBeGossiped(rs, prs, isValidator) {
			gossiper.gossipVote(ctx, rs, prs)
			return
		}
		// catchup logic -- if peer is lagging by more than 1, syncProposalBlockPart Commit
		// note that peer can ignore a commit if it doesn't have a complete block,
		// so we might need to resend it until it notifies us that it's all right
		if shouldCommitBeGossipedForCatchup(rs, prs, blockStore.Base()) {
			gossiper.gossipCommit(ctx, rs, prs)
		}
	}
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
