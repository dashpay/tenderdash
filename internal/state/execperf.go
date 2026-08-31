package state

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

// execPerf accumulates the wall-clock cost of each stage of ApplyBlock, so the
// per-block cost of block sync can be attributed to the application, to our own
// durable writes, or to signature verification. Enabled with TD_BLOCK_PERF=1.
type execPerf struct {
	mtx    sync.Mutex
	on     bool
	n      int
	every  int
	totals map[string]time.Duration
}

var perf = newExecPerf()

func newExecPerf() *execPerf {
	return &execPerf{
		on:     os.Getenv("TD_BLOCK_PERF") == "1",
		every:  100,
		totals: map[string]time.Duration{},
	}
}

type lap struct {
	t  time.Time
	on bool
}

func startLap() lap { return lap{t: time.Now(), on: perf.on} }

func (l *lap) done(name string) {
	if !l.on {
		return
	}
	now := time.Now()
	perf.add(name, now.Sub(l.t))
	l.t = now
}

// RecordPerf adds a duration to the block-sync stage totals from outside this
// package. No-op unless TD_BLOCK_PERF=1.
func RecordPerf(name string, d time.Duration) {
	if !perf.on {
		return
	}
	perf.add(name, d)
}

func (p *execPerf) add(name string, d time.Duration) {
	p.mtx.Lock()
	p.totals[name] += d
	p.mtx.Unlock()
}

// block records that one more block finished, and periodically reports the mean
// cost of each stage in milliseconds, resetting the counters.
func (p *execPerf) block(height int64, log interface{ Info(string, ...any) }) {
	if !p.on {
		return
	}
	p.mtx.Lock()
	p.n++
	if p.n < p.every {
		p.mtx.Unlock()
		return
	}
	n := p.n
	names := make([]string, 0, len(p.totals))
	for k := range p.totals {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]any, 0, len(names)*2)
	for _, k := range names {
		parts = append(parts, k, fmt.Sprintf("%.3f", float64(p.totals[k].Microseconds())/float64(n)/1000.0))
		p.totals[k] = 0
	}
	p.n = 0
	p.mtx.Unlock()
	log.Info("exec perf", append([]any{"height", height, "blocks", n}, parts...)...)
}
