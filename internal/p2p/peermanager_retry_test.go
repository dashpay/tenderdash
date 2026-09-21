package p2p

import (
	"context"
	"math/rand"
	"strings"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/types"
)

type reconnectJitterSource struct {
	calls int
}

func (s *reconnectJitterSource) Int63() int64 {
	s.calls++
	if s.calls == 1 {
		return 0
	}
	return int64(100 * time.Millisecond)
}

func (*reconnectJitterSource) Seed(int64) {}

func TestPeerManager_DialNext_UpgradeRetryJitter(t *testing.T) {
	persistent := NodeAddress{Protocol: "memory", NodeID: types.NodeID(strings.Repeat("a", 40))}
	ordinary := NodeAddress{Protocol: "memory", NodeID: types.NodeID(strings.Repeat("b", 40))}
	self := types.NodeID(strings.Repeat("c", 40))
	manager, err := NewPeerManager(t.Context(), self, dbm.NewMemDB(), PeerManagerOptions{
		PersistentPeers:        []types.NodeID{persistent.NodeID},
		MaxConnected:           1,
		MaxOutgoingConnections: 1,
		MaxConnectedUpgrade:    1,
		MinRetryTime:           10 * time.Millisecond,
		RetryTimeJitter:        200 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	manager.rand = rand.New(&reconnectJitterSource{})
	_, err = manager.Add(ordinary)
	require.NoError(t, err)
	require.NoError(t, manager.Dialed(ordinary))
	_, err = manager.Add(persistent)
	require.NoError(t, err)
	require.Equal(t, persistent, manager.TryDialNext())
	<-manager.dialWaker.Sleep()
	require.NoError(t, manager.DialFailed(t.Context(), persistent))

	// Retry eligibility must be rechecked when jitter extends the initial delay.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	address, err := manager.DialNext(ctx)
	require.NoError(t, err)
	require.Equal(t, persistent, address)
}

func TestPeerManager_OutgoingUpgradeWakeOnDialed(t *testing.T) {
	ordinary, persistent := retryAddress("a"), retryAddress("b")
	manager := newRetryManager(t, PeerManagerOptions{
		PersistentPeers:        []types.NodeID{persistent.NodeID},
		MaxConnected:           2,
		MaxOutgoingConnections: 1,
		MaxConnectedUpgrade:    1,
	})
	_, err := manager.Add(ordinary)
	require.NoError(t, err)
	require.Equal(t, ordinary, manager.TryDialNext())
	_, err = manager.Add(persistent)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result := make(chan NodeAddress, 1)
	go func() {
		address, _ := manager.DialNext(ctx)
		result <- address
	}()
	waitForDialWake(t, manager)
	require.Empty(t, result, "pending ordinary dial has no replaceable connection yet")
	require.NoError(t, manager.Dialed(ordinary))
	select {
	case address := <-result:
		require.Equal(t, persistent, address)
	case <-ctx.Done():
		t.Fatal("successful dial must wake the blocked persistent upgrade")
	}
}

func TestPeerManager_DialNext_CancelRetry(t *testing.T) {
	address := retryAddress("a")
	manager := newRetryManager(t, PeerManagerOptions{MinRetryTime: time.Hour})
	_, err := manager.Add(address)
	require.NoError(t, err)
	require.Equal(t, address, manager.TryDialNext())
	require.NoError(t, manager.DialFailed(t.Context(), address))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := manager.DialNext(ctx)
		done <- err
	}()
	waitForDialWake(t, manager)
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("canceling a retry must stop the waiting dialer")
	}
	require.False(t, manager.IsDialingOrConnected(address.NodeID))
}

func TestPeerManager_DialNext_WakeDuringRetry(t *testing.T) {
	a, b := retryAddress("a"), retryAddress("b")
	manager := newRetryManager(t, PeerManagerOptions{MinRetryTime: time.Hour})
	_, err := manager.Add(a)
	require.NoError(t, err)
	require.Equal(t, a, manager.TryDialNext())
	require.NoError(t, manager.DialFailed(t.Context(), a))
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result := make(chan NodeAddress, 1)
	go func() {
		address, _ := manager.DialNext(ctx)
		result <- address
	}()
	waitForDialWake(t, manager)
	_, err = manager.Add(b)
	require.NoError(t, err)
	select {
	case address := <-result:
		require.Equal(t, b, address, "new peers must wake the dialer before the retry deadline")
	case <-ctx.Done():
		t.Fatal("dialer ignored newly available peer while retry timer was pending")
	}
}

func retryAddress(id string) NodeAddress {
	return NodeAddress{Protocol: "memory", NodeID: types.NodeID(strings.Repeat(id, 40))}
}

func newRetryManager(t *testing.T, options PeerManagerOptions) *PeerManager {
	t.Helper()
	manager, err := NewPeerManager(t.Context(), types.NodeID(strings.Repeat("c", 40)), dbm.NewMemDB(), options)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	return manager
}

func waitForDialWake(t *testing.T, manager *PeerManager) {
	t.Helper()
	// Add/DialFailed queued a wake; consuming it proves DialNext reached its wait.
	require.Eventually(t, func() bool { return len(manager.dialWaker.Sleep()) == 0 }, time.Second, time.Millisecond)
}
