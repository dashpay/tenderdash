package types

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

// A verified commit's proof is accepted in place of verifying a commit again,
// so it is only as safe as what it names. These pin that it names exactly the
// inputs a verification is handed, that it can only be produced by verifying,
// and that anything it does not cover is verified in full — with the errors a
// full verification reports, since callers decide whether to evict a peer on
// their type.

// commitInputs is everything ValidatorSet.VerifyCommit is handed.
type commitInputs struct {
	chainID string
	vals    *ValidatorSet
	blockID BlockID
	height  int64
	commit  *Commit
}

// newCommitInputs returns a genuine threshold-signed commit, carrying
// threshold-recoverable vote extensions, and the inputs it verifies against.
func newCommitInputs(t *testing.T) commitInputs {
	t.Helper()
	ctx := context.Background()
	const height = int64(3)
	blockID := makeBlockIDRandom()
	voteSet, valSet, privVals := randVoteSet(ctx, t, height, 0, tmproto.PrecommitType, 4)
	commit, err := makeCommit(ctx, blockID, height, 0, voteSet, privVals)
	require.NoError(t, err)
	require.NotEmpty(t, commit.ThresholdVoteExtensions,
		"the commit must carry vote extensions for their bytes to be part of what is verified")
	in := commitInputs{chainID: voteSet.ChainID(), vals: valSet, blockID: blockID, height: height, commit: commit}
	require.NoError(t, in.verify(), "the genuine commit must verify, otherwise the negative cases prove nothing")
	return in
}

// clone returns a copy sharing no memory with in, so a case can rewrite any
// input without disturbing another.
func (in commitInputs) clone(t *testing.T) commitInputs {
	t.Helper()
	vals := in.vals.Copy()
	vals.QuorumHash = bytes.Clone(in.vals.QuorumHash)
	vals.ThresholdPublicKey = bls12381.PubKey(bytes.Clone(in.vals.ThresholdPublicKey.Bytes()))

	encoded, err := in.commit.ToProto().Marshal()
	require.NoError(t, err)
	var decoded tmproto.Commit
	require.NoError(t, decoded.Unmarshal(encoded))
	commit, err := CommitFromProto(&decoded)
	require.NoError(t, err)

	return commitInputs{chainID: in.chainID, vals: vals, blockID: in.blockID.Copy(), height: in.height, commit: commit}
}

func (in commitInputs) verify() error {
	return in.vals.VerifyCommit(in.chainID, in.blockID, in.height, in.commit)
}

func (in commitInputs) mint(budget VerificationBudget) (VerifiedCommit, error) {
	return VerifyCommitSignatures(in.vals, in.chainID, in.blockID, in.height, in.commit, budget)
}

func (in commitInputs) matches(v VerifiedCommit) error {
	return v.proof.checkMatches(in.chainID, in.vals, in.blockID, in.height, in.commit)
}

func (in commitInputs) verifyUnlessVerified(v VerifiedCommit) (bool, error) {
	return in.vals.VerifyCommitUnlessVerified(in.chainID, in.blockID, in.height, in.commit, v)
}

// A verified commit covers the commit it was minted for, and a caller holding
// it skips the threshold verification for that commit.
func TestVerifyCommitSignaturesCoversTheCommitItVerified(t *testing.T) {
	in := newCommitInputs(t)

	verified, err := in.mint(nil)
	require.NoError(t, err)
	require.NotNil(t, verified.proof)
	require.Same(t, in.commit, verified.Commit(), "the verified commit holds the caller's commit")
	require.NoError(t, in.matches(verified))

	// Equal inputs held in different memory: the proof is about values, and block
	// sync compares it against a validator set one height on that is a different
	// object holding the same quorum.
	require.NoError(t, in.clone(t).matches(verified))

	skipped, err := in.verifyUnlessVerified(verified)
	require.NoError(t, err)
	require.True(t, skipped, "a commit the proof covers must not be verified again")
}

