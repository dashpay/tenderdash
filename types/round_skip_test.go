package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

const roundSkipTestChainID = "round-skip-test-chain"

// roundSkipVotes returns the skip votes of the first n validators of valSet,
// which privVals[i] signs for valSet.Validators[i], as RandValidatorSet returns
// them.
func roundSkipVotes(t *testing.T, valSet *ValidatorSet, privVals []PrivValidator, n int, round int32) []*RoundSkipVote {
	t.Helper()
	votes := make([]*RoundSkipVote, 0, n)
	for i, val := range valSet.Validators[:n] {
		pb := &tmproto.RoundSkipVote{
			Height:             20,
			Round:              round,
			ValidatorProTxHash: val.ProTxHash,
			ValidatorIndex:     int32(i),
		}
		require.NoError(t, privVals[i].SignRoundSkipVote(
			context.Background(), roundSkipTestChainID, valSet.QuorumType, valSet.QuorumHash, pb))
		vote, err := RoundSkipVoteFromProto(pb)
		require.NoError(t, err)
		votes = append(votes, vote)
	}
	return votes
}

func TestRoundSkipVoteSignature(t *testing.T) {
	valSet, privVals := RandValidatorSet(4)
	vote := roundSkipVotes(t, valSet, privVals, 1, 2)[0]

	t.Run("should verify against the voter's key share", func(t *testing.T) {
		assert.NoError(t, vote.Verify(roundSkipTestChainID, valSet.QuorumType, valSet.QuorumHash, valSet.Validators[0].PubKey))
	})

	t.Run("should refuse another validator's key share", func(t *testing.T) {
		err := vote.Verify(roundSkipTestChainID, valSet.QuorumType, valSet.QuorumHash, valSet.Validators[1].PubKey)
		assert.ErrorIs(t, err, ErrRoundSkipVoteInvalidSignature)
	})

	t.Run("should refuse a vote moved to another round", func(t *testing.T) {
		moved := *vote
		moved.Round++
		err := moved.Verify(roundSkipTestChainID, valSet.QuorumType, valSet.QuorumHash, valSet.Validators[0].PubKey)
		assert.ErrorIs(t, err, ErrRoundSkipVoteInvalidSignature)
	})

	t.Run("should survive a proto round trip", func(t *testing.T) {
		decoded, err := RoundSkipVoteFromProto(vote.ToProto())
		require.NoError(t, err)
		assert.Equal(t, vote, decoded)
	})
}

func TestRecoverRoundSkipCertificate(t *testing.T) {
	valSet, privVals := RandValidatorSet(4)
	threshold := valSet.QuorumTypeThresholdCount()
	require.Less(t, threshold, valSet.Size())

	t.Run("should recover a certificate the quorum key verifies from the threshold of votes", func(t *testing.T) {
		certificate, err := RecoverRoundSkipCertificate(roundSkipTestChainID, valSet,
			roundSkipVotes(t, valSet, privVals, threshold, 2))
		require.NoError(t, err)
		assert.Equal(t, int64(20), certificate.Height)
		assert.Equal(t, int32(2), certificate.Round)
		assert.NoError(t, certificate.ValidateBasic())
		assert.NoError(t, certificate.Verify(roundSkipTestChainID, valSet))

		decoded, err := RoundSkipCertificateFromProto(certificate.ToProto())
		require.NoError(t, err)
		assert.Equal(t, certificate, decoded)
	})

	t.Run("should recover the same certificate from any votes past the threshold", func(t *testing.T) {
		some, err := RecoverRoundSkipCertificate(roundSkipTestChainID, valSet,
			roundSkipVotes(t, valSet, privVals, threshold, 2))
		require.NoError(t, err)
		all, err := RecoverRoundSkipCertificate(roundSkipTestChainID, valSet,
			roundSkipVotes(t, valSet, privVals, valSet.Size(), 2))
		require.NoError(t, err)
		assert.Equal(t, some.ThresholdSignature, all.ThresholdSignature)
	})

	t.Run("should refuse to recover from fewer votes than the threshold", func(t *testing.T) {
		_, err := RecoverRoundSkipCertificate(roundSkipTestChainID, valSet,
			roundSkipVotes(t, valSet, privVals, threshold-1, 2))
		assert.Error(t, err)
	})

	t.Run("should not count a validator twice", func(t *testing.T) {
		votes := roundSkipVotes(t, valSet, privVals, threshold-1, 2)
		votes = append(votes, votes[0])
		_, err := RecoverRoundSkipCertificate(roundSkipTestChainID, valSet, votes)
		assert.Error(t, err)
	})

	t.Run("should refuse votes for different rounds", func(t *testing.T) {
		votes := roundSkipVotes(t, valSet, privVals, threshold, 2)
		votes[0] = roundSkipVotes(t, valSet, privVals, 1, 3)[0]
		_, err := RecoverRoundSkipCertificate(roundSkipTestChainID, valSet, votes)
		assert.Error(t, err)
	})

	t.Run("should refuse a vote from outside the validator set", func(t *testing.T) {
		votes := roundSkipVotes(t, valSet, privVals, threshold, 2)
		stranger := *votes[0]
		stranger.ValidatorProTxHash = crypto.RandProTxHash()
		votes[0] = &stranger
		_, err := RecoverRoundSkipCertificate(roundSkipTestChainID, valSet, votes)
		assert.Error(t, err)
	})
}

