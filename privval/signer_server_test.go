package privval

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
)

type readObservedConn struct {
	net.Conn
	reading chan struct{}
	once    sync.Once
}

func (c *readObservedConn) Read(b []byte) (int, error) {
	c.once.Do(func() { close(c.reading) })
	return c.Conn.Read(b)
}

func TestSignerServerStopInterruptsIdleRead(t *testing.T) {
	conn, peer := net.Pipe()
	defer peer.Close()
	observed := &readObservedConn{Conn: conn, reading: make(chan struct{})}
	endpoint := NewSignerDialerEndpoint(log.NewNopLogger(), func() (net.Conn, error) {
		return observed, nil
	}, SignerDialerEndpointTimeoutReadWrite(time.Hour))
	server := NewSignerServer(endpoint, "test-chain", nil)
	require.NoError(t, server.Start(context.Background()))
	t.Cleanup(func() { _ = peer.Close(); server.Stop(); server.Wait() })
	<-observed.reading

	stopped := make(chan struct{})
	go func() { server.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		_ = peer.Close()
		<-stopped
		t.Fatal("Stop waited for the idle signer read deadline")
	}
	server.Wait()
	require.ErrorIs(t, conn.SetWriteDeadline(time.Now()), io.ErrClosedPipe)
}

func TestSignerServerStopClosesLateDial(t *testing.T) {
	conn, peer := net.Pipe()
	defer conn.Close()
	defer peer.Close()
	dialing, release := make(chan struct{}), make(chan struct{})
	endpoint := NewSignerDialerEndpoint(log.NewNopLogger(), func() (net.Conn, error) {
		close(dialing)
		<-release
		return conn, nil
	})
	server := NewSignerServer(endpoint, "test-chain", nil)
	require.NoError(t, server.Start(context.Background()))
	<-dialing
	server.Stop()
	close(release)
	server.Wait()
	require.ErrorIs(t, conn.SetWriteDeadline(time.Now()), io.ErrClosedPipe,
		"Stop must close a connection returned by an in-flight dial")
	require.False(t, endpoint.IsConnected())
}
