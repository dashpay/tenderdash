package workerpool

import (
	"context"

	"github.com/dashpay/tenderdash/libs/log"
)

type worker struct {
	id        int
	logger    log.Logger
	jobCh     chan *Job
	resultCh  chan Result
	stopCh    <-chan struct{}
	stoppedCh chan struct{}
}

func newWorker(id int, jobCh chan *Job, resultCh chan Result, stopCh <-chan struct{}, logger log.Logger) *worker {
	return &worker{
		id:        id,
		logger:    logger,
		jobCh:     jobCh,
		resultCh:  resultCh,
		stopCh:    stopCh,
		stoppedCh: make(chan struct{}, 1),
	}
}

func (w *worker) start(ctx context.Context) {
	defer func() {
		w.stoppedCh <- struct{}{}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case job, ok := <-w.jobCh:
			if !ok {
				return
			}
			job.SetStatus(JobReceived)
			result := job.Execute(ctx)
			select {
			case <-ctx.Done():
				return
			case <-w.stopCh:
				return
			case w.resultCh <- result:
			}
		}
	}
}

func (w *worker) stop(ctx context.Context) {
	defer func() {
		close(w.stoppedCh)
	}()
	select {
	case <-ctx.Done():
	case <-w.stoppedCh:
	}
}
