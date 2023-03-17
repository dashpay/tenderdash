package promise

import (
	"errors"
	"testing"

	sync "github.com/sasha-s/go-deadlock"
	"github.com/stretchr/testify/require"
)

type (
	message[T any] struct {
		val T
		err error
	}
)

func TestExample(t *testing.T) {
	var wg sync.WaitGroup
	errSomthingWentWrong := errors.New("something went wrong")
	msgCh := make(chan message[int])
	defer close(msgCh)

	// create a new promise instance to demonstrate of successful executing
	promise := newPromise(msgCh)
	wg.Add(1)
	go func() {
		defer wg.Done()
		// promise.Await() is awaited executing
		result, err := promise.Await()
		require.NoError(t, err)
		require.Equal(t, 100, result)
	}()
	// to send a message with a value to fulfill the promise
	msgCh <- message[int]{val: 100}
	wg.Wait()

	// create a new promise instance to demonstrate of failure executing
	promise = newPromise(msgCh)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := promise.Await()
		require.EqualError(t, err, errSomthingWentWrong.Error())
	}()
	msgCh <- message[int]{err: errSomthingWentWrong}
	wg.Wait()
}

func newPromise(msgCh chan message[int]) *Promise[int] {
	return New[int](func(resolve func(data int), reject func(err error)) {
		// here we need to put the logic that resolves or rejects this deferred execution
		// for this example is used go channel to simulate asynchronous behavior
		msg := <-msgCh
		// if a message has an error, then will call the reject function, otherwise, the message's value will be resolved
		if msg.err != nil {
			reject(msg.err)
			return
		}
		resolve(msg.val)
	})
}
