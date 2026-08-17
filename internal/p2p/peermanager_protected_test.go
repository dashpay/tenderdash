package p2p

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/crypto/ed25519"
	"github.com/dashpay/tenderdash/types"
)

// protectionTestOptions mirrors the production shape (a hard connection ceiling
// plus a few upgrade slots) at a size that is practical to fill in a test.
func protectionTestOptions() PeerManagerOptions {
	return PeerManagerOptions{
		MaxConnected:        8,
		MaxConnectedUpgrade: 2,
		MaxPeers:            256,
	}
}

func testSelfID(t *testing.T) types.NodeID {
	t.Helper()
	return types.NodeIDFromPubKey(ed25519.GenPrivKeyFromSecret([]byte("protection-self")).PubKey())
}

// floodID returns a distinct node ID for the n-th connection-flood peer. Node
// IDs are unauthenticated-cheap for an attacker, which is the point: the flood
// can mint as many as it likes.
func floodID(t *testing.T, n int) types.NodeID {
	t.Helper()
	return types.NodeIDFromPubKey(ed25519.GenPrivKeyFromSecret([]byte(fmt.Sprintf("flood-%d", n))).PubKey())
}

func validatorID(t *testing.T) types.NodeID {
	t.Helper()
	return types.NodeIDFromPubKey(ed25519.GenPrivKeyFromSecret([]byte("quorum-validator")).PubKey())
}

func isConnectedLocked(m *PeerManager, id types.NodeID) bool {
	m.mtx.Lock()
	defer m.mtx.Unlock()
	return m.isConnected(id)
}

// drainEvictions performs the eviction work the router would otherwise do, so
// that scheduled evictions actually free their slot. It fails the test if a
// protected peer is ever handed out for eviction.
func drainEvictions(ctx context.Context, t *testing.T, m *PeerManager, protected types.NodeID) {
	t.Helper()
	for {
		id, err := m.TryEvictNext()
		require.NoError(t, err)
		if id == "" {
			return
		}
		require.NotEqual(t, protected, id, "a current-quorum validator was scheduled for eviction")
		m.Disconnected(ctx, id)
	}
}

// TestProtectedValidatorSurvivesFloodErrorsAndRestart pins the connection-level
// half of honest service: a member of the active validator quorum keeps its
// connection slot under an inbound connection flood, keeps it after error
// scoring, and can reclaim one after a restart.
//
// The validator here reaches us *inbound* and is never dialed by this node,
// which is the case the dial-time scoring never covered: DIP-6 is a directed
// overlay, so roughly half of a node's quorum neighbors only ever connect
// inwards, and after a restart every neighbor does.
func TestProtectedValidatorSurvivesFloodErrorsAndRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db := dbm.NewMemDB()
	opts := protectionTestOptions()
	validator := validatorID(t)

	peerManager, err := NewPeerManager(ctx, testSelfID(t), db, opts)
	require.NoError(t, err)
	defer peerManager.Close()

	// The quorum is known before any connection is made.
	require.NoError(t, peerManager.SetProtectedPeers([]types.NodeID{validator}))

	// The validator connects to us; we never dial it.
	require.NoError(t, peerManager.Accepted(validator))

	// The flood takes every remaining slot.
	flood := 0
	for ; flood < int(opts.MaxConnected)-1; flood++ {
		require.NoError(t, peerManager.Accepted(floodID(t, flood)))
	}

	// Error scoring must not erode a protected peer. One decrement is enough to
	// make an unprotected peer displaceable by any fresh score-0 connection.
	for i := 0; i < 100; i++ {
		peerManager.processPeerEvent(ctx, PeerUpdate{NodeID: validator, Status: PeerStatusBad})
	}
	require.Equal(t, PeerScorePersistent, peerManager.Scores()[validator],
		"error scoring must not lower a protected peer's rank")

	// The flood keeps arriving and must never buy the validator's slot.
	for i := 0; i < 30; i++ {
		flood++
		_ = peerManager.Accepted(floodID(t, flood)) // refusal is a fine outcome; eviction of the validator is not
		drainEvictions(ctx, t, peerManager, validator)
	}
	require.True(t, isConnectedLocked(peerManager, validator),
		"a current-quorum validator was pushed off its slot by a connection flood")

	// Restart: a new manager over the same peer store, with protection derived
	// again from the quorum in force.
	require.NoError(t, peerManager.Close())
	restarted, err := NewPeerManager(ctx, testSelfID(t), db, opts)
	require.NoError(t, err)
	defer restarted.Close()
	require.NoError(t, restarted.SetProtectedPeers([]types.NodeID{validator}))

	// The flood re-establishes itself first and fills every slot.
	for i := 0; i < int(opts.MaxConnected); i++ {
		flood++
		require.NoError(t, restarted.Accepted(floodID(t, flood)))
	}

	// The validator reconnects and must still be admitted, displacing a flood peer.
	require.NoError(t, restarted.Accepted(validator),
		"a current-quorum validator was refused a slot after a restart")
	drainEvictions(ctx, t, restarted, validator)
	require.True(t, isConnectedLocked(restarted, validator))
}

