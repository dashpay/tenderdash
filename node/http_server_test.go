package node

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/libs/service"
)

type httpServerOwner struct {
	start func(context.Context)
}

func (o *httpServerOwner) OnStart(ctx context.Context) error { o.start(ctx); return nil }
func (*httpServerOwner) OnStop()                             {}

// A client that never finishes its request (e.g. a long pprof profile) must not
// block the owner's shutdown, and Wait must still join the handler.
func TestOwnedHTTPServerStopEvictsStalledClient(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	entered, handlerDone := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	srv := &http.Server{
		ReadHeaderTimeout: httpReadHeaderTimeout,
		Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			defer close(handlerDone)
			close(entered)
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}),
	}
	owner := &httpServerOwner{}
	bs := service.NewBaseService(log.NewNopLogger(), "http-owner", owner)
	owner.start = func(ctx context.Context) { serveOwnedHTTP(ctx, bs, srv, ln) }
	require.NoError(t, bs.Start(context.Background()))

	client, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer client.Close()
	_, err = io.WriteString(client, "GET /stalled HTTP/1.1\r\nHost: localhost\r\n\r\n")
	require.NoError(t, err)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler was not entered")
	}

	bs.Stop()
	stopped := make(chan struct{})
	go func() { bs.Wait(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		close(release)
		<-stopped
		t.Fatal("a stalled client blocked HTTP server shutdown")
	}
	select {
	case <-handlerDone:
	default:
		t.Fatal("Wait returned before the active handler finished")
	}
}
