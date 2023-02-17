package workerpool

import (
	"context"

	"github.com/tendermint/tendermint/libs/log"
)

type worker struct {
	id       int
	logger   log.Logger
	jobCh    chan Job
	resultCh chan Result
	quitCh   chan struct{}
	quitedCh chan struct{}
}

func newWorker(id int, jobCh chan Job, resultCh chan Result, logger log.Logger) *worker {
	return &worker{
		id:       id,
		logger:   logger,
		jobCh:    jobCh,
		resultCh: resultCh,
		quitCh:   make(chan struct{}),
		quitedCh: make(chan struct{}, 1),
	}
}

func (w *worker) start(ctx context.Context) {
	defer func() {
		w.quitedCh <- struct{}{}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.quitCh:
			return
		case job, ok := <-w.jobCh:
			if !ok {
				return
			}
			result := job.Execute(ctx)
			select {
			case <-ctx.Done():
				return
			case <-w.quitCh:
				return
			case w.resultCh <- result:
			}
		}
	}
}

func (w *worker) stop(ctx context.Context) {
	close(w.quitCh)
	defer func() {
		close(w.quitedCh)
	}()
	select {
	case <-ctx.Done():
	case <-w.quitedCh:
	}
}

func (w *worker) reset() {
	w.quitCh = make(chan struct{})
	w.quitedCh = make(chan struct{}, 1)
}

func isClosed[T any](ch chan T) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
