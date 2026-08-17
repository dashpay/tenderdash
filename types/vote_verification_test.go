package types

import (
	"bytes"
	"context"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

// A vote set that accepts a caller's word that a vote was verified is only as
// safe as the thing carrying that word. These pin what the carrier has to be:
// evidence naming what was verified, refused wherever the name does not match,
// and impossible to produce without doing the verification.

// The evidence a verification produces is what a vote set checks its own
// parameters against, and a vote it covers is stored without the signatures
// being checked a second time.
func TestAddVerifiedVoteStoresTheVoteItCovers(t *testing.T) {
	ctx := context.Background()
	voteSet, valSet, privVals := randVoteSet(ctx, t, 1, 0, tmproto.PrecommitType, 4)

	vote := signedPrecommit(ctx, t, voteSet, privVals[0], 0)
	val := valSet.GetByIndex(0)

	verified, err := VerifyVoteSignatures(vote, voteSet.ChainID(),
		valSet.QuorumType, valSet.QuorumHash, val.PubKey, val.ProTxHash, nil)
	require.NoError(t, err)

	added, err := voteSet.AddVerifiedVote(vote, verified)
	require.NoError(t, err)
	require.True(t, added)
	require.NotNil(t, voteSet.GetByIndex(0))
}

// Verification is never handed out for a vote that failed it, so there is no
// evidence to carry and nothing for a vote set to accept.
func TestVerifyVoteSignaturesYieldsNothingForABadSignature(t *testing.T) {
	ctx := context.Background()
	voteSet, valSet, privVals := randVoteSet(ctx, t, 1, 0, tmproto.PrecommitType, 4)

	vote := signedPrecommit(ctx, t, voteSet, privVals[0], 0)
	vote.BlockSignature = make([]byte, SignatureSize)
	val := valSet.GetByIndex(0)

	verified, err := VerifyVoteSignatures(vote, voteSet.ChainID(),
		valSet.QuorumType, valSet.QuorumHash, val.PubKey, val.ProTxHash, nil)
	require.Error(t, err)
	require.Equal(t, VoteVerification{}, verified)

	added, err := voteSet.AddVerifiedVote(vote, verified)
	require.False(t, added)
	require.ErrorIs(t, err, ErrVoteVerificationMismatch)
	require.Nil(t, voteSet.GetByIndex(0), "a vote nothing verified was stored")
}

// The zero value is what any code outside this package can construct without
// verifying anything. It must never admit a vote — including a vote that would
// have verified, since accepting it would mean the vote set is trusting the
// caller rather than the evidence.
func TestZeroVerificationAdmitsNothing(t *testing.T) {
	ctx := context.Background()
	voteSet, _, privVals := randVoteSet(ctx, t, 1, 0, tmproto.PrecommitType, 4)

	vote := signedPrecommit(ctx, t, voteSet, privVals[0], 0)

	added, err := voteSet.AddVerifiedVote(vote, VoteVerification{})
	require.False(t, added)
	require.ErrorIs(t, err, ErrVoteVerificationMismatch)
	require.Nil(t, voteSet.GetByIndex(0))
}

// Evidence is about one vote. Offering it for another must not admit that
// other vote, whatever else the two have in common — the whole point of naming
// the vote is that a verification cannot be reused.
func TestVerificationOfOneVoteDoesNotAdmitAnother(t *testing.T) {
	ctx := context.Background()
	voteSet, valSet, privVals := randVoteSet(ctx, t, 1, 0, tmproto.PrecommitType, 4)

	verifiedVote := signedPrecommit(ctx, t, voteSet, privVals[0], 0)
	val := valSet.GetByIndex(0)
	verified, err := VerifyVoteSignatures(verifiedVote, voteSet.ChainID(),
		valSet.QuorumType, valSet.QuorumHash, val.PubKey, val.ProTxHash, nil)
	require.NoError(t, err)

	// Same validator, same round, a different block and no usable signature for
	// it: exactly what a sender would want a stolen verification to admit.
	forged := verifiedVote.Copy()
	forged.BlockID = randBlockID()
	forged.BlockSignature = make([]byte, SignatureSize)

	added, err := voteSet.AddVerifiedVote(forged, verified)
	require.False(t, added)
	require.ErrorIs(t, err, ErrVoteVerificationMismatch)
	require.Nil(t, voteSet.GetByIndex(0))
}

// Evidence has to name the bytes that were verified, not the memory holding
// them. A vote is reachable from outside this package between the moment its
// signatures are checked and the moment it is stored — the application is handed
// the vote's own backing arrays on the way — so a vote whose signed content has
// been rewritten through that memory must be refused, even though it is
// literally the same vote object the verification was minted for.
func TestVerificationDoesNotCoverAVoteMutatedInPlace(t *testing.T) {
	mutations := map[string]func(vote *Vote){
		"block hash": func(vote *Vote) { vote.BlockID.Hash[0] ^= 0x01 },
		"state ID":   func(vote *Vote) { vote.BlockID.StateID[0] ^= 0x01 },
		"part set header hash": func(vote *Vote) {
			vote.BlockID.PartSetHeader.Hash[0] ^= 0x01
		},
		"block signature": func(vote *Vote) { vote.BlockSignature[0] ^= 0x01 },
		"vote extension": func(vote *Vote) {
			vote.VoteExtensions.GetExtensions()[0][0] ^= 0x01
		},
		"vote extension signature": func(vote *Vote) {
			vote.VoteExtensions.GetSignatures()[0][0] ^= 0x01
		},
	}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			voteSet, valSet, privVals := randVoteSet(ctx, t, 1, 0, tmproto.PrecommitType, 4)

			vote := signedPrecommitWithExtensions(ctx, t, voteSet, privVals[0], 0)
			val := valSet.GetByIndex(0)

			verified, err := VerifyVoteSignatures(vote, voteSet.ChainID(),
				valSet.QuorumType, valSet.QuorumHash, val.PubKey, val.ProTxHash, nil)
			require.NoError(t, err)

			// Written through the vote's own arrays: no field is reassigned and the
			// vote keeps the identity the verification was minted for.
			mutate(vote)

			require.Error(t,
				vote.Verify(voteSet.ChainID(), valSet.QuorumType, valSet.QuorumHash, val.PubKey, val.ProTxHash),
				"the mutation left the vote verifiable, so refusing it would prove nothing")

			added, err := voteSet.AddVerifiedVote(vote, verified)
			require.False(t, added)
			require.ErrorIs(t, err, ErrVoteVerificationMismatch)
			require.Nil(t, voteSet.GetByIndex(0), "a vote nothing verified was stored")
		})
	}
}

