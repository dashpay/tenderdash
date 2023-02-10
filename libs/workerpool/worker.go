package workerpool

import (
	"context"
	"errors"

	"github.com/tendermint/tendermint/libs/log"
)

var (
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
	resultCh     chan Result
	logger       log.Logger
	workers      []*worker

	pendingJobs int
}

// New ...
func New(poolSize int, opts ...OptionFunc) *WorkerPool {
	p := &WorkerPool{
		initPoolSize: poolSize,
		maxPoolSize:  poolSize,
		jobCh:        make(chan Job, poolSize),
		resultCh:     make(chan Result, poolSize),
		logger:       log.NewNopLogger(),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *WorkerPool) Add(jobs ...Job) {
	for _, job := range jobs {
		p.jobCh <- job
	}
}

func (p *WorkerPool) Receive(ctx context.Context) (Result, error) {
	select {
	case <-ctx.Done():
		// stop
		return Result{}, ctx.Err()
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
		w := newWorker(p.jobCh, p.resultCh, p.logger)
		p.workers = append(p.workers, w)
		go w.start(ctx)
	}
}

// Stop ...
func (p *WorkerPool) Stop() {
	for _, w := range p.workers {
		w.stop()
	}
}

type worker struct {
	logger   log.Logger
	jobCh    chan Job
	resultCh chan Result
	quitCh   chan struct{}
	quitedCh chan struct{}
}

func newWorker(jobCh chan Job, resultCh chan Result, logger log.Logger) *worker {
	return &worker{
		logger:   logger,
		jobCh:    jobCh,
		resultCh: resultCh,
		quitCh:   make(chan struct{}),
		quitedCh: make(chan struct{}),
	}
}

func (w *worker) start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.quitCh:
			w.quitedCh <- struct{}{}
			return
		case job, ok := <-w.jobCh:
			if !ok {
				return
			}
			w.resultCh <- job.Execute(ctx)
		}
	}
}

func (w *worker) stop() {
	close(w.quitCh)
	<-w.quitedCh
	close(w.quitedCh)
}
