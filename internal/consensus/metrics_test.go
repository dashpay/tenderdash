package consensus

import "github.com/go-kit/kit/metrics"

type recordingCounter struct {
	value float64
}

func (c *recordingCounter) With(...string) metrics.Counter {
	return c
}

func (c *recordingCounter) Add(delta float64) {
	c.value += delta
}
