package conn

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
)

func TestMConnectionErrorCallbackRetainsParentContext(t *testing.T) {
	local, remote := net.Pipe()
	t.Cleanup(func() { _ = local.Close(); _ = remote.Close() })
	type observation struct {
		err     error
		running bool
	}
	observed := make(chan observation, 1)
	release := make(chan struct{})
	var connection *MConnection
	connection = createMConnectionWithCallbacks(log.NewNopLogger(), local, nil,
		func(ctx context.Context, _ interface{}) {
			observed <- observation{err: ctx.Err(), running: connection.IsRunning()}
			<-release
		})
	require.NoError(t, connection.Start(context.Background()))
	t.Cleanup(func() { connection.Stop(); connection.Wait() })
	defer close(release)
	require.NoError(t, remote.Close())
	select {
	case result := <-observed:
		require.NoError(t, result.err)
		require.False(t, result.running)
	case <-time.After(time.Second):
		t.Fatal("error callback did not run")
	}
}
