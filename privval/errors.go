package privval

import (
	"errors"
	"fmt"

	"github.com/dashpay/tenderdash/types"
)

// ErrUnexpectedResponse indicates an empty Dash Core signing response.
var ErrUnexpectedResponse = errors.New("empty response")

// RemoteSignerError allows (remote) validators to include meaningful error
// descriptions in their reply.
type RemoteSignerError struct {
	// TODO(ismail): create an enum of known errors
	Code        int
	Description string
}

// remoteSignerNotImplemented is the code of a request the remote signer cannot
// serve at all.
const remoteSignerNotImplemented = 501

func (e *RemoteSignerError) Error() string {
	return fmt.Sprintf("signer returned error #%d: %s", e.Code, e.Description)
}

// Is reports a request the signer cannot serve at all as
// types.ErrSigningNotSupported, so callers stop asking.
func (e *RemoteSignerError) Is(target error) bool {
	return e.Code == remoteSignerNotImplemented && target == types.ErrSigningNotSupported
}
