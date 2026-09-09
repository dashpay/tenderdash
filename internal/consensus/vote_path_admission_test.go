package consensus

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/dash"
	"github.com/dashpay/tenderdash/internal/p2p"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// A vote is admitted on what it carries, not on who handed it over. Who handed
// it over is not evidence: a relayer passes on a vote it did not sign, and a
// forged vote says nothing about the peer that relayed it. What decides whether
// a vote counts is the signature, and what decides whether it is worth reading
// is the verification budget.
//
// These tests hold the two halves of that. A vote relayed by a peer this node
// has no standing with is processed on its merits, and a vote whose stated
// identity no validator holds is refused before it can allocate anything or
// spend anything.

// A relayer is not a signer. A validator's vote arriving from some other peer
// entirely is still the vote that validator signed, and is counted.
func TestVoteRelayedByANonValidatorPeerIsAccepted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// A peer in neither the current nor the previous validator set.
	const relayer = types.NodeID("unprivileged-relayer")

	cs, vss := makeState(ctx, t, makeStateArgs{validators: 4})
	stateData := cs.GetStateData()

	// Full node-address coverage on the validator set: the case where a filter
	// on the sender's node ID would have had everything it needed to drop this.
	for i, val := range stateData.Validators.Validators {
		val.NodeAddress = types.ValidatorAddress{NodeID: validatorNodeID(i)}
	}
	require.NoError(t, stateData.Save())

	// A vote the signing validator really did sign, for the height and round
	// this node is in.
	signer := vss[1]
	signer.Height, signer.Round = stateData.Height, stateData.Round
	vote := signVote(ctx, t, signer, tmproto.PrevoteType, stateData.state.ChainID, types.BlockID{},
		stateData.Validators.QuorumType, stateData.Validators.QuorumHash)

	runCtx := dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)
	go cs.receiveRoutine(runCtx, nil)
	_, inCh, _ := newReceivingReactor(ctx, t, cs, relayer)

	inCh <- p2p.Envelope{
		From:      relayer,
		ChannelID: p2p.ConsensusVoteChannel,
		Message:   &tmcons.Vote{Vote: vote.ToProto()},
	}

	require.Eventually(t, func() bool {
		prevotes := cs.GetStateData().Votes.Prevotes(vote.Round)
		return prevotes != nil && prevotes.GetByIndex(vote.ValidatorIndex) != nil
	}, 30*time.Second, time.Millisecond,
		"a vote signed by a validator was discarded for arriving from a peer that is not one; "+
			"on this topology most relayers are not validators, so that loses most of the gossip")
}

// What a vote costs its sender is the same whoever the sender is. A prevote
// carrying a signature that cannot verify is refused, and refusing it draws the
// one work unit a prevote is priced at — not more, and not none.
//
// Both halves matter. More, and a peer could buy verification beyond what the
// budget thinks it sold; none, and the flood would be outside the budget
// altogether, which is what made the round allocation worth fixing first.
func TestForgedVoteFromAnUnprivilegedPeerCostsOneWorkUnit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	const attacker = types.NodeID("unprivileged-attacker")

	h := newFloodHarness(ctx, t, floodHarnessArgs{validators: 4})
	vote := unsignedPrevote(ctx, t, h)

	stop := h.start(ctx)
	defer stop()
	_, inCh, _ := newReceivingReactor(ctx, t, h.cs, attacker)

	inCh <- p2p.Envelope{
		From:      attacker,
		ChannelID: p2p.ConsensusVoteChannel,
		Message:   &tmcons.Vote{Vote: vote.ToProto()},
	}

	require.Eventually(t, func() bool { return len(h.budget.charges()) > 0 },
		30*time.Second, time.Millisecond,
		"the vote was never verified, so nothing here is measured")
	require.Equal(t, []int{1}, h.budget.charges(),
		"a forged prevote must cost exactly the one verification a prevote is priced at")

	prevotes := h.stateData().Votes.Prevotes(vote.Round)
	require.NotNil(t, prevotes)
	require.Nil(t, prevotes.GetByIndex(vote.ValidatorIndex),
		"a vote whose signature does not verify was counted")
}

// A vote nobody could have signed enters no round and is charged nothing,
// because it is refused before either could happen. Rounds are allocated on what
// a vote claims about itself, so the claim has to be worth something before the
// allocation is made.
func TestClaimedProTxHashAllocatesNoRound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	h := newFloodHarness(ctx, t, floodHarnessArgs{validators: 4})
	stateData := h.stateData()
	const forgedRound = int32(5000)

	require.Nil(t, stateData.Votes.GetVoteSet(forgedRound, tmproto.PrevoteType),
		"the fixture must not already track the round this test mints")

	added, err := stateData.Votes.AddVoteWithVerificationBudget(
		mintedProTxHashVote(stateData.Height, forgedRound), h.budget)

	require.False(t, added)
	require.ErrorIs(t, err, types.ErrVoteInvalidValidatorProTxHash,
		"a vote with a made-up proTxHash must be rejected for that reason")
	require.Nil(t, stateData.Votes.GetVoteSet(forgedRound, tmproto.PrevoteType),
		"a made-up proTxHash bought a round of state again")
	require.Empty(t, h.budget.charges(),
		"a claim refused this cheaply must not reach the verification it cannot pay for")
}

// The same thing over the wire, from a peer with no standing at all: the
// allocation is out of its reach because of what its vote says, not because of
// who it is.
func TestUnprivilegedPeerCannotAllocateARound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	const attacker = types.NodeID("unprivileged-attacker")
	const forgedRound = int32(5000)

	cs, _ := makeState(ctx, t, makeStateArgs{validators: 4})
	stateData := cs.GetStateData()
	height := stateData.Height

	runCtx := dash.ContextWithProTxHash(ctx, cs.privValidator.ProTxHash)
	go cs.receiveRoutine(runCtx, nil)
	_, inCh, _ := newReceivingReactor(ctx, t, cs, attacker)

	inCh <- p2p.Envelope{
		From:      attacker,
		ChannelID: p2p.ConsensusVoteChannel,
		Message:   &tmcons.Vote{Vote: mintedProTxHashVote(height, forgedRound).ToProto()},
	}

	// Control: a message this node does act on, sent after the one above and
	// therefore handled after it. Once it has landed, the vote either allocated
	// its round or never got the chance.
	inCh <- p2p.Envelope{
		From:      attacker,
		ChannelID: p2p.ConsensusVoteChannel,
		Message:   &tmcons.Commit{Commit: forgedCommit(&stateData).ToProto()},
	}
	require.Eventually(t, func() bool { return len(cs.peerErrorQueue.ch) > 0 },
		30*time.Second, time.Millisecond,
		"the control message never reached the state, so this test proves nothing")

	require.Nil(t, cs.GetStateData().Votes.GetVoteSet(forgedRound, tmproto.PrevoteType),
		"a peer allocated a round of state on a name no validator holds")
}

// mintedProTxHashVote is a prevote carrying thirty-two random bytes where the
// signer's identity should be, for a round this node has never entered. Its
// validator index is in range, which is all any check above HeightVoteSet
// looks at.
func mintedProTxHashVote(height int64, round int32) *types.Vote {
	proTxHash := make([]byte, 32)
	_, _ = rand.Read(proTxHash)
	return &types.Vote{
		Type:               tmproto.PrevoteType,
		Height:             height,
		Round:              round,
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     0,
		BlockSignature:     make([]byte, types.SignatureSize),
	}
}

func validatorNodeID(i int) types.NodeID {
	return types.NodeID(string(rune('a'+i)) + "0000000000000000000000000000000000000000")
}
