package types

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"

	"github.com/dashpay/dashd-go/btcjson"
	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/hashicorp/go-multierror"
)

var (
	errUnableCopySigns = errors.New("unable to copy signatures: the sizes of extensions are not equal")
)

// VoteExtensions is a container where the key is vote-extension type and value is a list of VoteExtension
type VoteExtensions []VoteExtensionIf

// NewVoteExtensionsFromABCIExtended returns vote-extensions container for given ExtendVoteExtension
func NewVoteExtensionsFromABCIExtended(exts []*abci.ExtendVoteExtension) VoteExtensions {
	voteExtensions := make(VoteExtensions, 0, len(exts))

	for _, ext := range exts {
		ve := ext.ToVoteExtension()
		voteExtensions.Add(ve)
	}
	return voteExtensions
}

// Add creates and adds protobuf VoteExtension into a container by vote-extension type
func (e *VoteExtensions) Add(ext tmproto.VoteExtension) {
	*e = append(*e, VoteExtensionFromProto(ext))
}

// MakeVoteExtensionSignItems  creates a list SignItem structs for a vote extensions
func (e VoteExtensions) SignItems(
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash []byte,
	height int64,
	round int32,
) ([]crypto.SignItem, error) {

	items := make([]crypto.SignItem, 0, e.Len())

	for _, ext := range e {
		item, err := ext.SignItem(chainID, height, round, quorumType, quorumHash)
		if err != nil {
			return nil, err
		}

		items = append(items, item)
	}

	return items, nil
}

func (e VoteExtensions) GetSignatures() [][]byte {
	signatures := make([][]byte, 0, e.Len())

	for _, ext := range e {
		signatures = append(signatures, ext.GetSignature())
	}

	return signatures
}

func (e VoteExtensions) GetExtensions() [][]byte {
	exts := make([][]byte, 0, e.Len())

	for _, ext := range e {
		exts = append(exts, ext.GetExtension())
	}

	return exts
}

// Validate returns error if an added vote-extension is invalid
func (e VoteExtensions) Validate() error {
	var errs *multierror.Error

	for i, ext := range e {
		if err := ext.Validate(); err != nil {
			errs = multierror.Append(errs, fmt.Errorf("invalid %s vote extension %d: %w", ext.ToProto().Type, i, err))
		}
	}

	return errs.ErrorOrNil()
}

// IsEmpty returns true if a vote-extension container is empty, otherwise false
func (e VoteExtensions) IsEmpty() bool {
	return len(e) == 0
}

// ToProto transforms the current state of vote-extension container into VoteExtensions's protobuf
func (e VoteExtensions) ToProto() []*tmproto.VoteExtension {
	extensions := make([]*tmproto.VoteExtension, 0, e.Len())
	for _, ext := range e {
		pbExt := ext.ToProto()
		extensions = append(extensions, &pbExt)
	}

	return extensions
}

// ToExtendProto transforms the current state of vote-extension container into ExtendVoteExtension's protobuf
func (e VoteExtensions) ToExtendProto() []*abci.ExtendVoteExtension {
	proto := make([]*abci.ExtendVoteExtension, 0, e.Len())

	for _, ext := range e {
		if err := ext.Validate(); err != nil {
			panic(fmt.Errorf("invalid vote extension %v: %w", ext, err))
		}

		pb := ext.ToProto()

		var requestID *abci.ExtendVoteExtension_SignRequestId
		if pb.XSignRequestId != nil {
			if src := pb.GetSignRequestId(); len(src) > 0 {
				requestID = &abci.ExtendVoteExtension_SignRequestId{
					SignRequestId: bytes.Clone(src),
				}
			}
		}

		proto = append(proto, &abci.ExtendVoteExtension{
			Type:           pb.Type,
			Extension:      pb.Extension,
			XSignRequestId: requestID,
		})
	}

	return proto
}

