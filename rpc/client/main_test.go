package client_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/abci/example/kvstore"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
	rpctest "github.com/dashpay/tenderdash/rpc/test"
)

func NodeSuite(ctx context.Context, t *testing.T, logger log.Logger) (service.Service, *config.Config) {
	t.Helper()

	ctx, cancel := context.WithCancel(ctx)

	conf, err := rpctest.CreateConfig(t, t.Name())
	require.NoError(t, err)

	app, err := kvstore.NewMemoryApp(kvstore.WithLogger(logger.With("module", "kvstore")))
	require.NoError(t, err)

	// start a tendermint node in the background to test against.
	node, closer, err := rpctest.StartTendermint(ctx, conf, app, rpctest.SuppressStdout)
	require.NoError(t, err)
	t.Cleanup(func() {
		cancel()
		assert.NoError(t, closer(ctx))
		assert.NoError(t, app.Close())
		node.Wait()
	})
	return node, conf
}
