package conn

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
)

func TestMConnectionWaitDrainsCallback(t *testing.T) {
	local, remote := net.Pipe()
	entered, release := make(chan struct{}), make(chan struct{})
	receiver := createMConnectionWithCallbacks(log.NewNopLogger(), local,
		func(context.Context, ChannelID, []byte) { close(entered); <-release }, nil)
	sender := createTestMConnection(log.NewNopLogger(), remote)
	require.NoError(t, receiver.Start(context.Background()))
	require.NoError(t, sender.Start(context.Background()))
	t.Cleanup(func() { sender.Stop(); sender.Wait(); receiver.Stop(); receiver.Wait() })
	defer close(release)
	require.True(t, sender.Send(1, []byte("drain")))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("receive callback did not start")
	}
	receiver.Stop()
	done := make(chan struct{})
	go func() { receiver.Wait(); close(done) }()
	select {
	case <-done:
		t.Fatal("Wait returned while the receive callback was still running")
	case <-time.After(30 * time.Millisecond):
	}
}

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
	done := make(chan struct{})
	go func() { connection.Wait(); close(done) }()
	select {
	case <-done:
		t.Fatal("Wait returned before the error callback finished")
	case <-time.After(30 * time.Millisecond):
	}
}
