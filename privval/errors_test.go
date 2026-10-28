package privval

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dashpay/tenderdash/types"
)

// A request the remote signer cannot serve at all is reported as
// types.ErrSigningNotSupported, so consensus stops asking for it; any other
// remote signer error is not.
func TestRemoteSignerErrorSigningNotSupported(t *testing.T) {
	notImplemented := &RemoteSignerError{Code: remoteSignerNotImplemented, Description: "cannot sign heartbeats"}
	assert.ErrorIs(t, notImplemented, types.ErrSigningNotSupported)

	var wrapped error = &wrappingError{Err: notImplemented}
	assert.ErrorIs(t, wrapped, types.ErrSigningNotSupported, "must be found through wrapping")

	failed := &RemoteSignerError{Code: 500, Description: "core unavailable"}
	assert.False(t, errors.Is(failed, types.ErrSigningNotSupported))
}

// wrappingError is a minimal wrapper standing in for any caller that wraps the
// signer's error.
type wrappingError struct{ Err error }

func (e *wrappingError) Error() string { return "wrapped: " + e.Err.Error() }
func (e *wrappingError) Unwrap() error { return e.Err }
