package blocksync

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tendermint/tendermint/internal/state/mocks"
	statefactory "github.com/tendermint/tendermint/internal/state/test/factory"
	"github.com/tendermint/tendermint/internal/test/factory"
	tmrequire "github.com/tendermint/tendermint/internal/test/require"
	"github.com/tendermint/tendermint/types"
)

func TestBlockApplierApply(t *testing.T) {
	ctx := context.Background()
	mockBlockExec := mocks.NewExecutor(t)
	mockBlockStore := mocks.NewBlockStore(t)
	valSet, privVals := factory.MockValidatorSet()
	initialState := fakeInitialState(valSet)
	state := initialState.Copy()
	blocks := statefactory.MakeBlocks(ctx, t, 2, &state, privVals, 1)
	blockH1 := blocks[0]
	blockH1ID := blockH1.BlockID(nil)
	commitH1 := blocks[1].LastCommit
	blockH1Parts, err := blockH1.MakePartSet(types.BlockPartSizeBytes)
	require.NoError(t, err)

	testCases := []struct {
		block     *types.Block
		commit    *types.Commit
		mockFn    func()
		wantErr   string
		wantPanic bool
	}{
		{
			block:  blockH1,
			commit: commitH1,
			mockFn: func() {
				mockBlockStore.On("SaveBlock", blockH1, blockH1Parts, commitH1).Once()
				mockBlockExec.
					On("ValidateBlock", mock.Anything, initialState, blockH1).
					Once().
					Return(nil)
				mockBlockExec.
					On("ApplyBlock", mock.Anything, initialState, blockH1ID, blockH1, commitH1).
					Once().
					Return(state, nil)
			},
		},
		{
			block:  blockH1,
			commit: commitH1,
			mockFn: func() {
				mockBlockExec.
					On("ValidateBlock", mock.Anything, initialState, blockH1).
					Once().
					Return(errors.New("invalid block"))
			},
			wantErr: "invalid block",
		},
		{
			block:  blockH1,
			commit: commitH1,
			mockFn: func() {
				mockBlockStore.On("SaveBlock", blockH1, blockH1Parts, commitH1).Once()
				mockBlockExec.
					On("ValidateBlock", mock.Anything, initialState, blockH1).
					Once().
					Return(nil)
				mockBlockExec.
					On("ApplyBlock", mock.Anything, initialState, blockH1ID, blockH1, commitH1).
					Once().
					Return(state, errors.New("eeeeeeeee"))
			},
			wantPanic: true,
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			applier := newBlockApplier(mockBlockExec, mockBlockStore, applierWithState(initialState))
			if tc.mockFn != nil {
				tc.mockFn()
			}
			fn := func() {
				err := applier.Apply(ctx, tc.block, tc.commit)
				tmrequire.Error(t, tc.wantErr, err)
			}
			if tc.wantPanic {
				require.Panics(t, fn)
				return
			}
			fn()
		})
	}
}
