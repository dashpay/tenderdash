package core

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbm "github.com/cometbft/cometbft-db"

	mempoolmocks "github.com/dashpay/tenderdash/internal/mempool/mocks"
	"github.com/dashpay/tenderdash/internal/state/indexer"
	kvsink "github.com/dashpay/tenderdash/internal/state/indexer/sink/kv"
	"github.com/dashpay/tenderdash/rpc/coretypes"
	"github.com/dashpay/tenderdash/types"
)

func TestGenesisChunked(t *testing.T) {
	env := &Environment{
		genChunks: []string{"chunk0", "chunk1", "chunk2"},
	}

	testCases := []struct {
		name    string
		chunk   int64
		wantErr bool
		wantNum int
	}{
		{name: "negative", chunk: -1, wantErr: true},
		{name: "first", chunk: 0, wantErr: false, wantNum: 0},
		{name: "last", chunk: 2, wantErr: false, wantNum: 2},
		{name: "past last", chunk: 3, wantErr: true},
	}

	ctx := context.Background()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := env.GenesisChunked(ctx, &coretypes.RequestGenesisChunked{
				Chunk: coretypes.Int64(tc.chunk),
			})
			if tc.wantErr {
				require.Error(t, err)
				// The error reports the number of chunks (not len-1) and the
				// invalid index that was requested.
				assert.Contains(t, err.Error(), "there are 3 chunks")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantNum, res.ChunkNumber)
			assert.Equal(t, len(env.genChunks), res.TotalChunks)
		})
	}
}

func TestBlockSearchQueryLength(t *testing.T) {
	sink := kvsink.NewEventSink(dbm.NewMemDB())
	env := &Environment{EventSinks: []indexer.EventSink{sink}}

	testCases := []struct {
		name      string
		query     string
		lengthErr bool
	}{
		{name: "at limit", query: strings.Repeat("a", maxQueryLength), lengthErr: false},
		{name: "over limit", query: strings.Repeat("a", maxQueryLength+1), lengthErr: true},
	}

	ctx := context.Background()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.BlockSearch(ctx, &coretypes.RequestBlockSearch{Query: tc.query})
			require.Error(t, err) // both cases error; only the cause differs
			if tc.lengthErr {
				assert.Contains(t, err.Error(), "maximum query length exceeded")
			} else {
				// A query at the limit passes the length gate and fails later
				// (query parse), but never with the length-cap message.
				assert.NotContains(t, err.Error(), "maximum query length exceeded")
			}
		})
	}
}

func TestUnconfirmedTxsShrinkingMempool(t *testing.T) {
	const (
		perPage = 10
		page    = 3
	)

	testCases := []struct {
		name      string
		size      int
		reaped    int // number of txs ReapMaxTxs returns
		wantCount int
	}{
		// Size reports 50 but the mempool shrinks so ReapMaxTxs returns fewer
		// than skipCount (=20) entries: the page is empty and must not fault.
		{name: "shrunk below skip count", size: 50, reaped: 5, wantCount: 0},
		// Normal pagination: a full reap yields the expected page slice.
		{name: "normal pagination", size: 50, reaped: 30, wantCount: 10},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mp := mempoolmocks.NewMempool(t)
			mp.On("Size").Return(tc.size)
			mp.On("ReapMaxTxs", perPage*page).Return(makeTxs(tc.reaped))
			mp.On("SizeBytes").Return(int64(0))

			env := &Environment{Mempool: mp}
			pg := coretypes.Int64(page)
			pp := coretypes.Int64(perPage)
			res, err := env.UnconfirmedTxs(context.Background(), &coretypes.RequestUnconfirmedTxs{
				Page:    &pg,
				PerPage: &pp,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.wantCount, res.Count)
			assert.Equal(t, tc.size, res.Total)
		})
	}
}

func makeTxs(n int) types.Txs {
	txs := make(types.Txs, n)
	for i := range txs {
		txs[i] = types.Tx{byte(i)}
	}
	return txs
}
