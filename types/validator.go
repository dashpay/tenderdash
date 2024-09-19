package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/rs/zerolog"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	ce "github.com/dashpay/tenderdash/crypto/encoding"
	"github.com/dashpay/tenderdash/internal/jsontypes"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

// Validator Volatile state for each Validator
// NOTE: The ProposerPriority is not included in Validator.Hash();
// make sure to update that method if changes are made here
// The ProTxHash is part of Dash additions required for BLS threshold signatures
type Validator struct {
	ProTxHash   ProTxHash
	PubKey      crypto.PubKey
	VotingPower int64
	NodeAddress ValidatorAddress
}

type validatorJSON struct {
	PubKey           json.RawMessage  `json:"pub_key,omitempty"`
	VotingPower      int64            `json:"voting_power,string"`
	ProTxHash        ProTxHash        `json:"pro_tx_hash"`
	NodeAddress      ValidatorAddress `json:"address"`
	ProposerPriority int64            `json:"proposer_priority,string"`
}

func (v Validator) MarshalJSON() ([]byte, error) {
	val := validatorJSON{
		ProTxHash:   v.ProTxHash,
		VotingPower: v.VotingPower,
	}
	if v.PubKey != nil {
		pk, err := jsontypes.Marshal(v.PubKey)
		if err != nil {
			return nil, err
		}
		val.PubKey = pk
	}
	return json.Marshal(val)
}

func (v *Validator) UnmarshalJSON(data []byte) error {
	var val validatorJSON
	if err := json.Unmarshal(data, &val); err != nil {
		return err
	}
	if err := jsontypes.Unmarshal(val.PubKey, &v.PubKey); err != nil {
		return err
	}
	v.ProTxHash = val.ProTxHash
	v.VotingPower = val.VotingPower
	return nil
}

func NewTestValidatorGeneratedFromProTxHash(proTxHash crypto.ProTxHash) *Validator {
	return &Validator{
		VotingPower: DefaultDashVotingPower,
		ProTxHash:   proTxHash,
	}
}

func NewTestRemoveValidatorGeneratedFromProTxHash(proTxHash crypto.ProTxHash) *Validator {
	return &Validator{
		VotingPower: 0,
		ProTxHash:   proTxHash,
	}
}

func NewValidatorDefaultVotingPower(pubKey crypto.PubKey, proTxHash []byte) *Validator {
	return NewValidator(pubKey, DefaultDashVotingPower, proTxHash, "")
}

// NewValidator returns a new validator with the given pubkey and voting power.
func NewValidator(pubKey crypto.PubKey, votingPower int64, proTxHash ProTxHash, address string) *Validator {
	var (
		addr ValidatorAddress
		err  error
	)
	if address != "" {
		addr, err = ParseValidatorAddress(address)
		if err != nil {
			panic(err.Error())
		}
	}
	return &Validator{
		PubKey:      pubKey,
		VotingPower: votingPower,
		ProTxHash:   proTxHash,
		NodeAddress: addr,
	}
}

// ValidateBasic performs basic validation.
func (v *Validator) ValidateBasic() error {
	if v == nil {
		return errors.New("nil validator")
	}

	if v.ProTxHash == nil {
		return errors.New("validator does not have a provider transaction hash")
	}

	if v.VotingPower < 0 {
		return errors.New("validator has negative voting power")
	}

	if len(v.ProTxHash) != crypto.DefaultHashSize {
		return fmt.Errorf("validator proTxHash is the wrong size: %v", len(v.ProTxHash))
	}

	if !v.NodeAddress.Zero() {
		if err := v.NodeAddress.Validate(); err != nil {
			return fmt.Errorf("validator node address is invalid: %w", err)
		}
	}

	return nil
}

// ValidatePubKey performs basic validation on the public key.
func (v *Validator) ValidatePubKey() error {
	if v.PubKey == nil {
		return errors.New("validator does not have a public key")
	}

	if len(v.PubKey.Bytes()) != bls12381.PubKeySize {
		return fmt.Errorf("validator PubKey is the wrong size: %X", v.PubKey.Bytes())
	}
	return nil
}