// TestProtectedValidatorDisplacesFloodOnInbound checks the admission half on its
// own: at the connection ceiling, a protected peer is accepted and an
// unprotected one is refused.
func TestProtectedValidatorDisplacesFloodOnInbound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := protectionTestOptions()
	peerManager, err := NewPeerManager(ctx, testSelfID(t), dbm.NewMemDB(), opts)
	require.NoError(t, err)
	defer peerManager.Close()

	validator := validatorID(t)
	require.NoError(t, peerManager.SetProtectedPeers([]types.NodeID{validator}))

	for i := 0; i < int(opts.MaxConnected); i++ {
		require.NoError(t, peerManager.Accepted(floodID(t, i)))
	}

	// Another unprotected peer has nothing to offer over the peers already here.
	require.Error(t, peerManager.Accepted(floodID(t, 1000)))

	// The protected one does.
	require.NoError(t, peerManager.Accepted(validator))
	drainEvictions(ctx, t, peerManager, validator)
	require.True(t, isConnectedLocked(peerManager, validator))
}

// TestReservedSlotIsNotShedUnderConnectionPressure pins the other way a slot is
// taken: when every slot is in use, any error reported against a peer
// disconnects it outright, without consulting its rank. Since a flood is what
// puts the node at its connection ceiling in the first place, that path would
// otherwise hand an attacker a one-error eviction of any quorum member.
func TestReservedSlotIsNotShedUnderConnectionPressure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := protectionTestOptions()
	peerManager, err := NewPeerManager(ctx, testSelfID(t), dbm.NewMemDB(), opts)
	require.NoError(t, err)
	defer peerManager.Close()

	validator, sybil := validatorID(t), floodID(t, 0)
	require.NoError(t, peerManager.SetProtectedPeers([]types.NodeID{validator}))
	require.NoError(t, peerManager.Accepted(validator))
	for i := 0; i < int(opts.MaxConnected)-1; i++ {
		require.NoError(t, peerManager.Accepted(floodID(t, i)))
	}

	require.False(t, peerManager.ShouldDisconnectOnError(validator, false),
		"connection pressure must not disconnect a peer holding a reserved slot")
	require.True(t, peerManager.ShouldDisconnectOnError(sybil, false),
		"connection pressure must still shed peers without a reserved slot")
	require.True(t, peerManager.ShouldDisconnectOnError(validator, true),
		"a reserved slot is not a license to misbehave")
}

// TestReservedSlotAdmissionAtHardCeiling documents the one limit of a
// reservation: it wins an upgrade, and upgrades stop at
// MaxConnected+MaxConnectedUpgrade. At that hard ceiling a protected peer is
// refused like anyone else, and is admitted as soon as a scheduled eviction
// completes — a delay, never a permanent exclusion.
func TestReservedSlotAdmissionAtHardCeiling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := protectionTestOptions()
	ceiling := int(opts.MaxConnected) + int(opts.MaxConnectedUpgrade)
	peerManager, err := NewPeerManager(ctx, testSelfID(t), dbm.NewMemDB(), opts)
	require.NoError(t, err)
	defer peerManager.Close()

	validator := validatorID(t)
	require.NoError(t, peerManager.SetProtectedPeers([]types.NodeID{validator}))

	// A flood walks the connection count all the way to the hard ceiling: each
	// peer that reports an error drops below its neighbors and so becomes a
	// legal upgrade candidate for the next arrival.
	for i := 0; i < ceiling; i++ {
		require.NoError(t, peerManager.Accepted(floodID(t, i)))
		peerManager.processPeerEvent(ctx, PeerUpdate{NodeID: floodID(t, i), Status: PeerStatusBad})
	}

	require.Error(t, peerManager.Accepted(validator),
		"the hard connection ceiling applies to protected peers too")

	// Draining a single scheduled eviction is enough to let it in.
	evicted, err := peerManager.TryEvictNext()
	require.NoError(t, err)
	require.NotEmpty(t, evicted)
	peerManager.Disconnected(ctx, evicted)

	// Restore the flood to a clean rank, so that getting in below the ceiling
	// takes an actual reservation rather than just out-ranking a peer that
	// happens to have reported an error.
	for i := 0; i < ceiling; i++ {
		peerManager.processPeerEvent(ctx, PeerUpdate{NodeID: floodID(t, i), Status: PeerStatusGood})
	}
	require.Error(t, peerManager.Accepted(floodID(t, 5000)),
		"an unprotected peer has nothing to offer over the peers already connected")

	require.NoError(t, peerManager.Accepted(validator))
	require.True(t, isConnectedLocked(peerManager, validator))
}

