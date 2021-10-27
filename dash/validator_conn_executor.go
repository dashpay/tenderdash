package dash

import (
	"context"
	"fmt"

	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/rand"
	"github.com/tendermint/tendermint/libs/service"
	"github.com/tendermint/tendermint/libs/sync"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/types"
)

// validatorConnExecutorName contains name that is used to represent the ValidatorConnExecutor in BaseService and logs
const validatorConnExecutorName = "ValidatorConnExecutor"

// ISwitch defines p2p.Switch methods that are used by this Executor.
// Useful to create a mock  of the p2p.Switch.
type ISwitch interface {
	Peers() p2p.IPeerSet

	AddPersistentPeers(addrs []string) error
	RemovePersistentPeer(addr string) error

	DialPeersAsync(addrs []string) error
	IsDialingOrExistingAddress(*p2p.NetAddress) bool
	StopPeerGracefully(p2p.Peer)
}

// validatorMap maps validator ID to the validator
type validatorMap map[p2p.ID]*types.Validator

// newValidatorMap creates a new validatoMap based on a slice of Validators
func newValidatorMap(validators []*types.Validator) validatorMap {
	newMap := make(validatorMap, len(validators))
	for _, validator := range validators {
		newMap[validator.NodeAddress.NodeID] = validator
	}

	return newMap
}

// ValidatorConnExecutor retrieves validator update events and establishes new validator connections
// within the ValidatorSet.
// If it's already connected to a member of current validator set, it will keep that connection.
// Otherwise, it will randomly select some members of the active validator set and connect to them, to ensure
// it has connectivity with at least `NumConnections` members of the active validator set.
//
// Note that we mark peers that are members of active validator set as Persistent, so p2p subsystem
// will retry the connection if it fails.
type ValidatorConnExecutor struct {
	service.BaseService
	ctx          context.Context
	nodeID       p2p.ID
	eventBus     *types.EventBus
	p2pSwitch    ISwitch
	subscription types.Subscription

	// validatorSetMembers contains validators active in the current Validator Set, indexed by node ID
	validatorSetMembers validatorMap
	// connectedValidators contains validators we should be connected to, indexed by node ID
	connectedValidators validatorMap

	// mux is a mutex to ensure only one goroutine is processing connections
	mux sync.Mutex

	// *** configuration *** //

	// NumConnections is the number of connections to establish, defaults to 3
	NumConnections int
	// EventBusCapacity sets event bus buffer capacity, defaults to 10
	EventBusCapacity int
}

// NewValidatorConnExecutor creates a Service that connects to other validators within the same Validator Set.
// Don't forget to Start() and Stop() the service.
func NewValidatorConnExecutor(
	nodeID p2p.ID,
	eventBus *types.EventBus,
	sw ISwitch,
	logger log.Logger) *ValidatorConnExecutor {
	vc := &ValidatorConnExecutor{
		ctx:                 context.Background(),
		nodeID:              nodeID,
		eventBus:            eventBus,
		p2pSwitch:           sw,
		NumConnections:      3,
		EventBusCapacity:    10,
		validatorSetMembers: validatorMap{},
		connectedValidators: validatorMap{},
	}

	baseService := service.NewBaseService(logger, validatorConnExecutorName, vc)
	vc.BaseService = *baseService

	return vc
}

// OnStart implements Service to subscribe to Validator Update events
func (vc *ValidatorConnExecutor) OnStart() error {
	if err := vc.subscribe(); err != nil {
		return err
	}

	go func() {
		var err error
		for err == nil {
			err = vc.receiveEvents()
		}
		vc.Logger.Error("ValidatorConnExecutor goroutine finished", "reason", err)
	}()

	return nil
}

// OnStop implements Service to clean up (unsubscribe from all events, stop timers, etc.)
func (vc *ValidatorConnExecutor) OnStop() {
	if vc.eventBus != nil {
		err := vc.eventBus.UnsubscribeAll(vc.ctx, validatorConnExecutorName)
		if err != nil {
			vc.Logger.Error("cannot unsubscribe from channels", "error", err)
		}
		vc.eventBus = nil
	}
}