// Whether a signature checks out depends on the chain it was made for. A vote
// verified on one chain is not verified on another, so a vote set on the other
// chain must refuse the evidence instead of reading it as a verification of its
// own — and must refuse it outright rather than falling back to checking the
// signatures itself, since the price a precommit is admitted at assumes exactly
// one such check.
func TestVerificationFromAnotherChainIsRefused(t *testing.T) {
	ctx := context.Background()
	voteSet, valSet, privVals := randVoteSet(ctx, t, 1, 0, tmproto.PrecommitType, 4)
	vote := signedPrecommit(ctx, t, voteSet, privVals[0], 0)
	val := valSet.GetByIndex(0)

	verified, err := VerifyVoteSignatures(vote, voteSet.ChainID(),
		valSet.QuorumType, valSet.QuorumHash, val.PubKey, val.ProTxHash, nil)
	require.NoError(t, err)

	// The same validator set, voting on a different chain. What makes the
	// transplant worth refusing is that the vote does not verify here at all.
	otherChain := NewVoteSet("another_chain_id", voteSet.GetHeight(), voteSet.GetRound(),
		tmproto.PrecommitType, valSet)
	require.Error(t,
		vote.Verify(otherChain.ChainID(), valSet.QuorumType, valSet.QuorumHash, val.PubKey, val.ProTxHash),
		"the vote verifies on the other chain too, so refusing the evidence would prove nothing")

	added, err := otherChain.AddVerifiedVote(vote, verified)
	require.False(t, added)
	require.ErrorIs(t, err, ErrVoteVerificationMismatch)
	require.Nil(t, otherChain.GetByIndex(0), "a vote nothing verified here was stored")
}

