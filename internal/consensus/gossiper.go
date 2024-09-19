//go:generate ../../scripts/mockery_generate.sh Gossiper

package consensus

import (
	"context"
	"fmt"

	"github.com/gogo/protobuf/proto"
	"github.com/hashicorp/go-multierror"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// Gossiper is the interface that wraps the methods needed to gossip a state between connected peers
type Gossiper interface {
	GossipProposal(ctx context.Context, rs cstypes.RoundState, prs *cstypes.PeerRoundState)
	GossipProposalBlockParts(ctx context.Context, rs cstypes.RoundState, prs *cstypes.PeerRoundState)
	GossipBlockPartsForCatchup(ctx context.Context, rs cstypes.RoundState, prs *cstypes.PeerRoundState)
	GossipVote(ctx context.Context, rs cstypes.RoundState, prs *cstypes.PeerRoundState)
	GossipVoteSetMaj23(ctx context.Context, rs cstypes.RoundState, prs *cstypes.PeerRoundState)
	GossipCommit(ctx context.Context, rs cstypes.RoundState, prs *cstypes.PeerRoundState)
}

type msgGossiper struct {
	logger     log.Logger
	ps         *PeerState
	msgSender  *p2pMsgSender
	blockStore *blockRepository
	optimistic bool
}

func newVoteSetMaj23(height int64, round int32, msgType tmproto.SignedMsgType, maj23 types.BlockID) *tmcons.VoteSetMaj23 {
	return &tmcons.VoteSetMaj23{
		Height:  height,
		Round:   round,
		Type:    msgType,
		BlockID: maj23.ToProto(),
	}
}

func newVoteSetMaj23FromCommit(commit *types.Commit, msgType tmproto.SignedMsgType) *tmcons.VoteSetMaj23 {
	return newVoteSetMaj23(commit.Height, commit.Round, msgType, commit.BlockID)
}

func newVoteSetMaj23FromPRS(prs *cstypes.PeerRoundState, msgType tmproto.SignedMsgType, maj23 types.BlockID) *tmcons.VoteSetMaj23 {
	return newVoteSetMaj23(prs.Height, prs.Round, msgType, maj23)
}

// GossipVoteSetMaj23 sends VoteSetMaj23 messages to the peer
func (g *msgGossiper) GossipVoteSetMaj23(
	ctx context.Context,
	rs cstypes.RoundState,
	prs *cstypes.PeerRoundState,
) {
	msgs := make([]*tmcons.VoteSetMaj23, 0, 4)
	if rs.Height == prs.Height {
		// maybe send Height/Round/Prevotes
		maj23, ok := rs.Votes.Prevotes(prs.Round).TwoThirdsMajority()
		if ok {
			msgs = append(msgs, newVoteSetMaj23FromPRS(prs, tmproto.PrevoteType, maj23))
		}
	}
	if rs.Height == prs.Height && prs.ProposalPOLRound >= 0 {
		// maybe send Height/Round/ProposalPOL
		maj23, ok := rs.Votes.Prevotes(prs.ProposalPOLRound).TwoThirdsMajority()
		if ok {
			msgs = append(msgs, newVoteSetMaj23FromPRS(prs, tmproto.PrevoteType, maj23))
		}
	}
	if rs.Height == prs.Height {
		// maybe send Height/Round/Precommits
		maj23, ok := rs.Votes.Precommits(prs.Round).TwoThirdsMajority()
		if ok {
			msgs = append(msgs, newVoteSetMaj23FromPRS(prs, tmproto.PrecommitType, maj23))
		}
	}
	// Little point sending LastCommitRound/LastCommit, these are fleeting and
	// non-blocking.
	if prs.CatchupCommitRound != -1 && prs.Height > 0 {
		bsHeight := g.blockStore.Height()
		bsBase := g.blockStore.Base()
		if prs.Height <= bsHeight && prs.Height >= bsBase {
			// maybe send Height/CatchupCommitRound/CatchupCommit
			commit := g.blockStore.loadCommit(prs.Height)
			if commit != nil {
				msgs = append(msgs, newVoteSetMaj23FromCommit(commit, tmproto.PrecommitType))
			}
		}
	}
	logger := g.logger.With([]any{
		"height", prs.Height,
		"round", prs.Round,
	})
	for _, msg := range msgs {
		logger.Trace("syncing vote set +2/3 message")
		err := g.msgSender.send(ctx, msg)
		if err != nil {
			logger.Error("failed to syncing vote set +2/3 message to the peer", "error", err)
		}
	}
}

// GossipProposalBlockParts sends a block part message to the peer
func (g *msgGossiper) GossipProposalBlockParts(
	ctx context.Context,
	rs cstypes.RoundState,
	prs *cstypes.PeerRoundState,
) {
	index, ok := rs.ProposalBlockParts.BitArray().Sub(prs.ProposalBlockParts.Copy()).PickRandom()
	if !ok {
		return
	}
	logger := g.logger.With([]any{
		"height", prs.Height,
		"round", prs.Round,
		"part_index", index,
	})
	logger.Trace("syncing proposal block part to the peer")
	part := rs.ProposalBlockParts.GetPart(index)
	// NOTE: A peer might have received a different proposal message, so this Proposal msg will be rejected!
	err := g.syncProposalBlockPart(ctx, part, rs.Height, rs.Round)
	if err != nil {
		logger.Error("failed to sync proposal block part to the peer", "error", err)
	}
}

// GossipProposal sends a proposal message to the peer
func (g *msgGossiper) GossipProposal(ctx context.Context, rs cstypes.RoundState, prs *cstypes.PeerRoundState) {
	logger := g.logger.With([]any{
		"height", prs.Height,
		"round", prs.Round,
	})
	// Proposal: share the proposal metadata with peer.
	logger.Trace("syncing proposal")
	err := g.sync(ctx, rs.Proposal.ToProto(), updatePeerProposal(g.ps, rs.Proposal))
	if err != nil {
		logger.Error("failed to sync proposal to the peer", "error", err)
	}
	// ProposalPOL: lets peer know which POL votes we have so far. The peer
	// must receive ProposalMessage first. Note, rs.Proposal was validated,
	// so rs.Proposal.POLRound <= rs.Round, so we definitely have
	// rs.Votes.Prevotes(rs.Proposal.POLRound).
	if rs.Proposal.POLRound < 0 {
		return
	}
	pPol := rs.Votes.Prevotes(rs.Proposal.POLRound).BitArray()
	pPolProto := pPol.ToProto()
	propPOLMsg := &tmcons.ProposalPOL{
		Height:           rs.Height,
		ProposalPolRound: rs.Proposal.POLRound,
		ProposalPol:      *pPolProto,
	}
	logger.Trace("syncing proposal POL")
	err = g.sync(ctx, propPOLMsg, nil)
	if err != nil {
		logger.Error("failed to sync proposal POL to the peer", "error", err)
	}
}

// GossipBlockPartsForCatchup sends a block part for catch up
func (g *msgGossiper) GossipBlockPartsForCatchup(
	ctx context.Context,
	_ cstypes.RoundState,
	prs *cstypes.PeerRoundState,
) {
	index, ok := prs.ProposalBlockParts.Not().PickRandom()
	if !ok {
		return
	}
	logger := g.logger.With([]any{
		"height", prs.Height,
		"round", prs.Round,
	})
	meta, err := g.blockStore.loadMeta(prs.Height)
	if err != nil {
		logger.Error("couldn't find a block meta", "error", err)
		return
	}
	err = g.ensurePeerPartSetHeader(meta.BlockID.PartSetHeader, prs.ProposalBlockPartSetHeader)
	if err != nil {
		logger.Error("block and peer part-set headers do not match", "error", err)
		return
	}
	part, err := g.blockStore.loadPart(prs.Height, index)
	if err != nil {
		logger.Error("couldn't find a block part", "part_index", index, "error", err)
		return
	}
	err = g.syncProposalBlockPart(ctx, part, prs.Height, meta.Round)
	if err != nil {
		logger.Error("failed to sync proposal block part to the peer", "error", err)
	}
}

// GossipCommit sends a commit message to the peer
func (g *msgGossiper) GossipCommit(ctx context.Context, rs cstypes.RoundState, prs *cstypes.PeerRoundState) {
	if prs.HasCommit {
		return
	}
	logger := g.logger.With(
		"height", rs.Height,
		"peer_height", prs.Height,
	)
	var commit *types.Commit
	blockStoreBase := g.blockStore.Base()
	if rs.Height == prs.Height+1 {
		commit = rs.LastCommit
	}
	if rs.Height >= prs.Height+2 && prs.Height >= blockStoreBase {
		// Load the block commit for prs.Height, which contains precommit
		// signatures for prs.Height.
		commit = g.blockStore.LoadBlockCommit(prs.Height)
	}
	if commit == nil {
		if prs.Height == 0 {
			return // not an error when we are at genesis
		}
		logger.Error("commit not found")
		return
	}
	logger.Debug("syncing commit")
	err := g.sync(ctx, commit.ToProto(), updatePeerCommit(g.ps, commit))
	if err != nil {
		logger.Error("failed to sync commit to the peer", "error", err)
	}
}

// GossipVote sends a vote message to the peer
func (g *msgGossiper) GossipVote(ctx context.Context, rs cstypes.RoundState, prs *cstypes.PeerRoundState) {
	vote, found := g.pickVoteForGossip(rs, prs)
	if !found {
		return
	}
	protoVote := vote.ToProto()
	logger := g.logger.With([]any{
		"vote", vote,
		"val_proTxHash", vote.ValidatorProTxHash.ShortString(),
		"vote_height", vote.Height,
		"vote_round", vote.Round,
		"proto_vote_size", protoVote.Size(),
	})
	logger.Trace("syncing vote message")
	err := g.sync(ctx, protoVote, updatePeerVote(g.ps, vote))
	if err != nil {
		logger.Error("failed to sync vote message to the peer", "error", err)
	}
}

func (g *msgGossiper) syncProposalBlockPart(ctx context.Context, part *types.Part, height int64, round int32) error {
	protoPart, err := part.ToProto()
	if err != nil {
		return fmt.Errorf("failed to convert block part to proto, error: %w", err)
	}
	logger := g.logger.With([]any{
		"height", height,
		"round", round,
		"part_index", part.Index,
	})
	protoBlockPart := &tmcons.BlockPart{
		Height: height, // not our height, so it does not matter
		Round:  round,  // not our height, so it does not matter
		Part:   *protoPart,
	}
	logger.Debug("syncing proposal block part")
	err = g.sync(ctx, protoBlockPart, updatePeerProposalBlockPart(g.ps, height, round, int(part.Index)))
	if err != nil {
		logger.Error("failed to sync proposal block part to the peer", "error", err)
	}
	return nil
}

func (g *msgGossiper) sync(ctx context.Context, protoMsg proto.Message, syncFunc func() error) error {
	err := g.msgSender.send(ctx, protoMsg)
	if err != nil {
		if !g.optimistic {
			return err
		}
	}
	if syncFunc == nil {
		return err
	}
	syncErr := syncFunc()
	if syncErr != nil {
		err = multierror.Append(syncErr)
	}
	return err
}

func (g *msgGossiper) ensurePeerPartSetHeader(blockPartSetHeader types.PartSetHeader, peerPartSetHeader types.PartSetHeader) error {
	// ensure that the peer's PartSetHeader is correct
	if blockPartSetHeader.Equals(peerPartSetHeader) {
		return nil
	}
	g.logger.Debug(
		"peer ProposalBlockPartSetHeader mismatch",
		"block_part_set_header", blockPartSetHeader,
		"peer_block_part_set_header", peerPartSetHeader,
	)
	return fmt.Errorf("peer block part-set header %s is mismatch with block part-set header %s",
		peerPartSetHeader.String(),
		blockPartSetHeader.String())
}

// pickVoteForGossip picks a vote to sends it to the peer. It will return (*types.Vote and true) if
// there is a vote to send and (nil,false) otherwise.
func (g *msgGossiper) pickVoteForGossip(rs cstypes.RoundState, prs *cstypes.PeerRoundState) (*types.Vote, bool) {
	var voteSets []*types.VoteSet
	if prs.Round != -1 && prs.Round <= rs.Round {
		// if there are POL prevotes to send
		if prs.Step <= cstypes.RoundStepPropose && prs.ProposalPOLRound != -1 {
			voteSets = append(voteSets, rs.Votes.Prevotes(prs.ProposalPOLRound))
		}
		// if there are prevotes to send
		if prs.Step <= cstypes.RoundStepPrevoteWait {
			voteSets = append(voteSets, rs.Votes.Prevotes(prs.Round))
		}
		// if there are precommits to send
		if prs.Step <= cstypes.RoundStepPrecommitWait {
			voteSets = append(voteSets, rs.Votes.Precommits(prs.Round))
		}
		// if there are prevotes to send (which are needed because of validBlock mechanism)
		voteSets = append(voteSets, rs.Votes.Prevotes(prs.Round))
	}
	// if there are POLPrevotes to send
	if prs.ProposalPOLRound != -1 {
		voteSets = append(voteSets, rs.Votes.Prevotes(prs.ProposalPOLRound))
	}
	for _, voteSet := range voteSets {
		vote, ok := g.ps.PickVoteToSend(voteSet)
		if ok {
			return vote, true
		}
	}
	return nil, false
}

type blockRepository struct {
	sm.BlockStore
	logger log.Logger
}

// LoadCommit loads the commit for a given height.
func (r *blockRepository) loadCommit(height int64) *types.Commit {
	if height == r.Height() {
		commit := r.LoadSeenCommit()
		// NOTE: Retrieving the height of the most recent block and retrieving
		// the most recent commit does not currently occur as an atomic
		// operation. We check the height and commit here in case a more recent
		// commit has arrived since retrieving the latest height.
		if commit != nil && commit.Height == height {
			return commit
		}
	}

	return r.LoadBlockCommit(height)
}

func (r *blockRepository) loadMeta(height int64) (*types.BlockMeta, error) {
	// ensure that the peer's PartSetHeader is correct
	blockMeta := r.LoadBlockMeta(height)
	if blockMeta != nil {
		return blockMeta, nil
	}
	r.logger.Error(
		"failed to load block meta",
		"our_height", height,
		"blockstore_base", r.Base(),
		"blockstore_height", r.Height(),
	)
	return nil, fmt.Errorf("failed to load block meta at height %d", height)
}

func (r *blockRepository) loadPart(height int64, index int) (*types.Part, error) {
	part := r.LoadBlockPart(height, index)
	if part != nil {
		return part, nil
	}
	r.logger.Error(
		"failed to load block part",
		"height", height,
		"index", index,
	)
	return nil, errFailedLoadBlockPart
}

func updatePeerProposal(ps *PeerState, proposal *types.Proposal) func() error {
	return func() error {
		ps.SetHasProposal(proposal)
		return nil
	}
}

func updatePeerCommit(ps *PeerState, commit *types.Commit) func() error {
	return func() error {
		ps.SetHasCommit(commit)
		return nil
	}
}

func updatePeerVote(ps *PeerState, vote *types.Vote) func() error {
	return func() error {
		return ps.SetHasVote(vote)
	}
}

func updatePeerProposalBlockPart(ps *PeerState, height int64, round int32, index int) func() error {
	return func() error {
		ps.SetHasProposalBlockPart(height, round, index)
		return nil
	}
}