// subscribe subscribes to event bus to receive validator update messages
func (vc *ValidatorConnExecutor) subscribe() error {
	updatesSub, err := vc.eventBus.Subscribe(
		vc.ctx,
		validatorConnExecutorName,
		types.EventQueryValidatorSetUpdates,
		vc.EventBusCapacity,
	)
	if err != nil {
		return err
	}

	vc.subscription = updatesSub
	return nil
}

// receiveEvents processes received events and executes all the logic.
// Returns non-nil error only if fatal error occurred and the main goroutine should be terminated.
func (vc *ValidatorConnExecutor) receiveEvents() error {
	vc.Logger.Debug("ValidatorConnExecutor: waiting for an event")
	select {
	case msg := <-vc.subscription.Out():
		event, ok := msg.Data().(types.EventDataValidatorSetUpdates)
		if !ok {
			return fmt.Errorf("invalid type of validator set update message: %T", event)
		}
		if err := vc.handleValidatorUpdateEvent(event); err != nil {
			vc.Logger.Error("cannot handle validator update", "error", err)
			return nil // non-fatal, so no error returned to continue the loop
		}
		vc.Logger.Debug("validator updates processed successfully", "event", event)
	case <-vc.subscription.Cancelled():
		return fmt.Errorf("subscription cancelled due to error: %w", vc.subscription.Err())
	case <-vc.BaseService.Quit():
		return fmt.Errorf("quit signal received")
	case <-vc.ctx.Done():
		return fmt.Errorf("context expired: %w", vc.ctx.Err())
	}

	return nil
}

// handleValidatorUpdateEvent checks and executes event of type EventDataValidatorSetUpdates, received from event bus.
func (vc *ValidatorConnExecutor) handleValidatorUpdateEvent(event types.EventDataValidatorSetUpdates) error {
	vc.mux.Lock()
	defer vc.mux.Unlock()

	if len(event.ValidatorUpdates) < 1 {
		vc.Logger.Debug("no validators in ValidatorUpdates")
		return nil // not really an error
	}
	vc.validatorSetMembers = newValidatorMap(event.ValidatorUpdates)
	if err := vc.updateConnections(); err != nil {
		return fmt.Errorf("inter-validator set connections error: %w", err)
	}
	return nil
}

// selectValidators selects `count` validators from current ValidatorSet.
// It uses random algorithm right now.
// Returns map indexed by validator address.
// TODO ensure selected validator doesn't contain ourselves
func (vc *ValidatorConnExecutor) selectValidators(count int) validatorMap {
	selectedValidators := validatorMap{}
	activeValidators := vc.validatorSetMembers

	validatorSetSize := len(activeValidators)
	if validatorSetSize <= 0 {
		return validatorMap{}
	}

	// We need 1 more Validator in validator set, because one of them is current node
	if (validatorSetSize - 1) < count {
		count = validatorSetSize - 1
	}

	IDs := make([]p2p.ID, 0, len(activeValidators))

	for id := range activeValidators {
		IDs = append(IDs, id)
		// prefer validators that are already connected
		if vc.p2pSwitch.Peers().Has(id) {
			selectedValidators[id] = activeValidators[id]
			vc.Logger.Debug("keeping validator connection",
				"id", id, "address", activeValidators[id].NodeAddress.String())
			// we are good - no need to select new random Validators
			if len(selectedValidators) >= count {
				return selectedValidators
			}
		}
	}

	for len(selectedValidators) < count {
		index := rand.Intn(validatorSetSize)
		id := IDs[index]
		if id == vc.nodeID {
			// we can't connect to ourselves
			continue
		}
		if _, found := selectedValidators[IDs[index]]; !found {
			selectedValidators[id] = activeValidators[id]
			vc.Logger.Debug("selected validator to connect",
				"id", id, "address", activeValidators[id].NodeAddress.String())
		}
	}

	return selectedValidators
}