// signedPrecommit returns a precommit for a random block, signed by privVal as
// validator idx of voteSet's validator set.
func signedPrecommit(
	ctx context.Context,
	t *testing.T,
	voteSet *VoteSet,
	privVal PrivValidator,
	idx int32,
) *Vote {
	t.Helper()
	proTxHash, err := privVal.GetProTxHash(ctx)
	require.NoError(t, err)
	vote := &Vote{
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     idx,
		Height:             voteSet.GetHeight(),
		Round:              voteSet.GetRound(),
		Type:               tmproto.PrecommitType,
		BlockID:            randBlockID(),
	}
	proto := vote.ToProto()
	require.NoError(t, privVal.SignVote(ctx, voteSet.ChainID(),
		voteSet.valSet.QuorumType, voteSet.valSet.QuorumHash, proto, nil))
	require.NoError(t, vote.PopulateSignsFromProto(proto))
	return vote
}

// signedPrecommitWithExtensions is signedPrecommit carrying the
// threshold-recoverable vote extensions a Dash validator's precommit does, so
// that the extension bytes and their signatures are part of what was verified.
func signedPrecommitWithExtensions(
	ctx context.Context,
	t *testing.T,
	voteSet *VoteSet,
	privVal PrivValidator,
	idx int32,
) *Vote {
	t.Helper()
	proTxHash, err := privVal.GetProTxHash(ctx)
	require.NoError(t, err)
	vote := &Vote{
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     idx,
		Height:             voteSet.GetHeight(),
		Round:              voteSet.GetRound(),
		Type:               tmproto.PrecommitType,
		BlockID:            randBlockID(),
		VoteExtensions:     thresholdVoteExtensionsOfLen(t, 2),
	}
	proto := vote.ToProto()
	require.NoError(t, privVal.SignVote(ctx, voteSet.ChainID(),
		voteSet.valSet.QuorumType, voteSet.valSet.QuorumHash, proto, nil))
	require.NoError(t, vote.PopulateSignsFromProto(proto))
	return vote
}

// The validator a vote names is not covered by any signature, so rewriting it
// changes no digest. The signature check this evidence stands in for rejected
// such a vote outright, and the evidence has to reject it too — otherwise a
// check that was deleted survives only as an ordering accident in whichever
// caller happens to look first.
func TestVerificationDoesNotCoverARewrittenValidator(t *testing.T) {
	ctx := context.Background()
	voteSet, valSet, privVals := randVoteSet(ctx, t, 1, 0, tmproto.PrecommitType, 4)

	vote := signedPrecommitWithExtensions(ctx, t, voteSet, privVals[0], 0)
	val := valSet.GetByIndex(0)

	// The signing helpers hand the vote the validator set's own array. Give the
	// vote its own copy first, or writing through it would move the value this
	// is measured against and the mutation would prove nothing.
	vote.ValidatorProTxHash = bytes.Clone(vote.ValidatorProTxHash)

	verified, err := VerifyVoteSignatures(vote, voteSet.ChainID(),
		valSet.QuorumType, valSet.QuorumHash, val.PubKey, val.ProTxHash, nil)
	require.NoError(t, err)

	vote.ValidatorProTxHash[0] ^= 0x01

	require.Error(t,
		vote.Verify(voteSet.ChainID(), valSet.QuorumType, valSet.QuorumHash, val.PubKey, val.ProTxHash),
		"the rewrite left the vote verifiable, so refusing it would prove nothing")

	// Asked directly, because a vote set resolves the validator and rejects the
	// rewrite before it ever consults the evidence. That ordering is what makes
	// this hard to see, and relying on it is what this guards against: the
	// evidence has to be able to answer on its own.
	require.ErrorIs(t,
		verified.checkMatches(vote, voteSet.ChainID(), valSet.QuorumType,
			valSet.QuorumHash, val.PubKey, val.ProTxHash),
		ErrVoteVerificationMismatch,
		"the evidence still vouches for a vote naming a validator it never verified")

	added, err := voteSet.AddVerifiedVote(vote, verified)
	require.False(t, added)
	require.Error(t, err)
	require.Nil(t, voteSet.GetByIndex(0), "a vote naming a validator nothing verified was stored")
}

