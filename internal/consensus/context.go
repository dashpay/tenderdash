package consensus

import (
	"context"

	"github.com/dashpay/tenderdash/types"
)

type contextKey int

const (
	usePeerQueueCtx contextKey = iota
	msgInfoCtx
	verificationBudgetCtx
	voteVerificationCtx
	peerLaneSessionCtx
)

// ctxWithPeerQueue adds a key into the context with true value
// this function is used for test
func ctxWithPeerQueue(ctx context.Context) context.Context {
	return context.WithValue(ctx, usePeerQueueCtx, true)
}

// peerQueueFromCtx returns true if a key has been set, otherwise returns false
// this is used by chanMsgSender to send the message via peer-queue even if peerID hasn't been provided
func peerQueueFromCtx(ctx context.Context) bool {
	val := ctx.Value(usePeerQueueCtx)
	if val != nil {
		return val.(bool)
	}
	return false
}

// ctxWithPeerLaneSession carries the sender's current connection session, so
// the scheduler can refuse a message left in flight by a session that has since
// ended rather than let it create or revive a lane for a departed peer.
//
// A live session is always nonzero (admit assigns from one upward), so a zero
// session stands for a sender that was never admitted — a path that predates
// sessions — and binds nothing, leaving that message to the scheduler's default
// admission.
func ctxWithPeerLaneSession(ctx context.Context, session uint64) context.Context {
	if session == 0 {
		return ctx
	}
	return context.WithValue(ctx, peerLaneSessionCtx, session)
}

// peerLaneSessionFromCtx returns the connection session the context carries and
// whether it carries one at all. A message with no session — this node's own
// work, or a path that predates sessions — is left to the scheduler's default
// admission.
func peerLaneSessionFromCtx(ctx context.Context) (uint64, bool) {
	session, ok := ctx.Value(peerLaneSessionCtx).(uint64)
	return session, ok
}

// msgInfoWithCtx puts msgInfo into the context
func msgInfoWithCtx(ctx context.Context, mi msgInfo) context.Context {
	return context.WithValue(ctx, msgInfoCtx, mi)
}

// msgInfoWithCtx gets msgInfo from the context
func msgInfoFromCtx(ctx context.Context) msgInfo {
	val := ctx.Value(msgInfoCtx)
	return val.(msgInfo)
}

func ctxWithPeerVerificationBudget(
	ctx context.Context,
	peerID types.NodeID,
	fromReplay bool,
	budget types.VerificationBudget,
) context.Context {
	if peerID == "" || fromReplay || budget == nil {
		return ctx
	}
	return context.WithValue(ctx, verificationBudgetCtx, budget)
}

func verificationBudgetFromCtx(ctx context.Context) types.VerificationBudget {
	budget, _ := ctx.Value(verificationBudgetCtx).(types.VerificationBudget)
	return budget
}

// verifiedVote pairs the result of verifying a vote's signatures with the vote
// it was produced for, so that a context handed on to further work cannot
// present it as covering some other vote.
type verifiedVote struct {
	vote     *types.Vote
	verified types.VoteVerification
}

// ctxWithVoteVerification carries the result of verifying a vote's signatures
// to the step that stores the vote, so that one vote is verified once on its
// way through the add-vote middleware rather than once per step that needs to
// know the signatures are good.
//
// It carries evidence rather than a verdict: types.VoteVerification names the
// vote, chain, quorum and validator key it was produced for, and the vote set
// refuses it unless all of them are the ones it is about to store under. A
// value put here for the wrong vote therefore cannot admit that vote.
func ctxWithVoteVerification(
	ctx context.Context,
	vote *types.Vote,
	verified types.VoteVerification,
) context.Context {
	return context.WithValue(ctx, voteVerificationCtx, verifiedVote{vote: vote, verified: verified})
}

// voteVerificationFromCtx returns the verification carried for vote, and
// whether the context carries one for that vote at all. A verification
// belonging to a different vote is reported as absent, which leaves the vote to
// be verified where it always was rather than refused.
func voteVerificationFromCtx(ctx context.Context, vote *types.Vote) (types.VoteVerification, bool) {
	carried, ok := ctx.Value(voteVerificationCtx).(verifiedVote)
	if !ok || carried.vote != vote {
		return types.VoteVerification{}, false
	}
	return carried.verified, true
}
