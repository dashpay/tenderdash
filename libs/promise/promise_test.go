package promise

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
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
			exec: func(resolve func(data data), reject func(err error)) {
				resolve(data{value: 10})
			},
			wantVal: 10,
		},
		{
			exec: func(resolve func(data data), reject func(err error)) {
				reject(errors.New("reject"))
			},
			wantErr: "reject",
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test-case %d", i), func(t *testing.T) {
			p := New(tc.exec)
			res, err := p.Await()
			if tc.wantErr != "" {
				require.Errorf(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantVal, res.value)
		})
	}
}
