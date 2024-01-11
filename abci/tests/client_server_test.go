package tests

import (
	"context"
	"testing"

	"github.com/fortytw2/leaktest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	abciclientent "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/abci/example/kvstore"
	abciserver "github.com/dashpay/tenderdash/abci/server"
	"github.com/dashpay/tenderdash/libs/log"
)

func TestClientServerNoAddrPrefix(t *testing.T) {
	t.Cleanup(leaktest.Check(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		addr      = "localhost:26658"
		transport = "socket"
	)
	app, err := kvstore.NewMemoryApp()
	require.NoError(t, err)
	logger := log.NewTestingLogger(t)

	server, err := abciserver.NewServer(logger, addr, transport, app)
	assert.NoError(t, err, "expected no error on NewServer")
	err = server.Start(ctx)
	assert.NoError(t, err, "expected no error on server.Start")
	t.Cleanup(server.Wait)

	client, err := abciclientent.NewClient(logger, addr, transport, true, nil)
	assert.NoError(t, err, "expected no error on NewClient")
	err = client.Start(ctx)
	assert.NoError(t, err, "expected no error on client.Start")
	t.Cleanup(client.Wait)
}
