package types

import (
	"bytes"
	"errors"
	fmt "fmt"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
)

// VoteExtensions is a container type for grouped vote extensions by type
type VoteExtensions []*VoteExtension

var (
	errExtensionNil                       = errors.New("vote extension is nil")
	errExtensionSignEmpty                 = errors.New("vote extension signature is missing")
	errExtensionSignTooBig                = fmt.Errorf("vote extension signature is too big (max: %d)", bls12381.SignatureSize)
	errExtensionSignRequestIDNotSupported = errors.New("vote extension sign request id is not supported")
	errExtensionSignRequestIDWrongPrefix  = errors.New("vote extension sign request id must have dpevote or \\x06plwdtx prefix")
)

// Clone returns a shallow copy of current vote-extension
//
// Clone of nil will panic

func (v *VoteExtension) Clone() VoteExtension {
	if v == nil {
		panic("cannot clone nil vote-extension")
	}

	ve := VoteExtension{
		Type:      v.Type,
		Extension: v.Extension,
		Signature: v.Signature,
	}

	if v.XSignRequestId != nil && v.XSignRequestId.Size() > 0 {
		ve.XSignRequestId = &VoteExtension_SignRequestId{
			SignRequestId: v.GetSignRequestId(),
		}
	}

	return ve
}

// Copy returns a deep copy of current vote-extension.
func (v *VoteExtension) Copy() VoteExtension {
	if v == nil {
		panic("cannot copy nil vote-extension")
	}

	ve := VoteExtension{
		Type:      v.Type,
		Extension: bytes.Clone(v.Extension),
		Signature: bytes.Clone(v.Signature),
	}

	if v.XSignRequestId != nil && v.XSignRequestId.Size() > 0 {
		ve.XSignRequestId = &VoteExtension_SignRequestId{
			SignRequestId: bytes.Clone(v.GetSignRequestId()),
		}
	}

	return ve
}

func (v *VoteExtension) Equal(other *VoteExtension) bool {
	if v == nil || other == nil {
		return false
	}

	if v.Type != other.Type {
		return false
	}

	if !bytes.Equal(v.Extension, other.Extension) {
		return false
	}

	if !bytes.Equal(v.Signature, other.Signature) {
		return false
	}

	// one of them is nil, but not both
	if (v.XSignRequestId != nil) != (other.XSignRequestId != nil) {
		return false
	}

	if v.XSignRequestId != nil && other.XSignRequestId != nil {
		if !bytes.Equal(v.GetSignRequestId(), other.GetSignRequestId()) {
			return false
		}
	}

	return true
}

// Validate checks the validity of the vote-extension
func (v *VoteExtension) Validate() error {
	if v == nil {
		return errExtensionNil
	}

	if v.Type == VoteExtensionType_DEFAULT {
		return fmt.Errorf("vote extension type %s is not supported", v.Type.String())
	}

	if v.Type == VoteExtensionType_THRESHOLD_RECOVER_RAW {
		if len(v.Extension) != crypto.HashSize {
			return fmt.Errorf("invalid %s vote extension size: got %d, expected %d",
				v.Type.String(), len(v.Extension), crypto.HashSize)
		}
	}

	if len(v.Extension) > 0 && len(v.Signature) == 0 {
		return errExtensionSignEmpty
	}
	if len(v.Signature) > bls12381.SignatureSize {
		return errExtensionSignTooBig
	}

	if v.XSignRequestId != nil && v.XSignRequestId.Size() > 0 {
		if v.Type != VoteExtensionType_THRESHOLD_RECOVER_RAW {
			return errExtensionSignRequestIDNotSupported
		}
		var validPrefixes = []string{"\x06plwdtx", "dpevote"}
		requestID := v.GetSignRequestId()

		var validPrefix bool
		for _, prefix := range validPrefixes {
			if bytes.HasPrefix(requestID, []byte(prefix)) {
				validPrefix = true
				break
			}
		}

		if !validPrefix {
			return errExtensionSignRequestIDWrongPrefix
		}
	}

	return nil
}
func (v VoteExtensions) Contains(other VoteExtension) bool {
	for _, ext := range v {
		if ext.Equal(&other) {
			return true
		}
	}
	return false
}