// TestProtectedPeersRejectedSetIsNotKept checks what happens when a set of
// reservations cannot be honored: the previous set is dropped rather than left
// in force. Keeping it would reserve slots for a quorum that has rotated out —
// exactly the stale protection that keeping reservations out of the peer store
// is meant to prevent.
func TestProtectedPeersRejectedSetIsNotKept(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := protectionTestOptions()
	peerManager, err := NewPeerManager(ctx, testSelfID(t), dbm.NewMemDB(), opts)
	require.NoError(t, err)
	defer peerManager.Close()

	validator := validatorID(t)
	require.NoError(t, peerManager.SetProtectedPeers([]types.NodeID{validator}))
	require.NoError(t, peerManager.Accepted(validator))
	require.Equal(t, PeerScorePersistent, peerManager.Scores()[validator])

	// More reservations than the node has slots to spare.
	oversized := make([]types.NodeID, 0, opts.MaxConnected)
	for i := 0; i < int(opts.MaxConnected); i++ {
		oversized = append(oversized, floodID(t, i))
	}
	require.Error(t, peerManager.SetProtectedPeers(oversized))

	require.Equal(t, PeerScore(0), peerManager.Scores()[validator],
		"a rejected set must not leave the previous reservations in force")
	require.Equal(t, PeerScore(0), peerManager.Scores()[oversized[0]],
		"a rejected set must not be applied either")
}

// TestProtectedPeersIgnoresSelfAndMalformedIDs checks that this node never
// reserves a slot for itself, and that one malformed node ID costs only itself
// rather than the whole quorum's reservations.
func TestProtectedPeersIgnoresSelfAndMalformedIDs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	selfID := testSelfID(t)
	peerManager, err := NewPeerManager(ctx, selfID, dbm.NewMemDB(), protectionTestOptions())
	require.NoError(t, err)
	defer peerManager.Close()

	validator := validatorID(t)
	require.Error(t, peerManager.SetProtectedPeers([]types.NodeID{validator, selfID, "not-a-node-id"}))

	require.NoError(t, peerManager.Accepted(validator))
	require.Equal(t, PeerScorePersistent, peerManager.Scores()[validator],
		"a malformed entry must not cost the valid ones their reservation")
	require.False(t, isConnectedLocked(peerManager, selfID))
}

// TestReservedSlotSurvivesIncomingConnectionTimeout checks the eviction timer
// seed nodes use to recycle inbound connections does not recycle a quorum
// member's slot.
func TestReservedSlotSurvivesIncomingConnectionTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := protectionTestOptions()
	opts.MaxIncomingConnectionTime = time.Millisecond
	peerManager, err := NewPeerManager(ctx, testSelfID(t), dbm.NewMemDB(), opts)
	require.NoError(t, err)
	defer peerManager.Close()

	validator, sybil := validatorID(t), floodID(t, 0)
	require.NoError(t, peerManager.SetProtectedPeers([]types.NodeID{validator}))
	require.NoError(t, peerManager.Accepted(validator))
	require.NoError(t, peerManager.Accepted(sybil))

	// The unprotected peer is recycled; the quorum member is not.
	require.Eventually(t, func() bool {
		peerManager.mtx.Lock()
		defer peerManager.mtx.Unlock()
		return peerManager.evict[sybil]
	}, 5*time.Second, 5*time.Millisecond, "the incoming connection timer never fired")

	peerManager.mtx.Lock()
	reservedEvicted := peerManager.evict[validator]
	peerManager.mtx.Unlock()
	require.False(t, reservedEvicted, "a reserved slot must outlive the incoming connection timer")
}

