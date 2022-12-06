package consensus

const (
	SetProposedAppVersion = iota
	SetPrivValidator
	SetTimeoutTicker
	SetMetrics
	SetReplayMode
)

type Subscriber interface {
	Subscribe(*Observer)
}

type Observer struct {
	subscribers map[int][]func(data any) error
}

func (o *Observer) Subscribe(eventType int, listener func(any) error) {
	if o.subscribers == nil {
		o.subscribers = make(map[int][]func(data any) error)
	}
	o.subscribers[eventType] = append(o.subscribers[eventType], listener)
}

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
