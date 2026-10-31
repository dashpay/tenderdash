package core

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/libs/log"
)

func newCloseTestClient(t *testing.T, handler http.HandlerFunc) (*RPCClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client, err := NewRPCClient(strings.TrimPrefix(srv.URL, "http://"), "user", "pass", log.NewNopLogger())
	require.NoError(t, err)
	return client, srv
}

// Close joins an idle client, repeatedly, and later requests fail instead of hanging.
func TestRPCClientCloseJoinsIdleClient(t *testing.T) {
	client, _ := newCloseTestClient(t, func(http.ResponseWriter, *http.Request) {})
	client.closeTimeout = 5 * time.Second

	require.NoError(t, client.Close())
	require.NoError(t, client.Close())
	require.Error(t, client.Ping())
}

// The endpoint cannot cancel a request in flight, so Close must give up on it.
func TestRPCClientCloseBoundedByStuckRequest(t *testing.T) {
	entered := make(chan struct{})
	unblock := make(chan struct{})
	release := sync.OnceFunc(func() { close(unblock) })
	client, _ := newCloseTestClient(t, func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-unblock
	})
	t.Cleanup(release) // runs before the server's cleanup, which joins the handler
	client.closeTimeout = 50 * time.Millisecond
	pinged := make(chan error, 1)
	go func() { pinged <- client.Ping() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach the server")
	}

	closed := make(chan error, 1)
	go func() { closed <- client.Close() }()
	select {
	case err := <-closed:
		require.ErrorContains(t, err, "still in flight")
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked on the stuck request")
	}

	release()
	require.Error(t, <-pinged)
	client.closeTimeout = 5 * time.Second
	require.NoError(t, client.Close(), "the dispatcher did not exit once the request ended")
}
