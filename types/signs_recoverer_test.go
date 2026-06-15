package types

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

func TestSigsRecoverer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const (
		height  = 1000
		chainID = "dash-platform"
	)
	blockID := makeBlockID([]byte("blockhash"), 1000, []byte("partshash"), nil)
	quorumType := crypto.SmallQuorumType()
	quorumHash := crypto.RandQuorumHash()
	testCases := []struct {
		expectInvalidSig bool
		votes            []*Vote
	}{
		{
			votes: []*Vote{
				{
					ValidatorProTxHash: crypto.RandProTxHash(),
					Type:               tmproto.PrecommitType,
					BlockID:            blockID,
					VoteExtensions: mockVoteExtensions(t,
						tmproto.VoteExtensionType_THRESHOLD_RECOVER, "threshold",
						tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW, crypto.Checksum([]byte("threshold-raw")),
					),
				},
				{
					ValidatorProTxHash: crypto.RandProTxHash(),
					Type:               tmproto.PrecommitType,
					BlockID:            blockID,
					VoteExtensions: mockVoteExtensions(t,
						tmproto.VoteExtensionType_THRESHOLD_RECOVER, "threshold",
						tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW, crypto.Checksum([]byte("threshold-raw")),
					),
				},
			},
		},
		{
			votes: []*Vote{
				{
					ValidatorProTxHash: crypto.RandProTxHash(),
					Type:               tmproto.PrecommitType,
					BlockID:            blockID,
					VoteExtensions: mockVoteExtensions(t,
						tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW, crypto.Checksum([]byte("threshold-raw")),
						tmproto.VoteExtensionType_THRESHOLD_RECOVER, "threshold",
					),
				},
				{
					ValidatorProTxHash: crypto.RandProTxHash(),
					Type:               tmproto.PrecommitType,
					BlockID:            blockID,
					VoteExtensions: mockVoteExtensions(t,
						tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW, crypto.Checksum([]byte("threshold-raw")),
						tmproto.VoteExtensionType_THRESHOLD_RECOVER, "threshold",
					),
				},
			},
		},
		{
			expectInvalidSig: true,
			votes: []*Vote{
				{
					ValidatorProTxHash: crypto.RandProTxHash(),
					Type:               tmproto.PrecommitType,
					BlockID:            blockID,
					VoteExtensions: mockVoteExtensions(t,
						tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW, crypto.Checksum([]byte("threshold-raw")),
						tmproto.VoteExtensionType_THRESHOLD_RECOVER, "threshold",
					),
				},
				{
					ValidatorProTxHash: crypto.RandProTxHash(),
					Type:               tmproto.PrecommitType,
					BlockID:            blockID,
					VoteExtensions: mockVoteExtensions(t,
						tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW, crypto.Checksum([]byte("threshold-raw")),
						tmproto.VoteExtensionType_THRESHOLD_RECOVER, "threshold1",
					),
				},
			},
		},
		{
			expectInvalidSig: true,
			votes: []*Vote{
				{
					ValidatorProTxHash: crypto.RandProTxHash(),
					Type:               tmproto.PrecommitType,
					BlockID:            blockID,
					VoteExtensions: mockVoteExtensions(t,
						tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW, crypto.Checksum([]byte("threshold-raw1")),
						tmproto.VoteExtensionType_THRESHOLD_RECOVER, "threshold",
					),
				},
				{
					ValidatorProTxHash: crypto.RandProTxHash(),
					Type:               tmproto.PrecommitType,
					BlockID:            blockID,
					VoteExtensions: mockVoteExtensions(t,
						tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW, crypto.Checksum([]byte("threshold-raw")),
						tmproto.VoteExtensionType_THRESHOLD_RECOVER, "threshold",
					),
				},
			},
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case #%d", i), func(t *testing.T) {
			pubKeys := make([]crypto.PubKey, 0, len(tc.votes))
			IDs := make([][]byte, 0, len(tc.votes))
			pvs := make([]*MockPV, len(tc.votes))
			for i, vote := range tc.votes {
				protoVote := vote.ToProto()
				pvs[i] = NewMockPV(GenKeysForQuorumHash(quorumHash), UseProTxHash(vote.ValidatorProTxHash))
				err := pvs[i].SignVote(ctx, chainID, quorumType, quorumHash, protoVote, nil)
				require.NoError(t, err)
				err = vote.PopulateSignsFromProto(protoVote)
				require.NoError(t, err)
				pubKey, err := pvs[i].GetPubKey(ctx, quorumHash)
				require.NoError(t, err)
				pubKeys = append(pubKeys, pubKey)
				IDs = append(IDs, vote.ValidatorProTxHash)
			}

			quorumSigns, err := MakeQuorumSigns(chainID, quorumType, quorumHash, tc.votes[0].ToProto())
			require.NoError(t, err)

			thresholdPubKey, err := bls12381.RecoverThresholdPublicKeyFromPublicKeys(pubKeys, IDs)
			require.NoError(t, err)

			sr, err := NewSignsRecoverer(tc.votes)
			require.NoError(t, err)
			thresholdVoteSigns, err := sr.Recover()
			require.NoError(t, err)
			err = quorumSigns.Verify(thresholdPubKey, *thresholdVoteSigns)
			if tc.expectInvalidSig {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSigsRecoverer_UsingVoteSet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const (
		chainID = "dash-platform"
		height  = 1000
		n       = 4
	)
	blockID := makeBlockID([]byte("blockhash"), 1000, []byte("partshash"), nil)
	vals, pvs := RandValidatorSet(n)
	quorumType := crypto.SmallQuorumType()
	quorumHash, err := pvs[0].GetFirstQuorumHash(ctx)
	require.NoError(t, err)
	votes := make([]*Vote, n)
	for i := 0; i < n; i++ {
		proTxHash, err := pvs[i].GetProTxHash(ctx)
		require.NoError(t, err)
		votes[i] = &Vote{
			ValidatorProTxHash: proTxHash,
			ValidatorIndex:     int32(i),
			Height:             height,
			Round:              0,
			Type:               tmproto.PrecommitType,
			BlockID:            blockID,
			VoteExtensions: mockVoteExtensions(t,
				tmproto.VoteExtensionType_THRESHOLD_RECOVER, "default",
				tmproto.VoteExtensionType_THRESHOLD_RECOVER, "threshold",
			),
		}
		vpb := votes[i].ToProto()
		err = pvs[i].SignVote(ctx, chainID, quorumType, quorumHash, vpb, nil)
		require.NoError(t, err)
		err = votes[i].PopulateSignsFromProto(vpb)
		require.NoError(t, err)
	}
	voteSet := NewVoteSet(chainID, height, 0, tmproto.PrecommitType, vals)
	for _, vote := range votes {
		added, err := voteSet.AddVote(vote)
		require.NoError(t, err)
		require.True(t, added)
	}
}

func TestSignsRecovererErrors(t *testing.T) {
	blockID := makeBlockID([]byte("blockhash"), 1000, []byte("partshash"), nil)

	// DEFAULT (non-threshold) extensions exercise the count/type guards without
	// requiring recoverable threshold signatures.
	twoExts := func() VoteExtensions {
		return mockVoteExtensions(t,
			tmproto.VoteExtensionType_DEFAULT, "a",
			tmproto.VoteExtensionType_DEFAULT, "b",
		)
	}
	oneExt := func() VoteExtensions {
		return mockVoteExtensions(t, tmproto.VoteExtensionType_DEFAULT, "a")
	}

	testCases := []struct {
		name      string
		votes     []*Vote
		expectErr bool
	}{
		{
			name: "consistent extension counts",
			votes: []*Vote{
				{ValidatorProTxHash: crypto.RandProTxHash(), Type: tmproto.PrecommitType, BlockID: blockID, VoteExtensions: twoExts()},
				{ValidatorProTxHash: crypto.RandProTxHash(), Type: tmproto.PrecommitType, BlockID: blockID, VoteExtensions: twoExts()},
			},
			expectErr: false,
		},
		{
			name: "mismatched extension counts",
			votes: []*Vote{
				{ValidatorProTxHash: crypto.RandProTxHash(), Type: tmproto.PrecommitType, BlockID: blockID, VoteExtensions: twoExts()},
				{ValidatorProTxHash: crypto.RandProTxHash(), Type: tmproto.PrecommitType, BlockID: blockID, VoteExtensions: oneExt()},
			},
			expectErr: true,
		},
		{
			name: "non-precommit vote carrying extensions",
			votes: []*Vote{
				{ValidatorProTxHash: crypto.RandProTxHash(), Type: tmproto.PrevoteType, BlockID: blockID, VoteExtensions: twoExts()},
			},
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// The constructor must surface inconsistent input as an error, never a panic.
			require.NotPanics(t, func() {
				_, err := NewSignsRecoverer(tc.votes)
				if tc.expectErr {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			})
		})
	}
}

// mockVoteExtensions returns vote-extensions container, created by passed pairs list
// the format of pairs is
// 1. the length of pairs must be even
// 2. each pair consist of 2 elements: type and extension value
// example: types.VoteExtensionType_THRESHOLD_RECOVER, "defailt", types.VoteExtensionType_THRESHOLD_RECOVER, "threshold"
func mockVoteExtensions(t *testing.T, pairs ...interface{}) VoteExtensions {
	if len(pairs)%2 != 0 {
		t.Fatalf("the pairs length must be even")
	}
	ve := make(VoteExtensions, 0)
	for i := 0; i < len(pairs); i += 2 {
		extensionType, ok := pairs[i].(tmproto.VoteExtensionType)
		if !ok {
			t.Fatalf("given unsupported type %T", pairs[i])
		}
		ext := tmproto.VoteExtension{
			Type: extensionType,
		}
		switch v := pairs[i+1].(type) {
		case string:
			ext.Extension = []byte(v)
		case []byte:
			ext.Extension = v
		default:
			t.Fatalf("given unsupported type %T", pairs[i+1])
		}
		ve.Add(ext)

	}
	return ve
}
