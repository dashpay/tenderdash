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
		collector: newRoundSkipCollector(log.NewNopLogger(), nil),
	}
	f.track(height, round)
	return f
}

func (f *collectorFixture) track(height int64, round int32) {
	f.trackVersion(height, round, tmproto.VersionParams_CONSENSUS_VERSION_2)
}

func (f *collectorFixture) trackVersion(height int64, round int32, version tmproto.VersionParams_ConsensusVersion) {
	f.collector.track(&StateData{
		RoundState: cstypes.RoundState{Height: height, Round: round, Validators: f.valSet},
		state: sm.State{
			ChainID:         collectorTestChainID,
			ConsensusParams: types.ConsensusParams{Version: types.VersionParams{ConsensusVersion: int32(version)}},
		},
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

	t.Run("should collect nothing on a chain before CONSENSUS_VERSION_2", func(t *testing.T) {
		f := newCollectorFixture(t, 9, 0)
		f.trackVersion(9, 0, tmproto.VersionParams_CONSENSUS_VERSION_1)
		forged := f.vote(t, 1, 9, 0)
		forged.ValidatorProTxHash = f.valSet.Validators[0].ProTxHash
		forged.ValidatorIndex = 0
		added, certificate, err := f.collector.add(forged, true)
		require.NoError(t, err, "a vote on such a chain is not even verified")
		assert.False(t, added)
		assert.Nil(t, certificate)
	})

	t.Run("should drop a vote the verification budget cannot afford", func(t *testing.T) {
		f := newCollectorFixture(t, 9, 0)
		budget := &tokenBudget{}
		f.collector.budget = budget
		forged := f.vote(t, 1, 9, 0)
		forged.ValidatorProTxHash = f.valSet.Validators[0].ProTxHash
		forged.ValidatorIndex = 0

		added, _, err := f.collector.add(forged, true)
		require.NoError(t, err, "a vote that was not verified cannot be refused as forged")
		assert.False(t, added)

		budget.tokens = baseMessageCost
		_, _, err = f.collector.add(forged, true)
		assert.ErrorIs(t, err, types.ErrRoundSkipVoteInvalidSignature)
		assert.Equal(t, baseMessageCost, budget.charged)
	})

	t.Run("should wait for the shares the signature needs when the threshold is lowered", func(t *testing.T) {
		f := newCollectorFixture(t, 9, 0)
		shares := f.valSet.QuorumTypeThresholdCount()
		require.Greater(t, shares, 1)
		// A chain may lower the power +2/3 needs below the power of the shares
		// the quorum's threshold signature is recovered from.
		f.valSet.VotingPowerThreshold = uint64(shares-1) * uint64(types.DefaultDashVotingPower)
		require.Less(t, f.valSet.QuorumVotingThresholdPower(), f.valSet.QuorumTypeThresholdVotingPower())

		for i := 0; i < shares-1; i++ {
			_, certificate, err := f.collector.add(f.vote(t, int32(i), 9, 0), true)
			require.NoError(t, err)
			assert.Nil(t, certificate, "certificate from %d shares, %d needed", i+1, shares)
		}
		_, certificate, err := f.collector.add(f.vote(t, int32(shares-1), 9, 0), true)
		require.NoError(t, err)
		require.NotNil(t, certificate)
		assert.NoError(t, certificate.Verify(collectorTestChainID, f.valSet))
	})

	t.Run("should try again after a recovery fails", func(t *testing.T) {
		f := newCollectorFixture(t, 9, 0)
		shares := f.valSet.QuorumTypeThresholdCount()
		// This validator's own votes are counted unverified, so a bad share can
		// only come from here; it is what makes recovery fail.
		bad := f.vote(t, 0, 9, 0)
		bad.Signature = f.vote(t, 1, 9, 0).Signature
		_, _, err := f.collector.add(bad, false)
		require.NoError(t, err)
		for i := 1; i < shares; i++ {
			_, certificate, err := f.collector.add(f.vote(t, int32(i), 9, 0), true)
			require.NoError(t, err)
			require.Nil(t, certificate)
		}
		f.collector.mtx.Lock()
		recovered := f.collector.rounds[0].recovered
		f.collector.mtx.Unlock()
		assert.False(t, recovered, "a failed recovery must leave the round open to the next vote")
	})

	t.Run("should retry with votes counted while a recovery ran", func(t *testing.T) {
		f := newCollectorFixture(t, 9, 0)
		for i := 0; i < 2; i++ {
			_, _, err := f.collector.add(f.vote(t, int32(i), 9, 0), true)
			require.NoError(t, err)
		}
		f.collector.mtx.Lock()
		votes := f.collector.rounds[0]
		// A recovery starts from the votes so far, and one more vote is
		// counted while it runs.
		votes.recovered = true
		collected := votes.collect()
		f.collector.mtx.Unlock()
		_, _, err := f.collector.add(f.vote(t, 2, 9, 0), true)
		require.NoError(t, err)

		f.collector.mtx.Lock()
		defer f.collector.mtx.Unlock()
		next, retry := f.collector.afterFailedRecoveryLocked(0, votes, collected)
		assert.True(t, retry, "a vote counted during a failed recovery must be tried")
		assert.Len(t, next, len(collected)+1)
		assert.True(t, votes.recovered, "the retry still holds the round")

		_, retry = f.collector.afterFailedRecoveryLocked(0, votes, next)
		assert.False(t, retry, "with nothing new, the round is left to the next vote")
		assert.False(t, votes.recovered)
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
