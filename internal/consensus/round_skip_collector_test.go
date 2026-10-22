package consensus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cstypes "github.com/dashpay/tenderdash/internal/consensus/types"
	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

const collectorTestChainID = "collector-test-chain"

type collectorFixture struct {
	valSet    *types.ValidatorSet
	privVals  map[string]types.PrivValidator
	collector *roundSkipCollector
}

func newCollectorFixture(t *testing.T, height int64, round int32) *collectorFixture {
	t.Helper()
	valSet, pvs := types.RandValidatorSet(4)
	privVals := make(map[string]types.PrivValidator, len(pvs))
	for _, pv := range pvs {
		proTxHash, err := pv.GetProTxHash(context.Background())
		require.NoError(t, err)
		privVals[proTxHash.String()] = pv
	}
	f := &collectorFixture{
		valSet:    valSet,
		privVals:  privVals,
		collector: newRoundSkipCollector(log.NewNopLogger()),
	}
	f.track(height, round)
	return f
}

func (f *collectorFixture) track(height int64, round int32) {
	f.collector.track(&StateData{
		RoundState: cstypes.RoundState{Height: height, Round: round, Validators: f.valSet},
		state:      sm.State{ChainID: collectorTestChainID},
	})
}

// vote returns the skip vote of the validator at index, for height and round.
func (f *collectorFixture) vote(t *testing.T, index int32, height int64, round int32) *types.RoundSkipVote {
	t.Helper()
	val := f.valSet.Validators[index]
	pb := &tmproto.RoundSkipVote{
		Height:             height,
		Round:              round,
		ValidatorProTxHash: val.ProTxHash,
		ValidatorIndex:     index,
	}
	require.NoError(t, f.privVals[val.ProTxHash.String()].SignRoundSkipVote(
		context.Background(), collectorTestChainID, f.valSet.QuorumType, f.valSet.QuorumHash, pb))
	vote, err := types.RoundSkipVoteFromProto(pb)
	require.NoError(t, err)
	return vote
}

func TestRoundSkipCollector(t *testing.T) {
	t.Run("should return the certificate once, with the vote that completes +2/3", func(t *testing.T) {
		f := newCollectorFixture(t, 9, 1)
		threshold := f.valSet.QuorumTypeThresholdCount()

		for i := 0; i < threshold-1; i++ {
			added, certificate, err := f.collector.add(f.vote(t, int32(i), 9, 1), true)
			require.NoError(t, err)
			assert.True(t, added)
			assert.Nil(t, certificate, "certificate after %d votes", i+1)
		}
		added, certificate, err := f.collector.add(f.vote(t, int32(threshold-1), 9, 1), true)
		require.NoError(t, err)
		assert.True(t, added)
		require.NotNil(t, certificate)
		assert.Equal(t, int32(1), certificate.Round)
		assert.NoError(t, certificate.Verify(collectorTestChainID, f.valSet))

		added, certificate, err = f.collector.add(f.vote(t, int32(threshold), 9, 1), true)
		require.NoError(t, err)
		assert.True(t, added, "a vote past +2/3 is still new and relayed")
		assert.Nil(t, certificate, "the certificate is recovered only once")
	})

	t.Run("should relay a vote only the first time", func(t *testing.T) {
		f := newCollectorFixture(t, 9, 0)
		vote := f.vote(t, 0, 9, 0)
		added, _, err := f.collector.add(vote, true)
		require.NoError(t, err)
		assert.True(t, added)
		added, _, err = f.collector.add(vote, true)
		require.NoError(t, err)
		assert.False(t, added)
	})

	t.Run("should refuse a vote whose index names another validator", func(t *testing.T) {
		f := newCollectorFixture(t, 9, 0)
		vote := f.vote(t, 0, 9, 0)
		vote.ValidatorIndex = 1
		added, _, err := f.collector.add(vote, true)
		assert.ErrorIs(t, err, errRoundSkipVoteWrongValidator)
		assert.False(t, added)
	})

	t.Run("should refuse a vote signed by another validator", func(t *testing.T) {
		f := newCollectorFixture(t, 9, 0)
		forged := f.vote(t, 1, 9, 0)
		forged.ValidatorProTxHash = f.valSet.Validators[0].ProTxHash
		forged.ValidatorIndex = 0
		added, _, err := f.collector.add(forged, true)
		assert.ErrorIs(t, err, types.ErrRoundSkipVoteInvalidSignature)
		assert.False(t, added)
	})

	t.Run("should ignore votes for another height, a passed round or a round too far ahead", func(t *testing.T) {
		f := newCollectorFixture(t, 9, 2)
		for name, vote := range map[string]*types.RoundSkipVote{
			"other height": f.vote(t, 0, 10, 2),
			"passed round": f.vote(t, 0, 9, 1),
			"far round":    f.vote(t, 0, 9, 2+maxRoundSkipRoundsAhead+1),
		} {
			added, certificate, err := f.collector.add(vote, true)
			assert.NoError(t, err, name)
			assert.False(t, added, name)
			assert.Nil(t, certificate, name)
		}
		added, _, err := f.collector.add(f.vote(t, 0, 9, 2+maxRoundSkipRoundsAhead), true)
		require.NoError(t, err)
		assert.True(t, added, "a vote a few rounds ahead is collected")
	})

	t.Run("should forget the votes of a height it has left", func(t *testing.T) {
		f := newCollectorFixture(t, 9, 0)
		vote := f.vote(t, 0, 9, 0)
		added, _, err := f.collector.add(vote, true)
		require.NoError(t, err)
		require.True(t, added)

		f.track(10, 0)
		added, _, err = f.collector.add(vote, true)
		require.NoError(t, err)
		assert.False(t, added)
		added, _, err = f.collector.add(f.vote(t, 0, 10, 0), true)
		require.NoError(t, err)
		assert.True(t, added)
	})
}
