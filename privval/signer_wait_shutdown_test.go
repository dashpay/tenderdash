package privval

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
)

type waitObservedContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *waitObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestListenerStopInterruptsWaitForConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	endpoint := NewSignerListenerEndpoint(log.NewNopLogger(), listener,
		SignerListenerEndpointTimeoutReadWrite(time.Hour))
	require.NoError(t, endpoint.Start(context.Background()))
	ctx, cancel := context.WithCancel(context.Background())
	observed := &waitObservedContext{Context: ctx, waiting: make(chan struct{})}
	t.Cleanup(func() { cancel(); endpoint.Stop(); endpoint.Wait() })
	waited := make(chan error, 1)
	go func() { waited <- endpoint.WaitForConnection(observed, time.Hour) }()
	// Done is evaluated by WaitConnection while both endpoint locks are held.
	<-observed.waiting

	stopped := make(chan struct{})
	go func() { endpoint.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		cancel()
		<-stopped
		require.Error(t, <-waited)
		t.Fatal("Stop waited for WaitForConnection's caller context")
	}
	require.ErrorIs(t, <-waited, ErrEndpointClosed)
	endpoint.Wait()
}