// TestProtectedPeersReplacedOnQuorumChange checks that protection tracks the
// active quorum: it is dropped when a validator rotates out, and absent
// entirely for a node that is not part of any quorum.
func TestProtectedPeersReplacedOnQuorumChange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peerManager, err := NewPeerManager(ctx, testSelfID(t), dbm.NewMemDB(), protectionTestOptions())
	require.NoError(t, err)
	defer peerManager.Close()

	outgoing, incoming := validatorID(t), floodID(t, 500)

	require.NoError(t, peerManager.SetProtectedPeers([]types.NodeID{outgoing}))
	require.NoError(t, peerManager.Accepted(outgoing))
	require.Equal(t, PeerScorePersistent, peerManager.Scores()[outgoing])

	// New quorum: the old member loses protection, the new one gains it even
	// though it has never connected.
	require.NoError(t, peerManager.SetProtectedPeers([]types.NodeID{incoming}))
	require.Equal(t, PeerScore(0), peerManager.Scores()[outgoing])
	require.NoError(t, peerManager.Accepted(incoming))
	require.Equal(t, PeerScorePersistent, peerManager.Scores()[incoming])

	// Leaving the validator set clears protection entirely.
	require.NoError(t, peerManager.SetProtectedPeers(nil))
	require.Equal(t, PeerScore(0), peerManager.Scores()[incoming])
}

// TestProtectedPeersBudgetCountsAPeerOnce checks that a validator which is also
// a configured persistent peer spends one connection slot rather than two.
//
// Both kinds of peer hold a slot, and both are charged against the same budget,
// so counting a peer that is both twice can put a quorum over a budget it never
// actually exceeded — and that is enough to turn every reservation off.
func TestProtectedPeersBudgetCountsAPeerOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quorum := []types.NodeID{validatorID(t), floodID(t, 600), floodID(t, 601), floodID(t, 602)}

	opts := protectionTestOptions()
	// One of the quorum members is also configured as a persistent peer, so it
	// already holds the slot its reservation would hold.
	opts.PersistentPeers = []types.NodeID{quorum[0]}
	peerManager, err := NewPeerManager(ctx, testSelfID(t), dbm.NewMemDB(), opts)
	require.NoError(t, err)
	defer peerManager.Close()

	require.NoError(t, peerManager.SetProtectedPeers(quorum),
		"the quorum fits: one of its members is the persistent peer already counted")

	for _, id := range quorum {
		require.NoError(t, peerManager.Accepted(id))
		require.Equal(t, PeerScorePersistent, peerManager.Scores()[id],
			"every member of a quorum that fits must hold its reservation")
	}
}

// TestProtectedPeersOverBudgetKeepsWhatFits checks that a quorum too large for
// the slots left over does not cost every one of its members its reservation.
//
// The budget shrinks with each configured persistent peer, so an operator with
// enough of them turns the whole protection off — over a configuration that is
// otherwise legal, and with nothing to show for it but one error. Reserving as
// many members as there is room for keeps the protection doing its job, and the
// shortfall is still reported, since only an operator can fix it.
func TestProtectedPeersOverBudgetKeepsWhatFits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := protectionTestOptions()
	// Persistent peers enough to leave room for a single reservation.
	opts.PersistentPeers = []types.NodeID{floodID(t, 700), floodID(t, 701), floodID(t, 702)}
	peerManager, err := NewPeerManager(ctx, testSelfID(t), dbm.NewMemDB(), opts)
	require.NoError(t, err)
	defer peerManager.Close()

	quorum := []types.NodeID{validatorID(t), floodID(t, 800), floodID(t, 801)}
	require.Error(t, peerManager.SetProtectedPeers(quorum),
		"a quorum that does not fit must be reported")

	reserved := 0
	for _, id := range quorum {
		require.NoError(t, peerManager.Accepted(id))
		if peerManager.Scores()[id] == PeerScorePersistent {
			reserved++
		}
	}
	require.Positive(t, reserved,
		"a quorum that does not fit must not leave every validator slot unreserved")
	require.LessOrEqual(t, reserved, int(opts.MaxConnected)/2,
		"reservations must stay a minority of the connection slots")
}

