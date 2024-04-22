package proxy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/go-kit/kit/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	abcimocks "github.com/dashpay/tenderdash/abci/client/mocks"
	"github.com/dashpay/tenderdash/abci/example/kvstore"
	"github.com/dashpay/tenderdash/abci/server"
	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/abci/types/mocks"
	"github.com/dashpay/tenderdash/config"
	"github.com/dashpay/tenderdash/libs/log"
	tmrand "github.com/dashpay/tenderdash/libs/rand"
)

//----------------------------------------

type appConnTestI interface {
	Echo(context.Context, string) (*types.ResponseEcho, error)
	Flush(context.Context) error
	Info(context.Context, *types.RequestInfo) (*types.ResponseInfo, error)
}

type appConnTest struct {
	appConn abciclient.Client
}

func newAppConnTest(appConn abciclient.Client) appConnTestI {
	return &appConnTest{appConn}
}

func (app *appConnTest) Echo(ctx context.Context, msg string) (*types.ResponseEcho, error) {
	return app.appConn.Echo(ctx, msg)
}

func (app *appConnTest) Flush(ctx context.Context) error {
	return app.appConn.Flush(ctx)
}

func (app *appConnTest) Info(ctx context.Context, req *types.RequestInfo) (*types.ResponseInfo, error) {
	return app.appConn.Info(ctx, req)
}

//----------------------------------------

const SOCKET = "socket"

func TestEcho(t *testing.T) {
	sockPath := fmt.Sprintf("unix://%s/echo_%v.sock", t.TempDir(), tmrand.Str(6))
	logger := log.NewNopLogger()
	cfg := config.AbciConfig{Address: sockPath, Transport: SOCKET}
	client, err := abciclient.NewClient(logger, cfg, true)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start server

	app, err := kvstore.NewMemoryApp()
	require.NoError(t, err)

	s := server.NewSocketServer(logger.With("module", "abci-server"), sockPath, app)
	require.NoError(t, s.Start(ctx), "error starting socket server")
	t.Cleanup(func() { cancel(); s.Wait() })

	// Start client
	require.NoError(t, client.Start(ctx), "Error starting ABCI client")

	proxy := newAppConnTest(client)
	t.Log("Connected")

	for i := 0; i < 1000; i++ {
		_, err = proxy.Echo(ctx, fmt.Sprintf("echo-%v", i))
		if err != nil {
			t.Error(err)
		}
		// flush sometimes
		if i%128 == 0 {
			if err := proxy.Flush(ctx); err != nil {
				t.Error(err)
			}
		}
	}
	if err := proxy.Flush(ctx); err != nil {
		t.Error(err)
	}
}

func BenchmarkEcho(b *testing.B) {
	b.StopTimer() // Initialize
	sockPath := fmt.Sprintf("unix://%s/echo_%v.sock", b.TempDir(), tmrand.Str(6))
	logger := log.NewNopLogger()
	cfg := config.AbciConfig{Address: sockPath, Transport: SOCKET}
	client, err := abciclient.NewClient(logger, cfg, true)
	if err != nil {
		b.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start server
	app, err := kvstore.NewMemoryApp()
	require.NoError(b, err)

	s := server.NewSocketServer(logger.With("module", "abci-server"), sockPath, app)
	require.NoError(b, s.Start(ctx), "Error starting socket server")
	b.Cleanup(func() { cancel(); s.Wait() })

	// Start client
	require.NoError(b, client.Start(ctx), "Error starting ABCI client")

	proxy := newAppConnTest(client)
	b.Log("Connected")
	echoString := strings.Repeat(" ", 200)
	b.StartTimer() // Start benchmarking tests

	for i := 0; i < b.N; i++ {
		_, err = proxy.Echo(ctx, echoString)
		if err != nil {
			b.Error(err)
		}
		// flush sometimes
		if i%128 == 0 {
			if err := proxy.Flush(ctx); err != nil {
				b.Error(err)
			}
		}
	}
	if err := proxy.Flush(ctx); err != nil {
		b.Error(err)
	}

	b.StopTimer()
	// info := proxy.Info(types.RequestInfo{""})
	// b.Log("N: ", b.N, info)
}

func TestInfo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sockPath := fmt.Sprintf("unix://%s/echo_%v.sock", t.TempDir(), tmrand.Str(6))
	logger := log.NewNopLogger()
	cfg := config.AbciConfig{Address: sockPath, Transport: SOCKET}
	client, err := abciclient.NewClient(logger, cfg, true)
	require.NoError(t, err)

	// Start server
	app, err := kvstore.NewMemoryApp()
	require.NoError(t, err)

	s := server.NewSocketServer(logger.With("module", "abci-server"), sockPath, app)
	require.NoError(t, s.Start(ctx), "Error starting socket server")
	t.Cleanup(func() { cancel(); s.Wait() })

	// Start client
	require.NoError(t, client.Start(ctx), "Error starting ABCI client")

	proxy := newAppConnTest(client)
	t.Log("Connected")

	resInfo, err := proxy.Info(ctx, &RequestInfo)
	require.NoError(t, err)

	require.Equal(t, `{"appHash":"0000000000000000000000000000000000000000000000000000000000000000"}`, resInfo.Data, "Expected ResponseInfo with one element {\"appHash\":\"\"} but got something else")
}

