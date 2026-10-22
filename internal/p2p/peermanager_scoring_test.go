package p2p

import (
	"context"
	"strings"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto/ed25519"
	"github.com/dashpay/tenderdash/libs/log"
	"github.com/dashpay/tenderdash/types"
)

func TestPeerScoring(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// coppied from p2p_test shared variables
	selfKey := ed25519.GenPrivKeyFromSecret([]byte{0xf9, 0x1b, 0x08, 0xaa, 0x38, 0xee, 0x34, 0xdd})
	selfID := types.NodeIDFromPubKey(selfKey.PubKey())

	// create a mock peer manager
	db := dbm.NewMemDB()
	peerManager, err := NewPeerManager(ctx, selfID, db, PeerManagerOptions{})
	require.NoError(t, err)

	// create a fake node
	id := types.NodeID(strings.Repeat("a1", 20))
	added, err := peerManager.Add(NodeAddress{NodeID: id, Protocol: "memory"})
	require.NoError(t, err)
	require.True(t, added)

	t.Run("Synchronous", func(t *testing.T) {
		// update the manager and make sure it's correct
		require.Zero(t, peerManager.Scores()[id])

		// add a bunch of good status updates and watch things increase.
		for i := 1; i < 10; i++ {
			peerManager.processPeerEvent(ctx, PeerUpdate{
				NodeID: id,
				Status: PeerStatusGood,
			})
			require.EqualValues(t, i, peerManager.Scores()[id])
		}

		// watch the corresponding decreases respond to update
		for i := 10; i == 0; i-- {
			peerManager.processPeerEvent(ctx, PeerUpdate{
				NodeID: id,
				Status: PeerStatusBad,
			})
			require.EqualValues(t, i, peerManager.Scores()[id])
		}
	})
	t.Run("AsynchronousIncrement", func(t *testing.T) {
		start := peerManager.Scores()[id]
		pu := peerManager.Subscribe(ctx, "p2p")
		pu.SendUpdate(ctx, PeerUpdate{
			NodeID: id,
			Status: PeerStatusGood,
		})
		require.Eventually(t,
			func() bool { return start+1 == peerManager.Scores()[id] },
			time.Second,
			time.Millisecond,
			"startAt=%d score=%d", start, peerManager.Scores()[id])
	})
	t.Run("AsynchronousDecrement", func(t *testing.T) {
		start := peerManager.Scores()[id]
		pu := peerManager.Subscribe(ctx, "p2p")
		pu.SendUpdate(ctx, PeerUpdate{
			NodeID: id,
			Status: PeerStatusBad,
		})
		require.Eventually(t,
			func() bool { return start-1 == peerManager.Scores()[id] },
			time.Second,
			time.Millisecond,
			"startAt=%d score=%d", start, peerManager.Scores()[id])
	})
	t.Run("TestNonPersistantPeerUpperBound", func(t *testing.T) {
		start := int64(peerManager.Scores()[id] + 1)

		for i := start; i <= int64(PeerScorePersistent); i++ {
			peerManager.processPeerEvent(ctx, PeerUpdate{
				NodeID: id,
				Status: PeerStatusGood,
			})

			if i == int64(PeerScorePersistent) {
				require.EqualValues(t, MaxPeerScoreNotPersistent, peerManager.Scores()[id])
			} else {
				require.EqualValues(t, i, peerManager.Scores()[id])
			}
		}
	})
}

