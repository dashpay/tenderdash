package sync

import (
	"sync"
	"time"
)

// Waker is used to wake up a sleeper when some event occurs. It debounces
// multiple wakeup calls occurring between each sleep, and wakeups are
// non-blocking to avoid having to coordinate goroutines.
type Waker struct {
	wakeCh chan struct{}
	mtx    sync.Mutex
	timers []*time.Timer
}

// NewWaker creates a new Waker.
func NewWaker() *Waker {
	return &Waker{
		wakeCh: make(chan struct{}, 1), // buffer used for debouncing
	}
}

// Sleep returns a channel that blocks until Wake() is called.
func (w *Waker) Sleep() <-chan struct{} {
	return w.wakeCh
}

// Wake wakes up the sleeper.
func (w *Waker) Wake() {
	// A non-blocking send with a size 1 buffer ensures that we never block, and
	// that we queue up at most a single wakeup call between each Sleep().
	select {
	case w.wakeCh <- struct{}{}:
	default:
	}
}

// WakeAfter wakes up the sleeper after some delay.
func (w *Waker) WakeAfter(delay time.Duration) {
	w.mtx.Lock()
	defer w.mtx.Unlock()

	w.timers = append(w.timers, time.AfterFunc(delay, w.Wake))
}

// Close closes the waker and cleans up its resources
func (w *Waker) Close() error {
	w.mtx.Lock()
	defer w.mtx.Unlock()

	for _, timer := range w.timers {
		if timer != nil {
			timer.Stop()
		}
	}
	return nil
}
