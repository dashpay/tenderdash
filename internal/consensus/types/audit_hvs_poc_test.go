package types

import (
	"context"
	"crypto/rand"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/config"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// HeightVoteSet.AddVote enters a round it has not seen by allocating a
// RoundVoteSet — two vote sets sized to the validator set — and the round is
// whichever one the vote asks for. The allowance for that is keyed on the vote's
// ValidatorProTxHash, which is a claim: nothing upstream binds it to the sending
// peer, and the check that would test it against the validator set used to live
// inside VoteSet.addVote, which runs after the allocation.
//
// So thirty-two random bytes bought two fresh rounds. The vote was then refused
// without any BLS verification — the pro-tx-hash mismatch short-circuits before
// vote.Verify — so the message cost the victim memory and cost the verification
// budget nothing, which is what put it out of reach of every throttle.
//
// The claim is now tested before the round is entered. These tests hold that
// line from both sides: a name no validator holds buys nothing, and a name a
// validator does hold still buys the catch-up rounds it is entitled to.
func TestPoC_HeightVoteSet_UnboundedRoundAllocation(t *testing.T) {
	const (
		numValidators = 100 // Dash Evo mainnet scale
		numMessages   = 2000
	)

	valSet, _ := types.RandValidatorSet(numValidators)
	hvs := NewHeightVoteSet("test-chain", 100, valSet)

	require.Equal(t, 1, len(hvs.roundVoteSets), "fresh HeightVoteSet tracks only round 0")

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	// Each message: a fresh made-up proTxHash and a fresh round. 32 random
	// bytes is all the attacker needs; ValidateBasic only checks the length.
	for i := 0; i < numMessages; i++ {
		proTxHash := make([]byte, 32)
		_, _ = rand.Read(proTxHash)

		vote := &types.Vote{
			Type:               tmproto.PrevoteType,
			Height:             100,
			Round:              int32(1000 + i), // any round we have not seen
			BlockID:            types.BlockID{}, // nil block: prevotes need nothing more
			ValidatorProTxHash: proTxHash,
			ValidatorIndex:     0, // in range, so addVoteValidateVoteMw lets it through
			BlockSignature:     make([]byte, types.SignatureSize),
		}

		added, err := hvs.AddVote(vote)
		require.False(t, added)
		require.ErrorIs(t, err, types.ErrVoteInvalidValidatorProTxHash,
			"a vote must be refused for naming a validator the set does not hold there")
	}

	runtime.GC()
	runtime.ReadMemStats(&after)

	// The rejected votes left nothing behind.
	require.Equal(t, 1, len(hvs.roundVoteSets),
		"a rejected vote entered a round; the claim is being tested after the allocation again")
	require.Empty(t, hvs.peerCatchupRounds,
		"a rejected vote took a catch-up allowance under a name no validator holds")

	growth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	perMsg := growth / numMessages
	t.Logf("retained heap: %d bytes over %d messages of ~149 wire bytes each => %d bytes retained per message",
		growth, numMessages, perMsg)

	require.Less(t, perMsg, int64(149),
		"each message still retains more memory than it costs the attacker to send")
}

// The other side of the same line. A pro-tx hash is public, so naming a real
// validator costs an attacker nothing either — what it buys is bounded and, now
// that the vote survives to the signature check, charged.
//
// Two rounds per validator is the catch-up allowance a peer legitimately ahead
// of us needs, so the ceiling is that allowance spent by everyone at once: a
// height admits at most 2·|valSet| rounds it did not enter itself, and each one
// costs its sender a verification. Before, the ceiling was the round number
// space and the cost was nothing.
func TestPoC_HeightVoteSet_RoundAllocationCeilingUnderRealNames(t *testing.T) {
	const numValidators = 8

	valSet, _ := types.RandValidatorSet(numValidators)
	hvs := NewHeightVoteSet("test-chain", 100, valSet)
	budget := &recordingBudget{allow: false}

	round := int32(1000)
	for attempt := 0; attempt < 4; attempt++ {
		for i := 0; i < numValidators; i++ {
			vote := &types.Vote{
				Type:               tmproto.PrevoteType,
				Height:             100,
				Round:              round,
				ValidatorProTxHash: valSet.Validators[i].ProTxHash,
				ValidatorIndex:     int32(i),
				BlockSignature:     make([]byte, types.SignatureSize),
			}
			round++

			added, err := hvs.AddVoteWithVerificationBudget(vote, budget)
			require.False(t, added)
			if attempt < 2 {
				// Within the allowance: the round is entered, and the vote goes
				// on to the signature check that the budget is asked to fund.
				require.ErrorIs(t, err, types.ErrVerificationBudgetExhausted)
			} else {
				require.ErrorIs(t, err, ErrGotVoteFromUnwantedRound)
			}
		}
	}

	require.Equal(t, 1+2*numValidators, len(hvs.roundVoteSets),
		"a height admits at most two attacker-chosen rounds per validator, plus the round it is in")
	require.Len(t, budget.costs, 2*numValidators,
		"every round entered cost its sender a verification; a round entered for free is unthrottleable")
}

// Honest catch-up is what the allowance exists for, and it still works: a peer
// ahead of us sends a vote for a round we have not entered, signed by a
// validator we hold, and we enter that round to hold it.
func TestPoC_HeightVoteSet_LegitimateCatchupStillAllocates(t *testing.T) {
	cfg, err := config.ResetTestRoot(t.TempDir(), "consensus_hvs_catchup_allocation")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const catchupRound = int32(999)
	valSet, privVals := types.RandValidatorSet(10)
	chainID := cfg.ChainID()
	hvs := NewHeightVoteSet(chainID, 1, valSet)

	require.Nil(t, hvs.GetVoteSet(catchupRound, tmproto.PrecommitType),
		"the fixture must not already track the round this test catches up to")

	vote := makeVoteHR(ctx, t, 1, 0, catchupRound, privVals, chainID,
		valSet.QuorumType, valSet.QuorumHash, tmproto.StateID{Height: 1})

	added, err := hvs.AddVote(vote)
	require.NoError(t, err)
	require.True(t, added)
	require.NotNil(t, hvs.GetVoteSet(catchupRound, tmproto.PrecommitType),
		"a peer legitimately ahead of us can no longer make us track its round")
}
