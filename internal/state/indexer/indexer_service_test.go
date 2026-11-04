package indexer_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbm "github.com/dashpay/tenderdash/libs/db"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/internal/pubsub/query"
	"github.com/dashpay/tenderdash/internal/state/indexer"
	"github.com/dashpay/tenderdash/internal/state/indexer/sink/kv"
	tmlog "github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

func TestIndexerServiceIndexesBlocks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := tmlog.NewNopLogger()
	// event bus
	eventBus := eventbus.NewDefault(logger)
	err := eventBus.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(eventBus.Wait)

	assert.False(t, indexer.KVSinkEnabled([]indexer.EventSink{}))
	assert.False(t, indexer.IndexingEnabled([]indexer.EventSink{}))

	// event sink setup
	store := dbm.NewMemDB()
	eventSinks := []indexer.EventSink{kv.NewEventSink(store)}
	assert.True(t, indexer.KVSinkEnabled(eventSinks))
	assert.True(t, indexer.IndexingEnabled(eventSinks))

	service := indexer.NewService(indexer.ServiceArgs{
		Logger:   logger,
		Sinks:    eventSinks,
		EventBus: eventBus,
	})
	require.NoError(t, service.Start(ctx))
	t.Cleanup(service.Wait)

	// publish block with txs
	err = eventBus.PublishEventNewBlockHeader(types.EventDataNewBlockHeader{
		Header: types.Header{Height: 1},
		NumTxs: int64(2),
	})
	require.NoError(t, err)
	txResult1 := &abci.TxResult{
		Height: 1,
		Index:  uint32(0),
		Tx:     types.Tx("foo"),
		Result: abci.ExecTxResult{Code: 0},
	}
	err = eventBus.PublishEventTx(types.EventDataTx{TxResult: *txResult1})
	require.NoError(t, err)
	txResult2 := &abci.TxResult{
		Height: 1,
		Index:  uint32(1),
		Tx:     types.Tx("bar"),
		Result: abci.ExecTxResult{Code: 0},
	}
	err = eventBus.PublishEventTx(types.EventDataTx{TxResult: *txResult2})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		res, err := eventSinks[0].GetTxByHash(types.Tx("bar").Hash())
		return err == nil && res != nil
	}, 5*time.Second, 10*time.Millisecond)

	res, err := eventSinks[0].GetTxByHash(types.Tx("foo").Hash())
	require.NoError(t, err)
	require.Equal(t, txResult1, res)

	ok, err := eventSinks[0].HasBlock(1)
	require.NoError(t, err)
	require.True(t, ok)

	res, err = eventSinks[0].GetTxByHash(types.Tx("bar").Hash())
	require.NoError(t, err)
	require.Equal(t, txResult2, res)

	blocks, err := eventSinks[0].SearchBlockEvents(ctx, query.MustCompile("block.height = 1"))
	require.NoError(t, err)
	require.Equal(t, []int64{1}, blocks)
	txs, err := eventSinks[0].SearchTxEvents(ctx, query.MustCompile("tx.height = 1"))
	require.NoError(t, err)
	require.Len(t, txs, 2)

}

func TestTxIndexDuplicatedTx(t *testing.T) {
	var mockTx = types.Tx("MOCK_TX_HASH")

	testCases := []struct {
		name    string
		tx1     abci.TxResult
		tx2     abci.TxResult
		expSkip bool // do we expect the second tx to be skipped by tx indexer
	}{
		{"skip, previously successful",
			abci.TxResult{
				Height: 1,
				Index:  0,
				Tx:     mockTx,
				Result: abci.ExecTxResult{
					Code: abci.CodeTypeOK,
				},
			},
			abci.TxResult{
				Height: 2,
				Index:  0,
				Tx:     mockTx,
				Result: abci.ExecTxResult{
					Code: abci.CodeTypeOK + 1,
				},
			},
			true,
		},
		{"not skip, previously unsuccessful",
			abci.TxResult{
				Height: 1,
				Index:  0,
				Tx:     mockTx,
				Result: abci.ExecTxResult{
					Code: abci.CodeTypeOK + 1,
				},
			},
			abci.TxResult{
				Height: 2,
				Index:  0,
				Tx:     mockTx,
				Result: abci.ExecTxResult{
					Code: abci.CodeTypeOK + 1,
				},
			},
			false,
		},
		{"not skip, both successful",
			abci.TxResult{
				Height: 1,
				Index:  0,
				Tx:     mockTx,
				Result: abci.ExecTxResult{
					Code: abci.CodeTypeOK,
				},
			},
			abci.TxResult{
				Height: 2,
				Index:  0,
				Tx:     mockTx,
				Result: abci.ExecTxResult{
					Code: abci.CodeTypeOK,
				},
			},
			false,
		},
		{"not skip, both unsuccessful",
			abci.TxResult{
				Height: 1,
				Index:  0,
				Tx:     mockTx,
				Result: abci.ExecTxResult{
					Code: abci.CodeTypeOK + 1,
				},
			},
			abci.TxResult{
				Height: 2,
				Index:  0,
				Tx:     mockTx,
				Result: abci.ExecTxResult{
					Code: abci.CodeTypeOK + 1,
				},
			},
			false,
		},
		{"skip, same block, previously successful",
			abci.TxResult{
				Height: 1,
				Index:  0,
				Tx:     mockTx,
				Result: abci.ExecTxResult{
					Code: abci.CodeTypeOK,
				},
			},
			abci.TxResult{
				Height: 1,
				Index:  0,
				Tx:     mockTx,
				Result: abci.ExecTxResult{
					Code: abci.CodeTypeOK + 1,
				},
			},
			true,
		},
		{"not skip, same block, previously unsuccessful",
			abci.TxResult{
				Height: 1,
				Index:  0,
				Tx:     mockTx,
				Result: abci.ExecTxResult{
					Code: abci.CodeTypeOK + 1,
				},
			},
			abci.TxResult{
				Height: 1,
				Index:  0,
				Tx:     mockTx,
				Result: abci.ExecTxResult{
					Code: abci.CodeTypeOK,
				},
			},
			false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sink := kv.NewEventSink(dbm.NewMemDB())

			if tc.tx1.Height != tc.tx2.Height {
				// index the first tx
				err := sink.IndexTxEvents([]*abci.TxResult{&tc.tx1})
				require.NoError(t, err)

				// check if the second one should be skipped.
				ops, err := indexer.DeduplicateBatch([]*abci.TxResult{&tc.tx2}, sink)
				require.NoError(t, err)

				if tc.expSkip {
					require.Empty(t, ops)
				} else {
					require.Equal(t, []*abci.TxResult{&tc.tx2}, ops)
				}
			} else {
				// same block
				ops := []*abci.TxResult{&tc.tx1, &tc.tx2}
				ops, err := indexer.DeduplicateBatch(ops, sink)
				require.NoError(t, err)
				if tc.expSkip {
					// the second one is skipped
					require.Equal(t, []*abci.TxResult{&tc.tx1}, ops)
				} else {
					require.Equal(t, []*abci.TxResult{&tc.tx1, &tc.tx2}, ops)
				}
			}
		})
	}
}
