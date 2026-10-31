package abciclient_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	abciclient "github.com/dashpay/tenderdash/abci/client"
	"github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/libs/log"
)

var errChildStart = errors.New("child start failed")

type failingStartClient struct{ abciclient.Client }

func (failingStartClient) Start(context.Context) error { return errChildStart }
func (failingStartClient) IsRunning() bool             { return false }
func (failingStartClient) Wait()                       {}

// noStopClient satisfies Client while hiding Stop, which Client does not require.
type noStopClient struct{ abciclient.Client }

// A failed start must stop and join the children it started, and leave alone
// a client the caller had already started.
func TestRoutedClientFailedStartReleasesOnlyItsChildren(t *testing.T) {
	testCases := map[string]func(abciclient.Client) abciclient.Client{
		"child exposes Stop": func(c abciclient.Client) abciclient.Client { return c },
		"child hides Stop":   func(c abciclient.Client) abciclient.Client { return noStopClient{c} },
	}
	for name, wrap := range testCases {
		t.Run(name, func(t *testing.T) {
			logger := log.NewNopLogger()
			callerCtx, cancelCaller := context.WithCancel(context.Background())
			defer cancelCaller()
			callerOwned := abciclient.NewLocalClient(logger, types.NewBaseApplication())
			require.NoError(t, callerOwned.Start(callerCtx))
			attemptOwned := abciclient.NewLocalClient(logger, types.NewBaseApplication())
			routing := abciclient.Routing{
				"Info":  {{Client: callerOwned, ClientID: "caller"}},
				"Query": {{Client: wrap(attemptOwned), ClientID: "attempt"}},
			}
			routed, err := abciclient.NewRoutedClient(logger, failingStartClient{}, routing)
			require.NoError(t, err)

			started := make(chan error, 1)
			go func() { started <- routed.Start(context.Background()) }()
			select {
			case err := <-started:
				require.ErrorIs(t, err, errChildStart)
			case <-time.After(5 * time.Second):
				t.Fatal("failed start blocked joining a client")
			}
			require.False(t, attemptOwned.IsRunning(), "child started by the failed attempt kept running")
			joined := make(chan struct{})
			go func() { attemptOwned.Wait(); close(joined) }()
			select {
			case <-joined:
			case <-time.After(5 * time.Second):
				t.Fatal("child started by the failed attempt was not joined")
			}
			require.True(t, callerOwned.IsRunning(), "failed start stopped a caller-owned client")
		})
	}
}
