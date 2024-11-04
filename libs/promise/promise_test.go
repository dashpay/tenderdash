package promise

import (
	"errors"
	"fmt"
	"strconv"
	"testing"

	sync "github.com/sasha-s/go-deadlock"
	"github.com/stretchr/testify/require"

	tmrequire "github.com/dashpay/tenderdash/internal/test/require"
)

func TestPromise(t *testing.T) {
	type data struct {
		value int
	}
	testCases := []struct {
		exec    func(resolve func(data data), reject func(err error))
		wantVal int
		wantErr string
	}{
		{
			exec: func(resolve func(data data), _ func(err error)) {
				resolve(data{value: 10})
			},
			wantVal: 10,
		},
		{
			exec: func(_ func(data data), reject func(err error)) {
				reject(errors.New("reject"))
			},
			wantErr: "reject",
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			p := New(tc.exec)
			res, err := p.Await()
			tmrequire.Error(t, tc.wantErr, err)
			require.Equal(t, tc.wantVal, res.value)
		})
	}
}

func TestThen(t *testing.T) {
	type (
		T message[int]
		M message[string]
	)
	promise := New[T](func(resolve func(data T), _ func(err error)) {
		resolve(T{val: 100})
	})
	wrappedPromise := Then[T, M](promise, func(msg T) M {
		return M{val: strconv.Itoa(msg.val)}
	})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, err := wrappedPromise.Await()
		require.NoError(t, err)
		require.Equal(t, "100", res.val)
	}()
	wg.Wait()
}
