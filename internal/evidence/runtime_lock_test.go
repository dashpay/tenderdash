package evidence_test

import (
	"sync"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/evidence"
	"github.com/dashpay/tenderdash/internal/evidence/mocks"
	smmocks "github.com/dashpay/tenderdash/internal/state/mocks"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

func TestPoolStateReadableDuringBufferedEvidenceProcessing(t *testing.T) {
	for _, operation := range []string{"admission", "close"} {
		t.Run(operation, func(t *testing.T) {
			vals, priv := types.RandValidatorSet(1)
			state := createState(2, vals)
			blocks := &mocks.BlockStore{}
			blocks.On("Base").Return(int64(1))
			blocks.On("Height").Return(int64(100))
			blocks.On("LoadBlockMeta", int64(1)).Return(
				&types.BlockMeta{Header: types.Header{Time: defaultEvidenceTime}},
			)
			loading, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			states := &smmocks.Store{}
			states.On("LoadValidators", int64(1), blocks).
				Run(func(mock.Arguments) { close(loading); <-release }).Return(vals, nil).Once()
			pool := evidence.NewPool(log.NewNopLogger(), dbm.NewMemDB(), states, blocks,
				evidence.NopMetrics(), nil)
			require.NoError(t, pool.Start(state))
			defer pool.Close()
			ev, err := types.NewMockDuplicateVoteEvidenceWithValidator(t.Context(), 1,
				defaultEvidenceTime, priv[0], evidenceChainID, vals.QuorumType, vals.QuorumHash)
			require.NoError(t, err)
			pool.ReportConflictingVotes(ev.VoteA, ev.VoteB)

			var workers sync.WaitGroup
			defer workers.Wait()
			defer unblock()
			nextState := state
			nextState.LastBlockHeight++
			updated := make(chan struct{})
			workers.Add(1)
			go func() {
				defer workers.Done()
				pool.Update(t.Context(), nextState, nil)
				close(updated)
			}()
			select {
			case <-loading:
			case <-time.After(time.Second):
				t.Fatal("buffer processing did not reach validator loading")
			}

			attempted := make(chan struct{})
			finished := make(chan error, 1)
			workers.Add(1)
			go func() {
				defer workers.Done()
				close(attempted)
				if operation == "close" {
					finished <- pool.Close()
				} else {
					finished <- pool.AddEvidence(t.Context(), ev)
				}
			}()
			<-attempted
			select {
			case err := <-finished:
				t.Fatalf("%s bypassed the active update: %v", operation, err)
			case <-time.After(20 * time.Millisecond):
			}

			read := make(chan struct{})
			workers.Add(1)
			go func() {
				defer workers.Done()
				// Update publishes only after processing the old buffer.
				got := pool.State()
				if got.LastBlockHeight != state.LastBlockHeight || !pool.Enabled() {
					t.Error("state readers did not observe the published state")
				}
				close(read)
			}()
			select {
			case <-read:
			case <-time.After(time.Second):
				t.Fatal("state readers blocked on buffered evidence processing")
			}

			unblock()
			<-updated
			require.NoError(t, <-finished)
			require.Equal(t, nextState.LastBlockHeight, pool.State().LastBlockHeight)
			require.EqualValues(t, 1, pool.Size())
			states.AssertExpectations(t)
		})
	}
}
