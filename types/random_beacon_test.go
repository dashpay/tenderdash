package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

// randomBeaconFixture holds a block header, the BlockID its prevotes carry, and a prevote vote set
// over a validator set whose majority gate is below the voting power BLS recovery needs.
type randomBeaconFixture struct {
	header   Header
	blockID  BlockID
	valSet   *ValidatorSet
	privVals []PrivValidator
}

const randomBeaconTestRound = int32(2)

func newRandomBeaconFixture(t *testing.T) randomBeaconFixture {
	t.Helper()
	valSet, privVals := RandValidatorSet(10)
	// A majority gate of two validators, far below the seven the recovery needs.
	valSet.VotingPowerThreshold = uint64(2 * DefaultDashVotingPower)
	require.Less(t, valSet.QuorumVotingThresholdPower(), valSet.QuorumTypeThresholdVotingPower())

	header := MakeRandHeader()
	header.ChainID = "test_chain_id"
	header.Height = 5
	header.ValidatorsHash = valSet.Hash()
	blockID := BlockID{
		Hash:          header.Hash(),
		PartSetHeader: PartSetHeader{Total: 3, Hash: crypto.Checksum([]byte("parts"))},
		StateID:       header.StateID().Hash(),
	}
	return randomBeaconFixture{header: header, blockID: blockID, valSet: valSet, privVals: privVals}
}

// addPrevotes signs prevotes for the fixture's block from validators [from, to) into voteSet.
func (f randomBeaconFixture) addPrevotes(ctx context.Context, t *testing.T, voteSet *VoteSet, from, to int) {
	t.Helper()
	for i := from; i < to; i++ {
		proTxHash, err := f.privVals[i].GetProTxHash(ctx)
		require.NoError(t, err)
		vote := &Vote{
			ValidatorProTxHash: proTxHash,
			ValidatorIndex:     int32(i),
			Height:             f.header.Height,
			Round:              randomBeaconTestRound,
			Type:               tmproto.PrevoteType,
			BlockID:            f.blockID,
		}
		added, err := signAddVote(ctx, f.privVals[i], vote, voteSet)
		require.NoError(t, err)
		require.True(t, added)
	}
}

// recoverBeacon returns the random beacon recovered from the prevotes of every validator.
func (f randomBeaconFixture) recoverBeacon(ctx context.Context, t *testing.T) []byte {
	t.Helper()
	voteSet := NewVoteSet(f.header.ChainID, f.header.Height, randomBeaconTestRound, tmproto.PrevoteType, f.valSet,
		WithRandomBeacon(true))
	f.addPrevotes(ctx, t, voteSet, 0, len(f.privVals))
	beacon := voteSet.ThresholdBlockSignature()
	require.Len(t, beacon, SignatureSize)
	return beacon
}

// TestVoteSetRandomBeaconPrevoteMajority checks that, with WithRandomBeacon, a prevote majority counts only
// once its threshold signature is recovered, even when the majority gate is lower, and that the recovered
// signature verifies as the random beacon of the block and round.
func TestVoteSetRandomBeaconPrevoteMajority(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newRandomBeaconFixture(t)
	threshold := int(f.valSet.QuorumTypeThresholdVotingPower() / DefaultDashVotingPower)

	t.Run("without random beacon, the gate alone forms the majority", func(t *testing.T) {
		voteSet := NewVoteSet(f.header.ChainID, f.header.Height, randomBeaconTestRound, tmproto.PrevoteType, f.valSet)
		f.addPrevotes(ctx, t, voteSet, 0, 2)
		blockID, ok := voteSet.TwoThirdsMajority()
		require.True(t, ok)
		require.True(t, blockID.Equals(f.blockID))
		require.Nil(t, voteSet.ThresholdBlockSignature(), "prevotes are not recovered")
	})

	t.Run("with random beacon, the majority waits for recovery", func(t *testing.T) {
		voteSet := NewVoteSet(f.header.ChainID, f.header.Height, randomBeaconTestRound, tmproto.PrevoteType, f.valSet,
			WithRandomBeacon(true))
		f.addPrevotes(ctx, t, voteSet, 0, threshold-1)
		_, ok := voteSet.TwoThirdsMajority()
		require.False(t, ok, "the gate is reached, but the beacon cannot be recovered yet")
		require.Nil(t, voteSet.ThresholdBlockSignature())

		f.addPrevotes(ctx, t, voteSet, threshold-1, threshold)
		blockID, ok := voteSet.TwoThirdsMajority()
		require.True(t, ok)
		require.True(t, blockID.Equals(f.blockID))
		beacon := voteSet.ThresholdBlockSignature()
		require.Len(t, beacon, SignatureSize)

		signItem := RandomBeaconSignItem(f.header.ChainID, f.valSet.QuorumType, f.valSet.QuorumHash,
			f.header.Height, randomBeaconTestRound, f.blockID)
		require.True(t, signItem.VerifySignature(f.valSet.ThresholdPublicKey, beacon))

		// Any other set of threshold shares recovers the same beacon.
		others := NewVoteSet(f.header.ChainID, f.header.Height, randomBeaconTestRound, tmproto.PrevoteType, f.valSet,
			WithRandomBeacon(true))
		f.addPrevotes(ctx, t, others, len(f.privVals)-threshold, len(f.privVals))
		require.Equal(t, beacon, others.ThresholdBlockSignature())
	})
}

