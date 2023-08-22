package mempool

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"

	abcitypes "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/internal/p2p"
	tmrequire "github.com/dashpay/tenderdash/internal/test/require"
	"github.com/dashpay/tenderdash/libs/log"
	protomem "github.com/dashpay/tenderdash/proto/tendermint/mempool"
	"github.com/dashpay/tenderdash/types"
)

func TestMempoolP2PMessageHandler(t *testing.T) {
	ctx := context.Background()
	logger := log.NewTestingLogger(t)
	peerID1 := types.NodeID("peer1")
	ids := NewMempoolIDs()
	ids.ReserveForPeer(peerID1)
	tx := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 0}
	mockTxChecker := newMockTxChecker(t)
	testCases := []struct {
		mockFn   func()
		envelope p2p.Envelope
		wantErr  string
	}{
		{
			envelope: p2p.Envelope{Message: &protomem.Txs{Txs: [][]byte{}}},
			wantErr:  "empty txs received from peer",
		},
		{
			envelope: p2p.Envelope{
				From:    peerID1,
				Message: &protomem.Txs{Txs: [][]byte{tx}},
			},
			mockFn: func() {
				mockTxChecker.
					On("CheckTx", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Once().
					Return(nil)
			},
		},
		{
			envelope: p2p.Envelope{
				From:    peerID1,
				Message: &protomem.Txs{Txs: [][]byte{tx}},
			},
			mockFn: func() {
				txInfoArg := func(txInfo TxInfo) bool {
					return txInfo.SenderNodeID == peerID1
				}
				mockTxChecker.
					On("CheckTx", mock.Anything, types.Tx(tx), mock.Anything, mock.MatchedBy(txInfoArg)).
					Once().
					Return(nil)
			},
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			if tc.mockFn != nil {
				tc.mockFn()
			}
			hd := mempoolP2PMessageHandler{
				logger:  logger,
				checker: mockTxChecker,
				ids:     ids,
			}
			err := hd.Handle(ctx, nil, &tc.envelope)
			tmrequire.Error(t, tc.wantErr, err)
		})
	}
}

type txChecker struct {
	mock.Mock
}

func newMockTxChecker(t *testing.T) *txChecker {
	m := &txChecker{}
	m.Mock.Test(t)
	t.Cleanup(func() { m.AssertExpectations(t) })
	return m
}

func (m *txChecker) CheckTx(ctx context.Context, tx types.Tx, callback func(*abcitypes.ResponseCheckTx), txInfo TxInfo) error {
	ret := m.Called(ctx, tx, callback, txInfo)
	if fn, ok := ret.Get(0).(func(context.Context, types.Tx, func(*abcitypes.ResponseCheckTx), TxInfo) error); ok {
		return fn(ctx, tx, callback, txInfo)
	}
	return ret.Error(0)
}
