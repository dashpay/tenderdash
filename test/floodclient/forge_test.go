//go:build floodclient

package floodclient

import (
	"testing"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	tmcons "github.com/dashpay/tenderdash/proto/tendermint/consensus"
	"github.com/dashpay/tenderdash/types"
)

// testForgeConfig is a target description with one placeholder validator and a
// placeholder quorum hash — enough for every profile to build a well-formed
// message. The values do not have to be real: these tests check structural
// validity, which is independent of whether the identity matches a live node.
func testForgeConfig() ForgeConfig {
	return ForgeConfig{
		Validators: []ForgedValidator{{ProTxHash: make([]byte, crypto.ProTxHashSize), Index: 0}},
		QuorumHash: make([]byte, crypto.HashSize),
	}
}

// TestProfiles_StructurallyValid asserts every profile emits a message that
// passes the domain ValidateBasic for its type. This is the property the whole
// tool depends on: a structurally-invalid message is rejected at decode, before
// any defense, and proves nothing about the node shedding attack traffic. So
// each forged message must be rejected only at or after verification — never at
// decode. The vote/commit/proposal shapes are checked here against the public
// types package; the consensus-only shapes (block part, state, maj23) are
// checked against the reactor's own decode path in the consensus package's
// flood test (TestFloodProfiles_StructurallyValidAtReactorDecode).
func TestProfiles_StructurallyValid(t *testing.T) {
	profiles := BuildProfiles(testForgeConfig())

	// Every profile must at least marshal and be repeatable several times (the
	// alternating profiles must be valid on every branch).
	for name, prof := range profiles {
		t.Run(name+"/marshal", func(t *testing.T) {
			for i := 0; i < 4; i++ {
				_, err := proto.Marshal(prof.Next(1, 0))
				require.NoErrorf(t, err, "%s message %d must marshal", name, i)
			}
		})
	}

	t.Run("prevote", func(t *testing.T) {
		msg := profiles["prevote"].Next(1, 0).(*tmcons.Vote)
		v, err := types.VoteFromProto(msg.Vote)
		require.NoError(t, err)
		require.NoError(t, v.ValidateBasic())
		require.Equal(t, types.SignatureSize, len(v.BlockSignature))
	})

	t.Run("precommit-extensions", func(t *testing.T) {
		msg := profiles["precommit-extensions"].Next(1, 0).(*tmcons.Vote)
		require.Len(t, msg.Vote.VoteExtensions, numExtensions,
			"the profile must declare the maximum extensions so the node would pay for all of them")
		v, err := types.VoteFromProto(msg.Vote)
		require.NoError(t, err)
		require.NoError(t, v.ValidateBasic())
	})

	t.Run("commit", func(t *testing.T) {
		msg := profiles["commit"].Next(1, 0).(*tmcons.Commit)
		c, err := types.CommitFromProto(msg.Commit)
		require.NoError(t, err)
		require.NoError(t, c.ValidateBasic())
	})

	t.Run("proposal", func(t *testing.T) {
		msg := profiles["proposal"].Next(1, 0).(*tmcons.Proposal)
		p, err := types.ProposalFromProto(&msg.Proposal)
		require.NoError(t, err)
		require.NoError(t, p.ValidateBasic())
	})
}

// TestProfiles_MalformedRejected pins the intent of the profiles that
// deliberately emit messages the node refuses rather than valid attack traffic
// that reaches the verification defense. It is the counterpart of
// TestProfiles_StructurallyValid: those profiles must pass ValidateBasic, these
// must not, and MalformedProfiles is the boundary between the two rosters.
//
// The two shapes are refused at different layers, and each is pinned to its own:
// an undefined vote-extension type is rejected in the decode path (conversion,
// which CommitFromProto/VoteFromProto run before returning), before any
// signature is verified; an over-long extension list is decode-valid but
// declares more extensions than the protocol permits, which prices it out at the
// reactor's cost gate. Both are rejected without a signature verification, which
// is the property the tool exists to demonstrate.
func TestProfiles_MalformedRejected(t *testing.T) {
	profiles := BuildProfiles(testForgeConfig())

	// Every malformed profile is named as such, so the decode-validity roster and
	// this one partition the profile set with no profile in both or neither.
	malformed := MalformedProfiles()
	require.Len(t, malformed, 4)
	for name := range malformed {
		require.Containsf(t, profiles, name, "malformed profile %q is not registered", name)
	}

	t.Run("commit-unknown-extension/rejected-at-decode", func(t *testing.T) {
		msg := profiles["commit-unknown-extension"].Next(1, 0).(*tmcons.Commit)
		_, err := types.CommitFromProto(msg.Commit)
		require.Error(t, err, "a commit carrying an undefined vote-extension type must be rejected at decode")
	})

	t.Run("precommit-unknown-extension/rejected-at-decode", func(t *testing.T) {
		msg := profiles["precommit-unknown-extension"].Next(1, 0).(*tmcons.Vote)
		_, err := types.VoteFromProto(msg.Vote)
		require.Error(t, err, "a precommit carrying an undefined vote-extension type must be rejected at decode")
	})

	t.Run("commit-too-many-extensions/decode-valid-but-over-count", func(t *testing.T) {
		msg := profiles["commit-too-many-extensions"].Next(1, 0).(*tmcons.Commit)
		require.Greater(t, len(msg.Commit.ThresholdVoteExtensions), types.MaxVoteExtensions,
			"the profile must declare more extensions than the protocol permits")
		_, err := types.CommitFromProto(msg.Commit)
		require.NoError(t, err, "an over-long extension list is decode-valid; it is refused at the cost gate, not at decode")
	})

	t.Run("precommit-too-many-extensions/decode-valid-but-over-count", func(t *testing.T) {
		msg := profiles["precommit-too-many-extensions"].Next(1, 0).(*tmcons.Vote)
		require.Greater(t, len(msg.Vote.VoteExtensions), types.MaxVoteExtensions,
			"the profile must declare more extensions than the protocol permits")
		_, err := types.VoteFromProto(msg.Vote)
		require.NoError(t, err, "an over-long extension list is decode-valid; it is refused at the cost gate, not at decode")
	})
}

// TestProfiles_VoteIdentityRotation checks the vote profiles attribute forged
// votes to the configured validators in rotation, so a forged vote reaches the
// node's signature verification (a vote whose proTxHash does not match its index
// is rejected before the budget). Without this the cheap flood would exercise
// admission and decode but never the defense it targets.
func TestProfiles_VoteIdentityRotation(t *testing.T) {
	cfg := ForgeConfig{Validators: []ForgedValidator{
		{ProTxHash: bytesOf(crypto.ProTxHashSize, 1), Index: 0},
		{ProTxHash: bytesOf(crypto.ProTxHashSize, 2), Index: 1},
	}}
	prof := BuildProfiles(cfg)["prevote"]

	seen := map[int32]bool{}
	for i := 0; i < 4; i++ {
		v := prof.Next(1, 0).(*tmcons.Vote).Vote
		require.Len(t, v.ValidatorProTxHash, crypto.ProTxHashSize)
		seen[v.ValidatorIndex] = true
	}
	require.Len(t, seen, 2, "the flood must spread across the configured validators")
}

func bytesOf(n int, v byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = v
	}
	return b
}
