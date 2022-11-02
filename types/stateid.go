package types

import (
	"bytes"
	"fmt"
	"time"

	"github.com/tendermint/tendermint/crypto"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
)

// This file contains implementation of StateID logic.

//--------------------------------------------------------------------------------

// StateID represents essential information required to verify state, document and transactions.
// It is meant to be used by light clients (like mobile apps) to verify proofs.
// For signing purposes it is marshaled as fixed-size slice of bytes, with no tags and
// no delimiters. All integers are represented as little-endian.
type StateID struct {
	// AppVersion used when generating the block, equals to Header.Version.App.
	// 8 bytes
	AppVersion uint64 `json:"app_version"`
	// Height of block containing this state ID.
	// 8 bytes
	Height uint64 `json:"height"`
	// AppHash used in current block, equal to Header.AppHash.
	// 32 bytes
	AppHash [crypto.DefaultAppHashSize]byte `json:"app_hash"`
	// CoreChainLockedHeight for the block, equal to Header.CoreChainLockedHeight.
	// 4 bytes
	CoreChainLockedHeight uint32 `json:"core_chain_locked_height"`
	// Time of the block (Unix time), equal to Header.Time.
	// Encoded as a 64-bit signed int representing number of nanoseconds elapsed since January 1, 1970 UTC,
	// as specified in golang time.Time.UnixNano().
	// 8 bytes
	Time time.Time `json:"time"`
}

// Copy returns new StateID that is equal to this one
func (stateID StateID) Copy() StateID {
	copied := stateID

	return copied
}

// Equal returns true if the StateID matches the given StateID
func (stateID StateID) Equal(other StateID) bool {
	left, err := stateID.SignBytes()
	if err != nil {
		panic("cannot marshal stateID: " + err.Error())
	}
	right, err := other.SignBytes()
	if err != nil {
		panic("cannot marshal stateID: " + err.Error())
	}

	return bytes.Equal(left, right)
}

// ValidateBasic performs basic validation.
func (stateID StateID) ValidateBasic() error {
	// LastAppHash can be empty in case of genesis block.
	if err := ValidateAppHash(stateID.AppHash[:]); err != nil {
		return fmt.Errorf("wrong app Hash: %w", err)
	}
	if stateID.AppVersion == 0 {
		return fmt.Errorf("invalid stateID version %d", stateID.AppVersion)
	}
	if stateID.Time.IsZero() {
		return fmt.Errorf("invalid stateID time %s", stateID.Time.String())
	}

	return nil
}

// SignBytes returns bytes that should be signed
func (stateID StateID) SignBytes() ([]byte, error) {
	return tmbytes.MarshalFixedSize(stateID)
}

func (stateID StateID) Hash() tmbytes.HexBytes {
	bz, err := stateID.SignBytes()
	if err != nil {
		panic("cannot hash StateID: " + err.Error())
	}
	return crypto.Checksum(bz)
}

// String returns a human readable string representation of the StateID.
func (stateID StateID) String() string {
	return fmt.Sprintf(
		`v%d:h=%d,cl=%d,ah=%x,t=%s`,
		stateID.AppVersion,
		stateID.Height,
		stateID.CoreChainLockedHeight,
		stateID.AppHash[:3],
		stateID.Time.UTC().Format(time.RFC3339),
	)
}

// WithHeight returns new copy of stateID with height set to provided value.
// It is a convenience method used in tests.
// Note that this is Last Height from state, so it will be (height-1) for Vote.
func (stateID StateID) WithHeight(height int64) StateID {
	ret := stateID.Copy()
	ret.Height = uint64(height)

	return ret
}
