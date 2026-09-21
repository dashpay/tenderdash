package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

func TestHeightVoteSetRejectedCatchupVotesPreserveAllowance(t *testing.T) {
	for _, voteType := range []tmproto.SignedMsgType{tmproto.PrevoteType, tmproto.PrecommitType} {
		for _, mode := range []string{"invalid_signature", "budgeted_invalid_signature", "denied_budget", "invalid_proof"} {
			t.Run(voteType.String()+"/"+mode, func(t *testing.T) {
				valSet, privVals := types.RandValidatorSet(4)
				hvs := NewHeightVoteSet("catchup-test", 1, valSet)
				budget := &recordingBudget{allow: mode != "denied_budget"}
				for round := int32(10); round < 12; round++ {
					vote := signedCatchupVote(t, valSet, privVals, round, voteType)
					var added bool
					var err error
					switch mode {
					case "invalid_signature", "budgeted_invalid_signature":
						vote.BlockSignature = make([]byte, types.SignatureSize)
						if mode == "invalid_signature" {
							added, err = hvs.AddVote(vote)
						} else {
							added, err = hvs.AddVoteWithVerificationBudget(vote, budget)
						}
						require.ErrorIs(t, err, types.ErrVoteInvalidBlockSignature)
					case "denied_budget":
						added, err = hvs.AddVoteWithVerificationBudget(vote, budget)
						require.ErrorIs(t, err, types.ErrVerificationBudgetExhausted)
					case "invalid_proof":
						added, err = hvs.AddVerifiedVote(vote, types.VoteVerification{})
						require.ErrorIs(t, err, types.ErrVoteVerificationMismatch)
					}
					require.False(t, added)
				}

				vote := signedCatchupVote(t, valSet, privVals, 12, voteType)
				added, err := hvs.AddVote(vote)
				require.NoError(t, err, "rejected votes must not consume the validator's catch-up allowance")
				require.True(t, added)
				require.Len(t, hvs.roundVoteSets, 2)
				require.Equal(t, []int32{12}, hvs.peerCatchupRounds[vote.ValidatorProTxHash.String()])
				require.Nil(t, hvs.GetVoteSet(10, voteType))
				require.Nil(t, hvs.GetVoteSet(11, voteType))
				if mode == "budgeted_invalid_signature" || mode == "denied_budget" {
					require.Equal(t, []int{1, 1}, budget.costs)
				}
			})
		}
	}
}

func TestHeightVoteSetVerifiedCatchupVote(t *testing.T) {
	valSet, privVals := types.RandValidatorSet(4)
	hvs := NewHeightVoteSet("catchup-test", 1, valSet)
	vote := signedCatchupVote(t, valSet, privVals, 10, tmproto.PrevoteType)
	val := valSet.GetByIndex(vote.ValidatorIndex)
	budget := &recordingBudget{allow: true}
	verified, err := types.VerifyVoteSignatures(vote, "catchup-test", valSet.QuorumType,
		valSet.QuorumHash, val.PubKey, val.ProTxHash, budget)
	require.NoError(t, err)

	// A proof for another round cannot reserve a round or spend allowance.
	vote.Round = 11
	added, err := hvs.AddVerifiedVote(vote, verified)
	require.False(t, added)
	require.ErrorIs(t, err, types.ErrVoteVerificationMismatch)
	require.Nil(t, hvs.GetVoteSet(11, vote.Type))
	require.Empty(t, hvs.peerCatchupRounds)

	vote.Round = 10
	added, err = hvs.AddVerifiedVote(vote, verified)
	require.NoError(t, err)
	require.True(t, added)
	require.Same(t, vote, hvs.GetVoteSet(10, vote.Type).GetByIndex(vote.ValidatorIndex))
	require.Equal(t, []int32{10}, hvs.peerCatchupRounds[val.ProTxHash.String()])
	require.Equal(t, []int{1}, budget.costs)
}

func TestHeightVoteSetCatchupExistingRoundPreserved(t *testing.T) {
	valSet, privVals := types.RandValidatorSet(4)
	hvs := NewHeightVoteSet("catchup-test", 1, valSet)
	vote := signedCatchupVote(t, valSet, privVals, 10, tmproto.PrevoteType)
	budget := &recordingBudget{allow: true}
	added, err := hvs.AddVoteWithVerificationBudget(vote, budget)
	require.NoError(t, err)
	require.True(t, added)
	require.Equal(t, []int{1}, budget.costs)
	prevotes := hvs.GetVoteSet(10, tmproto.PrevoteType)
	precommits := hvs.GetVoteSet(10, tmproto.PrecommitType)

	added, err = hvs.AddVote(vote)
	require.NoError(t, err)
	require.False(t, added)
	precommit := signedCatchupVote(t, valSet, privVals, 10, tmproto.PrecommitType)
	added, err = hvs.AddVoteWithVerificationBudget(precommit, &recordingBudget{})
	require.False(t, added)
	require.ErrorIs(t, err, types.ErrVerificationBudgetExhausted)
	require.Same(t, prevotes, hvs.GetVoteSet(10, tmproto.PrevoteType))
	require.Same(t, precommits, hvs.GetVoteSet(10, tmproto.PrecommitType))
	require.Same(t, vote, prevotes.GetByIndex(vote.ValidatorIndex))
	require.Equal(t, []int32{10}, hvs.peerCatchupRounds[vote.ValidatorProTxHash.String()])

	budget.costs = nil
	added, err = hvs.AddVoteWithVerificationBudget(precommit, budget)
	require.NoError(t, err)
	require.True(t, added)
	require.Equal(t, []int{1}, budget.costs)
	require.Equal(t, []int32{10}, hvs.peerCatchupRounds[vote.ValidatorProTxHash.String()])
}

func TestHeightVoteSetConcurrentCatchupQuota(t *testing.T) {
	valSet, privVals := types.RandValidatorSet(4)
	hvs := NewHeightVoteSet("catchup-test", 1, valSet)
	type result struct {
		added bool
		err   error
	}
	results := make(chan result, 3)
	start := make(chan struct{})
	votes := make([]*types.Vote, 0, 3)
	for round := int32(10); round < 13; round++ {
		votes = append(votes, signedCatchupVote(t, valSet, privVals, round, tmproto.PrevoteType))
	}
	for _, vote := range votes {
		go func() {
			<-start
			added, err := hvs.AddVote(vote)
			results <- result{added: added, err: err}
		}()
	}
	close(start)
	accepted := 0
	for range 3 {
		got := <-results
		if got.added {
			require.NoError(t, got.err)
			accepted++
		} else {
			require.ErrorIs(t, got.err, ErrGotVoteFromUnwantedRound)
		}
	}
	require.Equal(t, 2, accepted)
	require.Len(t, hvs.roundVoteSets, 3)
	require.Len(t, hvs.peerCatchupRounds[valSet.GetByIndex(0).ProTxHash.String()], 2)
}

func signedCatchupVote(
	t *testing.T,
	valSet *types.ValidatorSet,
	privVals []types.PrivValidator,
	round int32,
	voteType tmproto.SignedMsgType,
) *types.Vote {
	t.Helper()
	vote := &types.Vote{
		Type:               voteType,
		Height:             1,
		Round:              round,
		ValidatorIndex:     0,
		ValidatorProTxHash: valSet.GetByIndex(0).ProTxHash,
	}
	pb := vote.ToProto()
	require.NoError(t, privVals[0].SignVote(context.Background(), "catchup-test",
		valSet.QuorumType, valSet.QuorumHash, pb, nil))
	vote.BlockSignature = pb.BlockSignature
	return vote
}
