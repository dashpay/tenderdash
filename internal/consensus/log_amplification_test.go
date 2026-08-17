package consensus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/dash"
	"github.com/dashpay/tenderdash/libs/log"
	tmtime "github.com/dashpay/tenderdash/libs/time"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// isPeerFloodableError classifies by error TYPE, not by whether a peer sent the
// message: a floodable validation error is logged at Debug, but an internal
// fault surfaced while handling a peer message (e.g. ErrPrivValidatorNotSet)
// must still be Error so it is not hidden under a flood.
func TestIsPeerFloodableError(t *testing.T) {
	// The production invalid-block-signature error, as wrapped in vote_set.go.
	floodWrapped := types.ErrInvalidVoteSignature(fmt.Errorf("failed to verify vote: %w", types.ErrVoteInvalidBlockSignature))

	assert.True(t, isPeerFloodableError(types.ErrVoteInvalidBlockSignature))
	assert.True(t, isPeerFloodableError(types.ErrVoteUnexpectedStep))
	assert.True(t, isPeerFloodableError(floodWrapped), "must see through the ErrInvalidVoteSignature wrapping")

	assert.False(t, isPeerFloodableError(ErrPrivValidatorNotSet),
		"an internal fault must not be treated as floodable")
	assert.False(t, isPeerFloodableError(errors.New("some other internal error")))
	assert.False(t, isPeerFloodableError(nil))
}

// A flood of invalid votes must not emit Error-level logs (one per message was
// the observed amplification). A floodable validation error logs at Debug; an
// internal fault surfaced while handling a peer message stays at Error.
func TestLoggingMiddleware_FloodableIsDebug_InternalIsError(t *testing.T) {
	vote := &types.Vote{
		Type:               tmproto.PrecommitType,
		Height:             1,
		ValidatorProTxHash: make([]byte, 32),
	}
	env := msgEnvelope{msgInfo: msgInfo{Msg: &VoteMessage{Vote: vote}, PeerID: "peerX"}}

	run := func(t *testing.T, handlerErr error) string {
		t.Helper()
		var buf bytes.Buffer
		logger, err := log.NewLogger("debug", &buf)
		require.NoError(t, err)
		mw := loggingMiddleware(logger)(func(_ context.Context, _ *StateData, _ msgEnvelope) error {
			return handlerErr
		})
		require.NoError(t, mw(context.Background(), &StateData{}, env))
		return buf.String()
	}

	t.Run("floodable error -> Debug, no Error", func(t *testing.T) {
		out := run(t, types.ErrVoteInvalidBlockSignature)
		assert.NotContains(t, out, `"level":"error"`, "a floodable validation error must not log at Error")
		assert.Contains(t, out, `"level":"debug"`)
	})

	t.Run("internal fault on a peer message -> Error", func(t *testing.T) {
		// The bug Codex caught: an internal fault surfacing while handling a
		// peer vote must not be silenced.
		out := run(t, ErrPrivValidatorNotSet)
		assert.Contains(t, out, `"level":"error"`, "an internal fault must stay at Error even for a peer message")
	})
}

// addVoteLoggingMw must not log a not-added vote at Error; a peer can flood
// those. Notable cases (non-deterministic / conflicting) are still logged at
// Error by addVoteErrorMw.
func TestAddVoteLoggingMw_NotAddedIsDebugNotError(t *testing.T) {
	var buf bytes.Buffer
	logger, err := log.NewLogger("debug", &buf)
	require.NoError(t, err)

	next := func(_ context.Context, _ *StateData, _ *types.Vote) (bool, error) {
		return false, errors.New("invalid signature")
	}
	mw := addVoteLoggingMw()(next)
	ctx := log.CtxWithLogger(context.Background(), logger)

	added, err := mw(ctx, &StateData{}, &types.Vote{Type: tmproto.PrecommitType})
	assert.False(t, added)
	require.Error(t, err)

	assert.NotContains(t, buf.String(), `"level":"error"`, "a not-added vote must not log at Error")
}

