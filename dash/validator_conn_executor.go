package dash

import (
	"context"
	"fmt"
	"time"

	"github.com/tendermint/tendermint/libs/cmap"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/rand"
	"github.com/tendermint/tendermint/libs/service"
	"github.com/tendermint/tendermint/libs/sync"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/types"
)

const ValidatorConnExecutorsSubscriber = "ValidatorConnExecutors"

// ValidatorConnExecutor retrieves validator update events and establishes new validator connections.
type ValidatorConnExecutor struct {
	ctx context.Context
	service.BaseService
	eventBus     *types.EventBus
	p2pSwitch    *p2p.Switch
	subscription types.Subscription

	activeValidators    []*types.Validator // Validators that are active in the current Validator Set
	activeValidatorsMux sync.RWMutex       // mutex for accessing `activeValidators`

	// TODO: dialTimers can be normal map[string]*time.Timer, as the `dialTimersMux` ensures it's thread safe
	dialTimers    *cmap.CMap // Scheduled validator connections, contains `*time.Timer` objects
	dialTimersMux sync.Mutex // Mutex for `dialTimers`

	// configuration

	NumConnections int // number of connections to establish
	MaxRetries     int // Maximum number a connection will be retried
}

func NewValidatorConnExecutor(
	ctx context.Context, eventBus *types.EventBus, sw *p2p.Switch, logger log.Logger) *ValidatorConnExecutor {

	vc := &ValidatorConnExecutor{
		ctx:              ctx,
		eventBus:         eventBus,
		p2pSwitch:        sw,
		NumConnections:   3,
		MaxRetries:       3,
		dialTimers:       cmap.NewCMap(),
		activeValidators: make([]*types.Validator, 0),
	}
	baseService := service.NewBaseService(logger, ValidatorConnExecutorsSubscriber, vc)
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
		vc.BaseService.Logger.Error("ValidatorConnExecutor goroutine finished", "reason", err)
	}()

	return nil
}

// OnStop implements Service to clean up (unsubscribe from all events, stop timers, etc.)
func (vc *ValidatorConnExecutor) OnStop() {
	if vc.eventBus != nil {
		err := vc.eventBus.UnsubscribeAll(vc.ctx, ValidatorConnExecutorsSubscriber)
		if err != nil {
			vc.BaseService.Logger.Error("cannot unsubscribe from channels", "error", err)
		}
		vc.eventBus = nil
	}

	vc.clearScheduledDials(true)
}

