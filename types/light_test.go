package types

import (
	"context"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/version"
)

func TestLightBlockValidateBasic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	header := MakeRandHeader()
	height := header.Height
	commit := randCommit(ctx, t, header.Height, header.StateID())
	vals, _ := RandValidatorSet(5)
	header.LastBlockID = commit.BlockID
	header.ValidatorsHash = vals.Hash()
	header.Version.Block = version.BlockProtocol
	vals2, _ := RandValidatorSet(3)
	vals3 := vals.Copy()
	vals3.QuorumHash = []byte("invalid")
	commit.BlockID.Hash = header.Hash()

	sh := &SignedHeader{
		Header: &header,
		Commit: commit,
	}

	testCases := []struct {
		name      string
		sh        *SignedHeader
		vals      *ValidatorSet
		expectErr string
	}{
		{
			name: "valid light block",
			sh:   sh,
			vals: vals,
		},
		{
			name:      "hashes don't match",
			sh:        sh,
			vals:      vals2,
			expectErr: "expected validator hash of header to match validator set hash",
		},
		{
			name:      "invalid validator set",
			sh:        sh,
			vals:      vals3,
			expectErr: "invalid validator set",
		},
		{
			name: "invalid signed header",
			sh: &SignedHeader{
				Header: &header,
				Commit: randCommit(ctx, t, height, header.StateID()),
			},
			vals:      vals,
			expectErr: "invalid signed header: commit signs block",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lightBlock := LightBlock{
				SignedHeader: tc.sh,
				ValidatorSet: tc.vals,
			}
			err := lightBlock.ValidateBasic(header.ChainID)
			if tc.expectErr != "" {
				assert.ErrorContains(t, err, tc.expectErr, tc.name)
			} else {
				assert.NoError(t, err, tc.name)
			}
		})
	}
}

func TestLightBlockProtobuf(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	header := MakeRandHeader()
	commit := randCommit(ctx, t, header.Height, RandStateID())
	vals, _ := RandValidatorSet(5)
	header.Height = commit.Height
	header.LastBlockID = commit.BlockID
	header.Version.Block = version.BlockProtocol
	header.ValidatorsHash = vals.Hash()
	commit.BlockID.Hash = header.Hash()
	commit.QuorumHash = vals.QuorumHash

	sh := &SignedHeader{
		Header: &header,
		Commit: commit,
	}

	testCases := []struct {
		name       string
		sh         *SignedHeader
		vals       *ValidatorSet
		toProtoErr bool
		toBlockErr bool
	}{
		{"valid light block", sh, vals, false, false},
		{"empty signed header", &SignedHeader{}, vals, false, false},
		{"empty validator set", sh, &ValidatorSet{}, false, true},
		{"empty light block", &SignedHeader{}, &ValidatorSet{}, false, true},
	}

	for _, tc := range testCases {
		lightBlock := &LightBlock{
			SignedHeader: tc.sh,
			ValidatorSet: tc.vals,
		}
		lbp, err := lightBlock.ToProto()
		if tc.toProtoErr {
			assert.Error(t, err, tc.name)
		} else {
			assert.NoError(t, err, tc.name)
		}

		lb, err := LightBlockFromProto(lbp)
		if tc.toBlockErr {
			assert.Error(t, err, tc.name)
		} else {
			assert.NoError(t, err, tc.name)
			assert.Equal(t, lightBlock, lb)
		}
	}

}

func TestSignedHeaderValidateBasic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	height := rand.Int63()
	commit := randCommit(ctx, t, height, RandStateID())

	chainID := "𠜎"
	timestamp := time.Date(math.MaxInt64, 0, 0, 0, 0, 0, math.MaxInt64, time.UTC)
	h := Header{
		Version:            version.Consensus{Block: version.BlockProtocol, App: math.MaxInt64},
		ChainID:            chainID,
		Height:             commit.Height,
		Time:               timestamp,
		LastBlockID:        commit.BlockID,
		LastCommitHash:     commit.Hash(),
		DataHash:           commit.Hash(),
		ValidatorsHash:     commit.Hash(),
		NextValidatorsHash: commit.Hash(),
		ConsensusHash:      commit.Hash(),
		AppHash:            commit.Hash(),
		ResultsHash:        commit.Hash(),
		EvidenceHash:       commit.Hash(),
		ProposerProTxHash:  crypto.ProTxHashFromSeedBytes([]byte("proposer_pro_tx_hash")),
	}

	validSignedHeader := SignedHeader{Header: &h, Commit: commit}
	validSignedHeader.Commit.BlockID.Hash = validSignedHeader.Hash()
	invalidSignedHeader := SignedHeader{}

	testCases := []struct {
		testName  string
		shHeader  *Header
		shCommit  *Commit
		expectErr bool
	}{
		{"Valid Signed Header", validSignedHeader.Header, validSignedHeader.Commit, false},
		{"Invalid Signed Header", invalidSignedHeader.Header, validSignedHeader.Commit, true},
		{"Invalid Signed Header", validSignedHeader.Header, invalidSignedHeader.Commit, true},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			sh := SignedHeader{
				Header: tc.shHeader,
				Commit: tc.shCommit,
			}
			err := sh.ValidateBasic(validSignedHeader.Header.ChainID)
			assert.Equalf(
				t,
				tc.expectErr,
				err != nil,
				"Validate Basic had an unexpected result",
				err,
			)
		})
	}
}
