package quorum

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dashpay/tenderdash/dash/quorum/mock"
	"github.com/dashpay/tenderdash/dash/quorum/selectpeers"
	"github.com/dashpay/tenderdash/internal/eventbus"
	"github.com/dashpay/tenderdash/types"
)

// TestValidatorConnExecutor_ReservesSlotsForQuorum checks that every member of
// the current quorum this node has to stay connected to gets a reserved
// connection slot, that the reservation follows the quorum when it rotates, and
// that a node outside the validator set reserves nothing.
//
// The reservation is asserted against the selection itself rather than against
// the dial history, because it has to cover quorum members regardless of which
// side opened the connection, and an already-connected member is never dialed.
func TestValidatorConnExecutor_ReservesSlotsForQuorum(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	me := mock.NewValidator(mySeedID)
	eventBus, dialer, vc := setup(ctx, t, me)
	defer cleanup(t, eventBus, dialer, vc)

	quorumOf := func(first uint16) []*types.Validator {
		validators := []*types.Validator{me}
		for i := first; i < first+8; i++ {
			validators = append(validators, mock.NewValidator(i))
		}
		return validators
	}

	first := quorumOf(1)
	publishQuorum(t, eventBus, dialer, first, expectedReservations(t, first, me))

	// A new quorum replaces the reservations rather than adding to them.
	second := quorumOf(20)
	publishQuorum(t, eventBus, dialer, second, expectedReservations(t, second, me))

	// Once this node is no longer a member of the validator set it reserves nothing.
	publishQuorum(t, eventBus, dialer, mock.NewValidators(8), nil)
}

// TestValidatorConnExecutor_ReservesOnlyChainPublishedNodeIDs checks that a
// validator whose node ID is not published with its validator address gets no
// reserved slot.
//
// A missing node ID is otherwise filled in from the address book, which peer
// exchange lets any connected peer write: it accepts an arbitrary node ID at an
// arbitrary address with no proof of ownership. Reserving a slot for whatever it
// names would let an attacker point a validator's address at its own node ID and
// be handed the validator's slot. Such a validator is still dialed — a wrong node
// ID merely fails the handshake — it is just not granted anything.
func TestValidatorConnExecutor_ReservesOnlyChainPublishedNodeIDs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	me := mock.NewValidator(mySeedID)
	eventBus, dialer, vc := setup(ctx, t, me)
	defer cleanup(t, eventBus, dialer, vc)

	// Below the DIP-6 minimum every other member is selected, so the quorum
	// below is exactly the set this node must stay connected to.
	withNodeID, withoutNodeID := mock.NewValidator(1), mock.NewValidator(2)
	withoutNodeID.NodeAddress = types.ValidatorAddress{
		Hostname: withoutNodeID.NodeAddress.Hostname,
		Port:     withoutNodeID.NodeAddress.Port,
	}
	require.Empty(t, withoutNodeID.NodeAddress.NodeID)

	publishQuorum(t, eventBus, dialer,
		[]*types.Validator{me, withNodeID, withoutNodeID},
		[]types.NodeID{withNodeID.NodeAddress.NodeID})
}

// publishQuorum feeds a validator set update through the event bus and waits for
// the executor's reservations to settle on wantProtected.
func publishQuorum(
	t *testing.T,
	eventBus *eventbus.EventBus,
	dialer *mock.DashDialer,
	validators []*types.Validator,
	wantProtected []types.NodeID,
) {
	t.Helper()

	require.NoError(t, eventBus.PublishEventValidatorSetUpdates(types.EventDataValidatorSetUpdate{
		ValidatorSetUpdates: validators,
		QuorumHash:          mock.NewQuorumHash(1000),
	}))

	want := nodeIDSet(wantProtected)
	require.Eventually(t, func() bool {
		return assert.ObjectsAreEqual(want, nodeIDSet(dialer.ProtectedPeerIDs()))
	}, 5*time.Second, 10*time.Millisecond, "want reserved slots for %v", wantProtected)
}

// expectedReservations returns the node IDs of the quorum members `me` has to
// stay connected to — both the ones it dials and the ones that dial it.
//
// It calls the same selector the executor calls, so it pins that the executor
// reserves the whole neighborhood and nothing else, not that the selection
// itself is right; that is what the selector's own inverse property test covers.
func expectedReservations(t *testing.T, validators []*types.Validator, me *types.Validator) []types.NodeID {
	t.Helper()

	selector := selectpeers.NewDIP6ValidatorSelector(mock.NewQuorumHash(1000))
	outbound, err := selector.SelectValidators(validators, me)
	require.NoError(t, err)
	inbound, err := selector.SelectInboundValidators(validators, me)
	require.NoError(t, err)

	ids := make([]types.NodeID, 0, len(outbound)+len(inbound))
	for _, validator := range append(outbound, inbound...) {
		ids = append(ids, validator.NodeAddress.NodeID)
	}
	return ids
}

func nodeIDSet(ids []types.NodeID) map[types.NodeID]bool {
	set := make(map[types.NodeID]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}