type noopStoppableClientImpl struct {
	abciclient.Client
	count int
}

func (c *noopStoppableClientImpl) Stop() { c.count++ }

func TestAppConns_Start_Stop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientMock := &abcimocks.Client{}
	clientMock.On("Start", mock.Anything).Return(nil)
	clientMock.On("Error").Return(nil)
	clientMock.On("IsRunning").Return(true)
	clientMock.On("Wait").Return(nil).Times(1)
	cl := &noopStoppableClientImpl{Client: clientMock}

	appConns := New(cl, log.NewNopLogger(), NopMetrics())

	err := appConns.Start(ctx)
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	cancel()
	appConns.Wait()

	clientMock.AssertExpectations(t)
	assert.Equal(t, 1, cl.count)
}

// Upon failure, we call tmos.Kill
func TestAppConns_Failure(t *testing.T) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGABRT)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientMock := &abcimocks.Client{}
	clientMock.On("SetLogger", mock.Anything).Return()
	clientMock.On("Start", mock.Anything).Return(nil)
	clientMock.On("IsRunning").Return(true)
	clientMock.On("Wait").Return(nil)
	clientMock.On("Error").Return(errors.New("EOF"))
	cl := &noopStoppableClientImpl{Client: clientMock}

	appConns := New(cl, log.NewNopLogger(), NopMetrics())

	err := appConns.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { cancel(); appConns.Wait() })

	select {
	case sig := <-c:
		t.Logf("signal %q successfully received", sig)
	case <-ctx.Done():
		t.Fatal("expected process to receive SIGTERM signal")
	}
}
func TestFailureMetrics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewTestingLogger(t)
	mtr := mockMetrics()
	hst := mtr.MethodTiming.(*mockMetric)

	app := mocks.NewApplication(t)
	app.On("CheckTx", mock.Anything, mock.Anything).Return(&types.ResponseCheckTx{}, errors.New("some error")).Once()
	app.On("CheckTx", mock.Anything, mock.Anything).Return(&types.ResponseCheckTx{}, nil).Times(2)
	app.On("Info", mock.Anything, mock.Anything).Return(&types.ResponseInfo{}, nil)

	// promtest.ToFloat64(hst)
	cli := abciclient.NewLocalClient(logger, app)

	proxy := New(cli, log.NewNopLogger(), mtr)

	var err error
	for i := 0; i < 5; i++ {
		_, err = proxy.Info(ctx, &types.RequestInfo{})
		assert.NoError(t, err)
	}

	for i := 0; i < 3; i++ {
		_, err = proxy.CheckTx(ctx, &types.RequestCheckTx{})
		if i == 0 {
			assert.Error(t, err)
		} else {
			assert.NoError(t, err)
		}
	}

	cancel() // this should stop all clients
	proxy.Wait()
	assert.Equal(t, 5, hst.count["method=info,type=sync,success=true"])
	assert.Equal(t, 1, hst.count["method=check_tx,type=sync,success=false"])
	assert.Equal(t, 2, hst.count["method=check_tx,type=sync,success=true"])
}

func mockMetrics() *Metrics {
	return &Metrics{
		MethodTiming: &mockMetric{
			labels: []string{},
			count:  make(map[string]int),
			mtx:    &sync.Mutex{},
		},
	}
}

type mockMetric struct {
	labels []string
	/// count maps concatenated labels to the count of observations.
	count map[string]int
	mtx   *sync.Mutex
}

var _ = metrics.Histogram(&mockMetric{})

func (m *mockMetric) With(labelValues ...string) metrics.Histogram {
	m.mtx.Lock()
	defer m.mtx.Unlock()

	return &mockMetric{
		labels: append(m.labels, labelValues...),
		count:  m.count,
		mtx:    m.mtx, // pointer, as we use the same m.count
	}
}

func (m *mockMetric) Observe(_value float64) {
	m.mtx.Lock()
	defer m.mtx.Unlock()

	labels := ""
	for i, label := range m.labels {
		labels += label
		if i < len(m.labels)-1 {
			if i%2 == 0 {
				labels += "="
			} else {
				labels += ","
			}
		}
	}

	m.count[labels]++
}

func (m *mockMetric) String() (s string) {
	m.mtx.Lock()
	defer m.mtx.Unlock()

	for labels, total := range m.count {
		s += fmt.Sprintf("%s: %d\n", labels, total)
	}

	return s
}
