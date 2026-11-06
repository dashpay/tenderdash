package p2p

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/stretchr/testify/require"

	dbm "github.com/dashpay/tenderdash/libs/db"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

// Dialing must stop for either reason a service context can finish.
func TestRouterDialPeersStopsOnContextEnd(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "canceled"
		if deadline {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			if deadline {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}
			cancel()
			wantErr := context.Canceled
			if deadline {
				wantErr = context.DeadlineExceeded
			}
			require.ErrorIs(t, ctx.Err(), wantErr)

			// DialNext returns the context error before accessing manager state.
			router := &Router{
				peerManager: &PeerManager{},
				options: RouterOptions{NumConcurrentDials: func() int {
					return 1
				}},
			}
			done := make(chan struct{})
			go func() {
				router.dialPeers(ctx)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("router dialing did not stop after its context ended")
			}
		})
	}
}

// A deadline must stop the workers started through the real service lifecycle.
func TestRouterDeadlineShutdownJoinsWorkers(t *testing.T) {
	defer leaktest.CheckTimeout(t, time.Second)()
	db := dbm.NewMemDB()
	defer db.Close()
	const nodeID types.NodeID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	logger := log.NewNopLogger()
	manager, err := NewPeerManager(context.Background(), nodeID, db, PeerManagerOptions{})
	require.NoError(t, err)
	transport := NewMemoryNetwork(logger, 1).CreateTransport(nodeID)
	endpoint, err := transport.Endpoint()
	require.NoError(t, err)
	dialing := make(chan struct{})
	var started sync.Once
	router, err := NewRouter(logger, NopMetrics(), nil, manager,
		func() *types.NodeInfo { return &types.NodeInfo{NodeID: nodeID} },
		transport, endpoint, RouterOptions{NumConcurrentDials: func() int {
			started.Do(func() { close(dialing) })
			return 1
		}})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, router.Start(ctx))
	select {
	case <-dialing:
	case <-ctx.Done():
		t.Fatal("router dial worker did not start before the deadline")
	}
	require.NoError(t, ctx.Err(), "router must start before the deadline expires")
	<-ctx.Done()
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	done := make(chan struct{})
	go func() {
		router.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("router Wait did not finish after its parent deadline")
	}
}
