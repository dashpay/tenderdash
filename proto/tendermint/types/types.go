package types

import (
	bytes "bytes"
	"fmt"

	"github.com/gogo/protobuf/types"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/internal/libs/protoio"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
)

// IsZero returns true when the object is a zero-value or nil
func (m *BlockID) IsZero() bool {
	return m == nil || (len(m.Hash) == 0 && m.PartSetHeader.IsZero() && len(m.StateID) == 0)
}

func (m *BlockID) ToCanonicalBlockID() *CanonicalBlockID {
	if m == nil || m.IsZero() {
		return nil
	}
	cbid := CanonicalBlockID{
		Hash:          m.Hash,
		PartSetHeader: m.PartSetHeader.ToCanonicalPartSetHeader(),
	}

	return &cbid
}

func (m *PartSetHeader) ToCanonicalPartSetHeader() CanonicalPartSetHeader {
	if m == nil || m.IsZero() {
		return CanonicalPartSetHeader{}
	}
	cps := CanonicalPartSetHeader(*m)
	return cps
}

// IsZero returns true when the object is a zero-value or nil
func (m *PartSetHeader) IsZero() bool {
	return m == nil || len(m.Hash) == 0
}

// VoteExtensionsToMap creates a map where a key is vote-extension type and value is the extensions grouped by type
func (m *Vote) VoteExtensionsToMap() VoteExtensions {
	if m == nil {
		return nil
	}
	res := make(map[VoteExtensionType][]*VoteExtension)
	for _, ext := range m.VoteExtensions {
		res[ext.Type] = append(res[ext.Type], ext)
	}
	return res
}

// SignBytes represent data to be signed for the given vote.
// It's a 64-byte slice containing concatenation of:
// * Checksum of CanonicalVote
// * Checksum of StateID
func (m Vote) SignBytes(chainID string) ([]byte, error) {
	pbVote, err := m.ToCanonicalVote(chainID)
	if err != nil {
		return nil, err
	}
	return tmbytes.MarshalFixedSize(pbVote)
}

// CanonicalizeVote transforms the given Vote to a CanonicalVote, which does
// not contain ValidatorIndex and ValidatorProTxHash fields.
func (m Vote) ToCanonicalVote(chainID string) (CanonicalVote, error) {
	var (
		blockIDBytes []byte
		stateIDBytes []byte
		err          error
	)
	blockID := m.BlockID.ToCanonicalBlockID()
	if blockID != nil {
		if blockIDBytes, err = blockID.Checksum(); err != nil {
			return CanonicalVote{}, err
		}
		stateIDBytes = m.BlockID.StateID
	} else {
		blockIDBytes = crypto.Checksum(nil)
		stateIDBytes = crypto.Checksum(nil)
	}

	return CanonicalVote{
		Type:    m.Type,
		Height:  m.Height,       // encoded as sfixed64
		Round:   int64(m.Round), // encoded as sfixed64
		BlockID: blockIDBytes,
		StateID: stateIDBytes,
		ChainID: chainID,
	}, nil
}

func (s StateID) signBytes() ([]byte, error) {
	marshaled, err := protoio.MarshalDelimited(&s)
	if err != nil {
		return nil, err
	}

	return marshaled, nil
}

// Hash calculates hash of a StateID to be used in BlockID and other places.
// It will panic() in case of (very unlikely) error.
func (s StateID) Hash() (bz []byte) {
	var err error

	if bz, err = s.signBytes(); err != nil {
		panic("cannot marshal: " + err.Error())
	}

	return crypto.Checksum(bz)
}

var zeroAppHash = make([]byte, crypto.DefaultAppHashSize)

func (s *StateID) IsZero() bool {

	return s == nil ||
		((len(s.AppHash) == 0 || bytes.Equal(s.AppHash, zeroAppHash)) &&
			s.AppVersion == 0 &&
			s.CoreChainLockedHeight == 0 &&
			s.Height == 0 &&
			s.Time.Equal(types.Timestamp{}))
}

// Copy returns new StateID that is equal to this one
func (s StateID) Copy() StateID {
	copied := s
	copied.AppHash = make([]byte, len(s.AppHash))
	copy(copied.AppHash, s.AppHash)

	return copied
}

func (stateID StateID) String() string {
	return fmt.Sprintf(
		`v%d:h=%d,cl=%d,ah=%s,t=%s`,
		stateID.AppVersion,
		stateID.Height,
		stateID.CoreChainLockedHeight,
		tmbytes.HexBytes(stateID.AppHash).ShortString(),
		stateID.Time.String(),
	)
}

// Equal returns true if the StateID matches the given StateID
func (s StateID) Equal(other StateID) bool {
	left, err := s.signBytes()
	if err != nil {
		panic("cannot marshal stateID: " + err.Error())
	}
	right, err := other.signBytes()
	if err != nil {
		panic("cannot marshal stateID: " + err.Error())
	}

	return bytes.Equal(left, right)
}

// ValidateBasic performs basic validation.
func (s StateID) ValidateBasic() error {
	if s.Time.Equal(types.Timestamp{}) {
		return fmt.Errorf("invalid stateID time %s", s.Time.String())
	}
	if len(s.AppHash) != crypto.DefaultAppHashSize {
		return fmt.Errorf(
			"invalid apphash %X len, expected: %d, got: %d",
			s.AppHash,
			crypto.DefaultAppHashSize,
			len(s.AppHash),
		)
	}

	return nil
}