// Copy creates a new copy of the validator so we can mutate ProposerPriority.
// Panics if the validator is nil.
func (v *Validator) Copy() *Validator {
	vCopy := *v
	return &vCopy
}

// String returns a string representation of String.
//
// 1. address
// 2. public key
// 3. voting power
// 4. proposer priority
// 5. node address
func (v *Validator) String() string {
	if v == nil {
		return "nil-Validator"
	}
	return fmt.Sprintf("Validator{%v %v VP:%v N:%s}",
		v.ProTxHash,
		v.PubKey,
		v.VotingPower,
		v.NodeAddress.String())
}

func (v *Validator) ShortStringBasic() string {
	if v == nil {
		return "nil-Validator"
	}
	return fmt.Sprintf("Validator{%v %v}",
		v.ProTxHash.ShortString(),
		v.PubKey)
}

// MarshalZerologObject implements zerolog.LogObjectMarshaler
func (v *Validator) MarshalZerologObject(e *zerolog.Event) {
	e.Str("protxhash", v.ProTxHash.ShortString())
	e.Int64("voting_power", v.VotingPower)
	e.Str("address", v.NodeAddress.String())

	if v.PubKey != nil {
		pubkey := v.PubKey.HexString()
		if len(pubkey) > 8 {
			pubkey = pubkey[:8]
		}
		e.Str("pub_key", pubkey)
		e.Str("pub_key_type", v.PubKey.Type())
	}
}

// ValidatorListString returns a prettified validator list for logging purposes.
func ValidatorListString(vals []*Validator) string {
	chunks := make([]string, len(vals))
	for i, val := range vals {
		chunks[i] = fmt.Sprintf("%s:%s:%d", val.ProTxHash, val.PubKey, val.VotingPower)
	}

	return strings.Join(chunks, ",")
}

// Bytes computes the unique encoding of a validator with a given voting power.
// These are the bytes that gets hashed in consensus. It excludes address
// as its redundant with the pubkey. This also excludes ProposerPriority
// which changes every round.
func (v *Validator) Bytes() []byte {
	pk := ce.MustPubKeyToProto(v.PubKey)

	pbv := tmproto.SimpleValidator{
		PubKey:      &pk,
		VotingPower: v.VotingPower,
	}

	bz, err := pbv.Marshal()
	if err != nil {
		panic(err)
	}
	return bz
}

// ToProto converts Validator to protobuf
func (v *Validator) ToProto() (*tmproto.Validator, error) {
	if v == nil {
		return nil, errors.New("nil validator")
	}

	if v.ProTxHash == nil {
		return nil, errors.New("the validator must have a proTxHash")
	}

	vp := tmproto.Validator{
		VotingPower: v.VotingPower,
		ProTxHash:   v.ProTxHash,
	}

	if v.PubKey != nil && len(v.PubKey.Bytes()) > 0 {
		pk, err := ce.PubKeyToProto(v.PubKey)
		if err != nil {
			return nil, err
		}
		vp.PubKey = &pk
	}
	vp.NodeAddress = v.NodeAddress.String()

	return &vp, nil
}

// ValidatorFromProto sets a protobuf Validator to the given pointer.
// It returns an error if the public key is invalid.
func ValidatorFromProto(vp *tmproto.Validator) (*Validator, error) {
	if vp == nil {
		return nil, errors.New("nil validator")
	}
	v := new(Validator)
	v.VotingPower = vp.GetVotingPower()
	v.ProTxHash = vp.ProTxHash

	var err error
	if vp.PubKey != nil && vp.PubKey.Sum != nil {
		if v.PubKey, err = ce.PubKeyFromProto(*vp.PubKey); err != nil {
			return nil, err
		}
	}

	if vp.NodeAddress != "" {
		if v.NodeAddress, err = ParseValidatorAddress(vp.NodeAddress); err != nil {
			return nil, err
		}
	}

	return v, nil
}
