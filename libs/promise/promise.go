package promise

import (
	"fmt"

	sync "github.com/sasha-s/go-deadlock"
)

// Promise is a simple implementation of a component that is represented eventual completion or failure
// of asynchronous operation and its resulting val
type Promise[T any] struct {
	mtx    sync.Mutex
	result T
	err    error
	doneCh chan struct{}
}

// New creates and returns a new Promise instance
// the execution function will be called in a separate goroutine and not block the caller's execution process,
// this function manages of execution flow and will decide based on its internal state which function should call
// The "resolve" function is invoked on success otherwise invokes a "reject"
func New[T any](execution func(resolve func(data T), reject func(err error))) *Promise[T] {
	promise := &Promise[T]{
		doneCh: make(chan struct{}),
	}
	go func() {
		defer func() {
			r := recover()
			if r != nil {
				promise.reject(fmt.Errorf("recovered: %v", r))
			}
			close(promise.doneCh)
		}()
		execution(promise.resolve, promise.reject)
	}()
	return promise
}

// Await waits for execution to complete
func (p *Promise[T]) Await() (T, error) {
	<-p.doneCh
	return p.result, p.err
}

func (p *Promise[T]) resolve(val T) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	p.result = val
}

func (p *Promise[T]) reject(err error) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	p.err = err
}

// Then wraps origin promise and use the resolver function to convert origin result value of type M into type T
func Then[T, M any](promise *Promise[T], resolveT func(T) M) *Promise[M] {
	return New(func(resolveM func(M), reject func(error)) {
		resultT, err := promise.Await()
		if err != nil {
			reject(err)
			return
		}
		resolveM(resolveT(resultT))
	})
}
