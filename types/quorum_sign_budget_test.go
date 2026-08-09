package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type recordingVerificationBudget struct {
	decisions []bool
	costs     []int
}

func (b *recordingVerificationBudget) Allow(cost int) bool {
	b.costs = append(b.costs, cost)
	if len(b.decisions) == 0 {
		return true
	}
	decision := b.decisions[0]
	b.decisions = b.decisions[1:]
	return decision
}

type recordingVerifyPubKey struct {
	pubKeyBLS
	results []bool
	calls   int
}

func (p *recordingVerifyPubKey) VerifySignatureDigest(_ []byte, _ []byte) bool {
	p.calls++
	if len(p.results) == 0 {
		return true
	}
	result := p.results[0]
	p.results = p.results[1:]
	return result
}

func quorumSignsWithExtensions(n int) QuorumSigns {
	signatures := make([][]byte, n)
	for i := range signatures {
		signatures[i] = []byte("signature")
	}
	return QuorumSigns{
		BlockSign:               []byte("block-signature"),
		VoteExtensionSignatures: signatures,
	}
}

func quorumSignDataWithExtensions(n int) QuorumSignData {
	items := make([]SignItem, n)
	for i := range items {
		items[i] = SignItem{SignHash: make([]byte, 32)}
	}
	return QuorumSignData{
		Block:                  SignItem{SignHash: make([]byte, 32)},
		VoteExtensionSignItems: items,
	}
}

func TestQuorumSignDataVerifyWithBudget_StagedPermitsWrapVerification(t *testing.T) {
	testCases := []struct {
		name       string
		extensions int
		decisions  []bool
		verify     []bool
		wantCosts  []int
		wantCalls  int
		wantErr    error
	}{
		{
			name:      "block permit denied before verification",
			decisions: []bool{false},
			wantCosts: []int{1},
			wantErr:   ErrVerificationBudgetExhausted,
		},
		{
			name:       "invalid block signature does not reserve extension permits",
			extensions: MaxVoteExtensions,
			decisions:  []bool{true},
			verify:     []bool{false},
			wantCosts:  []int{1},
			wantCalls:  1,
			wantErr:    ErrVoteInvalidBlockSignature,
		},
		{
			name:       "extension permit denied after valid block verification",
			extensions: MaxVoteExtensions,
			decisions:  []bool{true, false},
			verify:     []bool{true},
			wantCosts:  []int{1, MaxVoteExtensions},
			wantCalls:  1,
			wantErr:    ErrVerificationBudgetExhausted,
		},
		{
			name:       "valid signatures consume actual staged costs",
			extensions: 2,
			decisions:  []bool{true, true},
			verify:     []bool{true, true, true},
			wantCosts:  []int{1, 2},
			wantCalls:  3,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			budget := &recordingVerificationBudget{decisions: tc.decisions}
			pubKey := &recordingVerifyPubKey{results: tc.verify}
			signData := quorumSignDataWithExtensions(tc.extensions)

			err := signData.VerifyWithBudget(pubKey, quorumSignsWithExtensions(tc.extensions), budget)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.wantCosts, budget.costs)
			require.Equal(t, tc.wantCalls, pubKey.calls)
		})
	}
}

func TestQuorumSignDataVerifyWithBudget_CountMismatchDoesNotConsumeExtensionPermits(t *testing.T) {
	budget := &recordingVerificationBudget{}
	pubKey := &recordingVerifyPubKey{results: []bool{true}}
	signData := quorumSignDataWithExtensions(1)

	err := signData.VerifyWithBudget(pubKey, quorumSignsWithExtensions(0), budget)

	var mismatch ErrVoteExtensionCountMismatch
	require.ErrorAs(t, err, &mismatch)
	require.Equal(t, []int{1}, budget.costs)
	require.Equal(t, 1, pubKey.calls)
}
