// Package metricspy provides metrics fakes that record what was observed.
package metricspy

import "github.com/go-kit/kit/metrics"

// Histogram records every observation under the value of one label, so a test
// can check both which label value a sample was attributed to and how large it
// was. Samples is shared by every histogram derived through With.
type Histogram struct {
	Samples map[string][]float64
	label   string
	key     string
}

// NewHistogram returns a histogram that groups observations by the value of
// the named label. Observations made without that label land under "".
func NewHistogram(label string) *Histogram {
	return &Histogram{Samples: map[string][]float64{}, label: label}
}

func (h *Histogram) With(labelValues ...string) metrics.Histogram {
	next := &Histogram{Samples: h.Samples, label: h.label, key: h.key}
	for i := 0; i+1 < len(labelValues); i += 2 {
		if labelValues[i] == h.label {
			next.key = labelValues[i+1]
		}
	}
	return next
}

func (h *Histogram) Observe(value float64) {
	h.Samples[h.key] = append(h.Samples[h.key], value)
}
