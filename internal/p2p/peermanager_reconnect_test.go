package p2p_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/internal/p2p"
	"github.com/dashpay/tenderdash/types"
)

func reconnectAddress(id string) p2p.NodeAddress {
	return p2p.NodeAddress{Protocol: "memory", NodeID: types.NodeID(strings.Repeat(id, 40))}
}

func reconnectManager(t *testing.T, options p2p.PeerManagerOptions) *p2p.PeerManager {
	t.Helper()
	manager, err := p2p.NewPeerManager(t.Context(), selfID, dbm.NewMemDB(), options)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	return manager
}

func addReconnectPeer(t *testing.T, manager *p2p.PeerManager, address p2p.NodeAddress) {
	t.Helper()
	added, err := manager.Add(address)
	require.NoError(t, err)
	require.True(t, added)
}

func TestPeerManager_PersistentReconnect(t *testing.T) {
	for _, tc := range []struct {
		name     string
		total    uint16
		outgoing uint16
	}{
		{"both_limits", 1, 1},
		{"outgoing_only", 3, 1},
		{"unlimited_outgoing", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			persistent, ordinary := reconnectAddress("a"), reconnectAddress("b")
			manager := reconnectManager(t, p2p.PeerManagerOptions{
				PersistentPeers:          []types.NodeID{persistent.NodeID},
				MaxConnected:             tc.total,
				MaxOutgoingConnections:   tc.outgoing,
				MaxConnectedUpgrade:      1,
				DisconnectCooldownPeriod: 50 * time.Millisecond,
			})
			addReconnectPeer(t, manager, persistent)
			require.Equal(t, persistent, manager.TryDialNext())
			require.NoError(t, manager.Dialed(persistent))
			manager.Disconnected(t.Context(), persistent.NodeID)
			addReconnectPeer(t, manager, ordinary)
			require.NoError(t, manager.Dialed(ordinary))

			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			address, err := manager.DialNext(ctx)
			require.NoError(t, err, "cooldown must wake the blocked dialer at capacity")
			require.Equal(t, persistent, address)
			require.NoError(t, manager.Dialed(address))
			victim, err := manager.TryEvictNext()
			require.NoError(t, err)
			require.Equal(t, ordinary.NodeID, victim)
			manager.Disconnected(t.Context(), victim)
			require.Zero(t, manager.TryDialNext())
		})
	}
}

func TestPeerManager_OutgoingUpgradeLimits(t *testing.T) {
	a, b, c := reconnectAddress("a"), reconnectAddress("b"), reconnectAddress("c")
	for _, tc := range []struct {
		name       string
		upgrades   uint16
		persistent []types.NodeID
		dialing    bool
	}{
		{"disabled", 0, []types.NodeID{b.NodeID}, false},
		{"ordinary_peer", 1, nil, false},
		{"all_reserved", 1, []types.NodeID{a.NodeID, b.NodeID}, false},
		{"pending_ordinary_dial", 1, nil, true},
		{"pending_reserved_dial", 1, []types.NodeID{b.NodeID}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := reconnectManager(t, p2p.PeerManagerOptions{
				PersistentPeers:        tc.persistent,
				MaxConnected:           3,
				MaxOutgoingConnections: 1,
				MaxConnectedUpgrade:    tc.upgrades,
				PeerScores:             map[types.NodeID]p2p.PeerScore{b.NodeID: 2},
			})
			addReconnectPeer(t, manager, a)
			require.Equal(t, a, manager.TryDialNext())
			if !tc.dialing {
				require.NoError(t, manager.Dialed(a))
			}
			addReconnectPeer(t, manager, b)
			addReconnectPeer(t, manager, c)
			require.Zero(t, manager.TryDialNext())
		})
	}
}

func TestPeerManager_OutgoingUpgradeVictim(t *testing.T) {
	persistent, outgoing, incoming := reconnectAddress("a"), reconnectAddress("b"), reconnectAddress("c")
	manager := reconnectManager(t, p2p.PeerManagerOptions{
		PersistentPeers:        []types.NodeID{persistent.NodeID},
		MaxConnected:           3,
		MaxOutgoingConnections: 1,
		MaxConnectedUpgrade:    1,
		PeerScores: map[types.NodeID]p2p.PeerScore{
			outgoing.NodeID: 2, incoming.NodeID: 1,
		},
	})
	addReconnectPeer(t, manager, outgoing)
	require.NoError(t, manager.Dialed(outgoing))
	addReconnectPeer(t, manager, incoming)
	require.NoError(t, manager.Accepted(incoming.NodeID))
	addReconnectPeer(t, manager, persistent)
	require.Equal(t, persistent, manager.TryDialNext())
	require.NoError(t, manager.Dialed(persistent))
	victim, err := manager.TryEvictNext()
	require.NoError(t, err)
	require.Equal(t, outgoing.NodeID, victim, "an incoming victim would not free outgoing capacity")
}

