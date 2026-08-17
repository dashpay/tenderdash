package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

const unknownExtensionType = tmproto.VoteExtensionType(42)

// hostileCommit builds the poison an unprivileged peer can put on
// ConsensusVoteChannel: a Commit whose ThresholdVoteExtensions carry a vote-extension
// type outside the generated enum. proto3 enums are open, so gogoproto unmarshals any
// varint into VoteExtensionType.
//
// It is built as a struct rather than decoded through CommitFromProto, which rejects
// an unknown extension type. The subject of the tests below is defense in depth —
// what GetCanonicalVote and VerifyCommit do if a poison commit reaches memory by a
// route that skips ValidateBasic. Both run on the consensus goroutine, where a panic
// is fatal, so "unreachable today" is no reason to stop pinning that they error.
func hostileCommit(extType tmproto.VoteExtensionType) *Commit {
	return &Commit{
		Height: 100,
		Round:  0,
		BlockID: BlockID{
			Hash:          crypto.Checksum([]byte("block")),
			PartSetHeader: PartSetHeader{Total: 1, Hash: crypto.Checksum([]byte("parts"))},
			StateID:       crypto.Checksum([]byte("state")),
		},
		QuorumHash:              crypto.Checksum([]byte("quorum")),
		ThresholdBlockSignature: make([]byte, SignatureSize),
		ThresholdVoteExtensions: []*tmproto.VoteExtension{
			{Type: extType, Extension: []byte("x"), Signature: make([]byte, 96)},
		},
	}
}

// hostileCommitProto is the same poison in wire form. ToProto is the real bridge
// between the two shapes, so the wire message cannot drift from the struct.
func hostileCommitProto(extType tmproto.VoteExtensionType) *tmproto.Commit {
	return hostileCommit(extType).ToProto()
}

// The decode boundary itself rejects the poison. This is the property that lets every
// downstream consumer — WAL replay, the block store, gossip — assume a decoded Commit
// carries only dispatchable extension types.
func TestCommitFromProto_UnknownExtensionType_ReturnsError(t *testing.T) {
	var err error
	require.NotPanics(t, func() {
		// CommitFromProto returns `commit, commit.ValidateBasic()`, handing back a
		// populated value alongside the error, so only the error is asserted here.
		_, err = CommitFromProto(hostileCommitProto(unknownExtensionType))
	})
	require.ErrorIs(t, err, ErrUnknownVoteExtensionType,
		"an unknown extension type must be rejected at the decode boundary")

	// A defined type on the same message decodes, so the rejection is the type.
	ok, err := CommitFromProto(hostileCommitProto(tmproto.VoteExtensionType_THRESHOLD_RECOVER))
	require.NoError(t, err)
	assert.NotNil(t, ok)
}

// An unknown vote-extension type must be rejected with an error, not a panic
// and not a substituted value. proto3 enums are open, so any peer can put an
// undefined type on the wire; converting it is reachable from untrusted input.
// Returning a default-typed extension instead would silently change the
// sign-hash derived from it, so the conversion must fail closed.
func TestVoteExtensionFromProto_UnknownType_ReturnsError(t *testing.T) {
	var (
		ext VoteExtensionIf
		err error
	)
	require.NotPanics(t, func() {
		ext, err = VoteExtensionFromProto(tmproto.VoteExtension{
			Type:      unknownExtensionType,
			Extension: []byte("x"),
			Signature: make([]byte, 96),
		})
	})
	require.ErrorIs(t, err, ErrUnknownVoteExtensionType,
		"an unknown extension type must be rejected, not converted")
	assert.Nil(t, ext, "no extension value may be fabricated for an unknown type")
}

// The conversion loop dereferences every element, so a nil entry is a panic on a
// path reachable from untrusted network input. A decoder can produce a nil element
// for an empty repeated-field entry, so the loop must reject it fail-closed rather
// than trust the slice to be dense.
func TestVoteExtensionsFromProto_NilElement_ReturnsError(t *testing.T) {
	var (
		extensions VoteExtensions
		err        error
	)
	require.NotPanics(t, func() {
		extensions, err = VoteExtensionsFromProto(
			&tmproto.VoteExtension{Type: tmproto.VoteExtensionType_THRESHOLD_RECOVER, Extension: []byte("ok")},
			nil,
		)
	}, "a nil vote extension must not panic the conversion")
	require.ErrorIs(t, err, ErrNilVoteExtension)
	require.EqualError(t, err, "nil vote extension at index 1",
		"the rejection must name the offending index")
	assert.Nil(t, extensions, "no partial container may be returned")
}

// GetCanonicalVote is called by ValidatorSet.VerifyCommit on the consensus
// goroutine. An attacker-supplied unknown extension type must surface as an
// error there, never a panic.
func TestCommit_GetCanonicalVote_UnknownType_ReturnsError(t *testing.T) {
	commit := hostileCommit(unknownExtensionType)

	var (
		canonVote *Vote
		err       error
	)
	require.NotPanics(t, func() {
		canonVote, err = commit.GetCanonicalVote()
	}, "unknown vote-extension type must not panic while building the canonical vote")
	require.ErrorIs(t, err, ErrUnknownVoteExtensionType,
		"the canonical vote must not be built from an unknown extension type")
	assert.Nil(t, canonVote)
}

