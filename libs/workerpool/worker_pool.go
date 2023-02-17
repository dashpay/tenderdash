package workerpool

import (
	"context"
	"errors"
	"github.com/tendermint/tendermint/libs/log"
)

var (
	ErrWorkerPoolStopped = errors.New("worker-pool has stopped")

	ErrCannotReadResultChannel = errors.New("cannot read result from a channel")
)

type (
	// JobSender is an interface that wraps basic job sender methods
	JobSender interface {
		Send(ctx context.Context, jobs ...Job) error
	}
	// JobReceiver is an interface that wraps basic job receiver methods
	JobReceiver interface {
		Receive(ctx context.Context) (Result, error)
	}
	// Job is an interface that wraps the job methods that a job must implement to be executed on a worker
	Job interface {
		Execute(ctx context.Context) Result
	}
	// Result this structure is the result of the job and will be sent to main goroutine via result channel
	Result struct {
		Value any
		Err   error
	}
	// OptionFunc is function type for a worker-pool optional functions
	OptionFunc func(*WorkerPool)
)

// WithLogger sets a logger to worker-pool using option function
func WithLogger(logger log.Logger) OptionFunc {
	return func(p *WorkerPool) {
		p.logger = logger
	}
}

// WithResultCh sets a result channel to worker-pool using option function
func WithResultCh(resultCh chan Result) OptionFunc {
	return func(p *WorkerPool) {
		p.resultCh = resultCh
	}
}

// WithJobCh sets a job channel to worker-pool using option function
func WithJobCh(jobCh chan Job) OptionFunc {
	return func(p *WorkerPool) {
		p.jobCh = jobCh
	}
}

// WorkerPool is an implementation of a component that allows creating a set of workers
// to process arbitrary jobs in background
type WorkerPool struct {
	initPoolSize int
	maxPoolSize  int
	jobCh        chan Job
	doneCh       chan struct{}
	resultCh     chan Result
	logger       log.Logger
	workers      []*worker
}

// New creates, initializes and returns a new WorkerPool instance
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
	for i := 0; i < p.initPoolSize; i++ {
		p.workers[i] = newWorker(i, p.jobCh, p.resultCh, p.logger)
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Send sends one or many jobs to the workers via job channel
func (p *WorkerPool) Send(ctx context.Context, jobs ...Job) (err error) {
	defer func() {
		if recover() != nil {
			// this might happen if we try to send a job to the closed channel
			err = ErrWorkerPoolStopped
		}
	}()
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

// Receive waits for a job result from a worker via result channel
func (p *WorkerPool) Receive(ctx context.Context) (Result, error) {
	select {
	case <-ctx.Done():
		// stop
		return Result{}, ctx.Err()
	case <-p.doneCh:
		return Result{}, ErrWorkerPoolStopped
	case res, ok := <-p.resultCh:
		if !ok {
			return Result{}, ErrCannotReadResultChannel
		}
		return res, nil
	}
}

// Start starts a pool of workers to process jobs
func (p *WorkerPool) Start(ctx context.Context) {
	for i := 0; i < p.initPoolSize; i++ {
		go p.workers[i].start(ctx)
	}
}

// Run resets and starts a pool of workers to process jobs
func (p *WorkerPool) Run(ctx context.Context) {
	p.Reset()
	p.Start(ctx)
}

// Stop stops the worker-pool and all dependent workers
func (p *WorkerPool) Stop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-p.doneCh:
		return
	default:
		close(p.doneCh)
		defer close(p.jobCh)
		defer close(p.resultCh)
	}
	for _, w := range p.workers {
		w.stop(ctx)
	}
}

// Reset resets some data to initial values, among with these are
func (p *WorkerPool) Reset() {
	p.doneCh = make(chan struct{}, 1)
	p.jobCh = make(chan Job, p.initPoolSize)
	p.resultCh = make(chan Result, p.initPoolSize)
	p.workers = make([]*worker, p.initPoolSize)
	for i := 0; i < p.initPoolSize; i++ {
		p.workers[i] = newWorker(i, p.jobCh, p.resultCh, p.logger)
	}
}