// Nothing is handed out for a commit that failed verification, so there is
// nothing to skip on: the commit is verified again and rejected again.
func TestVerifyCommitSignaturesYieldsNothingForABadCommit(t *testing.T) {
	in := newCommitInputs(t).clone(t)
	in.commit.ThresholdBlockSignature[0] ^= 0xff

	verified, err := in.mint(nil)
	require.ErrorAs(t, err, &ErrInvalidCommitSignature{})
	require.Equal(t, VerifiedCommit{}, verified)

	skipped, err := in.verifyUnlessVerified(verified)
	require.False(t, skipped)
	require.ErrorAs(t, err, &ErrInvalidCommitSignature{})
}

// Missing inputs are reported rather than dereferenced: block sync verifies
// peer-supplied commits through here.
func TestVerifyCommitSignaturesRejectsMissingInputs(t *testing.T) {
	in := newCommitInputs(t)

	verified, err := VerifyCommitSignatures(nil, in.chainID, in.blockID, in.height, in.commit, nil)
	require.ErrorIs(t, err, ErrValidatorSetNilOrEmpty)
	require.Equal(t, VerifiedCommit{}, verified)

	verified, err = VerifyCommitSignatures(in.vals, in.chainID, in.blockID, in.height, nil, nil)
	require.Error(t, err)
	require.Equal(t, VerifiedCommit{}, verified)
}

// A VerifiedCommit without proof is what code that verified nothing holds —
// consensus, the replayer and block sync all pass the zero value. Nothing
// outside this package can build a proof-less VerifiedCommit that names a
// real commit; this in-package literal exists only to pin that even that
// stronger case — a bare commit with no proof, and no exported way to
// construct it — may never let a verification be skipped, not even when it
// holds the very commit being verified, and may never cause a commit to be
// rejected: it only ever falls through to a real verification.
func TestUnverifiedCommitMatchesNothing(t *testing.T) {
	in := newCommitInputs(t)

	holding := VerifiedCommit{commit: in.commit}
	require.Same(t, in.commit, holding.Commit())
	require.Nil(t, holding.proof)

	for name, unverified := range map[string]VerifiedCommit{
		"zero value":                     {},
		"holding the commit it verifies": holding,
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, in.matches(unverified), errCommitProofMismatch)

			skipped, err := in.verifyUnlessVerified(unverified)
			require.NoError(t, err, "a genuine commit offered without proof must still pass")
			require.False(t, skipped)

			forged := in.clone(t)
			forged.commit.ThresholdBlockSignature[0] ^= 0xff
			skipped, err = forged.verifyUnlessVerified(unverified)
			require.False(t, skipped)
			require.ErrorAs(t, err, &ErrInvalidCommitSignature{})
		})
	}
}

