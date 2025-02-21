package consensus

import (
	"context"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/internal/libs/clist"
	"github.com/dashpay/tenderdash/internal/mempool"
	"github.com/dashpay/tenderdash/internal/proxy"
	"github.com/dashpay/tenderdash/libs/log"
	tmstate "github.com/dashpay/tenderdash/proto/tendermint/state"
	"github.com/dashpay/tenderdash/types"
)

//-----------------------------------------------------------------------------

type emptyMempool struct{}

var _ mempool.Mempool = emptyMempool{}

func (emptyMempool) Lock()     {}
func (emptyMempool) Unlock()   {}
func (emptyMempool) Size() int { return 0 }
func (emptyMempool) CheckTx(context.Context, types.Tx, func(*abci.ResponseCheckTx), mempool.TxInfo) error {
	return nil
}
func (emptyMempool) RemoveTxByKey(_txKey types.TxKey) error  { return nil }
func (emptyMempool) ReapMaxBytesMaxGas(_, _ int64) types.Txs { return types.Txs{} }
func (emptyMempool) ReapMaxTxs(_n int) types.Txs             { return types.Txs{} }
func (emptyMempool) Update(
	_ context.Context,
	_ int64,
	_ types.Txs,
	_ []*abci.ExecTxResult,
	_ mempool.PreCheckFunc,
	_ mempool.PostCheckFunc,
	_ bool,
) error {
	return nil
}
func (emptyMempool) GetTxByHash(_ types.TxKey) types.Tx      { return nil }
func (emptyMempool) Flush()                                  {}
func (emptyMempool) FlushAppConn(_ctx context.Context) error { return nil }
func (emptyMempool) TxsAvailable() <-chan struct{}           { return make(chan struct{}) }
func (emptyMempool) EnableTxsAvailable()                     {}
func (emptyMempool) SizeBytes() int64                        { return 0 }

func (emptyMempool) TxsFront() *clist.CElement    { return nil }
func (emptyMempool) TxsWaitChan() <-chan struct{} { return nil }

func (emptyMempool) InitWAL() error { return nil }
func (emptyMempool) CloseWAL()      {}

type mockMempool struct {
	emptyMempool
	calls []types.Txs
}

func (m *mockMempool) ReapMaxBytesMaxGas(_, _ int64) types.Txs {
	if len(m.calls) == 0 {
		return types.Txs{}
	}
	txs := m.calls[0]
	return txs
}

func (m *mockMempool) Update(
	_ context.Context,
	_ int64,
	_ types.Txs,
	_ []*abci.ExecTxResult,
	_ mempool.PreCheckFunc,
	_ mempool.PostCheckFunc,
	_ bool,
) error {
	if len(m.calls) > 0 {
		m.calls = m.calls[1:]
	}
	return nil
}

//-----------------------------------------------------------------------------
// mockProxyApp uses Responses to FinalizeBlock to give the right results.
//
// Useful because we don't want to call Commit() twice for the same block on
// the real app.

func newMockProxyApp(
	logger log.Logger,
	appHash []byte,
	abciResponses *tmstate.ABCIResponses,
) (abciclient.Client, error) {
	return proxy.New(abciclient.NewLocalClient(logger, &mockProxyApp{
		appHash:       appHash,
		abciResponses: abciResponses,
	}), logger, proxy.NopMetrics()), nil
}

type mockProxyApp struct {
	abci.BaseApplication

	appHash       []byte
	txCount       int
	abciResponses *tmstate.ABCIResponses
}

func (mock *mockProxyApp) ProcessProposal(_ context.Context, _req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
	r := mock.abciResponses.ProcessProposal
	if r == nil {
		return &abci.ResponseProcessProposal{}, nil
	}
	return r, nil
}

func (mock *mockProxyApp) FinalizeBlock(_ context.Context, _req *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	mock.txCount++

	return &abci.ResponseFinalizeBlock{}, nil
}