// Fingerprint returns a fingerprint of all vote-extensions in a state of this container
func (e VoteExtensions) Fingerprint() []byte {
	if e.IsEmpty() {
		return tmbytes.Fingerprint(nil)
	}
	sha := sha256.New()
	for _, ext := range e {
		pb := ext.ToProto()
		// type + extension
		if _, err := sha.Write(big.NewInt(int64(pb.Type)).Bytes()); err != nil {
			panic(err)
		}
		if _, err := sha.Write(pb.Extension); err != nil {
			panic(err)
		}
	}
	return tmbytes.Fingerprint(sha.Sum(nil))
}

// IsSameWithProto compares the current state of the vote-extension with the same in VoteExtensions's protobuf
// checks only the value of extensions
func (e VoteExtensions) IsSameWithProto(right tmproto.VoteExtensions) bool {
	for t, ext := range e {
		pb := ext.ToProto()
		other := right[t]
		if !pb.Equal(other) {
			return false
		}
	}
	return true
}

func (e VoteExtensions) Len() int {
	return len(e)
}

// VoteExtensionsFromProto creates VoteExtensions container from VoteExtensions's protobuf
func VoteExtensionsFromProto(pve ...*tmproto.VoteExtension) VoteExtensions {
	if len(pve) == 0 {
		return nil
	}
	voteExtensions := make(VoteExtensions, 0, len(pve))
	for _, ext := range pve {
		voteExtensions = append(voteExtensions, VoteExtensionFromProto(*ext))
	}

	return voteExtensions
}

// Copy creates a deep copy of VoteExtensions
func (e VoteExtensions) Copy() VoteExtensions {
	copied := make(VoteExtensions, 0, len(e))
	for _, ext := range e {
		copied = append(copied, ext.Copy())
	}

	return copied
}

// Filter returns a new VoteExtensions container with vote-extensions filtered by provided function.
// It does not copy data, just creates a new container with references to the same data
func (e VoteExtensions) Filter(fn func(ext VoteExtensionIf) bool) VoteExtensions {
	result := make(VoteExtensions, 0, len(e))
	for _, ext := range e {
		if fn(ext) {
			result = append(result, ext)
		}
	}

	return result[:]
}

// CopySignsFromProto copies the signatures from VoteExtensions's protobuf into the current VoteExtension state
func (e VoteExtensions) CopySignsFromProto(src tmproto.VoteExtensions) error {
	if len(e) != len(src) {
		return errUnableCopySigns
	}
	for i, ext := range e {
		ext.SetSignature(src[i].Signature)
	}

	return nil
}

// CopySignsToProto copies the signatures from the current VoteExtensions into VoteExtension's protobuf
func (e VoteExtensions) CopySignsToProto(dest tmproto.VoteExtensions) error {
	if len(e) != len(dest) {
		return errUnableCopySigns
	}
	for i, ext := range e {
		pb := ext.ToProto()
		dest[i].Signature = pb.Signature
	}

	return nil
}

type VoteExtensionIf interface {
	// Return type of this vote extension
	GetType() tmproto.VoteExtensionType
	// Return extension bytes
	GetExtension() []byte
	// Return signature bytes
	GetSignature() []byte
	// Copy creates a deep copy of VoteExtension
	Copy() VoteExtensionIf
	// ToProto transforms the current state of vote-extension into VoteExtension's proto-generated object.
	// It should prioritize performance and can do a shallow copy of the vote-extension,
	// so the returned object should not be modified.
	ToProto() tmproto.VoteExtension
	SignItem(chainID string, height int64, round int32, quorumType btcjson.LLMQType, quorumHash []byte) (crypto.SignItem, error)
	IsThresholdRecoverable() bool
	Validate() error

	SetSignature(sig []byte)
}

func VoteExtensionFromProto(ve tmproto.VoteExtension) VoteExtensionIf {
	switch ve.Type {
	case tmproto.VoteExtensionType_DEFAULT:
		return &GenericVoteExtension{VoteExtension: ve}
	case tmproto.VoteExtensionType_THRESHOLD_RECOVER:
		return &ThresholdVoteExtension{GenericVoteExtension: GenericVoteExtension{VoteExtension: ve}}
	case tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW:
		return &ThresholdRawVoteExtension{GenericVoteExtension: GenericVoteExtension{VoteExtension: ve}}
	default:
		panic(fmt.Errorf("unknown vote extension type: %s", ve.Type.String()))
	}
}

// VOTE EXTENSION TYPES