// TestReservedPeerBanksGoodStandingNotPunishment checks what a peer is worth
// the moment its reservation ends.
//
// A reservation is temporary: it lasts as long as the peer is in the active
// quorum. Withholding punishment while it holds is deliberate — a reserved peer
// must not be able to score its way out of a slot it is entitled to. Withholding
// credit is not: an hour of clean service that banks nothing leaves the peer at
// its starting score, so it becomes the lowest-ranked connected peer at the
// exact moment the quorum rotates and the peers replacing it may not be
// connected yet.
func TestReservedPeerBanksGoodStandingNotPunishment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peerManager, err := NewPeerManager(ctx, testSelfID(t), dbm.NewMemDB(), protectionTestOptions())
	require.NoError(t, err)
	defer peerManager.Close()

	validator, successor := validatorID(t), floodID(t, 900)
	require.NoError(t, peerManager.SetProtectedPeers([]types.NodeID{validator}))
	require.NoError(t, peerManager.Accepted(validator))

	const clean = 20
	for i := 0; i < clean; i++ {
		peerManager.processPeerEvent(ctx, PeerUpdate{NodeID: validator, Status: PeerStatusGood})
	}
	for i := 0; i < 5; i++ {
		peerManager.processPeerEvent(ctx, PeerUpdate{NodeID: validator, Status: PeerStatusBad})
	}
	require.Equal(t, PeerScorePersistent, peerManager.Scores()[validator],
		"a reservation outranks whatever the peer has banked while it holds")

	// The quorum rotates and the reservation ends.
	require.NoError(t, peerManager.SetProtectedPeers([]types.NodeID{successor}))
	require.Equal(t, PeerScore(clean), peerManager.Scores()[validator],
		"clean service must be banked while reserved, and errors must not be")
}

// TestProtectedPeersOverBudgetKeepsThePeersThatCostNothing checks which members
// a quorum too large for the budget loses.
//
// A quorum member that is also a configured persistent peer holds its slot
// either way, so taking its reservation away frees nothing while still costing
// the budget a slot. Cutting the set down has to come out of the members that
// occupy a slot of their own, or the reservations and the persistent peers
// together still exceed the minority of the connection slots they are bounded
// to.
func TestProtectedPeersOverBudgetKeepsThePeersThatCostNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quorum := []types.NodeID{
		validatorID(t), floodID(t, 1000), floodID(t, 1001), floodID(t, 1002), floodID(t, 1003),
	}

	// The member the caller offered last is the first to be dropped, so making
	// that one persistent puts the member that costs nothing at the front of the
	// queue — which is exactly the case that has to be preferred instead.
	persistent := quorum[len(quorum)-1]

	opts := protectionTestOptions()
	opts.PersistentPeers = []types.NodeID{persistent}
	peerManager, err := NewPeerManager(ctx, testSelfID(t), dbm.NewMemDB(), opts)
	require.NoError(t, err)
	defer peerManager.Close()

	// One more member than the budget has room for.
	require.Error(t, peerManager.SetProtectedPeers(quorum))

	peerManager.mtx.Lock()
	defer peerManager.mtx.Unlock()
	require.True(t, peerManager.protectedPeers[persistent],
		"the member that already holds a slot must not be the one dropped")
	require.LessOrEqual(t,
		len(peerManager.protectedPeers)+peerManager.persistentOutside(peerManager.protectedPeers),
		int(opts.MaxConnected)/2,
		"reservations and persistent peers together must stay a minority of the connection slots")
}

// TestProtectedPeersTrimFollowsTheCallersOrder checks that a member cannot
// choose whether it survives a trim.
//
// A node ID is a hash of a key its owner picks, and grinding a low one costs
// seconds — so ordering the candidates by node ID would hand every reservation
// to whoever ground the lowest ID and take them from the honest members. The
// order the caller offers is derived from the quorum, which no single member
// decides.
func TestProtectedPeersTrimFollowsTheCallersOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := protectionTestOptions()
	// Persistent peers enough to leave room for a single reservation.
	opts.PersistentPeers = []types.NodeID{floodID(t, 1100), floodID(t, 1101), floodID(t, 1102)}
	peerManager, err := NewPeerManager(ctx, testSelfID(t), dbm.NewMemDB(), opts)
	require.NoError(t, err)
	defer peerManager.Close()

	// Offered last, and holding the lowest node ID of the three.
	quorum := []types.NodeID{floodID(t, 1200), floodID(t, 1201), floodID(t, 1202)}
	lowest := quorum[0]
	for _, id := range quorum {
		if id < lowest {
			lowest = id
		}
	}
	ordered := make([]types.NodeID, 0, len(quorum))
	for _, id := range quorum {
		if id != lowest {
			ordered = append(ordered, id)
		}
	}
	ordered = append(ordered, lowest)

	require.Error(t, peerManager.SetProtectedPeers(ordered))

	peerManager.mtx.Lock()
	defer peerManager.mtx.Unlock()
	require.Equal(t, 1, len(peerManager.protectedPeers))
	require.True(t, peerManager.protectedPeers[ordered[0]],
		"the reservation must go to the member the quorum's own order put first")
	require.False(t, peerManager.protectedPeers[lowest],
		"holding the lowest node ID must not buy a reservation")
}