func makeMockPeerStore(t *testing.T, peers ...peerInfo) *peerStore {
	t.Helper()
	s, err := newPeerStore(dbm.NewMemDB(), log.NewNopLogger())
	if err != nil {
		t.Fatal(err)
	}
	for idx := range peers {
		if err := s.Set(peers[idx]); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestPeerRanking(t *testing.T) {
	t.Run("InactiveSecond", func(t *testing.T) {
		t.Skip("inactive status is not currently factored into peer rank.")

		store := makeMockPeerStore(t,
			peerInfo{ID: "second", Inactive: true},
			peerInfo{ID: "first", Inactive: false},
		)

		ranked := store.Ranked()
		if len(ranked) != 2 {
			t.Fatal("missing peer in ranked output")
		}
		if ranked[0].ID != "first" {
			t.Error("inactive peer is first")
		}
		if ranked[1].ID != "second" {
			t.Error("active peer is second")
		}
	})
	t.Run("ScoreOrder", func(t *testing.T) {
		for _, test := range []struct {
			Name   string
			First  int64
			Second int64
		}{
			{
				Name:   "Mirror",
				First:  100,
				Second: -100,
			},
			{
				Name:   "VeryLow",
				First:  0,
				Second: -100,
			},
			{
				Name:   "High",
				First:  300,
				Second: 256,
			},
		} {
			t.Run(test.Name, func(t *testing.T) {
				store := makeMockPeerStore(t,
					peerInfo{
						ID:           "second",
						MutableScore: test.Second,
					},
					peerInfo{
						ID:           "first",
						MutableScore: test.First,
					})

				ranked := store.Ranked()
				if len(ranked) != 2 {
					t.Fatal("missing peer in ranked output")
				}
				if ranked[0].ID != "first" {
					t.Error("higher peer is first")
				}
				if ranked[1].ID != "second" {
					t.Error("higher peer is second")
				}
			})
		}
	})
}

func TestLastDialed(t *testing.T) {
	t.Run("Zero", func(t *testing.T) {
		p := &peerInfo{}
		ts, ok := p.LastDialed()
		if !ts.IsZero() {
			t.Error("timestamp should be zero:", ts)
		}
		if ok {
			t.Error("peer reported success, despite none")
		}
	})
	t.Run("NeverDialed", func(t *testing.T) {
		p := &peerInfo{
			AddressInfo: map[NodeAddress]*peerAddressInfo{
				{NodeID: "kip"}:    {},
				{NodeID: "merlin"}: {},
			},
		}
		ts, ok := p.LastDialed()
		if !ts.IsZero() {
			t.Error("timestamp should be zero:", ts)
		}
		if ok {
			t.Error("peer reported success, despite none")
		}
	})
	t.Run("Ordered", func(t *testing.T) {
		base := time.Now()
		for _, test := range []struct {
			Name            string
			SuccessTime     time.Time
			FailTime        time.Time
			ExpectedSuccess bool
		}{
			{
				Name: "Zero",
			},
			{
				Name:            "Success",
				SuccessTime:     base.Add(time.Hour),
				FailTime:        base,
				ExpectedSuccess: true,
			},
			{
				Name:            "Equal",
				SuccessTime:     base,
				FailTime:        base,
				ExpectedSuccess: true,
			},
			{
				Name:            "Failure",
				SuccessTime:     base,
				FailTime:        base.Add(time.Hour),
				ExpectedSuccess: false,
			},
		} {
			t.Run(test.Name, func(t *testing.T) {
				p := &peerInfo{
					AddressInfo: map[NodeAddress]*peerAddressInfo{
						{NodeID: "kip"}:    {LastDialSuccess: test.SuccessTime},
						{NodeID: "merlin"}: {LastDialFailure: test.FailTime},
					},
				}
				ts, ok := p.LastDialed()
				if test.ExpectedSuccess && !ts.Equal(test.SuccessTime) {
					if !ts.Equal(test.FailTime) {
						t.Fatal("got unexpected timestamp:", ts)
					}

					t.Error("last dialed time reported incorrect value:", ts)
				}
				if !test.ExpectedSuccess && !ts.Equal(test.FailTime) {
					if !ts.Equal(test.SuccessTime) {
						t.Fatal("got unexpected timestamp:", ts)
					}

					t.Error("last dialed time reported incorrect value:", ts)
				}
				if test.ExpectedSuccess != ok {
					t.Error("test reported incorrect outcome for last dialed type")
				}
			})
		}

	})

}

// TestPeerScoringUnknownPeer asserts a score change is recorded for a peer that
// is not yet in the peer store.
//
// processPeerEvent starts from a zero peerInfo on a store miss, and peerStore.Set
// rejects an entry with no ID, so without carrying the ID across the penalty is
// discarded and the peer keeps a clean record.
func TestPeerScoringUnknownPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	selfKey := ed25519.GenPrivKeyFromSecret([]byte{0x2a, 0x3b, 0x4c, 0x5d})
	pm, err := NewPeerManager(ctx, types.NodeIDFromPubKey(selfKey.PubKey()), dbm.NewMemDB(), PeerManagerOptions{})
	require.NoError(t, err)

	unknown := types.NodeID(strings.Repeat("b2", 20))
	_, inStore := pm.store.Get(unknown)
	require.False(t, inStore, "fixture requires a peer the store has never seen")

	pm.processPeerEvent(ctx, PeerUpdate{NodeID: unknown, Status: PeerStatusBad})

	require.EqualValues(t, -1, pm.Scores()[unknown], "penalty must be recorded for a peer not yet in the store")
}

// A peer first recorded by a score change has no addresses yet. PeerManager.Add
// writes into peerInfo.AddressInfo directly, so an entry stored without that map
// allocated panics on the first address later learned for that peer.
//
// The map is asserted before Add so the failure is a clean assertion rather than
// a panic that takes the test binary with it.
func TestPeerScoringUnknownPeer_LaterAddressDoesNotPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	selfKey := ed25519.GenPrivKeyFromSecret([]byte{0x7e, 0x1f, 0x2a, 0x3b})
	pm, err := NewPeerManager(ctx, types.NodeIDFromPubKey(selfKey.PubKey()), dbm.NewMemDB(), PeerManagerOptions{})
	require.NoError(t, err)

	unknown := types.NodeID(strings.Repeat("c3", 20))
	_, inStore := pm.store.Get(unknown)
	require.False(t, inStore, "fixture requires a peer the store has never seen")

	pm.processPeerEvent(ctx, PeerUpdate{NodeID: unknown, Status: PeerStatusBad})

	stored, ok := pm.store.Get(unknown)
	require.True(t, ok, "the penalty must have stored an entry")
	require.NotNil(t, stored.AddressInfo, "a stored peer must carry an initialized address map")

	added, err := pm.Add(NodeAddress{NodeID: unknown, Protocol: "memory"})
	require.NoError(t, err)
	require.True(t, added, "the first address for a penalized peer must be stored")
}

// SetMutableScore skips persistence, so it must still invalidate the Ranked() cache.
func TestPeerStoreSetMutableScoreReranks(t *testing.T) {
	low := peerInfo{ID: types.NodeID(strings.Repeat("a1", 20)), MutableScore: -1}
	high := peerInfo{ID: types.NodeID(strings.Repeat("b2", 20)), MutableScore: 1}
	store := makeMockPeerStore(t, low, high)
	require.Equal(t, high.ID, store.Ranked()[0].ID)

	store.SetMutableScore(low.ID, 2)

	require.Equal(t, low.ID, store.Ranked()[0].ID)
	got, ok := store.Get(low.ID)
	require.True(t, ok)
	require.EqualValues(t, 2, got.MutableScore)
}
