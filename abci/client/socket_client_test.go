package abciclient

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/abci/server"
	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/abci/types/mocks"
	"github.com/dashpay/tenderdash/libs/log"
)

// TestSocketClientTimeout tests that the socket client times out correctly.
func TestSocketClientTimeout(t *testing.T) {
	const (
		Success              = 0
		FailDuringEnqueue    = 1
		FailDuringProcessing = 2

		baseTime = 10 * time.Millisecond
	)
	type testCase struct {
		name            string
		timeout         time.Duration
		enqueueSleep    time.Duration
		processingSleep time.Duration
		expect          int
	}
	testCases := []testCase{
		{name: "immediate", timeout: baseTime, enqueueSleep: 0, processingSleep: 0, expect: Success},
		{name: "small enqueue delay", timeout: 4 * baseTime, enqueueSleep: 1 * baseTime, processingSleep: 0, expect: Success},
		{name: "small processing delay", timeout: 4 * baseTime, enqueueSleep: 0, processingSleep: 1 * baseTime, expect: Success},
		{name: "within timeout", timeout: 4 * baseTime, enqueueSleep: 1 * baseTime, processingSleep: 1 * baseTime, expect: Success},
		{name: "timeout during enqueue", timeout: 3 * baseTime, enqueueSleep: 4 * baseTime, processingSleep: 1 * baseTime, expect: FailDuringEnqueue},
		{name: "timeout during processing", timeout: 4 * baseTime, enqueueSleep: 1 * baseTime, processingSleep: 4 * baseTime, expect: FailDuringProcessing},
	}

	logger := log.NewTestingLogger(t)

	for i, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			// wait until all threads end, otherwise we'll get data race in t.Log()
			wg := sync.WaitGroup{}
			defer wg.Wait()

			mainCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			socket := "unix://" + t.TempDir() + "/socket." + strconv.Itoa(i)

			checkTxExecuted := atomic.Bool{}

			app := mocks.NewApplication(t)
			app.On("Echo", mock.Anything, mock.Anything).Return(&types.ResponseEcho{}, nil).Maybe()
			app.On("Info", mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
				wg.Add(1)
				logger.Debug("Info before sleep")
				time.Sleep(tc.enqueueSleep)
				logger.Debug("Info after sleep")
				wg.Done()
			}).Return(&types.ResponseInfo{}, nil).Maybe()
			app.On("CheckTx", mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
				wg.Add(1)
				logger.Debug("CheckTx before sleep")
				checkTxExecuted.Store(true)
				time.Sleep(tc.processingSleep)
				logger.Debug("CheckTx after sleep")
				wg.Done()
			}).Return(&types.ResponseCheckTx{}, nil).Maybe()

			service, err := server.NewServer(logger, socket, "socket", app)
			require.NoError(t, err)
			svr := service.(*server.SocketServer)
			err = svr.Start(mainCtx)
			require.NoError(t, err)
			defer svr.Stop()

			cli := NewSocketClient(logger, socket, true).(*socketClient)

			err = cli.Start(mainCtx)
			require.NoError(t, err)
			defer cli.Stop()

			reqCtx, reqCancel := context.WithTimeout(context.Background(), tc.timeout)
			defer reqCancel()
			// Info is here just to block for some time, so we don't want to enforce timeout on it

			wg.Add(1)
			go func() {
				_, _ = cli.Info(mainCtx, &types.RequestInfo{})
				wg.Done()
			}()

			time.Sleep(1 * time.Millisecond) // ensure the goroutine has started

			_, err = cli.CheckTx(reqCtx, &types.RequestCheckTx{})
			switch tc.expect {
			case Success:
				require.NoError(t, err)
				require.True(t, checkTxExecuted.Load())
			case FailDuringEnqueue:
				require.Error(t, err)
				require.False(t, checkTxExecuted.Load())
			case FailDuringProcessing:
				require.Error(t, err)
				require.True(t, checkTxExecuted.Load())
			}
		})
	}
}
