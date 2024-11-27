package workerpool

import (
	"context"
	"errors"
	"sync/atomic"

	sync "github.com/sasha-s/go-deadlock"

	"github.com/dashpay/tenderdash/libs/log"
	tmmath "github.com/dashpay/tenderdash/libs/math"
)

var (
	ErrWorkerPoolStopped = errors.New("worker-pool has stopped")

	ErrCannotReadResultChannel = errors.New("cannot read result from a channel")
)

const (
	JobCreated JobStatus = iota
	JobSending
	JobSent
	JobReceived
	JobExecuting
	JobExecuted
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
	JobStatus int
	// Job wraps the job-handler that will be executed on a worker.
	// Once the worker consumes the Job from the job-channel, it will execute the job's handler
	// and the obtained execution Result will be published on the result channel
	Job struct {
		status  atomic.Int32
		Handler JobHandler
	}
	// JobHandler is a function that must be wrapped by a Job
	JobHandler func(ctx context.Context) Result
	// Result this structure is the result of the job and will be sent to main goroutine via result channel
	Result struct {
		Value any
		Err   error
	}
	// OptionFunc is function type for a worker-pool optional functions
	OptionFunc func(*WorkerPool)
)

// String returns string representation of JobStatus
func (j JobStatus) String() string {
	switch j {
	case JobCreated:
		return "created"
	case JobSending:
		return "sending"
	case JobSent:
		return "sent"
	case JobReceived:
		return "received"
	case JobExecuting:
		return "executing"
	case JobExecuted:
		return "executed"
	}
	return "unknown"
}

// NewJob creates a new Job instance with the job's handler
func NewJob(handler JobHandler) *Job {
	job := &Job{Handler: handler}
	job.SetStatus(JobCreated)
	return job
}

// Execute invokes the job's handler
func (j *Job) Execute(ctx context.Context) Result {
	j.SetStatus(JobExecuting)
	defer j.SetStatus(JobExecuted)
	return j.Handler(ctx)
}

func (j *Job) Status() JobStatus {
	status := j.status.Load()
	return JobStatus(status)
}

func (j *Job) SetStatus(status JobStatus) {
	j.status.Swap(tmmath.MustConvertInt32(status))
}

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
func WithJobCh(jobCh chan *Job) OptionFunc {
	return func(p *WorkerPool) {
		p.jobCh = jobCh
	}
}

// WorkerPool is an implementation of a component that allows creating a set of workers
// to process arbitrary jobs in background
type WorkerPool struct {
	initPoolSize int
	jobCh        chan *Job
	wg           sync.WaitGroup
	wgMtx        sync.Mutex
	stopped      atomic.Bool
	doneCh       chan struct{}
	resultCh     chan Result
	logger       log.Logger
	workers      []*worker
}

// New creates, initializes and returns a new WorkerPool instance
func New(poolSize int, opts ...OptionFunc) *WorkerPool {
	p := &WorkerPool{
		initPoolSize: poolSize,
		jobCh:        make(chan *Job, poolSize),
		resultCh:     make(chan Result, poolSize),
		doneCh:       make(chan struct{}),
		logger:       log.NewNopLogger(),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Send sends one or many jobs to the workers via job channel
func (p *WorkerPool) Send(ctx context.Context, jobs ...*Job) error {
	if p.stopped.Load() {
		return ErrWorkerPoolStopped
	}
	p.wgMtx.Lock()
	p.wg.Add(1)
	p.wgMtx.Unlock()
	defer p.wg.Done()
	for _, job := range jobs {
		if p.stopped.Load() {
			return ErrWorkerPoolStopped
		}
		job.SetStatus(JobSending)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.doneCh:
			return ErrWorkerPoolStopped
		case p.jobCh <- job:
			job.SetStatus(JobSent)
		}
	}
	return nil
}

// Receive waits for a job result from a worker via result channel
func (p *WorkerPool) Receive(ctx context.Context) (Result, error) {
	if p.stopped.Load() {
		return Result{}, ErrWorkerPoolStopped
	}
	select {
	case <-ctx.Done():
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
	if p.stopped.Swap(false) {
		return
	}
	if len(p.workers) == 0 {
		p.initWorkers()
	}
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
	if p.stopped.Swap(true) {
		return
	}
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
	done := make(chan struct{})
	go func() {
		for range p.jobCh {
		}
		close(done)
	}()
	p.wg.Wait()
	close(p.jobCh)
	close(p.resultCh)
	<-done
}

// Reset resets some data to initial values, among with these are
func (p *WorkerPool) Reset() {
	if !p.stopped.Swap(false) {
		return
	}
	p.doneCh = make(chan struct{})
	p.jobCh = make(chan *Job, p.initPoolSize)
	p.resultCh = make(chan Result, p.initPoolSize*2)
	for i := 0; i < p.initPoolSize; i++ {
		p.workers[i] = newWorker(i, p.jobCh, p.resultCh, p.doneCh, p.logger)
	}
}

func (p *WorkerPool) initWorkers() {
	p.workers = make([]*worker, p.initPoolSize)
	for i := 0; i < p.initPoolSize; i++ {
		p.workers[i] = newWorker(i, p.jobCh, p.resultCh, p.doneCh, p.logger)
	}
}
