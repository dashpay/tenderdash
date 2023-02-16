package workerpool

import (
	"context"
	"errors"
	"github.com/tendermint/tendermint/libs/log"
)

var (
	ErrWorkerPoolStopped = errors.New("worker-pool has stopped")

	errCannotReadResultChannel = errors.New("cannot read result from a channel")
)

// Job ...
type (
	Job interface {
		Execute(ctx context.Context) Result
	}
	Result struct {
		Value any
		Err   error
	}
	OptionFunc func(*WorkerPool)
)

// WithLogger ...
func WithLogger(logger log.Logger) OptionFunc {
	return func(p *WorkerPool) {
		p.logger = logger
	}
}

func WithResultCh(resultCh chan Result) OptionFunc {
	return func(p *WorkerPool) {
		p.resultCh = resultCh
	}
}

func WithJobCh(jobCh chan Job) OptionFunc {
	return func(p *WorkerPool) {
		p.jobCh = jobCh
	}
}

// WorkerPool ...
type WorkerPool struct {
	initPoolSize int
	maxPoolSize  int
	jobCh        chan Job
	doneCh       chan struct{}
	resultCh     chan Result
	logger       log.Logger
	workers      []*worker
}

// New ...
func New(poolSize int, opts ...OptionFunc) *WorkerPool {
	p := &WorkerPool{
		initPoolSize: poolSize,
		maxPoolSize:  poolSize,
		jobCh:        make(chan Job, poolSize),
		doneCh:       make(chan struct{}),
		resultCh:     make(chan Result, poolSize),
		workers:      make([]*worker, poolSize),
		logger:       log.NewNopLogger(),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *WorkerPool) Add(ctx context.Context, jobs ...Job) error {
	for _, job := range jobs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.doneCh:
			return ErrWorkerPoolStopped
		case p.jobCh <- job:
		}
	}
	return nil
}

func (p *WorkerPool) Receive(ctx context.Context) (Result, error) {
	select {
	case <-ctx.Done():
		// stop
		return Result{}, ctx.Err()
	case <-p.doneCh:
		return Result{}, ErrWorkerPoolStopped
	case res, ok := <-p.resultCh:
		if !ok {
			return Result{}, errCannotReadResultChannel
		}
		return res, nil
	}
}

// Run ...
func (p *WorkerPool) Run(ctx context.Context) {
	for i := 0; i < p.initPoolSize; i++ {
		p.workers[i] = newWorker(i, p.jobCh, p.resultCh, p.logger)
		go p.workers[i].start(ctx)
	}
}

// Stop ...
func (p *WorkerPool) Stop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-p.doneCh:
		return
	default:
		close(p.doneCh)
	}
	for _, w := range p.workers {
		w.stop(ctx)
	}
}

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
