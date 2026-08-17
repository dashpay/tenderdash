package privval

import (
	"errors"
	"fmt"
	"net"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/ed25519"
	"github.com/dashpay/tenderdash/libs/log"
	tmnet "github.com/dashpay/tenderdash/libs/net"
)

// ValidateQuorumParams checks the quorum fields of a remote signing request
// before they reach the private validator: sign-hash construction narrows the
// quorum type to uint8 and copies the quorum hash into a fixed-size BLS hash,
// panicking on values it cannot represent.
func ValidateQuorumParams(quorumType int32, quorumHash crypto.QuorumHash) error {
	if err := btcjson.LLMQType(quorumType).Validate(); err != nil {
		return fmt.Errorf("quorum type %d: %w", quorumType, err)
	}
	if len(quorumHash) != crypto.QuorumHashSize {
		return fmt.Errorf("quorum hash must be %d bytes, got %d", crypto.QuorumHashSize, len(quorumHash))
	}
	return nil
}

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
		// TODO: persist this key and pin/verify the remote signer's static key so both
		// endpoints can verify each other across restarts.
		listener = NewTCPListener(ln, ed25519.GenPrivKey())
	default:
		panic("invalid protocol: " + protocol) // semantically unreachable
	}

	return NewSignerListenerEndpoint(logger.With("module", "privval"), listener), nil
}