// A proof is about one commit under one set of inputs. Changing any one of
// them — on the verifier's side or in the commit — must not be covered, and
// must fall through to a verification that rejects it exactly as VerifyCommit
// does.
func TestVerifiedCommitDoesNotCoverADifferentVerification(t *testing.T) {
	genuine := newCommitInputs(t)
	verified, err := genuine.mint(nil)
	require.NoError(t, err)

	testCases := []struct {
		name   string
		mutate func(in *commitInputs)
		// panics is set where VerifyCommit itself dereferences the missing input;
		// those cases are only asked whether the proof matches
		panics bool
	}{
		{name: "chain ID", mutate: func(in *commitInputs) { in.chainID += "-fork" }},
		{name: "height", mutate: func(in *commitInputs) { in.height++ }},
		{name: "block ID", mutate: func(in *commitInputs) { in.blockID.Hash[0] ^= 0x01 }},
		{name: "quorum type", mutate: func(in *commitInputs) { in.vals.QuorumType++ }},
		{name: "quorum hash", mutate: func(in *commitInputs) { in.vals.QuorumHash = crypto.RandQuorumHash() }},
		{name: "threshold key", mutate: func(in *commitInputs) {
			in.vals.ThresholdPublicKey = bls12381.GenPrivKey().PubKey()
		}},
		{name: "threshold key bytes", mutate: func(in *commitInputs) {
			in.vals.ThresholdPublicKey.Bytes()[0] ^= 0x01
		}},
		{name: "nil threshold key", mutate: func(in *commitInputs) { in.vals.ThresholdPublicKey = nil }, panics: true},
		{name: "nil validator set", mutate: func(in *commitInputs) { in.vals = nil }, panics: true},
		{name: "nil commit", mutate: func(in *commitInputs) { in.commit = nil }, panics: true},
		{name: "commit height", mutate: func(in *commitInputs) { in.commit.Height++ }},
		{name: "commit round", mutate: func(in *commitInputs) { in.commit.Round++ }},
		{name: "commit block hash", mutate: func(in *commitInputs) { in.commit.BlockID.Hash[0] ^= 0x01 }},
		{name: "commit state ID", mutate: func(in *commitInputs) { in.commit.BlockID.StateID[0] ^= 0x01 }},
		{name: "commit quorum hash", mutate: func(in *commitInputs) { in.commit.QuorumHash = crypto.RandQuorumHash() }},
		{name: "commit block signature", mutate: func(in *commitInputs) {
			in.commit.ThresholdBlockSignature[0] ^= 0x01
		}},
		{name: "commit vote extension", mutate: func(in *commitInputs) {
			ext := in.commit.ThresholdVoteExtensions[len(in.commit.ThresholdVoteExtensions)-1]
			ext.Extension[0] ^= 0x01
		}},
		{name: "commit vote-extension signature", mutate: func(in *commitInputs) {
			in.commit.ThresholdVoteExtensions[0].Signature[0] ^= 0x01
		}},
		{name: "commit gains a vote extension", mutate: func(in *commitInputs) {
			in.commit.ThresholdVoteExtensions = append(in.commit.ThresholdVoteExtensions,
				&tmproto.VoteExtension{
					Type:      tmproto.VoteExtensionType_THRESHOLD_RECOVER,
					Extension: []byte("extra"),
					Signature: bytes.Clone(in.commit.ThresholdVoteExtensions[0].Signature),
				})
		}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			in := genuine.clone(t)
			tc.mutate(&in)

			require.ErrorIs(t, in.matches(verified), errCommitProofMismatch)
			if tc.panics {
				return
			}

			want := in.verify()
			require.Error(t, want, "the mutation left the commit verifiable, so not covering it would prove nothing")
			skipped, err := in.verifyUnlessVerified(verified)
			require.False(t, skipped)
			require.Equal(t, want, err, "an uncovered commit must be verified exactly as VerifyCommit verifies it")
		})
	}
}

// A proof owns what it records. Rewriting the inputs it was minted from
// afterwards — through the commit, block ID or validator set the caller still
// holds — must not change what it covers. The commit itself is held by the
// caller's pointer on purpose: it is what the proof is about, not part of it,
// so rewriting it must make the proof refuse even that very commit.
func TestVerifiedCommitProofSurvivesMutationOfItsInputs(t *testing.T) {
	genuine := newCommitInputs(t)
	minted := genuine.clone(t)
	pristine := genuine.clone(t)

	verified, err := minted.mint(nil)
	require.NoError(t, err)
	require.Same(t, minted.commit, verified.Commit(),
		"the verified commit embeds the caller's commit pointer by design")

	minted.blockID.Hash[0] ^= 0xff
	minted.commit.ThresholdBlockSignature[0] ^= 0xff
	minted.commit.ThresholdVoteExtensions[0].Signature[0] ^= 0xff
	minted.vals.QuorumHash[0] ^= 0xff
	minted.vals.ThresholdPublicKey.Bytes()[0] ^= 0xff

	require.NoError(t, pristine.matches(verified),
		"the proof must not alias the block ID, commit or validator set it was minted from")
	require.ErrorIs(t, minted.matches(verified), errCommitProofMismatch,
		"the rewritten inputs, including the embedded commit itself, are not what was verified")
}

// The threshold key is an interface, so a caller can mint with an
// implementation that verifies anything while reporting the real key's bytes.
// Whether a proof covers a verification has to be decided by the key the
// consumer trusts, not by the key the proof was minted with.
func TestVerifiedCommitFromAKeyThatAnswersYesMatchesNothing(t *testing.T) {
	in := newCommitInputs(t).clone(t)
	in.commit.ThresholdBlockSignature = make([]byte, SignatureSize)

	hostile := in.vals.Copy()
	hostile.ThresholdPublicKey = yesPubKey{bytes: bytes.Clone(in.vals.ThresholdPublicKey.Bytes())}

	// whether such a key can mint at all is not the point — the zero value it
	// leaves behind on failure is refused for the same reason
	verified, _ := VerifyCommitSignatures(hostile, in.chainID, in.blockID, in.height, in.commit, nil)

	require.ErrorIs(t, in.matches(verified), errCommitProofMismatch,
		"a proof minted by a key of the caller's choosing was accepted")
	skipped, err := in.verifyUnlessVerified(verified)
	require.False(t, skipped)
	require.ErrorAs(t, err, &ErrInvalidCommitSignature{})
}

