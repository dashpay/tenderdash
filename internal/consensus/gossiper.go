package consensus

import (
	"context"
	"fmt"

	"github.com/gogo/protobuf/proto"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/log"
	tmcons "github.com/tendermint/tendermint/proto/tendermint/consensus"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

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

func (g *msgGossiper) gossipVoteSetMaj23(
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
	keyVals := []any{
		"height", prs.Height,
		"round", prs.Round,
	}
	for _, msg := range msgs {
		g.logger.Debug("syncing vote set +2/3 message")
		err := g.msgSender.send(ctx, msg)
		if err != nil {
			g.logger.Error("failed to syncing vote set +2/3 message to the peer", logKeyValsWithError(keyVals, err)...)
		}
	}
}

func (g *msgGossiper) gossipProposalBlockParts(
	ctx context.Context,
	rs cstypes.RoundState,
	prs *cstypes.PeerRoundState,
) {
	index, ok := rs.ProposalBlockParts.BitArray().Sub(prs.ProposalBlockParts.Copy()).PickRandom()
	if !ok {
		return
	}
	keyVals := []any{
		"height", prs.Height,
		"round", prs.Round,
		"part_index", index,
	}
	g.logger.Debug("syncing proposal block part to the peer", keyVals...)
	part := rs.ProposalBlockParts.GetPart(index)
	// NOTE: A peer might have received a different proposal message, so this Proposal msg will be rejected!
	err := g.syncProposalBlockPart(ctx, part, rs.Height, rs.Round)
	if err != nil {
		g.logger.Error("failed to sync proposal block part to the peer", logKeyValsWithError(keyVals, err)...)
	}
}

func (g *msgGossiper) gossipProposal(ctx context.Context, rs cstypes.RoundState, prs *cstypes.PeerRoundState) {
	keyVals := []any{
		"height", prs.Height,
		"round", prs.Round,
	}
	// Proposal: share the proposal metadata with peer.
	g.logger.Debug("syncing proposal", keyVals...)
	err := g.sync(ctx, rs.Proposal.ToProto(), updatePeerProposal(g.ps, rs.Proposal))
	if err != nil {
		g.logger.Error("failed to sync proposal to the peer", logKeyValsWithError(keyVals, err)...)
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
	g.logger.Debug("syncing proposal POL", keyVals...)
	err = g.sync(ctx, propPOLMsg, nil)
	if err != nil {
		g.logger.Error("failed to sync proposal POL to the peer", logKeyValsWithError(keyVals, err)...)
	}
}

func (g *msgGossiper) gossipBlockPartsAndCommitForCatchup(
	ctx context.Context,
	rs cstypes.RoundState,
	prs *cstypes.PeerRoundState,
) {
	err := g.gossipBlockPartsForCatchup(ctx, prs)
	if err != nil {
		return
	}
	// block parts already delivered -  send commits?
	if rs.Height == 0 || prs.HasCommit {
		return
	}
	g.gossipCommit(ctx, rs, prs)
}

func (g *msgGossiper) gossipCommit(ctx context.Context, rs cstypes.RoundState, prs *cstypes.PeerRoundState) {
	keyVals := []any{
		"height", rs.Height,
		"peer_height", prs.Height,
	}
	var commit *types.Commit
	blockStoreBase := g.blockStore.Base()
	if prs.Height+1 == rs.Height && !prs.HasCommit {
		commit = rs.LastCommit
	} else if rs.Height >= prs.Height+2 && prs.Height >= blockStoreBase && !prs.HasCommit {
		// Load the block commit for prs.Height, which contains precommit
		// signatures for prs.Height.
		commit = g.blockStore.LoadBlockCommit(prs.Height)
	}
	if commit == nil {
		if prs.Height == 0 {
			return // not an error when we are at genesis
		}
		g.logger.Error("commit not found", keyVals...)
		return
	}
	g.logger.Debug("syncing commit", keyVals...)
	err := g.sync(ctx, commit.ToProto(), updatePeerCommit(g.ps, commit))
	if err != nil {
		g.logger.Error("failed to sync commit to the peer", logKeyValsWithError(keyVals, err))
	}
}

func (g *msgGossiper) gossipVote(ctx context.Context, rs cstypes.RoundState, prs *cstypes.PeerRoundState) {
	votes := getVoteSetForGossip(rs, prs)
	if votes == nil {
		return
	}
	vote, ok := g.ps.PickVoteToSend(votes)
	if !ok {
		return
	}
	protoVote := vote.ToProto()
	keyVals := []any{
		"vote", vote,
		"val_proTxHash", vote.ValidatorProTxHash.ShortString(),
		"vote_height", vote.Height,
		"vote_round", vote.Round,
		"proto_vote_size", protoVote.Size(),
	}
	g.logger.Debug("syncing vote message", keyVals...)
	err := g.sync(ctx, protoVote, updatePeerVote(g.ps, vote))
	if err != nil {
		g.logger.Error("failed to sync vote message to the peer", logKeyValsWithError(keyVals, err)...)
	}
}

func (g *msgGossiper) gossipBlockPartsForCatchup(ctx context.Context, prs *cstypes.PeerRoundState) error {
	index, ok := prs.ProposalBlockParts.Not().PickRandom()
	if !ok {
		return nil
	}
	meta, err := g.blockStore.loadMeta(prs.Height)
	if err != nil {
		return err
	}
	valid := g.ensurePeerPartSetHeader(meta.BlockID.PartSetHeader, prs.ProposalBlockPartSetHeader)
	if !valid {
		return nil
	}
	part, err := g.blockStore.loadPart(prs.Height, index)
	if err != nil {
		return err
	}
	return g.syncProposalBlockPart(ctx, part, prs.Height, meta.Round)
}

func (g *msgGossiper) syncProposalBlockPart(ctx context.Context, part *types.Part, height int64, round int32) error {
	protoPart, err := part.ToProto()
	if err != nil {
		return fmt.Errorf("failed to convert block part to proto, error: %w", err)
	}
	keyVals := []any{
		"height", height,
		"round", round,
		"part_index", part.Index,
	}
	protoBlockPart := &tmcons.BlockPart{
		Height: height, // not our height, so it does not matter
		Round:  round,  // not our height, so it does not matter
		Part:   *protoPart,
	}
	g.logger.Debug("syncing proposal block part", keyVals...)
	err = g.sync(ctx, protoBlockPart, updatePeerProposalBlockPart(g.ps, height, round, int(part.Index)))
	if err != nil {
		g.logger.Error("failed to sync proposal block part to the peer", logKeyValsWithError(keyVals, err)...)
	}
	return nil
}

func (g *msgGossiper) sync(ctx context.Context, protoMsg proto.Message, syncFunc func() error) error {
	var err *peerSyncErr
	sendErr := g.msgSender.send(ctx, protoMsg)
	if sendErr != nil {
		err = &peerSyncErr{sendErr: sendErr}
		if !g.optimistic {
			return err
		}
	}
	if syncFunc == nil {
		return err
	}
	syncErr := syncFunc()
	if syncErr == nil {
		return err
	}
	if err == nil {
		err = &peerSyncErr{}
	}
	err.syncErr = syncErr
	return err
}

func (g *msgGossiper) ensurePeerPartSetHeader(blockPartSetHeader types.PartSetHeader, peerPartSetHeader types.PartSetHeader) bool {
	// ensure that the peer's PartSetHeader is correct
	if blockPartSetHeader.Equals(peerPartSetHeader) {
		return true
	}
	g.logger.Info(
		"peer ProposalBlockPartSetHeader mismatch",
		"block_part_set_header", blockPartSetHeader,
		"peer_block_part_set_header", peerPartSetHeader,
	)
	return false
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

type peerSyncErr struct {
	sendErr error
	syncErr error
}

func (e *peerSyncErr) Error() string {
	err := ""
	if e.sendErr != nil {
		err += e.sendErr.Error()
	}
	if e.syncErr == nil {
		return err
	}
	if err != "" {
		err += ": "
	}
	err += e.syncErr.Error()
	return err
}

func logKeyValsWithError(keyVals []any, err error) []any {
	if err == nil {
		return keyVals
	}
	return append(keyVals, "error", err)
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
