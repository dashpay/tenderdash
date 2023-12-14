package types

import (
	"bytes"
	"errors"
	fmt "fmt"

	"github.com/dashpay/tenderdash/crypto/bls12381"
)

// VoteExtensions is a container type for grouped vote extensions by type
type VoteExtensions map[VoteExtensionType][]*VoteExtension

var (
	errExtensionNil                       = errors.New("vote extension is nil")
	errExtensionSignEmpty                 = errors.New("vote extension signature is missing")
	errExtensionSignTooBig                = fmt.Errorf("vote extension signature is too big (max: %d)", bls12381.SignatureSize)
	errExtensionSignRequestIdNotSupported = errors.New("vote extension sign request id is not supported")
	errExtensionSignRequestIdWrongPrefix  = errors.New("vote extension sign request id must have dpevote or plwdtx prefix")
)

// Clone returns a copy of current vote-extension
//
// Clone of nil will panic

func (v *VoteExtension) Clone() VoteExtension {
	if v == nil {
		panic("cannot clone nil vote-extension")
	}

	var xSignRequestID isVoteExtension_XSignRequestId

	if v.XSignRequestId != nil && v.XSignRequestId.Size() > 0 {
		src := v.GetSignRequestId()
		dst := make([]byte, len(src))
		copy(dst, src)
		xSignRequestID = &VoteExtension_SignRequestId{
			SignRequestId: dst,
		}
	}

	return VoteExtension{
		Type:           v.Type,
		Extension:      v.Extension,
		Signature:      v.Signature,
		XSignRequestId: xSignRequestID,
	}
}

// Validate checks the validity of the vote-extension
func (v *VoteExtension) Validate() error {
	if v == nil {
		return errExtensionNil
	}

	if len(v.Extension) > 0 && len(v.Signature) == 0 {
		return errExtensionSignEmpty
	}
	if len(v.Signature) > bls12381.SignatureSize {
		return errExtensionSignTooBig
	}

	if v.XSignRequestId != nil && v.XSignRequestId.Size() > 0 {
		if v.Type != VoteExtensionType_THRESHOLD_RECOVER_RAW {
			return errExtensionSignRequestIdNotSupported
		}
		var validPrefixes = []string{"plwdtx", "dpevote"}
		requestID := v.GetSignRequestId()

		var validPrefix bool
		for _, prefix := range validPrefixes {
			if bytes.HasPrefix(requestID, []byte(prefix)) {
				validPrefix = true
				break
			}
		}

		if !validPrefix {
			return errExtensionSignRequestIdWrongPrefix
		}
	}

	return nil
}
