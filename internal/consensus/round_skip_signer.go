package consensus

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

// roundSkipSigner signs this validator's round skip votes away from the
// consensus goroutine, one at a time.
//
// The signer behind a validator can be Dash Core, over an RPC client whose
// calls cannot be interrupted: one outlives the deadline its caller sets for
// as long as Core takes to answer. Signed on the consensus goroutine, a skip
// vote would hold up every proposal, vote and certificate the node receives
// meanwhile, though the round can do without this validator's vote: a
// certificate from the others' votes moves this node on too.
//
// One vote at a time, so a signer that never answers holds one goroutine, not
// one for every silence timeout.
type roundSkipSigner struct {
	busy     atomic.Bool
	signedCh chan signedRoundSkipVote
}

// signedRoundSkipVote is what came of signing a skip vote: the vote with its
// signature, or the error that kept it from being signed, and the silence the
// vote answers.
type signedRoundSkipVote struct {
	vote    *tmproto.RoundSkipVote
	silence proposerSilence
	err     error
}

func newRoundSkipSigner() *roundSkipSigner {
	return &roundSkipSigner{signedCh: make(chan signedRoundSkipVote, 1)}
}

// sign starts signing vote, unless an earlier vote is still being signed, and
// reports whether it started. The outcome is delivered on signed, and the
// signer takes no other vote until the consensus goroutine reads it there.
func (s *roundSkipSigner) sign(
	ctx context.Context,
	pv privValidator,
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	vote *tmproto.RoundSkipVote,
	timeout time.Duration,
	silence proposerSilence,
) bool {
	if !s.busy.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		signCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		err := pv.SignRoundSkipVote(signCtx, chainID, quorumType, quorumHash, vote)
		select {
		case s.signedCh <- signedRoundSkipVote{vote: vote, silence: silence, err: err}:
		case <-ctx.Done():
			s.busy.Store(false)
		}
	}()
	return true
}

// signed delivers the outcome of each vote sign started. The reader calls done
// once it has one.
func (s *roundSkipSigner) signed() <-chan signedRoundSkipVote {
	return s.signedCh
}

// done lets the signer take the next vote.
func (s *roundSkipSigner) done() {
	s.busy.Store(false)
}
