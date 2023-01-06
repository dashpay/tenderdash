package consensus

const (
	SetProposedAppVersion = iota
	SetPrivValidator
	SetTimeoutTicker
	SetMetrics
	SetReplayMode
)

// Subscriber is a subscriber interface to an observer
// This interface should be implemented by the listener to hide the subscribing process at a subject
type Subscriber interface {
	Subscribe(*Observer)
}

// Observer is a simple observer
type Observer struct {
	subscribers map[int][]func(data any) error
}

// NewObserver creates and returns an observer
func NewObserver() *Observer {
	return &Observer{
		subscribers: make(map[int][]func(data any) error),
	}
}

// Subscribe subscribes to an event
func (o *Observer) Subscribe(eventType int, listener func(any) error) {
	if o.subscribers == nil {
		o.subscribers = make(map[int][]func(data any) error)
	}
	o.subscribers[eventType] = append(o.subscribers[eventType], listener)
}

// Notify notifies subscribers of an event
func (o *Observer) Notify(eventType int, data any) error {
	subs, ok := o.subscribers[eventType]
	if !ok {
		return nil
	}
	for _, sub := range subs {
		err := sub(data)
		if err != nil {
			return err
		}
	}
	return nil
}
