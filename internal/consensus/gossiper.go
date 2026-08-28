//go:generate ../../scripts/mockery_generate.sh Gossiper

package consensus

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cosmos/gogoproto/proto"
	"github.com/hashicorp/go-multierror"
	"github.com/jonboulle/clockwork"

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
	// GossipBlockPartsForCatchup sends at most one part per call and enforces a
	// quiet gap of catchupResendInterval between full-part-set replays — it does
	// NOT bound the send rate to one per interval for a multi-part block, which
	// spends several ticks before that gap applies (see catchupResendInterval).
	// The limit is per gossip worker, not per peer: a peer that reconnects gets a
	// fresh worker and a fresh pass. Must be called from a single goroutine per
	// Gossiper instance — its internal locking guards field visibility only, not
	// the check-then-send sequence a second caller would race.
	GossipBlockPartsForCatchup(ctx context.Context, rs cstypes.RoundState, prs *cstypes.PeerRoundState)
	GossipVote(ctx context.Context, rs cstypes.RoundState, prs *cstypes.PeerRoundState)
	GossipVoteSetMaj23(ctx context.Context, rs cstypes.RoundState, prs *cstypes.PeerRoundState)
	GossipCommit(ctx context.Context, rs cstypes.RoundState, prs *cstypes.PeerRoundState)
}

// catchupResendInterval is the quiet gap enforced once a catch-up pass ends —
// by exhausting its budget, failing a send, or being interrupted by a height
// change — before a fresh pass may open. Catch-up deliberately never marks
// parts as delivered, so without this gap a peer we believe is behind would be
// served on every gossip tick. The gap is between passes, not between sends
// within one: a pass spends one send per gossip tick for as many ticks as the
// peer reports parts missing, so it bounds full-part-set replays to roughly
// one per (missing_parts * PeerGossipSleepDuration + catchupResendInterval),
// not to a flat one per catchupResendInterval — see peer-gossip-sleep-duration
// in config/config.go for the cadence this scales with. It also does nothing
// at all if that cadence is configured slower than this interval.
const catchupResendInterval = 500 * time.Millisecond

