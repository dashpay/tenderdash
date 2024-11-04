package types

import (
	"fmt"
	"math/bits"
	"time"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	"github.com/dashpay/tenderdash/crypto/ed25519"
	tmmath "github.com/dashpay/tenderdash/libs/math"
)

// ValidateHash returns an error if the hash is not empty, but its
// size != crypto.HashSize.
func ValidateHash(h []byte) error {
	if len(h) > 0 && len(h) != crypto.HashSize {
		return fmt.Errorf("expected size to be %d bytes, got %d bytes",
			crypto.HashSize,
			len(h),
		)
	}
	return nil
}

// ValidateAppHash returns an error if the app hash size is invalid.
func ValidateAppHash(h []byte) error {
	if len(h) != crypto.DefaultAppHashSize {
		return fmt.Errorf("expected size to be %d bytes, got %d bytes", crypto.DefaultAppHashSize, len(h))
	}
	return nil
}

// ValidateSignatureSize returns an error if the signature is not empty, but its
// size != hash.Size.
func ValidateSignatureSize(keyType crypto.KeyType, h []byte) error {
	var signatureSize int // default
	switch keyType {
	case crypto.Ed25519:
		signatureSize = ed25519.SignatureSize
	case crypto.BLS12381:
		signatureSize = bls12381.SignatureSize
	default:
		panic("key type unknown")
	}
	if len(h) > 0 && len(h) != signatureSize {
		return fmt.Errorf("expected size to be %d bytes, got %d bytes",
			bls12381.SignatureSize,
			len(h),
		)
	}
	return nil
}

// checkTimely returns 0 when message is timely, -1 when received too early, 1 when received too late.
func checkTimely(timestamp time.Time, recvTime time.Time, sp SynchronyParams, round int32) int {
	// The message delay values are scaled as rounds progress.
	// Every 10 rounds, the message delay is doubled to allow consensus to
	// proceed in the case that the chosen value was too small for the given network conditions.
	// For more information and discussion on this mechanism, see the relevant github issue:
	// https://github.com/tendermint/spec/issues/371
	maxShift := bits.LeadingZeros64(tmmath.MustConvertUint64(sp.MessageDelay)) - 1
	nShift := int((round / 10))

	if nShift > maxShift {
		// if the number of 'doublings' would would overflow the size of the int, use the
		// maximum instead.
		nShift = maxShift
	}
	msgDelay := sp.MessageDelay * time.Duration(1<<nShift)

	// lhs is `proposedBlockTime - Precision` in the first inequality
	lhs := timestamp.Add(-sp.Precision)
	// rhs is `proposedBlockTime + MsgDelay + Precision` in the second inequality
	rhs := timestamp.Add(msgDelay).Add(sp.Precision)

	if recvTime.Before(lhs) {
		return -1
	}
	if recvTime.After(rhs) {
		return 1
	}
	return 0
}
