package abciclient

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/go-kit/kit/metrics"
)

const (
	// MetricsSubsystem is a subsystem shared by all metrics exposed by this
	// package.
	MetricsSubsystem = "abci"
)

var (
	// valueToLabelRegexp is used to find the golang package name and type name
	// so that the name can be turned into a prometheus label where the characters
	// in the label do not include prometheus special characters such as '*' and '.'.
	valueToLabelRegexp = regexp.MustCompile(`\*?(\w+)\.(.*)`)
)

//go:generate go run ../../scripts/metricsgen -struct=Metrics

// Metrics contains metrics exposed by this package.
type Metrics struct {
	// Number of messages in ABCI Socket queue
	QueuedMessages metrics.Gauge `metrics_labels:"type, priority"`

	// labels cache
	labels metricsLabelCache
}

type metricsLabelCache struct {
	mtx               sync.RWMutex
	messageLabelNames map[reflect.Type]string
}

func (m *Metrics) EnqueuedMessage(reqres *requestAndResponse) {
	priority := strconv.Itoa(int(reqres.priority()))
	typ := "nil"
	if reqres != nil && reqres.Request != nil {
		typ = m.labels.ValueToMetricLabel(reqres.Request.Value)
	}

	m.QueuedMessages.With("type", typ, "priority", priority).Add(1)
}

func (m *Metrics) DequeuedMessage(reqres *requestAndResponse) {
	priority := strconv.Itoa(int(reqres.priority()))
	typ := "nil"
	if reqres != nil && reqres.Request != nil {
		typ = m.labels.ValueToMetricLabel(reqres.Request.Value)
	}

	m.QueuedMessages.With("type", typ, "priority", priority).Add(-1)
}

// ValueToMetricLabel is a method that is used to produce a prometheus label value of the golang
// type that is passed in.
// This method uses a map on the Metrics struct so that each label name only needs
// to be produced once to prevent expensive string operations.
func (m *metricsLabelCache) ValueToMetricLabel(i interface{}) string {
	if m.messageLabelNames == nil {
		m.mtx.Lock()
		m.messageLabelNames = map[reflect.Type]string{}
		m.mtx.Unlock()
	}

	t := reflect.TypeOf(i)
	m.mtx.RLock()

	if s, ok := m.messageLabelNames[t]; ok {
		m.mtx.RUnlock()
		return s
	}
	m.mtx.RUnlock()

	s := t.String()
	ss := valueToLabelRegexp.FindStringSubmatch(s)
	l := fmt.Sprintf("%s_%s", ss[1], ss[2])
	m.mtx.Lock()
	defer m.mtx.Unlock()
	m.messageLabelNames[t] = l
	return l
}