// A public key is an interface value over bytes the caller owns, and every path
// that hands one out — a validator copy, a vote set's own lookup — shares that
// one backing array. Evidence that keeps the key itself therefore reads whatever
// those bytes say at the moment it is consulted, which is not what the signature
// check ran against. Rewriting the array to another validator's key moves the
// evidence and the vote set's expectation together, so the two agree on a key no
// signature was ever checked under.
func TestVerificationDoesNotCoverAVoteWhoseKeyWasRewritten(t *testing.T) {
	ctx := context.Background()
	voteSet, valSet, privVals := randVoteSet(ctx, t, 1, 0, tmproto.PrecommitType, 4)

	vote := signedPrecommitWithExtensions(ctx, t, voteSet, privVals[0], 0)
	val := valSet.GetByIndex(0)

	keyA := bytes.Clone(val.PubKey.Bytes())
	keyB := bytes.Clone(valSet.GetByIndex(1).PubKey.Bytes())
	require.NotEqual(t, keyA, keyB)

	verified, err := VerifyVoteSignatures(vote, voteSet.ChainID(),
		valSet.QuorumType, valSet.QuorumHash, val.PubKey, val.ProTxHash, nil)
	require.NoError(t, err)

	// Written through the array the validator set, the copy handed to the
	// verifier and the vote set's own lookup all share.
	copy(val.PubKey.Bytes(), keyB)
	require.Equal(t, keyB, voteSet.valSet.GetByIndex(0).PubKey.Bytes(),
		"the rewrite did not reach the key the vote set checks against, so it would prove nothing")

	require.Error(t,
		vote.Verify(voteSet.ChainID(), valSet.QuorumType, valSet.QuorumHash, val.PubKey, val.ProTxHash),
		"the vote verifies under the rewritten key, so refusing it would prove nothing")

	require.ErrorIs(t,
		verified.checkMatches(vote, voteSet.ChainID(), valSet.QuorumType,
			valSet.QuorumHash, val.PubKey, val.ProTxHash),
		ErrVoteVerificationMismatch,
		"the evidence still vouches for a key nothing was verified under")

	added, err := voteSet.AddVerifiedVote(vote, verified)
	require.False(t, added)
	require.ErrorIs(t, err, ErrVoteVerificationMismatch)
	require.Nil(t, voteSet.GetByIndex(0), "a vote nothing verified was stored")
}

// Only a precommit may carry vote extensions, and the check this evidence stands
// in for enforced that. Extensions are outside every digest a vote signs — the
// canonical bytes omit them and the signing data keeps only the
// threshold-recoverable ones — so hanging a generic extension on a verified
// prevote leaves every recorded digest and signature intact. The evidence has to
// refuse it anyway, because a fresh verification would.
func TestVerificationDoesNotCoverAPrevoteGivenExtensions(t *testing.T) {
	ctx := context.Background()
	voteSet, valSet, privVals := randVoteSet(ctx, t, 1, 0, tmproto.PrevoteType, 4)

	vote := signedPrevote(ctx, t, voteSet, privVals[0], 0)
	val := valSet.GetByIndex(0)

	verified, err := VerifyVoteSignatures(vote, voteSet.ChainID(),
		valSet.QuorumType, valSet.QuorumHash, val.PubKey, val.ProTxHash, nil)
	require.NoError(t, err)

	require.NoError(t, vote.VoteExtensions.Add(tmproto.VoteExtension{
		Type:      tmproto.VoteExtensionType_DEFAULT,
		Extension: []byte("not allowed on a prevote"),
	}))

	require.Error(t,
		vote.Verify(voteSet.ChainID(), valSet.QuorumType, valSet.QuorumHash, val.PubKey, val.ProTxHash),
		"the extension left the vote verifiable, so refusing it would prove nothing")

	require.ErrorIs(t,
		verified.checkMatches(vote, voteSet.ChainID(), valSet.QuorumType,
			valSet.QuorumHash, val.PubKey, val.ProTxHash),
		ErrVoteVerificationMismatch,
		"the evidence still vouches for a vote a fresh verification rejects")

	added, err := voteSet.AddVerifiedVote(vote, verified)
	require.False(t, added)
	require.ErrorIs(t, err, ErrVoteVerificationMismatch)
	require.Nil(t, voteSet.GetByIndex(0), "a malformed vote nothing verified was stored")
}

