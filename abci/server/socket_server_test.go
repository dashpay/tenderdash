package server

import (
	"bufio"
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/libs/log"
)

const (
	stalledQueries  = 32
	stalledRespSize = 1 << 20
)

// largeQueryApp answers every query with a large value and reports each call.
type largeQueryApp struct {
	types.BaseApplication
	served chan struct{}
}

func (a *largeQueryApp) Query(context.Context, *types.RequestQuery) (*types.ResponseQuery, error) {
	a.served <- struct{}{}
	return &types.ResponseQuery{Value: make([]byte, stalledRespSize)}, nil
}

// A client that stops reading responses leaves the response writer blocked
// on the socket while the request reader waits for the next request. Stop
// must still unblock both before Wait joins them.
func TestSocketServerStopUnblocksStalledConnection(t *testing.T) {
	addr := "unix://" + filepath.Join(t.TempDir(), "abci.sock")
	app := &largeQueryApp{served: make(chan struct{}, stalledQueries)}
	srv := NewSocketServer(log.NewNopLogger(), addr, app).(*SocketServer)
	require.NoError(t, srv.Start(context.Background()))

	conn, err := net.Dial("unix", srv.addr)
	require.NoError(t, err)
	defer conn.Close()
	// Responses (32 MiB) far exceed socket buffers; nothing is ever read back.
	w := bufio.NewWriter(conn)
	for i := 0; i < stalledQueries; i++ {
		require.NoError(t, types.WriteMessage(types.ToRequestQuery(&types.RequestQuery{}), w))
	}
	require.NoError(t, w.Flush())
	for i := 0; i < stalledQueries; i++ {
		select {
		case <-app.served:
		case <-time.After(5 * time.Second):
			t.Fatalf("server processed only %d of %d queries", i, stalledQueries)
		}
	}

	srv.Stop()
	stopped := make(chan struct{})
	go func() { srv.Wait(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		_ = conn.Close()
		<-stopped
		t.Fatal("Wait blocked on a connection with a stalled response write")
	}
}