// Callers evict a peer on ErrInvalidCommitSignature alone, and tolerate every
// other commit failure. Minting a verified commit, and consuming one whose
// proof does not cover the commit, must report exactly the error VerifyCommit
// reports — same type, same content — for every failure VerifyCommit
// distinguishes.
func TestVerifiedCommitKeepsTheVerifyCommitErrors(t *testing.T) {
	genuine := newCommitInputs(t)
	verified, err := genuine.mint(nil)
	require.NoError(t, err)

	testCases := []struct {
		name   string
		mutate func(in *commitInputs)
		typed  bool
		check  func(t *testing.T, err error)
	}{
		{
			name:   "forged threshold signature",
			mutate: func(in *commitInputs) { in.commit.ThresholdBlockSignature[0] ^= 0xff },
			typed:  true,
		},
		{
			name:   "wrong block ID",
			mutate: func(in *commitInputs) { in.blockID = makeBlockIDRandom() },
		},
		{
			name:   "wrong quorum hash",
			mutate: func(in *commitInputs) { in.commit.QuorumHash = crypto.RandQuorumHash() },
		},
		{
			name:   "wrong height",
			mutate: func(in *commitInputs) { in.height++ },
			check: func(t *testing.T, err error) {
				require.ErrorAs(t, err, &ErrInvalidCommitHeight{})
			},
		},
		{
			// Extensions are outside the block signature's digest and a DEFAULT one
			// yields no sign item, so the block signature verifies and the counts
			// then disagree: what an honest peer with another extension
			// configuration produces.
			name: "vote-extension count mismatch",
			mutate: func(in *commitInputs) {
				in.commit.ThresholdVoteExtensions = append(in.commit.ThresholdVoteExtensions,
					&tmproto.VoteExtension{
						Type:      tmproto.VoteExtensionType_DEFAULT,
						Extension: []byte("not threshold-recoverable"),
						Signature: make([]byte, SignatureSize),
					})
			},
			check: func(t *testing.T, err error) {
				require.ErrorAs(t, err, &ErrVoteExtensionCountMismatch{})
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			in := genuine.clone(t)
			tc.mutate(&in)

			want := in.verify()
			require.Error(t, want)
			if tc.typed {
				require.ErrorAs(t, want, &ErrInvalidCommitSignature{})
			} else {
				require.NotErrorAs(t, want, &ErrInvalidCommitSignature{})
			}
			if tc.check != nil {
				tc.check(t, want)
			}

			minted, err := in.mint(nil)
			require.Equal(t, want, err, "minting must fail exactly as VerifyCommit fails")
			require.Equal(t, VerifiedCommit{}, minted)

			skipped, err := in.verifyUnlessVerified(verified)
			require.False(t, skipped)
			require.Equal(t, want, err, "an uncovered commit must fail exactly as VerifyCommit fails")
		})
	}
}

// Minting charges the verification budget exactly as VerifyCommitWithBudget
// does, and an exhausted budget is reported as such rather than as forgery.
func TestVerifyCommitSignaturesChargesTheBudget(t *testing.T) {
	in := newCommitInputs(t)

	exhausted := &recordingVerificationBudget{decisions: []bool{false}}
	verified, err := in.mint(exhausted)
	require.ErrorIs(t, err, ErrVerificationBudgetExhausted)
	require.NotErrorAs(t, err, &ErrInvalidCommitSignature{})
	require.Equal(t, VerifiedCommit{}, verified)
	require.Equal(t, []int{1}, exhausted.costs)

	charged := &recordingVerificationBudget{}
	verified, err = in.mint(charged)
	require.NoError(t, err)
	require.NoError(t, in.matches(verified))

	reference := &recordingVerificationBudget{}
	require.NoError(t, in.vals.VerifyCommitWithBudget(in.chainID, in.blockID, in.height, in.commit, reference))
	require.Equal(t, reference.costs, charged.costs)
}