// TestCommitVerifyRandomBeacon checks the commit fields of consensus version 2 against the block header.
func TestCommitVerifyRandomBeacon(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newRandomBeaconFixture(t)
	beacon := f.recoverBeacon(ctx, t)
	finalAppHash := crypto.Checksum(append(f.header.AppHash.Copy(), beacon...))

	makeCommit := func() *Commit {
		return &Commit{
			Height: f.header.Height,
			Round:  randomBeaconTestRound,
			BlockID: BlockID{
				Hash:          f.blockID.Hash,
				PartSetHeader: f.blockID.PartSetHeader,
				StateID:       f.header.StateIDWithAppHash(finalAppHash).Hash(),
			},
			QuorumHash:              f.valSet.QuorumHash,
			ThresholdBlockSignature: crypto.CRandBytes(SignatureSize),
			Beacon:                  beacon,
			AppHash:                 finalAppHash,
		}
	}

	commit := makeCommit()
	require.NoError(t, commit.VerifyRandomBeacon(f.header.ChainID, f.valSet, &f.header))
	appHash, err := commit.VerifyAppHash(&f.header)
	require.NoError(t, err)
	require.EqualValues(t, finalAppHash, appHash)
	assert.True(t, commit.BlockID.SameBlock(f.blockID))
	assert.False(t, commit.BlockID.Equals(f.blockID), "the commit signs another state ID than the prevotes")

	testCases := map[string]func(c *Commit){
		"app hash not the one signed":  func(c *Commit) { c.AppHash = f.header.AppHash.Copy() },
		"missing app hash":             func(c *Commit) { c.AppHash = nil },
		"missing beacon":               func(c *Commit) { c.Beacon = nil },
		"beacon of another signature":  func(c *Commit) { c.Beacon = c.ThresholdBlockSignature },
		"beacon of another round":      func(c *Commit) { c.Round++ },
		"commit of another block hash": func(c *Commit) { c.BlockID.Hash = crypto.Checksum([]byte("other")) },
	}
	for name, mutate := range testCases {
		t.Run(name, func(t *testing.T) {
			c := makeCommit()
			mutate(c)
			require.ErrorIs(t, c.VerifyRandomBeacon(f.header.ChainID, f.valSet, &f.header), ErrInvalidCommitBeacon)
		})
	}

	t.Run("before consensus version 2 the header app hash is signed", func(t *testing.T) {
		c := makeCommit()
		c.BlockID.StateID = f.header.StateID().Hash()
		c.AppHash = nil
		appHash, err := c.VerifyAppHash(&f.header)
		require.NoError(t, err)
		require.EqualValues(t, f.header.AppHash, appHash)

		c.BlockID.StateID = RandStateID().Hash()
		_, err = c.VerifyAppHash(&f.header)
		require.Error(t, err)
	})

	t.Run("without random beacon fields", func(t *testing.T) {
		c := makeCommit()
		stripped := c.WithoutRandomBeacon()
		require.Nil(t, stripped.Beacon)
		require.Nil(t, stripped.AppHash)
		require.NotNil(t, c.Beacon, "the original commit is not changed")
		require.Same(t, stripped, stripped.WithoutRandomBeacon())
	})
}

// TestLightBlockRandomBeacon checks that a light block whose commit signs the app hash after the block's
// randomness validates, and that proofs are verified against that app hash.
func TestLightBlockRandomBeacon(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newRandomBeaconFixture(t)
	beacon := f.recoverBeacon(ctx, t)
	finalAppHash := crypto.Checksum(append(f.header.AppHash.Copy(), beacon...))
	commit := &Commit{
		Height: f.header.Height,
		Round:  randomBeaconTestRound,
		BlockID: BlockID{
			Hash:          f.blockID.Hash,
			PartSetHeader: f.blockID.PartSetHeader,
			StateID:       f.header.StateIDWithAppHash(finalAppHash).Hash(),
		},
		QuorumHash:              f.valSet.QuorumHash,
		ThresholdBlockSignature: crypto.CRandBytes(SignatureSize),
		Beacon:                  beacon,
		AppHash:                 finalAppHash,
	}
	lightBlock := LightBlock{
		SignedHeader: &SignedHeader{Header: &f.header, Commit: commit},
		ValidatorSet: f.valSet,
	}
	require.NoError(t, lightBlock.ValidateBasic(f.header.ChainID))
	appHash, err := lightBlock.CommittedAppHash()
	require.NoError(t, err)
	require.EqualValues(t, finalAppHash, appHash)

	// The header's app hash is not the one the commit signs.
	commit.AppHash = f.header.AppHash.Copy()
	require.Error(t, lightBlock.ValidateBasic(f.header.ChainID))
}

func TestBlockIDSameBlock(t *testing.T) {
	blockID := makeBlockIDRandom()
	other := blockID.Copy()
	other.StateID = RandStateID().Hash()
	assert.True(t, blockID.SameBlock(other))
	assert.False(t, blockID.Equals(other))

	other.PartSetHeader.Total++
	assert.False(t, blockID.SameBlock(other))
	other = blockID.Copy()
	other.Hash = crypto.Checksum([]byte("other"))
	assert.False(t, blockID.SameBlock(other))
}

func TestConsensusParamsConsensusVersion(t *testing.T) {
	for version, valid := range map[int32]bool{0: true, 1: true, 2: true, 3: false, -1: false} {
		params := DefaultConsensusParams()
		params.Version.ConsensusVersion = version
		err := params.ValidateConsensusParams()
		if valid {
			assert.NoError(t, err, "version %d", version)
		} else {
			assert.Error(t, err, "version %d", version)
		}
		assert.Equal(t, version >= 2, params.Version.RandomBeacon(), "version %d", version)
	}
}