func (vc *ValidatorConnExecutor) disconnectValidator(validator *types.Validator) error {
	vc.Logger.Debug("disconnect Validator", "validator", validator)
	address := validator.NodeAddress.String()

	err := vc.p2pSwitch.RemovePersistentPeer(address)
	if err != nil {
		return err
	}

	id := validator.NodeAddress.NodeID
	peer := vc.p2pSwitch.Peers().Get(id)
	if peer == nil {
		return fmt.Errorf("cannot stop peer %s: not found", id)
	}

	vc.Logger.Debug("stopping peer", "id", id, "address", address)
	vc.p2pSwitch.StopPeerGracefully(peer)
	return nil
}

// disconnectValidators disconnects connected validators which are not a part of the exceptions map
func (vc *ValidatorConnExecutor) disconnectValidators(exceptions validatorMap) error {
	for currentKey := range vc.connectedValidators {
		if _, isException := exceptions[currentKey]; !isException {
			validator := vc.connectedValidators[currentKey]
			if err := vc.disconnectValidator(validator); err != nil {
				vc.Logger.Error("cannot disconnect Validator", "error", err)
				continue // no return, as we see it as non-fatal
			}
			delete(vc.connectedValidators, currentKey)
		}
	}

	return nil
}

// updateConnections processes current validator set, selects a few validators and schedules connections
// to be established. It will also disconnect previous validators.
func (vc *ValidatorConnExecutor) updateConnections() error {
	if vc.NumConnections <= 0 {
		return fmt.Errorf("number of connections to establish should be positive, is %d", vc.NumConnections)
	}

	// We only do something if we are part of new ValidatorSet
	if _, ok := vc.validatorSetMembers[vc.nodeID]; !ok {
		vc.Logger.Debug("not a member of active ValidatorSet")
		// We need to disconnect connected validators. It needs to be done explicitly
		// because they are marked as persistent and will never disconnect themselves.
		return vc.disconnectValidators(validatorMap{})
	}

	// Find new newValidators
	newValidators := vc.selectValidators(vc.NumConnections)

	// Disconnect existing validators unless they are selected to be connected again
	if err := vc.disconnectValidators(newValidators); err != nil {
		return fmt.Errorf("cannot disconnect unused validators: %w", err)
	}

	// Connect to new validators
	addresses := make([]string, 0, len(newValidators))

	for id, validator := range newValidators {
		if _, alreadyConnected := vc.connectedValidators[id]; alreadyConnected {
			vc.Logger.Debug("validator already connected", "id", id)
			continue
		}

		address, err := validator.NodeAddress.NetAddress()
		if err != nil {
			vc.Logger.Error("cannot parse validator address", "address", validator.NodeAddress, "err", err)
			continue
		}
		addressString := address.String()

		if vc.p2pSwitch.IsDialingOrExistingAddress(address) {
			vc.Logger.Debug("already dialing this validator", "id", id, "address", addressString)
			continue
		}

		addresses = append(addresses, addressString)
		vc.connectedValidators[id] = validator
	}

	if len(addresses) == 0 {
		vc.Logger.P2PDebug("no validators will be dialed")
		return nil
	}

	if err := vc.p2pSwitch.AddPersistentPeers(addresses); err != nil {
		vc.Logger.Error("cannot set validators as persistent", "peers", addresses, "err", err)
		return fmt.Errorf("cannot set validators as persistent: %w", err)
	}

	if err := vc.p2pSwitch.DialPeersAsync(addresses); err != nil {
		vc.Logger.Error("cannot dial validators", "peers", addresses, "err", err)
		return fmt.Errorf("cannot dial peers: %w", err)
	}
	vc.Logger.P2PDebug("connected to Validators", "addresses", addresses)

	return nil
}
