package example

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/abci/example/code"
	"github.com/dashpay/tenderdash/abci/example/kvstore"
	abciserver "github.com/dashpay/tenderdash/abci/server"
	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/libs/log"
	tmnet "github.com/dashpay/tenderdash/libs/net"
	"github.com/dashpay/tenderdash/proto/tendermint/version"
)

func TestKVStore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := log.NewNopLogger()

	t.Log("### Testing KVStore")
	app, err := kvstore.NewMemoryApp(kvstore.WithLogger(logger), kvstore.WithAppVersion(0))
	require.NoError(t, err)
	testBulk(ctx, t, logger, app)
}

func TestBaseApp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := log.NewNopLogger()

	t.Log("### Testing BaseApp")
	testBulk(ctx, t, logger, types.NewBaseApplication())
}

func TestGRPC(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewNopLogger()

	t.Log("### Testing GRPC")
	testGRPCSync(ctx, t, logger, types.NewBaseApplication())
}

func testBulk(ctx context.Context, t *testing.T, logger log.Logger, app types.Application) {
	t.Helper()

	const numDeliverTxs = 700000
	socketFile := fmt.Sprintf("test-%08x.sock", rand.Int31n(1<<30))
	defer os.Remove(socketFile)
	socket := fmt.Sprintf("unix://%v", socketFile)
	// Start the listener
	server := abciserver.NewSocketServer(logger.With("module", "abci-server"), socket, app)
	t.Cleanup(server.Wait)
	err := server.Start(ctx)
	require.NoError(t, err)

	// Connect to the socket
	client := abciclient.NewSocketClient(logger.With("module", "abci-client"), socket, false)
	t.Cleanup(client.Wait)

	err = client.Start(ctx)
	require.NoError(t, err)

	// Construct request
	rpp := &types.RequestProcessProposal{
		Height:  1,
		Txs:     make([][]byte, numDeliverTxs),
		Version: &version.Consensus{App: 1},
	}
	for counter := 0; counter < numDeliverTxs; counter++ {
		rpp.Txs[counter] = []byte("test")
	}
	// Send bulk request
	res, err := client.ProcessProposal(ctx, rpp)
	require.NoError(t, err)
	require.Equal(t, numDeliverTxs, len(res.TxResults), "Number of txs doesn't match")
	for _, tx := range res.TxResults {
		require.Equal(t, tx.Code, code.CodeTypeOK, "Tx failed")
	}

	// Send final flush message
	err = client.Flush(ctx)
	require.NoError(t, err)
}

//-------------------------
// test grpc

func dialerFunc(_ctx context.Context, addr string) (net.Conn, error) {
	return tmnet.Connect(addr)
}

func testGRPCSync(ctx context.Context, t *testing.T, logger log.Logger, app types.Application) {
	t.Helper()
	numDeliverTxs := 680000
	socketFile := fmt.Sprintf("/tmp/test-%08x.sock", rand.Int31n(1<<30))
	defer os.Remove(socketFile)
	socket := fmt.Sprintf("unix://%v", socketFile)

	// Start the listener
	server := abciserver.NewGRPCServer(logger.With("module", "abci-server"), socket, app)

	require.NoError(t, server.Start(ctx))
	t.Cleanup(server.Wait)

	// Connect to the socket
	conn, err := grpc.NewClient(socket,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialerFunc),
	)
	require.NoError(t, err, "Error dialing GRPC server")

	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})

	client := types.NewABCIApplicationClient(conn)

	// Construct request
	rpp := types.RequestProcessProposal{Txs: make([][]byte, numDeliverTxs)}
	for counter := 0; counter < numDeliverTxs; counter++ {
		rpp.Txs[counter] = []byte("test")
	}

	// Send request
	response, err := client.ProcessProposal(ctx, &rpp)
	require.NoError(t, err, "Error in GRPC FinalizeBlock")
	require.Equal(t, numDeliverTxs, len(response.TxResults), "Number of txs returned via GRPC doesn't match")
	for _, tx := range response.TxResults {
		require.Equal(t, tx.Code, code.CodeTypeOK, "Tx failed")
	}
}