type msgGossiper struct {
	logger     log.Logger
	ps         *PeerState
	msgSender  *p2pMsgSender
	blockStore *blockRepository
	optimistic bool
	clock      clockwork.Clock

	// Catch-up pass accounting for the one peer this gossiper serves, touched
	// only from GossipBlockPartsForCatchup (see its single-goroutine-caller
	// requirement on the Gossiper interface). catchupMu guards the fields
	// themselves against a hypothetical second caller, but not the
	// draw-then-send-then-settle sequence across them — that still requires the
	// single-caller invariant to hold.
	//
	// Governing invariant: catchupRemaining > 0 means a pass is currently open,
	// which means catchupRetryAt is already in the past; catchupRetryAt is only
	// ever in the future while catchupRemaining <= 0. Every write to either
	// field must preserve this together.
	catchupMu        sync.Mutex
	catchupHeight    int64
	catchupRemaining int
	catchupRetryAt   time.Time
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
	// This is regular (same-height) gossip: the peer is at our height and is
	// actively collecting parts, so we optimistically record the part as
	// delivered to avoid resending it on every tick.
	err := g.syncProposalBlockPart(ctx, part, rs.Height, rs.Round, true)
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

// GossipBlockPartsForCatchup sends a block part for catch up; see the doc on
// the Gossiper interface for its rate limit and goroutine-affinity contract.
func (g *msgGossiper) GossipBlockPartsForCatchup(
	ctx context.Context,
	_ cstypes.RoundState,
	prs *cstypes.PeerRoundState,
) {
	if !g.beginCatchupAttempt(prs) {
		return
	}
	if !g.sendCatchupBlockPart(ctx, prs) {
		// The peer controls both the budget a pass opens with and the inputs that
		// fail here, so a pass that cannot produce a send is abandoned rather than
		// charged one slot per tick.
		g.endCatchupPass()
	}
}

// sendCatchupBlockPart sends the peer one part of its incomplete block, drawn
// at random from the parts it reports missing, and reports whether the part
// was handed to the p2p channel. A false result means either the send could
// not be enqueued (in practice, only on reactor shutdown — see
// internal/p2p/channel.go) or the block-store inputs were unusable; it is not
// a delivery acknowledgement from the peer.
func (g *msgGossiper) sendCatchupBlockPart(ctx context.Context, prs *cstypes.PeerRoundState) bool {
	logger := g.logger.With([]any{
		"height", prs.Height,
		"round", prs.Round,
	})
	index, ok := prs.ProposalBlockParts.Not().PickRandom()
	if !ok {
		// Defensive: prs is a per-tick deep copy, so the draw cannot come up empty.
		return false
	}
	logger = logger.With("part_index", index)
	meta, err := g.blockStore.loadMeta(prs.Height)
	if err != nil {
		return false
	}
	err = g.ensurePeerPartSetHeader(meta.BlockID.PartSetHeader, prs.ProposalBlockPartSetHeader)
	if err != nil {
		logger.Error("block and peer part-set headers do not match", "error", err)
		return false
	}
	part, err := g.blockStore.loadPart(prs.Height, index)
	if err != nil {
		return false
	}
	// Catch-up gossip: do NOT optimistically record the part as delivered.
	//
	// A lagging peer can be at a step where it is not yet expecting block parts
	// (e.g. RoundStepNewHeight/Propose with a nil ProposalBlockParts) and will
	// silently drop the part. If we optimistically marked it delivered, our view
	// of the peer would show the full part set and we would never resend it,
	// leaving the peer wedged with a stored commit but an incomplete block (it
	// learns the part-set header only once the catch-up commit arrives). The
	// part is instead replayed every pass until the peer applies the block and
	// advances its height.
	if err := g.syncProposalBlockPart(ctx, part, prs.Height, meta.Round, false); err != nil {
		logger.Error("failed to send catch-up block part to the peer", "error", err)
		return false
	}
	return true
}

// beginCatchupAttempt draws a send from the current catch-up pass, reporting
// false once the pass is spent and until its retry interval elapses. A pass is
// worth at most one send per part the peer reported missing when it opened, so a
// peer that dropped those parts is served again every interval while it stays
// behind. Any attempt that fails ends the pass early, via endCatchupPass.
func (g *msgGossiper) beginCatchupAttempt(prs *cstypes.PeerRoundState) bool {
	if prs.ProposalBlockParts == nil {
		return false
	}
	g.catchupMu.Lock()
	defer g.catchupMu.Unlock()
	now := g.clock.Now()
	if prs.Height != g.catchupHeight {
		// A peer only ever reports a higher height, so this cannot be replayed
		// to reopen a pass — but it can interrupt one before it exhausts its own
		// budget, which (per the invariant above) means catchupRetryAt was never
		// armed for it. Arm it here too, or a peer advancing its height every
		// tick gets a fresh full-budget pass every tick forever.
		if g.catchupRemaining > 0 {
			g.catchupRetryAt = now.Add(catchupResendInterval)
		}
		g.catchupHeight = prs.Height
		g.catchupRemaining = 0
	}
	noOpenPass := g.catchupRemaining <= 0
	if noOpenPass && now.Before(g.catchupRetryAt) {
		// Still backed off: this tick cannot send regardless of what the peer
		// currently reports missing, so skip the bit-array scan below entirely.
		// Most ticks land here at the production defaults (one pass's worth of
		// backoff spans roughly catchupResendInterval / PeerGossipSleepDuration
		// ticks), and the peer-controlled bit-array can be up to
		// MaxBlockPartsCount bits.
		return false
	}
	// PickRandom draws from the parts the peer reports missing, so the pass is
	// worth that many sends rather than one per entry in the bit-array. Derived
	// as size minus set bits rather than via Not().CountTrueBits(), which would
	// allocate and copy the whole bit-array just to count it.
	missing := prs.ProposalBlockParts.Size() - prs.ProposalBlockParts.CountTrueBits()
	if missing == 0 {
		if !noOpenPass {
			// The peer now reports a complete part set while a pass was still
			// open (budget not yet exhausted). This does NOT let the peer send
			// more than the budget fixed at pass open — catchupRemaining only
			// ever counts down, never up, so total sends per pass are unaffected
			// either way. Left open regardless, though, noOpenPass stays false on
			// every later tick no matter how much time passes, which costs two
			// things: the backoff-skip check above never re-engages, so the
			// bit-array scan above runs on every tick for as long as the peer
			// holds the pass open this way (the CPU cost the backoff-skip check
			// exists to bound); and the next report of missing parts resumes
			// sending on that same tick rather than after a fresh
			// catchupResendInterval. Close the pass here to bound both. This
			// does mean a peer that legitimately completes its part set and
			// later falls behind again waits out one interval before catch-up
			// resumes - accepted deliberately: a peer that just reported a
			// complete set is by definition not starving, and the wait is
			// negligible against block time.
			g.catchupRemaining = 0
			g.catchupRetryAt = now.Add(catchupResendInterval)
		}
		return false
	}
	if noOpenPass {
		// The budget is fixed here, for the whole pass. The peer owns the
		// bit-array behind missing and may replace it between ticks, so
		// re-deriving it to raise the budget mid-pass would let it widen the
		// pass.
		g.catchupRemaining = missing
	} else {
		// The peer's reported missing count can only shrink honestly as parts
		// are actually delivered elsewhere (e.g. via non-catch-up gossip), so
		// clamp the remaining budget down to match; never let it raise the
		// budget mid-pass. Without this, a peer that swaps in a bit-array with
		// fewer unset bits mid-pass keeps the larger budget the pass opened
		// with, and every remaining attempt draws from the now-small missing
		// set - funding repeated duplicate sends of the few parts still unset.
		g.catchupRemaining = min(g.catchupRemaining, missing)
	}
	g.catchupRemaining--
	if g.catchupRemaining <= 0 {
		// Arm the deadline as the pass closes, not as it opens: a multi-part
		// pass spans more ticks than the interval, so a deadline set at open
		// would already have elapsed by the time the budget runs out.
		g.catchupRetryAt = now.Add(catchupResendInterval)
	}
	return true
}

// endCatchupPass abandons the rest of the current pass and starts its retry
// interval now. Without it a pass that cannot send costs a block-store read
// and an error log on every gossip tick until its budget runs out.
func (g *msgGossiper) endCatchupPass() {
	g.catchupMu.Lock()
	defer g.catchupMu.Unlock()
	g.catchupRemaining = 0
	g.catchupRetryAt = g.clock.Now().Add(catchupResendInterval)
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

// syncProposalBlockPart sends a single block part to the peer. When
// markPeerHasPart is true the peer's round state is optimistically updated to
// record that it now has the part; callers performing catch-up should pass
// false so the part keeps being resent until the peer actually applies it.
func (g *msgGossiper) syncProposalBlockPart(ctx context.Context, part *types.Part, height int64, round int32, markPeerHasPart bool) error {
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
	var syncFunc func() error
	if markPeerHasPart {
		syncFunc = updatePeerProposalBlockPart(g.ps, height, round, int(part.Index))
	}
	logger.Debug("syncing proposal block part")
	return g.sync(ctx, protoBlockPart, syncFunc)
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
