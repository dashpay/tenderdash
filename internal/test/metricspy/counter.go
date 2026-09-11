package metricspy

import (
	"sync"

	"github.com/go-kit/kit/metrics"
)

// Counter sums everything added to it, whatever the labels, so a test can read
// how often a code path was taken. Every counter derived through With shares
// the same total.
type Counter struct {
	mtx   *sync.Mutex
	total *float64
}

// NewCounter returns a counter at zero.
func NewCounter() *Counter {
	return &Counter{mtx: &sync.Mutex{}, total: new(float64)}
}

func (c *Counter) With(_ ...string) metrics.Counter { return c }

func (c *Counter) Add(delta float64) {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	*c.total += delta
}

// Value returns the sum of everything added so far.
func (c *Counter) Value() float64 {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	return *c.total
}