// GenericVoteExtension is a default type of VoteExtension
type GenericVoteExtension struct {
	tmproto.VoteExtension
}

func (e GenericVoteExtension) Copy() VoteExtensionIf {
	return &GenericVoteExtension{VoteExtension: e.VoteExtension.Copy()}
}

func (e GenericVoteExtension) ToProto() tmproto.VoteExtension {
	return e.VoteExtension
}

func (e GenericVoteExtension) SignItem(chainID string, height int64, round int32, quorumType btcjson.LLMQType, quorumHash []byte) (crypto.SignItem, error) {
	return signItem(&e.VoteExtension, chainID, height, round, quorumType, quorumHash)
}

func (e GenericVoteExtension) IsThresholdRecoverable() bool {
	return false
}

func (e *GenericVoteExtension) SetSignature(sig []byte) {
	e.Signature = sig
}

// ThresholdVoteExtension is a threshold type of VoteExtension
type ThresholdVoteExtension struct {
	GenericVoteExtension
	ThresholdSignature []byte
}

func (e ThresholdVoteExtension) Copy() VoteExtensionIf {
	return &ThresholdVoteExtension{GenericVoteExtension: GenericVoteExtension{
		VoteExtension: e.VoteExtension.Copy(),
	},
	}
}

func (e ThresholdVoteExtension) IsThresholdRecoverable() bool {
	return true
}

// ThresholdRawVoteExtension is a threshold raw type of VoteExtension
type ThresholdRawVoteExtension struct {
	GenericVoteExtension
	ThresholdSignature []byte
}

func (e ThresholdRawVoteExtension) Copy() VoteExtensionIf {
	return &ThresholdRawVoteExtension{GenericVoteExtension: GenericVoteExtension{
		VoteExtension: e.VoteExtension.Copy(),
	}}
}

func (e ThresholdRawVoteExtension) IsThresholdRecoverable() bool {
	return true
}

func (e ThresholdRawVoteExtension) SignItem(chainID string, height int64, round int32, quorumType btcjson.LLMQType, quorumHash []byte) (crypto.SignItem, error) {
	var signRequestID []byte
	var err error

	ext := &e.VoteExtension

	if ext.XSignRequestId != nil && ext.XSignRequestId.Size() > 0 {
		signRequestID = crypto.Checksum(crypto.Checksum(ext.GetSignRequestId()))
	} else {
		if signRequestID, err = voteExtensionRequestID(height, round); err != nil {
			return crypto.SignItem{}, err
		}
	}

	// ensure signBytes is 32 bytes long
	signBytes := make([]byte, crypto.DefaultHashSize)
	copy(signBytes, ext.Extension)

	signItem, err := crypto.NewSignItemFromHash(quorumType, quorumHash, signRequestID, signBytes), nil
	if err != nil {
		return crypto.SignItem{}, err
	}
	signItem.Raw = ext.Extension

	return signItem, nil
}

// voteExtensionRequestID returns vote extension sign request ID used to generate
// threshold signatures
func voteExtensionRequestID(height int64, round int32) ([]byte, error) {
	return heightRoundRequestID("dpevote", height, round), nil
}

// voteExtensionSignBytes returns the proto-encoding of the canonicalized vote
// extension for signing. Panics if the marshaling fails.
//
// Similar to VoteSignBytes, the encoded Protobuf message is varint
// length-prefixed for backwards-compatibility with the Amino encoding.
func voteExtensionSignBytes(chainID string, height int64, round int32, ext *tmproto.VoteExtension) []byte {
	bz, err := CanonicalizeVoteExtension(chainID, ext, height, round)
	if err != nil {
		panic(err)
	}
	return bz
}

func signItem(ext *tmproto.VoteExtension, chainID string, height int64, round int32, quorumType btcjson.LLMQType, quorumHash []byte) (crypto.SignItem, error) {
	requestID, err := voteExtensionRequestID(height, round)
	if err != nil {
		return crypto.SignItem{}, err
	}
	signBytes := voteExtensionSignBytes(chainID, height, round, ext)
	return crypto.NewSignItem(quorumType, quorumHash, requestID, signBytes), nil
}