// subscribe subscribes to event bus to receive validator update messages
func (vc *ValidatorConnExecutor) subscribe() error {
	updatesSub, err := vc.eventBus.Subscribe(
		vc.ctx,
		ValidatorConnExecutorsSubscriber,
		types.EventQueryValidatorSetUpdates,
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

	vc.BaseService.Logger.Debug("ValidatorConnExecutor: waiting for an event")

	select {
	case msg := <-vc.subscription.Out():
		event, ok := msg.Data().(types.EventDataValidatorSetUpdates)
		if !ok {
			return fmt.Errorf("invalid type of validator set update message: %T", event)
		}
		if len(event.ValidatorUpdates) < 1 {
			vc.BaseService.Logger.Debug("ValidatorConnExecutor: no validators in ValidatorUpdates")
			return nil // non-fatal, so no error returned
		}

		// TODO process validators only if our node belongs to active validator set

		if err := vc.parseValidatorUpdate(event.ValidatorUpdates); err != nil {
			vc.BaseService.Logger.Error("ValidatorConnExecutor: error when processing validator updates", "error", err)
			return nil // non-fatal, so no error returned
		}
		vc.clearScheduledDials(false)
		if err := vc.establishConnections(); err != nil {
			vc.BaseService.Logger.Error("ValidatorConnExecutors: error when scheduling validator connections", "error", err)
			return nil // non-fatal, so no error returned
		}

		vc.BaseService.Logger.Debug("ValidatorConnExecutors: validator updates processed successfully", "event", event)

	case <-vc.subscription.Cancelled():
		return fmt.Errorf("subscription cancelled due to error: %w", vc.subscription.Err())
	case <-vc.BaseService.Quit():
		return fmt.Errorf("quit signal received")
	case <-vc.ctx.Done():
		return fmt.Errorf("context expired: %w", vc.ctx.Err())
	}

	return nil
}

// parseValidatorUpdate parses received validator update and saves active validator set
func (vc *ValidatorConnExecutor) parseValidatorUpdate(validators []*types.Validator) error {
	vc.activeValidatorsMux.Lock()
	defer vc.activeValidatorsMux.Unlock()

	vc.activeValidators = make([]*types.Validator, len(validators))
	for i, validator := range validators {
		vc.activeValidators[i] = validator
	}

	return nil
}

// selectValidators selects `count` validators from current ValidatorSet.
// It uses random algorithm right now.
// TODO ensure selected validator doesn't contain ourselves
func (vc *ValidatorConnExecutor) selectValidators(count int) []*types.Validator {
	vc.activeValidatorsMux.RLock()
	defer vc.activeValidatorsMux.RUnlock()

	ret := make([]*types.Validator, 0, count)
	size := len(vc.activeValidators)
	if size < count {
		count = size
	}

	selectedMap := map[int]bool{}

	for len(ret) < count {
		index := rand.Intn(size)
		if !selectedMap[index] {
			selectedMap[index] = true
			ret = append(ret, vc.activeValidators[index])
		}
	}

	return ret
}

// establishConnections processes current validator set, selects a few validators and schedules connections
// to be established.
func (vc *ValidatorConnExecutor) establishConnections() error {

	// TODO this needs to be smarter, eg. disconnect already connected validators that are not selected for inclusion

	if vc.NumConnections <= 0 {
		return fmt.Errorf("number of connections to establish should be positive, is %d", vc.NumConnections)
	}

	validators := vc.selectValidators(vc.NumConnections)
	for _, validator := range validators {
		address, err := validator.Address.NetAddress()
		if err != nil {
			vc.BaseService.Logger.Error("cannot parse validator address", "address", validator.Address, "err", err)
			continue
		}
		if vc.p2pSwitch.IsDialingOrExistingAddress(address) {
			continue
		}
		vc.Logger.Info("Will dial address", "addr", address)
		vc.scheduleDial(validator, time.Millisecond, vc.MaxRetries)
	}

	return nil
}

// scheduleDial schedules new connection to be established after `delay` passes.
func (vc *ValidatorConnExecutor) scheduleDial(v *types.Validator, delay time.Duration, maxAttempts int) {
	vc.dialTimersMux.Lock()
	defer vc.dialTimersMux.Unlock()

	if vc.dialTimers == nil {
		vc.BaseService.Logger.Error("Cannot schedule validator dial", "reason", "Dial timers is nil, are we shutting down?")
		return
	}

	timer := time.AfterFunc(delay, func() { vc.dial(v, maxAttempts) })

	vc.dialTimers.Set(v.Address.NodeID, timer)
}

// unscheduleDial stops the timer and removes scheduled dial (if any).
func (vc *ValidatorConnExecutor) unscheduleDial(v *types.Validator) {
	vc.dialTimersMux.Lock()
	defer vc.dialTimersMux.Unlock()

	if vc.dialTimers == nil {
		vc.BaseService.Logger.Debug("Cannot unschedule validator dial", "reason", "Dial timers is nil, are we shutting down?")
		return
	}

	item := vc.dialTimers.Get(v.Address.NodeID)
	if timer, ok := item.(*time.Timer); ok && timer != nil {
		timer.Stop()
	}

	vc.dialTimers.Delete(v.Address.NodeID)
}

// clearScheduledDials stops all timers generated by scheduleDial and deletes them.
// if `onStop` is true, we will also stop the scheduling mechanism to avoid scheduling further events.
func (vc *ValidatorConnExecutor) clearScheduledDials(onStop bool) {
	vc.dialTimersMux.Lock()
	defer vc.dialTimersMux.Unlock()

	if vc.dialTimers == nil {
		vc.BaseService.Logger.Debug("Cannot clear validator dials", "reason", "Dial timers is nil, are we shutting down?")
		return
	}

	items := vc.dialTimers.Values()
	for _, item := range items {
		timer := item.(*time.Timer)
		if timer != nil {
			timer.Stop()
		}
	}

	vc.dialTimers.Clear()

	if onStop {
		vc.dialTimers = nil
	}
}

// dial dials the Validator as long as `retriesLeft` is positive. On error, it schedules a retry.
func (vc *ValidatorConnExecutor) dial(validator *types.Validator, retriesLeft int) {
	const retryTime = 3 * time.Second

	if retriesLeft > 0 {

		// validPeers = append(validPeers, val.validator)
		addr, err := validator.Address.NetAddress()
		if err != nil {
			vc.BaseService.Logger.Error("cannot generate validator NetAddress",
				"address", validator.Address.String(), "error", err)
			vc.unscheduleDial(validator)
			return
		}

		err = vc.p2pSwitch.DialPeerWithAddress(addr)
		switch err.(type) {
		case nil:
			vc.BaseService.Logger.Debug("connected to validator",
				"address", validator.Address.String(), "error", err)
		case p2p.ErrCurrentlyDialingOrExistingAddress:
			vc.BaseService.Logger.Debug("validator already connected",
				"address", validator.Address.String(), "error", err)
		default:
			vc.BaseService.Logger.Debug("cannot connect to validator",
				"address", validator.Address.String(), "error", err)
			vc.scheduleDial(validator, retryTime, retriesLeft-1)
		}
	} else {
		vc.unscheduleDial(validator)
	}
}
