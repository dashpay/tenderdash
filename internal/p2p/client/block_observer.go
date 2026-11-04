package client

import (
	"context"
	"sync"
)

// BlockRequestObserver observes client admission and the end of waiting for a
// correlated reply. Callbacks must be short, nonblocking bookkeeping operations.
// Admission is not wire delivery; local transport/router queues remain included.
// Finished runs once after Started, before promise delivery, even on cancellation.
type BlockRequestObserver interface {
	Started()
	Finished()
}

type blockObserverKey struct{}

// WithBlockRequestObserver attaches an observer to one GetBlock call.
func WithBlockRequestObserver(ctx context.Context, observer BlockRequestObserver) context.Context {
	return context.WithValue(ctx, blockObserverKey{}, observer)
}

// pendingResponse serializes admission, the first response and retirement. A
// reply can arrive while Send is returning; in that case its active time is zero.
type pendingResponse struct {
	mtx      sync.Mutex
	ch       chan result
	observer BlockRequestObserver
	admitted bool
	settled  bool
}

func (p *pendingResponse) admit() {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	p.admitted = true
	if p.observer != nil {
		p.observer.Started()
		if p.settled {
			p.observer.Finished()
		}
	}
}

func (p *pendingResponse) finish() {
	if !p.settled {
		p.settled = true
		if p.admitted && p.observer != nil {
			p.observer.Finished()
		}
	}
}

func (p *pendingResponse) deliver(res result) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if p.settled {
		return
	}
	p.finish()
	select {
	case p.ch <- res:
	default:
	}
}

func (p *pendingResponse) retire() {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	p.finish()
}