// End-to-end through the function the consensus goroutine actually calls:
// StateData.verifyCommit -> ValidatorSet.VerifyCommit. Before the fix this
// panics (and receiveRoutine re-panics, terminating the process). After the
// fix it must return an error the caller can turn into a PeerError.
//
// The commit is a genuinely valid one - the positive control below proves it
// verifies - and only then is a single extension type flipped to an undefined
// value. Asserting the specific rejection is what keeps this test honest: a
// commit carrying a junk signature would fail verification anyway, so a bare
// require.Error would still pass with the unknown-type rejection deleted.
func TestValidatorSet_VerifyCommit_UnknownExtensionType_ReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const height = int64(3)
	blockID := makeBlockIDRandom()

	voteSet, valSet, privVals := randVoteSet(ctx, t, height, 0, tmproto.PrecommitType, 4)
	commit, err := makeCommit(ctx, blockID, height, 0, voteSet, privVals)
	require.NoError(t, err)

	// Positive control: without the hostile mutation this commit verifies.
	require.NoError(t, valSet.VerifyCommit(voteSet.ChainID(), blockID, height, commit),
		"the unmutated commit must verify, otherwise the negative case below proves nothing")

	commit.ThresholdVoteExtensions[0].Type = unknownExtensionType

	var verifyErr error
	require.NotPanics(t, func() {
		verifyErr = valSet.VerifyCommit(voteSet.ChainID(), blockID, height, commit)
	}, "VerifyCommit must not panic on an attacker-supplied vote-extension type")
	require.ErrorIs(t, verifyErr, ErrUnknownVoteExtensionType,
		"the commit must be rejected for the unknown extension type specifically")
}

// MakeQuorumSigns is the shared chokepoint for commit (VerifyCommit) and vote
// (VerifyExtensionSign) verification, and it takes the wire-format vote. An
// unknown extension type must be rejected here — otherwise it is silently
// excluded from the sign items and the message is processed instead of rejected.
func TestMakeQuorumSigns_UnknownExtensionType_ReturnsError(t *testing.T) {
	protoVote := &tmproto.Vote{
		Type:   tmproto.PrecommitType,
		Height: 100,
		Round:  0,
		VoteExtensions: []*tmproto.VoteExtension{{
			Type:      unknownExtensionType,
			Extension: []byte("x"),
			Signature: make([]byte, 96),
		}},
	}

	var err error
	require.NotPanics(t, func() {
		_, err = MakeQuorumSigns("test-chain", 106, crypto.Checksum([]byte("q")), protoVote)
	})
	require.ErrorIs(t, err, ErrUnknownVoteExtensionType,
		"MakeQuorumSigns must reject an unknown vote-extension type")
}

// A peer's precommit reaches the node through MsgFromProto -> VoteFromProto
// (internal/consensus/msgs.go). Rejecting there keeps an unknown extension type
// out of the vote entirely, so it can never reach the ABCI VerifyVoteExtension
// call, and the reactor drops the message instead of the process dying.
func TestVoteFromProto_UnknownExtensionType_ReturnsError(t *testing.T) {
	protoVote := &tmproto.Vote{
		Type:   tmproto.PrecommitType,
		Height: 100,
		Round:  0,
		BlockID: tmproto.BlockID{
			Hash:          crypto.Checksum([]byte("block")),
			PartSetHeader: tmproto.PartSetHeader{Total: 1, Hash: crypto.Checksum([]byte("parts"))},
			StateID:       crypto.Checksum([]byte("state")),
		},
		VoteExtensions: []*tmproto.VoteExtension{{
			Type:      unknownExtensionType,
			Extension: []byte("x"),
			Signature: make([]byte, 96),
		}},
	}

	var (
		vote *Vote
		err  error
	)
	require.NotPanics(t, func() {
		vote, err = VoteFromProto(protoVote)
	}, "decoding a peer precommit must not panic on an unknown extension type")
	require.ErrorIs(t, err, ErrUnknownVoteExtensionType,
		"an unknown extension type must be rejected at the decode boundary")
	assert.Nil(t, vote)
}

// A second, distinct boundary from the decode path above: VerifyExtensionSign
// operates on an already-built Vote, so it is reachable whenever a Vote is
// assembled without passing through VoteFromProto. The extension is therefore
// built as a struct literal - a converter cannot guard this path, which is
// exactly why the check must also hold inside VerifyExtensionSign.
func TestVote_VerifyExtensionSign_UnknownType_ReturnsError(t *testing.T) {
	vals, _ := RandValidatorSet(4)
	val := vals.Validators[0]

	vote := &Vote{
		Type:               tmproto.PrecommitType,
		Height:             100,
		Round:              0,
		ValidatorProTxHash: val.ProTxHash,
		ValidatorIndex:     0,
		BlockID: BlockID{
			Hash: crypto.Checksum([]byte("block")),
			PartSetHeader: PartSetHeader{
				Total: 1,
				Hash:  crypto.Checksum([]byte("parts")),
			},
		},
		VoteExtensions: VoteExtensions{
			&GenericVoteExtension{VoteExtension: tmproto.VoteExtension{
				Type:      unknownExtensionType,
				Extension: []byte("x"),
				Signature: make([]byte, 96),
			}},
		},
	}

	var err error
	require.NotPanics(t, func() {
		err = vote.VerifyExtensionSign("test-chain", val.PubKey, vals.QuorumType, vals.QuorumHash)
	}, "VerifyExtensionSign must not panic on an unknown extension type")
	require.ErrorIs(t, err, ErrUnknownVoteExtensionType,
		"an unknown extension type must be rejected before reaching ABCI VerifyVoteExtension")
}
