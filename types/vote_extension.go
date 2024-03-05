package types

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"

	"github.com/dashpay/dashd-go/btcjson"
	"github.com/hashicorp/go-multierror"
	"github.com/rs/zerolog"

	abci "github.com/dashpay/tenderdash/abci/types"
	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	"github.com/dashpay/tenderdash/internal/libs/protoio"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
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
) ([]SignItem, error) {

	items := make([]SignItem, 0, e.Len())

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
		pb := ext.ToProto()
		eve := &abci.ExtendVoteExtension{
			Type:      pb.Type,
			Extension: pb.Extension,
		}

		if pb.XSignRequestId != nil {
			if src := pb.GetSignRequestId(); len(src) > 0 {
				eve.XSignRequestId = &abci.ExtendVoteExtension_SignRequestId{
					SignRequestId: bytes.Clone(src),
				}
			}
		}

		proto = append(proto, eve)
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
	if len(e) != len(right) {
		return false
	}

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
	if e == nil || e.IsEmpty() {
		return nil
	}

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

func (e VoteExtensions) SetSignatures(src [][]byte) error {
	if len(e) != len(src) {
		return errUnableCopySigns
	}

	for i, ext := range e {
		ext.SetSignature(src[i])
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

// Marshal VoteExtensions as zerolog array
func (e VoteExtensions) MarshalZerologArray(a *zerolog.Array) {
	for _, ext := range e {
		a.Object(ext)
	}
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
	SignItem(chainID string, height int64, round int32, quorumType btcjson.LLMQType, quorumHash []byte) (SignItem, error)
	IsThresholdRecoverable() bool
	// Validate returns error if a vote-extension is invalid.
	// It should not modify the state of the vote-extension.
	Validate() error

	SetSignature(sig []byte)

	zerolog.LogObjectMarshaler
}

type ThresholdVoteExtensionIf interface {
	VoteExtensionIf

	AddThresholdSignature(validator ProTxHash, sig []byte) error
	// Recover threshold signature from collected signatures
	//
	// Returns recovered signature or error. VoteExtension, including any signature already set, is not modified.
	ThresholdRecover() ([]byte, error)
}

func VoteExtensionFromProto(ve tmproto.VoteExtension) VoteExtensionIf {
	switch ve.Type {
	case tmproto.VoteExtensionType_DEFAULT:
		return &GenericVoteExtension{VoteExtension: ve}
	case tmproto.VoteExtensionType_THRESHOLD_RECOVER:
		ext := newThresholdVoteExtension(ve)
		return &ext
	case tmproto.VoteExtensionType_THRESHOLD_RECOVER_RAW:
		ext := newThresholdVoteExtension(ve)
		return &ThresholdRawVoteExtension{ThresholdVoteExtension: ext}
	default:
		panic(fmt.Errorf("unknown vote extension type: %s", ve.Type.String()))
	}
}

func newThresholdVoteExtension(ve tmproto.VoteExtension) ThresholdVoteExtension {
	return ThresholdVoteExtension{GenericVoteExtension: GenericVoteExtension{VoteExtension: ve}}
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
	return e.VoteExtension.Clone()
}

func (e GenericVoteExtension) SignItem(chainID string, height int64, round int32, quorumType btcjson.LLMQType, quorumHash []byte) (SignItem, error) {
	requestID, err := voteExtensionRequestID(height, round)
	if err != nil {
		return SignItem{}, err
	}
	canonical, err := CanonicalizeVoteExtension(chainID, &e.VoteExtension, height, round)
	if err != nil {
		panic(err)
	}

	signBytes, err := protoio.MarshalDelimited(&canonical)
	if err != nil {
		panic(err)
	}

	si := NewSignItem(quorumType, quorumHash, requestID, signBytes)
	// we do not reverse fields when calculating SignHash for vote extensions
	// si.UpdateSignHash(false)
	return si, nil
}

func (e GenericVoteExtension) IsThresholdRecoverable() bool {
	return false
}

func (e *GenericVoteExtension) SetSignature(sig []byte) {
	e.Signature = sig
}

func (e GenericVoteExtension) MarshalZerologObject(o *zerolog.Event) {
	o.Str("type", e.GetType().String())
	o.Hex("extension", e.GetExtension())
	o.Hex("signature", e.GetSignature())
	o.Hex("sign_request_id", e.GetSignRequestId())
}

//nolint:stylecheck // name is the same as in protobuf-generated code
func (e GenericVoteExtension) GetSignRequestId() []byte {
	if e.XSignRequestId == nil {
		return nil
	}
	id, ok := e.XSignRequestId.(*tmproto.VoteExtension_SignRequestId)
	if !ok || id == nil {
		return nil
	}

	return id.SignRequestId
}

type ThresholdSignature [bls12381.SignatureSize]byte

// ThresholdVoteExtension is a threshold type of VoteExtension
type ThresholdVoteExtension struct {
	GenericVoteExtension
	// threshold signatures for this vote extension, collected from validators
	thresholdSignatures map[[crypto.ProTxHashSize]byte]ThresholdSignature
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

func (e *ThresholdVoteExtension) AddThresholdSignature(validator ProTxHash, sig []byte) error {
	if e.thresholdSignatures == nil {
		e.thresholdSignatures = make(map[[crypto.ProTxHashSize]byte]ThresholdSignature)
	}

	proTxHash := [crypto.ProTxHashSize]byte(validator)
	e.thresholdSignatures[proTxHash] = ThresholdSignature(sig)

	return nil
}

// ThresholdRecover recovers threshold signature from collected signatures
func (e *ThresholdVoteExtension) ThresholdRecover() ([]byte, error) {
	proTxHashes := make([][]byte, 0, len(e.thresholdSignatures))
	signatures := make([][]byte, 0, len(e.thresholdSignatures))

	// collect signatures and proTxHashes
	for proTxHash, signature := range e.thresholdSignatures {
		if len(signature) != bls12381.SignatureSize {
			return nil, fmt.Errorf("invalid vote extension signature len from validator %s: got %d, expected %d",
				proTxHash, len(signature), bls12381.SignatureSize)
		}

		proTxHashes = append(proTxHashes, bytes.Clone(proTxHash[:]))
		signatures = append(signatures, bytes.Clone(signature[:]))
	}

	if len(signatures) > 0 {
		thresholdSignature, err := bls12381.RecoverThresholdSignatureFromShares(signatures, proTxHashes)
		if err != nil {
			return nil, fmt.Errorf("error recovering vote extension %s %X threshold signature: %w",
				e.GetType().String(), e.GetExtension(), err)
		}

		return thresholdSignature, nil
	}

	return nil, fmt.Errorf("vote extension %s of type %X does not have any signatures for threshold-recovering",
		e.GetType().String(), e.GetExtension())
}

// ThresholdRawVoteExtension is a threshold raw type of VoteExtension
type ThresholdRawVoteExtension struct {
	ThresholdVoteExtension
}

func (e ThresholdRawVoteExtension) Copy() VoteExtensionIf {
	inner := e.ThresholdVoteExtension.Copy().(*ThresholdVoteExtension)
	return &ThresholdRawVoteExtension{ThresholdVoteExtension: *inner}
}

// SignItem creates a SignItem for a threshold raw vote extension
//
// Note: signItem.Msg left empty by purpose, as we don't want hash to be checked in Verify()
func (e ThresholdRawVoteExtension) SignItem(_ string, height int64, round int32, quorumType btcjson.LLMQType, quorumHash []byte) (SignItem, error) {
	var signRequestID []byte
	var err error

	ext := &e.VoteExtension

	if ext.XSignRequestId != nil && ext.XSignRequestId.Size() > 0 {
		receivedReqID := ext.GetSignRequestId()
		signRequestID = crypto.Checksum(crypto.Checksum(receivedReqID)) // reverse ext.GetSignRequestId()?
		signRequestID = tmbytes.Reverse(signRequestID)
	} else {
		if signRequestID, err = voteExtensionRequestID(height, round); err != nil {
			return SignItem{}, err
		}
	}

	// ensure Extension is 32 bytes long
	if len(ext.Extension) != crypto.DefaultHashSize {
		return SignItem{}, fmt.Errorf("invalid vote extension %s %X: extension must be %d bytes long",
			ext.Type.String(), ext.Extension, crypto.DefaultHashSize)
	}

	// We sign extension as it is, without any hashing, etc.
	// However, as it is reversed in SignItem.UpdateSignHash, we need to reverse it also here to undo
	// that reversal.
	msgHash := tmbytes.Reverse(ext.Extension)

	signItem := NewSignItemFromHash(quorumType, quorumHash, signRequestID, msgHash)
	// signItem.Msg left empty by purpose, as we don't want hash to be checked in Verify()

	return signItem, nil
}

// voteExtensionRequestID returns vote extension sign request ID used to generate
// threshold signatures
func voteExtensionRequestID(height int64, round int32) ([]byte, error) {
	return heightRoundRequestID("dpevote", height, round), nil
}
