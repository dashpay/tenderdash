package consensus

import (
	"context"

	"github.com/cosmos/gogoproto/proto"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
)

func queryMaj23GossipHandler(ps *PeerState, gossiper Gossiper) gossipHandlerFunc {
	return func(ctx context.Context, stateData StateData) {
		// If peer is not a validator, we do nothing
		if !stateData.isValidator(ps.GetProTxHash()) {
			return
		}
		rs := stateData.RoundState
		prs := ps.GetRoundState()
		gossiper.GossipVoteSetMaj23(ctx, rs, prs)
	}
}

// roundSkipGossipHandler sends this node's round skip certificate to a peer at
// its height that is not known to hold it.
//
// Certificates are broadcast once, when a node first holds one, and nothing
// else moves a node past a round whose proposer went silent while everyone
// waited: there are no prevotes to follow into the next round, and the next
// proposal is sent only to peers already in its round. A peer that missed the
// broadcast would wait in the old round until the height ended.
//
// That goes for a peer in a later round too. It can have got there by
// following +2/3 prevotes of that round, without the certificate, and be the
// only link between this node and a peer still in the skipped round. So the
// certificate goes to every peer until it sends one back, which it does when
// it first holds it, or one for a later round.
func roundSkipGossipHandler(
	ps *PeerState,
	logger log.Logger,
	send func(ctx context.Context, msg proto.Message) error,
) gossipHandlerFunc {
	return func(ctx context.Context, stateData StateData) {
		certificate := stateData.RoundSkip
		if certificate == nil {
			return
		}
		prs := ps.GetRoundState()
		// A peer committing the height has no use for it.
		if prs.Height != certificate.Height || prs.Step >= cstypes.RoundStepApplyCommit ||
			ps.HasRoundSkip(certificate.Height, certificate.Round) {
			return
		}
		err := send(ctx, &tmcons.RoundSkipCertificate{Certificate: *certificate.ToProto()})
		if err != nil {
			logger.Debug("cannot send round skip certificate to a peer",
				"height", certificate.Height, "round", certificate.Round, "peer_round", prs.Round, "err", err)
		}
	}
}

func votesAndCommitGossipHandler(
	ps *PeerState,
	blockStore sm.BlockStore,
	gossiper Gossiper,
) gossipHandlerFunc {
	return func(ctx context.Context, stateData StateData) {
		rs := stateData.RoundState
		prs := ps.GetRoundState()
		isValidator := stateData.isValidator(ps.GetProTxHash())
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
	return func(ctx context.Context, stateData StateData) {
		isValidator := stateData.isValidator(ps.GetProTxHash())
		rs := stateData.RoundState
		prs := ps.GetRoundState()

		// Send proposal Block parts?
		if shouldBlockPartsBeGossiped(rs, prs, isValidator) {
			if !isValidator && prs.HasCommit && prs.ProposalBlockParts == nil {
				// We can assume if they have the commit then they should have the same part set header
				ps.UpdateProposalBlockParts(rs.ProposalBlockParts)
				prs = ps.GetRoundState()
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
				gossiper.GossipBlockPartsForCatchup(ctx, rs, prs)
				// GossipBlockPartsForCatchup is rate-limited between passes, not
				// per tick (see catchupResendInterval); GossipCommit runs
				// regardless, so the peer still gets its missing commit while a
				// part-set pass is between replays.
				gossiper.GossipCommit(ctx, rs, prs)
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
				"blockstore_base", blockStoreBase,
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
	if rs.Height != prs.Height || rs.Round < prs.Round {
		return false
	}

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
