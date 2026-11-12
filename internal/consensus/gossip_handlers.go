package consensus

import (
	"context"

	"github.com/cosmos/gogoproto/proto"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	tmmath "github.com/dashpay/tenderdash/libs/math"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	"github.com/dashpay/tenderdash/types"
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

// roundVoteResendGossipHandler sends a validator peer at this node's height
// one vote of the peer's round at every pass, while the peer waits for
// transactions in this node's round, which a skip certificate began, or is in
// a round this node has left.
//
// Vote gossip takes a vote as the peer's once the channel takes it; one lost
// after that is offered again only after a redelivery interval, then with
// backoff (see PeerState.ReofferUnackedVotes), and a later round's votes are
// not offered to a peer in an earlier round at all. On a chain that skips
// rounds, a peer that lost the votes of its round waits all that time: a
// waiting round has no timeout and its proposer's heartbeats keep arriving,
// and the prevote and precommit steps start their timeouts only on votes.
// Votes are picked at random, so over the passes the peer gets each one this
// node has.
//
// A peer waiting in this node's round, after round 0, is sent the round's
// prevotes: one ends the wait (see StateData.waitsAfterRoundSkip). A peer in a
// round this node has left is sent that round's prevotes and precommits by
// turns, until it announces a later round: +2/3 precommits of any kind move it
// to the next round from any step (see addVoteDispatchPrecommitMw), and a
// prevote ends its wait there as in this node's round. It is sent too, by
// turns, the votes of the latest round after its own that +2/3 of any kind
// prevoted or precommitted in (see laterRoundVotes): this node may have
// reached its round on those alone, holding no vote of the peer's round.
func roundVoteResendGossipHandler(
	ps *PeerState,
	logger log.Logger,
	send func(ctx context.Context, msg proto.Message) error,
) gossipHandlerFunc {
	// The worker runs a handler on one goroutine (see runHandler), so its
	// passes need no lock.
	passes := 0
	return func(ctx context.Context, stateData StateData) {
		if !roundSkipping(&stateData) || !stateData.isValidator(ps.GetProTxHash()) {
			return
		}
		rs := stateData.RoundState
		prs := ps.GetRoundState()
		if prs.Height != rs.Height || prs.Round < 0 {
			return
		}
		var voteSets []*types.VoteSet
		switch {
		case prs.Round < rs.Round:
			later := laterRoundVotes(rs.Votes, prs.Round, rs.Round)
			voteSets = make([]*types.VoteSet, 0, 2+len(later))
			voteSets = append(voteSets, rs.Votes.Prevotes(prs.Round), rs.Votes.Precommits(prs.Round))
			voteSets = append(voteSets, later...)
		case prs.Round == rs.Round && prs.Round > 0 && prs.Step == cstypes.RoundStepNewRound:
			voteSets = []*types.VoteSet{rs.Votes.Prevotes(prs.Round)}
		default:
			return
		}
		passes++
		vote := pickRandomVote(voteSets, passes)
		if vote == nil {
			return
		}
		if err := send(ctx, vote.ToProto()); err != nil {
			logger.Debug("cannot resend a vote of its round to a peer",
				"height", vote.Height, "round", vote.Round, "type", vote.Type, "peer_round", prs.Round, "err", err)
		}
	}
}

// laterRoundVotes returns the vote sets of the latest round after from, up to
// round, that hold +2/3 of any kind, or none.
//
// A node goes on to a later round from any round before it once +2/3 of any
// kind prevoted or precommitted there (see addVoteDispatchPrevoteMw and
// addVoteDispatchPrecommitMw), so those votes take a peer from round from to
// that round, or the one after it, in one step. A peer keeps a vote of a round
// it has not reached as a catch-up round of its voter, two per voter at a
// height (see HeightVoteSet.AddVote), and drops any more without blame.
func laterRoundVotes(votes *cstypes.HeightVoteSet, from, round int32) []*types.VoteSet {
	voteSets := make([]*types.VoteSet, 0, 2)
	for ; round > from && len(voteSets) == 0; round-- {
		for _, voteSet := range []*types.VoteSet{votes.Prevotes(round), votes.Precommits(round)} {
			if voteSet.HasTwoThirdsAny() {
				voteSets = append(voteSets, voteSet)
			}
		}
	}
	return voteSets
}

// pickRandomVote returns a vote picked at random from the first of voteSets,
// counted from turn, that holds one. The gossip goroutine reads the vote sets
// while the consensus goroutine adds to them, so only through their locked
// accessors.
func pickRandomVote(voteSets []*types.VoteSet, turn int) *types.Vote {
	for i := range voteSets {
		voteSet := voteSets[(turn+i)%len(voteSets)]
		index, ok := voteSet.BitArray().PickRandom()
		if !ok {
			continue
		}
		valIndex, err := tmmath.SafeConvertInt32(index)
		if err != nil {
			continue
		}
		if vote := voteSet.GetByIndex(valIndex); vote != nil {
			return vote
		}
	}
	return nil
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
