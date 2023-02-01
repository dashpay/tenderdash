package eventemitter

import (
	sync "github.com/sasha-s/go-deadlock"

	"github.com/tendermint/tendermint/libs/log"
)

// ListenerFunc is a type function of an event listener
type ListenerFunc func(data EventData) error

// EventData is a generic event data can be typed and registered with
// tendermint/go-amino via concrete implementation of this interface.
type EventData any

// Subscriber is the interface reactors and other modules must export to become
// eventable.
type Subscriber interface {
	Subscribe(emitter *EventEmitter)
}

// EventEmitter is a simple implementation of event emitter, where listeners
// subscribe to certain events and, when an event is emitted (see Emitter),
// notified via a callback function
type EventEmitter struct {
	mtx    sync.RWMutex
	logger log.Logger
	events map[string][]ListenerFunc
}

// OptionFunc is an option function to be able to set custom values to event emitter
type OptionFunc func(*EventEmitter)

// WithLogger sets logger to event emitter
func WithLogger(logger log.Logger) OptionFunc {
	return func(emitter *EventEmitter) {
		emitter.logger = logger
	}
}

// New creates and returns a new event emitter instance
func New(opts ...OptionFunc) *EventEmitter {
	emitter := &EventEmitter{
		logger: log.NewNopLogger(),
		events: make(map[string][]ListenerFunc),
	}
	for _, opt := range opts {
		opt(emitter)
	}
	return emitter
}

// AddListener adds a listener function to the event set, that will be called during Emit operation
func (e *EventEmitter) AddListener(event string, listener ListenerFunc) {
	e.mtx.Lock()
	defer e.mtx.Unlock()
	e.events[event] = append(e.events[event], listener)
}

// Emit synchronously invokes all registered listeners for the given event name and data
func (e *EventEmitter) Emit(event string, data EventData) {
	e.mtx.RLock()
	listeners := e.events[event][:]
	e.mtx.RUnlock()
	for _, listener := range listeners {
		err := listener(data)
		if err != nil {
			e.logger.Error("got error from a listener", "error", err)
		}
	}
}
