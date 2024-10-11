package types

import (
	"bytes"
	"context"
	"testing"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

// Check VerifyCommit
// verification.
func TestValidatorSet_VerifyCommit_All(t *testing.T) {
	var (
		proTxHash  = crypto.RandProTxHash()
		privKey    = bls12381.GenPrivKey()
		pubKey     = privKey.PubKey()
		v1         = NewValidatorDefaultVotingPower(pubKey, proTxHash)
		quorumHash = crypto.RandQuorumHash()
		vset       = NewValidatorSet([]*Validator{v1}, v1.PubKey, btcjson.LLMQType_5_60, quorumHash, true)

		chainID = "Lalande21185"
	)

	vote := examplePrecommit(t)
	vote.ValidatorProTxHash = proTxHash
	v := vote.ToProto()

	dataToSign, err := MakeQuorumSigns(chainID, btcjson.LLMQType_5_60, quorumHash, v)
	require.NoError(t, err)

	sig, err := dataToSign.SignWithPrivkey(privKey)
	require.NoError(t, err)

	vote.BlockSignature = sig.BlockSign
	err = vote.VoteExtensions.SetSignatures(sig.VoteExtensionSignatures)
	require.NoError(t, err)

	commit := NewCommit(vote.Height,
		vote.Round,
		vote.BlockID,
		vote.VoteExtensions,

		&CommitSigns{
			QuorumSigns: sig,
			QuorumHash:  quorumHash,
		},
	)

	vote2 := *vote
	signBytes, err := v.SignBytes("EpsilonEridani")
	require.NoError(t, err)
	blockSig2, err := privKey.SignDigest(signBytes)
	require.NoError(t, err)
	vote2.BlockSignature = blockSig2

	testCases := []struct {
		description string
		chainID     string
		blockID     BlockID
		height      int64
		commit      *Commit
		expErr      bool
	}{
		{"good", chainID, vote.BlockID, vote.Height, commit, false},
		{"invalid block signature", "EpsilonEridani", vote.BlockID, vote.Height, commit, true},
		{"wrong block ID", chainID, makeBlockIDRandom(), vote.Height, commit, true},
		{
			description: "invalid commit -- wrong block ID",
			chainID:     chainID,
			blockID: BlockID{
				Hash:          vote.BlockID.Hash.Copy(),
				PartSetHeader: vote.BlockID.PartSetHeader,
				StateID:       RandStateID().Hash(),
			},
			height: vote.Height,
			commit: commit,
			expErr: true,
		},
		{"wrong height", chainID, vote.BlockID, vote.Height - 1, commit, true},
		// block sign malformed
		{"invalid block signature", chainID, vote.BlockID, vote.Height,
			NewCommit(vote.Height, vote.Round, vote.BlockID, vote.VoteExtensions,
				&CommitSigns{
					QuorumHash: quorumHash,
					QuorumSigns: QuorumSigns{
						BlockSign:               []byte("invalid block signature"),
						VoteExtensionSignatures: sig.VoteExtensionSignatures,
					}}), true},
		// quorum signs are replaced with vote2 non-threshold signature
		{"invalid block signature", chainID, vote.BlockID, vote.Height,
			NewCommit(vote.Height, vote.Round, vote.BlockID,
				vote.VoteExtensions,
				&CommitSigns{
					QuorumHash:  quorumHash,
					QuorumSigns: QuorumSigns{BlockSign: vote2.BlockSignature, VoteExtensionSignatures: vote2.VoteExtensions.GetSignatures()},
				},
			), true},
		// quorum hash mismatch
		{"wrong quorum hash", chainID, vote.BlockID, vote.Height,
			NewCommit(vote.Height, vote.Round, vote.BlockID,
				vote.VoteExtensions,
				&CommitSigns{
					QuorumHash:  bytes.Repeat([]byte{0xaa}, crypto.QuorumHashSize),
					QuorumSigns: QuorumSigns{BlockSign: vote2.BlockSignature, VoteExtensionSignatures: vote2.VoteExtensions.GetSignatures()},
				},
			), true},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			err := vset.VerifyCommit(tc.chainID, tc.blockID, tc.height, tc.commit)
			if tc.expErr {
				if assert.Error(t, err, "VerifyCommit") {
					assert.Contains(t, err.Error(), tc.description, "VerifyCommit")
				}
			} else {
				assert.NoError(t, err, "VerifyCommit")
			}
		})
	}
}

//-------------------------------------------------------------------

func TestValidatorSet_VerifyCommit_CheckThresholdSignatures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		chainID = "test_chain_id"
		h       = int64(3)
	)
	blockID := makeBlockIDRandom()

	voteSet, valSet, vals := randVoteSet(ctx, t, h, 0, tmproto.PrecommitType, 4)
	commit, err := makeCommit(ctx, blockID, h, 0, voteSet, vals)
	require.NoError(t, err)

	// malleate threshold sigs signature
	vote := voteSet.GetByIndex(3)
	v := vote.ToProto()
	err = vals[3].SignVote(ctx, "CentaurusA", valSet.QuorumType, valSet.QuorumHash, v, nil)
	require.NoError(t, err)
	commit.ThresholdBlockSignature = v.BlockSignature
	err = valSet.VerifyCommit(chainID, blockID, h, commit)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "invalid block signature")
	}

	goodVote := voteSet.GetByIndex(0)
	recoverer := NewSignsRecoverer(voteSet.votes)
	thresholdSigns, err := recoverer.Recover()
	require.NoError(t, err)
	commit.ThresholdBlockSignature = thresholdSigns.BlockSign
	exts := goodVote.VoteExtensions.Copy()
	for i, ext := range exts {
		ext.SetSignature(thresholdSigns.VoteExtensionSignatures[i])
	}
	commit.ThresholdVoteExtensions = exts.ToProto()
	err = valSet.VerifyCommit(chainID, blockID, h, commit)
	require.NoError(t, err)
}