// The key a verification runs against is an interface, so a caller inside this
// process can supply an implementation that answers yes to everything. Evidence
// must not be usable on the strength of such a key: what a vote set accepts has
// to be decided by the key material the vote set itself trusts, not by an
// implementation the minting caller chose.
func TestVerificationFromAKeyThatAnswersYesAdmitsNothing(t *testing.T) {
	ctx := context.Background()
	voteSet, valSet, privVals := randVoteSet(ctx, t, 1, 0, tmproto.PrecommitType, 4)

	vote := signedPrecommit(ctx, t, voteSet, privVals[0], 0)
	val := valSet.GetByIndex(0)

	// No usable signature, and a key that says otherwise while impersonating the
	// real validator's bytes.
	vote.BlockSignature = make([]byte, SignatureSize)
	hostile := yesPubKey{bytes: bytes.Clone(val.PubKey.Bytes())}

	// Whether such a key can mint at all is not the point — the zero value it
	// leaves behind on failure is refused for the same reason.
	verified, _ := VerifyVoteSignatures(vote, voteSet.ChainID(),
		valSet.QuorumType, valSet.QuorumHash, hostile, val.ProTxHash, nil)
	require.ErrorIs(t,
		verified.checkMatches(vote, voteSet.ChainID(), valSet.QuorumType,
			valSet.QuorumHash, val.PubKey, val.ProTxHash),
		ErrVoteVerificationMismatch,
		"evidence minted by a key of the caller's choosing was accepted")

	added, err := voteSet.AddVerifiedVote(vote, verified)
	require.False(t, added)
	require.Error(t, err)
	require.Nil(t, voteSet.GetByIndex(0), "a forged vote was stored")
}

// yesPubKey verifies every signature and considers itself equal to every key,
// while reporting whichever bytes it was built with.
type yesPubKey struct {
	bytes []byte
}

var _ crypto.PubKey = yesPubKey{}

func (k yesPubKey) Address() crypto.Address                       { return crypto.AddressHash(k.bytes) }
func (k yesPubKey) Bytes() []byte                                 { return k.bytes }
func (k yesPubKey) VerifySignature(_ []byte, _ []byte) bool       { return true }
func (k yesPubKey) VerifySignatureDigest(_ []byte, _ []byte) bool { return true }
func (k yesPubKey) Equals(crypto.PubKey) bool                     { return true }
func (k yesPubKey) Type() string                                  { return "yes" }
func (k yesPubKey) TypeTag() string                               { return "yes" }
func (k yesPubKey) String() string                                { return "yes" }
func (k yesPubKey) HexString() string                             { return hex.EncodeToString(k.bytes) }

// signedPrevote returns a prevote for a random block, signed by privVal as
// validator idx of voteSet's validator set.
func signedPrevote(
	ctx context.Context,
	t *testing.T,
	voteSet *VoteSet,
	privVal PrivValidator,
	idx int32,
) *Vote {
	t.Helper()
	proTxHash, err := privVal.GetProTxHash(ctx)
	require.NoError(t, err)
	vote := &Vote{
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     idx,
		Height:             voteSet.GetHeight(),
		Round:              voteSet.GetRound(),
		Type:               tmproto.PrevoteType,
		BlockID:            randBlockID(),
	}
	proto := vote.ToProto()
	require.NoError(t, privVal.SignVote(ctx, voteSet.ChainID(),
		voteSet.valSet.QuorumType, voteSet.valSet.QuorumHash, proto, nil))
	require.NoError(t, vote.PopulateSignsFromProto(proto))
	return vote
}