// A peer can force a proposal to be rejected at will — a forged signature, a
// stale core-chain height, an out-of-range POL round — and nothing about that
// says the sender is at fault. Logging one Error line per rejection turns a
// proposal flood into a log flood, so these are Debug.
func TestProposalRejectionsAreFloodable(t *testing.T) {
	for _, err := range []error{
		ErrInvalidProposalSignature,
		ErrInvalidProposalPOLRound,
		ErrInvalidProposalCoreHeight,
		ErrInvalidProposalForCommit,
		ErrUnableToVerifyProposal,
	} {
		assert.True(t, isPeerFloodableError(err), "%v is peer-triggerable at will", err)
	}
}

// A vote names the validator it claims to come from twice: by index and by
// pro-tx hash. A peer can put any pro-tx hash it likes on a vote it copied off
// the wire, and the disagreement is caught before the signatures are read, so
// producing one costs the sender nothing and it must not reach the log at
// Error.
//
// A missing or malformed public key is deliberately absent: that is this node's
// own validator set being wrong, which no peer can cause and which must stay
// visible.
func TestVoteValidatorIdentityRejectionsAreFloodable(t *testing.T) {
	assert.True(t, isPeerFloodableError(types.ErrVoteInvalidValidatorProTxHash),
		"a mismatched validator pro-tx hash is peer-triggerable at will")
	assert.True(t,
		isPeerFloodableError(fmt.Errorf("vote.ValidatorProTxHash does not match: %w",
			types.ErrVoteInvalidValidatorProTxHash)),
		"must see through wrapping")

	assert.False(t, isPeerFloodableError(types.ErrVoteMissingValidatorPubKey),
		"a validator set without a public key is this node's fault, not a sender's")
	assert.False(t, isPeerFloodableError(types.ErrVoteInvalidValidatorPubKeySize),
		"a validator set with a malformed public key is this node's fault, not a sender's")
}

// The same for a block part: an invalid proof, an out-of-range index or an
// index that disagrees with its proof are all free for a peer to produce.
func TestBlockPartRejectionsAreFloodable(t *testing.T) {
	for _, err := range []error{
		types.ErrPartSetInvalidProof,
		types.ErrPartSetUnexpectedIndex,
		types.ErrPartSetIndexMismatch,
	} {
		assert.True(t, isPeerFloodableError(err), "%v is peer-triggerable at will", err)
	}
	assert.True(t,
		isPeerFloodableError(fmt.Errorf("adding part: %w", types.ErrPartSetInvalidProof)),
		"must see through wrapping")
}

// The proposal that fails verification is attacker-controlled and unbounded in
// size, so echoing it into the log is an amplification vector on its own —
// independently of the level the line is written at.
func TestRejectedProposalIsNotEchoedIntoTheLog(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var buf bytes.Buffer
	logger, err := log.NewLogger("debug", &buf)
	require.NoError(t, err)

	cs, _ := makeState(ctx, t, makeStateArgs{validators: 4, logger: logger})
	stateData := cs.GetStateData()
	ctx = dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)

	proposal := forgedProposal(&stateData)
	buf.Reset()
	require.NoError(t, cs.msgDispatcher.dispatch(ctx, &stateData, msgInfo{
		Msg:         &ProposalMessage{Proposal: proposal},
		PeerID:      "peer",
		ReceiveTime: tmtime.Now(),
	}))

	out := buf.String()
	assert.NotContains(t, out, `"level":"error"`,
		"a rejected proposal must not write an Error line")
	assert.NotContains(t, out, fmt.Sprintf("%X", proposal.Signature),
		"the rejected proposal's payload must not be echoed into the log")
}

// A HasVote message names a validator index, and a peer may name any index it
// likes: the one that is out of range is rejected against the validator set
// this node has for that height, which costs the sender nothing to provoke.
// Announcing votes is also the cheapest thing on the state channel, so an Error
// line per rejection is a log flood for the asking.
func TestHasVoteIndexRejectionIsFloodable(t *testing.T) {
	assert.True(t, isPeerFloodableError(ErrPeerStateInvalidVoteIndex),
		"a vote index outside our validator set is peer-triggerable at will")
}
