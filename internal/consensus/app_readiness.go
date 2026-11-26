package consensus

import (
	"context"
	"sync"
)

// appReadiness asks whether the application answers, for the proposer
// heartbeat, with one probe in flight at a time.
//
// A probe whose caller stops waiting stays in flight until the application
// answers it or life ends, and later callers wait on that probe instead of
// sending another. The socket ABCI client keeps each request it sent until the
// answer comes, since answers arrive in the order the requests went out. An
// application that takes requests and leaves them unanswered would otherwise
// get one more probe every heartbeat interval for as long as it hangs.
type appReadiness struct {
	probe func(ctx context.Context) error

	mtx      sync.Mutex
	inflight *readinessProbe
}

// readinessProbe is one probe, and its answer once done is closed.
type readinessProbe struct {
	done chan struct{}
	err  error
}

func newAppReadiness(probe func(ctx context.Context) error) *appReadiness {
	return &appReadiness{probe: probe}
}

// check reports whether the application answers before ctx ends. A probe it
// starts runs under life, which outlives ctx.
func (r *appReadiness) check(life, ctx context.Context) error {
	r.mtx.Lock()
	probe := r.inflight
	if probe == nil {
		probe = &readinessProbe{done: make(chan struct{})}
		r.inflight = probe
		go r.run(life, probe)
	}
	r.mtx.Unlock()

	select {
	case <-probe.done:
		return probe.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *appReadiness) run(life context.Context, probe *readinessProbe) {
	err := r.probe(life)

	r.mtx.Lock()
	r.inflight = nil
	r.mtx.Unlock()

	probe.err = err
	close(probe.done)
}
