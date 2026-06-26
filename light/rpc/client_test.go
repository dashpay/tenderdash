package rpc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto/merkle"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/log"
	lcmock "github.com/dashpay/tenderdash/light/rpc/mocks"
	tmcrypto "github.com/dashpay/tenderdash/proto/tendermint/crypto"
	rpcclient "github.com/dashpay/tenderdash/rpc/client"
	rpcmock "github.com/dashpay/tenderdash/rpc/client/mocks"
	"github.com/dashpay/tenderdash/rpc/coretypes"
	"github.com/dashpay/tenderdash/types"
)

var errUnexpectedHeight = errors.New("light block not verified at this height")

// txResults builds a deterministic set of transaction results and its hash.
func txResults(t *testing.T) ([]*abci.ExecTxResult, tmbytes.HexBytes) {
	t.Helper()
	results := []*abci.ExecTxResult{
		{Code: 0, Data: []byte("first")},
		{Code: 1, Data: []byte("second")},
	}
	h, err := abci.TxResultsHash(results)
	require.NoError(t, err)
	return results, h
}

func headerLightBlock(height int64, appHash, resultsHash tmbytes.HexBytes) *types.LightBlock {
	return &types.LightBlock{
		SignedHeader: &types.SignedHeader{
			Header: &types.Header{Height: height, AppHash: appHash, ResultsHash: resultsHash},
		},
	}
}

// TestBlockResultsVerificationHeight verifies that BlockResults anchors the
// results hash to the trusted header at the same height (same-block execution),
// not the header at height+1.
func TestBlockResultsVerificationHeight(t *testing.T) {
	const height = int64(10)
	results, resultsHash := txResults(t)
	differentHash := tmbytes.HexBytes("a-different-results-hash")

	testCases := []struct {
		name string
		// answerHeight is the height the light client is configured to answer
		// for; if BlockResults queries any other height the mock returns an
		// error, so a successful run proves it anchored to answerHeight.
		answerHeight int64
		headerHash   tmbytes.HexBytes
		wantErr      bool
	}{
		{
			name:         "results verified against same-height header",
			answerHeight: height,
			headerHash:   resultsHash,
			wantErr:      false,
		},
		{
			name:         "results hash mismatch fails verification",
			answerHeight: height,
			headerHash:   differentHash,
			wantErr:      true,
		},
		{
			name:         "anchoring to height+1 header is not used",
			answerHeight: height + 1,
			headerHash:   resultsHash,
			wantErr:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			next := &rpcmock.Client{}
			next.On("BlockResults", mock.Anything, mock.Anything).
				Return(&coretypes.ResultBlockResults{Height: height, TxsResults: results}, nil)

			lc := &lcmock.LightClient{}
			lc.On("VerifyLightBlockAtHeight", mock.Anything, tc.answerHeight, mock.Anything).
				Return(headerLightBlock(tc.answerHeight, nil, tc.headerHash), nil)
			// Any other height is treated as "block not verified".
			lc.On("VerifyLightBlockAtHeight", mock.Anything, mock.Anything, mock.Anything).
				Return(nil, errUnexpectedHeight)

			c := NewClient(log.NewNopLogger(), next, lc)
			h := height
			_, err := c.BlockResults(ctx, &h)

			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				lc.AssertCalled(t, "VerifyLightBlockAtHeight", mock.Anything, height, mock.Anything)
			}
		})
	}
}

// TestABCIQueryVerificationHeight verifies that ABCIQueryWithOptions anchors the
// value proof to the trusted header at the response height (same-block
// execution), not the header at height+1.
func TestABCIQueryVerificationHeight(t *testing.T) {
	const respHeight = int64(7)
	appHash := tmbytes.HexBytes("app-hash-for-height-7")

	ctx := context.Background()

	next := &rpcmock.Client{}
	next.On("ABCIQueryWithOptions", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&coretypes.ResultABCIQuery{
			Response: abci.ResponseQuery{
				Code:   0,
				Key:    []byte("key"),
				Value:  []byte("value"),
				Height: respHeight,
				ProofOps: &tmcrypto.ProofOps{
					Ops: []tmcrypto.ProofOp{{Type: "noop", Key: []byte("key")}},
				},
			},
		}, nil)

	var queriedHeight int64
	lc := &lcmock.LightClient{}
	lc.On("VerifyLightBlockAtHeight", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			queriedHeight = args.Get(1).(int64)
		}).
		Return(headerLightBlock(respHeight, appHash, nil), nil)

	c := NewClient(log.NewNopLogger(), next, lc,
		KeyPathFn(func(_ string, key []byte) (merkle.KeyPath, error) {
			return merkle.KeyPath{}.AppendKey(key, merkle.KeyEncodingURL), nil
		}))

	// The stub proof will not verify, but by then the light client has already
	// been asked for the trusted header at a specific height, which is the
	// behavior under test.
	_, _ = c.ABCIQueryWithOptions(ctx, "/store/x/key", []byte("key"),
		rpcclient.ABCIQueryOptions{Height: respHeight, Prove: true})

	require.Equal(t, respHeight, queriedHeight,
		"value proof must be anchored to the header at the response height, not height+1")
}
