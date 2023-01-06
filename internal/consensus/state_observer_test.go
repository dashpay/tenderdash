package consensus

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestObserver(t *testing.T) {
	const (
		testEvent1 = iota
		testEvent2
	)
	observer := NewObserver()
	called := false
	observer.Subscribe(testEvent1, func(a any) error {
		called = a.(bool)
		return nil
	})
	require.False(t, called)
	err := observer.Notify(testEvent1, true)
	require.NoError(t, err)
	require.True(t, called)
	err = observer.Notify(testEvent2, true)
	require.NoError(t, err)
	observer.Subscribe(testEvent2, func(a any) error {
		return errors.New("error")
	})
	err = observer.Notify(testEvent2, nil)
	require.Error(t, err)
}
