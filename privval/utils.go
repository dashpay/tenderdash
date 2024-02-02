package privval

import (
	"errors"
	"fmt"
	"net"

	"github.com/dashpay/tenderdash/crypto/ed25519"
	"github.com/dashpay/tenderdash/libs/log"
	tmnet "github.com/dashpay/tenderdash/libs/net"
)

// IsConnTimeout returns a boolean indicating whether the error is known to
// report that a connection timeout occurred. This detects both fundamental
// network timeouts, as well as ErrConnTimeout errors.
func IsConnTimeout(err error) bool {
	_, ok := errors.Unwrap(err).(timeoutError)
	switch {
	case errors.As(err, &EndpointTimeoutError{}):
		return true
	case ok:
		return true
	default:
		return false
	}
}

// NewSignerListener creates a new SignerListenerEndpoint using the corresponding listen address
func NewSignerListener(listenAddr string, logger log.Logger) (*SignerListenerEndpoint, error) {
	protocol, address := tmnet.ProtocolAndAddress(listenAddr)
	if protocol != "unix" && protocol != "tcp" {
		return nil, fmt.Errorf("unsupported address family %q, want unix or tcp", protocol)
	}

	ln, err := net.Listen(protocol, address)
	if err != nil {
		return nil, err
	}

	var listener net.Listener
	switch protocol {
	case "unix":
		listener = NewUnixListener(ln)
	case "tcp":
		// TODO: persist this key so external signer can actually authenticate us
		listener = NewTCPListener(ln, ed25519.GenPrivKey())
	default:
		panic("invalid protocol: " + protocol) // semantically unreachable
	}

	return NewSignerListenerEndpoint(logger.With("module", "privval"), listener), nil
}
