//go:build gofuzz || go1.18

package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/abci/example/kvstore"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/internal/mempool"
	"github.com/dashpay/tenderdash/libs/log"
)

func FuzzMempool(f *testing.F) {
	app, err := kvstore.NewMemoryApp()
	require.NoError(f, err)

	logger := log.NewNopLogger()
	conn := abciclient.NewLocalClient(logger, app)
	err = conn.Start(context.TODO())
	if err != nil {
		panic(err)
	}

	cfg := config.DefaultMempoolConfig()
	cfg.Broadcast = false

	mp := mempool.NewTxMempool(logger, cfg, conn)

	f.Fuzz(func(_ *testing.T, data []byte) {
		_ = mp.CheckTx(context.Background(), data, nil, mempool.TxInfo{})
	})
}