func TestPeerManager_OutgoingUpgradeFailureRetry(t *testing.T) {
	persistent, ordinary := reconnectAddress("a"), reconnectAddress("b")
	manager := reconnectManager(t, p2p.PeerManagerOptions{
		PersistentPeers:        []types.NodeID{persistent.NodeID},
		MaxConnected:           1,
		MaxOutgoingConnections: 1,
		MaxConnectedUpgrade:    1,
		MinRetryTime:           20 * time.Millisecond,
		MaxRetryTime:           20 * time.Millisecond,
	})
	addReconnectPeer(t, manager, ordinary)
	require.NoError(t, manager.Dialed(ordinary))
	addReconnectPeer(t, manager, persistent)
	require.Equal(t, persistent, manager.TryDialNext())
	require.NoError(t, manager.DialFailed(t.Context(), persistent))
	victim, err := manager.TryEvictNext()
	require.NoError(t, err)
	require.Empty(t, victim, "failed probes must keep the working connection")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	address, err := manager.DialNext(ctx)
	require.NoError(t, err)
	require.Equal(t, persistent, address)
	require.NoError(t, manager.Dialed(address))
	victim, err = manager.TryEvictNext()
	require.NoError(t, err)
	require.Equal(t, ordinary.NodeID, victim)
}

func TestPeerManager_OutgoingUpgradeVictimDisconnected(t *testing.T) {
	persistent, ordinary := reconnectAddress("a"), reconnectAddress("b")
	manager := reconnectManager(t, p2p.PeerManagerOptions{
		PersistentPeers:        []types.NodeID{persistent.NodeID},
		MaxConnected:           3,
		MaxOutgoingConnections: 1,
		MaxConnectedUpgrade:    1,
	})
	addReconnectPeer(t, manager, ordinary)
	require.NoError(t, manager.Dialed(ordinary))
	addReconnectPeer(t, manager, persistent)
	require.Equal(t, persistent, manager.TryDialNext())
	manager.Disconnected(t.Context(), ordinary.NodeID)
	require.NoError(t, manager.Dialed(persistent))
	victim, err := manager.TryEvictNext()
	require.NoError(t, err)
	require.Empty(t, victim)
}

func TestPeerManager_OutgoingUpgradeConcurrent(t *testing.T) {
	a, b, c := reconnectAddress("a"), reconnectAddress("b"), reconnectAddress("c")
	manager := reconnectManager(t, p2p.PeerManagerOptions{
		PersistentPeers:        []types.NodeID{b.NodeID, c.NodeID},
		MaxConnected:           3,
		MaxOutgoingConnections: 1,
		MaxConnectedUpgrade:    1,
	})
	addReconnectPeer(t, manager, a)
	require.NoError(t, manager.Dialed(a))
	addReconnectPeer(t, manager, b)
	addReconnectPeer(t, manager, c)
	results := make(chan p2p.NodeAddress, 8)
	var wg sync.WaitGroup
	for range cap(results) {
		wg.Go(func() { results <- manager.TryDialNext() })
	}
	wg.Wait()
	close(results)
	var dial p2p.NodeAddress
	for address := range results {
		if address != (p2p.NodeAddress{}) {
			require.Zero(t, dial, "only one upgrade slot and victim are available")
			dial = address
		}
	}
	require.NotZero(t, dial)
	require.NoError(t, manager.Dialed(dial))
	require.Zero(t, manager.TryDialNext(), "eviction must finish before reusing the upgrade slot")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := manager.DialNext(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestPeerManager_OutgoingUpgradeReservation(t *testing.T) {
	ordinary, b, c := reconnectAddress("a"), reconnectAddress("b"), reconnectAddress("c")
	manager := reconnectManager(t, p2p.PeerManagerOptions{
		PersistentPeers:        []types.NodeID{b.NodeID, c.NodeID},
		MaxConnected:           2,
		MaxOutgoingConnections: 1,
		MaxConnectedUpgrade:    2,
	})
	addReconnectPeer(t, manager, ordinary)
	require.NoError(t, manager.Dialed(ordinary))
	addReconnectPeer(t, manager, b)
	addReconnectPeer(t, manager, c)
	address := manager.TryDialNext()
	require.NotZero(t, address)
	require.Zero(t, manager.TryDialNext(), "two upgrade slots cannot reserve the same victim")
	require.NoError(t, manager.DialFailed(t.Context(), address))
	next := manager.TryDialNext()
	require.NotZero(t, next, "failed attempt must release the victim")
	require.NotEqual(t, address, next, "retries are disabled for the failed address")
	require.NoError(t, manager.Dialed(next))
	victim, err := manager.TryEvictNext()
	require.NoError(t, err)
	require.Equal(t, ordinary.NodeID, victim)
}
