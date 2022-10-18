package types

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	typespb "github.com/gogo/protobuf/types"

	"github.com/tendermint/tendermint/crypto"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
)

// This file contains implementation of StateID logic.

//--------------------------------------------------------------------------------

const StateIDVersion = 1

// StateID
type StateID struct {
	// Version of StateID
	Version uint16 `json:"version"`
	// Height of current block (the one containing state ID signature)
	Height uint64 `json:"height"`
	// AppHash used in current block (the one containing state ID signature)
	AppHash tmbytes.HexBytes `json:"last_app_hash"`
	// core_chain_locked_height is encoded as 32-bit little-endian unsigned  int, on 4 bytes.
	CoreChainLockedHeight uint32    `json:"core_chain_locked_height"`
	Time                  time.Time `json:"time"`
}

// Copy returns new StateID that is equal to this one
func (stateID StateID) Copy() StateID {
	copied := stateID
	copied.AppHash = make([]byte, len(stateID.AppHash))
	if copy(copied.AppHash, stateID.AppHash) != len(stateID.AppHash) {
		panic("Cannot copy LastAppHash, this should never happen. Out of memory???")
	}

	return copied
}

// Equals returns true if the StateID matches the given StateID
func (stateID StateID) Equal(other StateID) bool {
	left, err := stateID.Marshal()
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
	if err := ValidateAppHash(stateID.AppHash); err != nil {
		return fmt.Errorf("wrong app Hash: %w", err)
	}

	if stateID.Height < 0 {
		return fmt.Errorf("stateID height is not valid: %d < 0", stateID.Height)
	}

	if stateID.Version == 0 {
		return fmt.Errorf("invalid stateID version %d", stateID.Version)
	}

	if stateID.Time.IsZero() {
		return fmt.Errorf("invalid stateID time %s", stateID.Time.String())
	}
	return nil
}

// Marshal marshals the stateID to a fixed-size bytes buffer.
func (stateID StateID) Marshal() ([]byte, error) {
	return tmbytes.MarshalFixedSize(stateID)
}

// SignBytes returns bytes that should be signed
func (stateID StateID) SignBytes() ([]byte, error) {
	return stateID.Marshal()
}

func (stateID StateID) Hash() []byte {
	bz, err := stateID.SignBytes()
	if err != nil {
		panic("cannot hash StateID:" + err.Error())
	}
	return crypto.Checksum(bz)
}

// String returns a human readable string representation of the StateID.
func (stateID StateID) String() string {

	return fmt.Sprintf(`%d:%v`, stateID.Height, stateID.AppHash)
}

// ToProto converts StateID to protobuf
func (stateID StateID) ToProto() tmproto.StateID {
	pbTime, err := typespb.TimestampProto(stateID.Time)
	if err != nil {
		panic(fmt.Errorf("cannot convert time %s to protobuf: %w", stateID.Time.String(), err))
	}
	return tmproto.StateID{
		Version:               uint32(stateID.Version),
		AppHash:               stateID.AppHash,
		CoreChainLockedHeight: 0,
		Height:                stateID.Height,
		Time:                  pbTime,
	}
}

// WithHeight returns new copy of stateID with height set to provided value.
// It is a convenience method used in tests.
// Note that this is Last Height from state, so it will be (height-1) for Vote.
func (stateID StateID) WithHeight(height int64) StateID {
	ret := stateID.Copy()
	ret.Height = uint64(height)

	return ret
}

// FromProto sets a protobuf BlockID to the given pointer.
// It returns an error if the block id is invalid.
func StateIDFromProto(sID *tmproto.StateID) (*StateID, error) {
	if sID == nil {
		return nil, errors.New("nil StateID")
	}

	stateID := new(StateID)

	stateID.AppHash = sID.AppHash
	stateID.Height = sID.Height

	return stateID, stateID.ValidateBasic()
}