func TestRoundSkipCertificateVerify(t *testing.T) {
	valSet, privVals := RandValidatorSet(4)
	certificate, err := RecoverRoundSkipCertificate(roundSkipTestChainID, valSet,
		roundSkipVotes(t, valSet, privVals, valSet.QuorumTypeThresholdCount(), 2))
	require.NoError(t, err)

	t.Run("should refuse a certificate moved to another round", func(t *testing.T) {
		moved := *certificate
		moved.Round++
		assert.ErrorIs(t, moved.Verify(roundSkipTestChainID, valSet), ErrRoundSkipCertificateInvalidSignature)
	})

	t.Run("should refuse a certificate for another chain", func(t *testing.T) {
		assert.ErrorIs(t, certificate.Verify("other-chain", valSet), ErrRoundSkipCertificateInvalidSignature)
	})

	t.Run("should refuse a certificate signed by another quorum", func(t *testing.T) {
		otherSet, _ := RandValidatorSet(4)
		assert.ErrorIs(t, certificate.Verify(roundSkipTestChainID, otherSet), ErrRoundSkipCertificateWrongQuorum)
	})
}

func TestRoundSkipsProvable(t *testing.T) {
	valSet, _ := RandValidatorSet(4)
	assert.True(t, RoundSkipsProvable(valSet), "by default the quorum needs what its signature proves")

	lowered := valSet.Copy()
	lowered.VotingPowerThreshold = uint64(DefaultDashVotingPower)
	assert.True(t, RoundSkipsProvable(lowered), "a lowered threshold is met by the shares the signature needs")

	raised := valSet.Copy()
	raised.VotingPowerThreshold = uint64(valSet.TotalVotingPower())
	assert.False(t, RoundSkipsProvable(raised), "a certificate cannot prove a raised threshold")

	assert.False(t, RoundSkipsProvable(nil))
}

func TestRoundSkipThresholdPower(t *testing.T) {
	valSet, privVals := RandValidatorSet(4)
	shares := valSet.QuorumTypeThresholdCount()
	require.Greater(t, shares, 1)

	t.Run("should need the quorum's voting threshold by default", func(t *testing.T) {
		assert.Equal(t, valSet.QuorumVotingThresholdPower(), RoundSkipThresholdPower(valSet))
	})

	t.Run("should never need less than the shares the signature is recovered from", func(t *testing.T) {
		lowered := valSet.Copy()
		lowered.VotingPowerThreshold = uint64(shares-1) * uint64(DefaultDashVotingPower)
		assert.Equal(t, lowered.QuorumTypeThresholdVotingPower(), RoundSkipThresholdPower(lowered))

		_, err := RecoverRoundSkipCertificate(roundSkipTestChainID, lowered,
			roundSkipVotes(t, lowered, privVals, shares-1, 2))
		assert.Error(t, err, "recovery from fewer shares than the signature needs")

		certificate, err := RecoverRoundSkipCertificate(roundSkipTestChainID, lowered,
			roundSkipVotes(t, lowered, privVals, shares, 2))
		require.NoError(t, err)
		assert.NoError(t, certificate.Verify(roundSkipTestChainID, lowered))
	})

	t.Run("should need a raised threshold in full", func(t *testing.T) {
		raised := valSet.Copy()
		raised.VotingPowerThreshold = uint64(valSet.TotalVotingPower())
		assert.Equal(t, valSet.TotalVotingPower(), RoundSkipThresholdPower(raised))
	})
}

func TestRoundSkipRequestID(t *testing.T) {
	t.Run("should not share a request ID with a vote, a proposal or a heartbeat", func(t *testing.T) {
		id := RoundSkipRequestID(5, 1)
		assert.NotEqual(t, BlockRequestID(5, 1), id)
		assert.NotEqual(t, ProposalRequestID(&Proposal{Height: 5, Round: 1}), id)
		assert.NotEqual(t, ProposerHeartbeatRequestID(5, 1, 0), id)
	})
}

func TestRoundSkipValidateBasic(t *testing.T) {
	validVote := func() *RoundSkipVote {
		return &RoundSkipVote{
			Height:             1,
			ValidatorProTxHash: crypto.RandProTxHash(),
			Signature:          make([]byte, SignatureSize),
		}
	}
	require.NoError(t, validVote().ValidateBasic())
	voteCases := map[string]func(v *RoundSkipVote){
		"should refuse a vote at height 0":          func(v *RoundSkipVote) { v.Height = 0 },
		"should refuse a vote for a negative round": func(v *RoundSkipVote) { v.Round = -1 },
		"should refuse a vote with a negative index": func(v *RoundSkipVote) {
			v.ValidatorIndex = -1
		},
		"should refuse a vote with a short pro tx hash": func(v *RoundSkipVote) {
			v.ValidatorProTxHash = v.ValidatorProTxHash[:4]
		},
		"should refuse a vote without a signature": func(v *RoundSkipVote) { v.Signature = nil },
	}
	for name, change := range voteCases {
		t.Run(name, func(t *testing.T) {
			v := validVote()
			change(v)
			assert.Error(t, v.ValidateBasic())
		})
	}

	validCertificate := func() *RoundSkipCertificate {
		return &RoundSkipCertificate{
			Height:             1,
			QuorumHash:         crypto.RandQuorumHash(),
			ThresholdSignature: make([]byte, SignatureSize),
		}
	}
	require.NoError(t, validCertificate().ValidateBasic())
	certificateCases := map[string]func(c *RoundSkipCertificate){
		"should refuse a certificate at height 0": func(c *RoundSkipCertificate) { c.Height = 0 },
		"should refuse a certificate with a short quorum hash": func(c *RoundSkipCertificate) {
			c.QuorumHash = c.QuorumHash[:4]
		},
		"should refuse a certificate with a short signature": func(c *RoundSkipCertificate) {
			c.ThresholdSignature = c.ThresholdSignature[:10]
		},
	}
	for name, change := range certificateCases {
		t.Run(name, func(t *testing.T) {
			c := validCertificate()
			change(c)
			assert.Error(t, c.ValidateBasic())
		})
	}
}
