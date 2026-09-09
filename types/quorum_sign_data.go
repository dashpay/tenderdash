package types

import (
	"bytes"
	"fmt"

	bls "github.com/dashpay/bls-signatures/go-bindings"
	"github.com/dashpay/dashd-go/btcjson"
	"github.com/rs/zerolog"

	"github.com/dashpay/tenderdash/crypto"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	tmmath "github.com/dashpay/tenderdash/libs/math"
	"github.com/dashpay/tenderdash/proto/tendermint/types"
)

// QuorumSignData holds data which is necessary for signing and verification block, state, and each vote-extension in a list
type QuorumSignData struct {
	Block                  SignItem
	VoteExtensionSignItems []SignItem
}

// Signs items inside QuorumSignData using a given private key.
//
// Mainly for testing.
func (q QuorumSignData) SignWithPrivkey(key crypto.PrivKey) (QuorumSigns, error) {
	var err error
	var signs QuorumSigns
	if signs.BlockSign, err = key.SignDigest(q.Block.SignHash); err != nil {
		return signs, err
	}

	signs.VoteExtensionSignatures = make([][]byte, 0, len(q.VoteExtensionSignItems))
	for _, item := range q.VoteExtensionSignItems {
		var sign []byte
		if sign, err = key.SignDigest(item.SignHash); err != nil {
			return signs, err
		}
		signs.VoteExtensionSignatures = append(signs.VoteExtensionSignatures, sign)
	}

	return signs, nil
}

// Verify verifies a block and threshold vote extensions quorum signatures.
// It needs quorum to be reached so that we have enough signatures to verify.
func (q QuorumSignData) Verify(pubKey crypto.PubKey, signatures QuorumSigns) error {
	return q.verify(pubKey, signatures, nil)
}

// VerifyWithBudget verifies block and vote-extension signatures after acquiring
// permits for the work at each verification stage.
func (q QuorumSignData) VerifyWithBudget(
	pubKey crypto.PubKey,
	signatures QuorumSigns,
	budget VerificationBudget,
) error {
	return q.verify(pubKey, signatures, budget)
}

func (q QuorumSignData) verify(
	pubKey crypto.PubKey,
	signatures QuorumSigns,
	budget VerificationBudget,
) error {
	if budget != nil && !budget.Allow(1) {
		return ErrVerificationBudgetExhausted
	}
	// Verify the single block signature first and stop if it fails. Vote-extension
	// verification costs one BLS pairing per extension, so authenticating the block
	// signature up front bounds the work an unauthenticated sender can force to a
	// single pairing. The set of accepted inputs is unchanged; only the work done
	// before rejecting a forged message differs.
	if err := q.VerifyBlock(pubKey, signatures); err != nil {
		return err
	}
	if err := q.validateVoteExtensionCount(signatures); err != nil {
		return err
	}
	if cost := len(q.VoteExtensionSignItems); budget != nil && cost > 0 && !budget.Allow(cost) {
		return ErrVerificationBudgetExhausted
	}
	return q.VerifyVoteExtensions(pubKey, signatures)
}

// VerifyBlock verifies block signature
func (q QuorumSignData) VerifyBlock(pubKey crypto.PubKey, signatures QuorumSigns) error {
	if !q.Block.VerifySignature(pubKey, signatures.BlockSign) {
		return ErrVoteInvalidBlockSignature
	}

	return nil
}

// VerifyVoteExtensions verifies threshold vote extensions signatures
func (q QuorumSignData) VerifyVoteExtensions(pubKey crypto.PubKey, signatures QuorumSigns) error {
	if err := q.validateVoteExtensionCount(signatures); err != nil {
		return err
	}

	// Return on the first invalid extension. Each check is one BLS pairing, so
	// stopping early bounds the work a sender with an invalid signature can force.
	// The error deliberately omits the extension bytes, hashes and signature: it
	// is logged once per rejected peer message and its contents are attacker-
	// controlled, so echoing them back turns verification into a log-amplification
	// vector.
	for i, signItem := range q.VoteExtensionSignItems {
		if !signItem.VerifySignature(pubKey, signatures.VoteExtensionSignatures[i]) {
			return fmt.Errorf("vote-extension %d signature is invalid", i)
		}
	}

	return nil
}

func (q QuorumSignData) validateVoteExtensionCount(signatures QuorumSigns) error {
	if len(q.VoteExtensionSignItems) != len(signatures.VoteExtensionSignatures) {
		return ErrVoteExtensionCountMismatch{
			Extensions: len(q.VoteExtensionSignItems),
			Signatures: len(signatures.VoteExtensionSignatures),
		}
	}
	return nil
}

// ErrVoteExtensionCountMismatch is returned when the number of vote-extension
// signatures does not match the number of threshold-recoverable vote extensions
// derived from a vote or commit. An honest peer running a different
// vote-extension configuration reaches this, so it is an application/version
// disagreement rather than a forged signature: commit verification must not
// treat it as evictable cryptographic misbehavior.
type ErrVoteExtensionCountMismatch struct {
	Extensions int
	Signatures int
}

func (e ErrVoteExtensionCountMismatch) Error() string {
	return fmt.Sprintf("count of vote extension signatures (%d) doesn't match recoverable vote extensions (%d)",
		e.Signatures, e.Extensions)
}

// MakeQuorumSignsWithVoteSet creates and returns QuorumSignData struct built with a vote-set and an added vote
func MakeQuorumSignsWithVoteSet(voteSet *VoteSet, vote *types.Vote) (QuorumSignData, error) {
	return MakeQuorumSigns(
		voteSet.chainID,
		voteSet.valSet.QuorumType,
		voteSet.valSet.QuorumHash,
		vote,
	)
}

// MaxVoteExtensions bounds the number of vote-extensions accepted in a single
// vote or commit. Each threshold-recoverable extension costs one BLS signature
// verification, so without a bound an unprivileged peer could pack ~10^4
// extensions into a single ~1 MB message and force that many verifications on
// the consensus goroutine.
//
// Dash Platform's ExtendVote returns at most
// withdrawal_transactions_per_block_limit threshold-recoverable extensions,
// currently 4. This cap keeps generous headroom (8x) for a future protocol
// increase of that limit while still bounding the per-message verification
// work. If that limit is ever raised above this value, raise this constant in
// the same coordinated release.
const MaxVoteExtensions = 32

// makeVerifyQuorumSigns builds sign data for verifying a vote or commit received
// from a peer. Unlike MakeQuorumSigns it rejects a message that carries more
// vote-extensions than any legitimate participant produces (MaxVoteExtensions),
// bounding the BLS verification work a single message can force.
//
// The cap lives here, on the verification path, and deliberately NOT in
// MakeQuorumSigns: MakeQuorumSigns is also how a validator builds sign data for
// its OWN votes when signing, and a node must always be able to sign whatever
// extension count the application produces. Enforcing the anti-DoS bound on the
// signing path would let the cap halt the validator instead of an attacker.
func makeVerifyQuorumSigns(
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	protoVote *types.Vote,
) (QuorumSignData, error) {
	if n := len(protoVote.GetVoteExtensions()); n > MaxVoteExtensions {
		return QuorumSignData{}, fmt.Errorf("too many vote extensions: %d (max %d)", n, MaxVoteExtensions)
	}
	return MakeQuorumSigns(chainID, quorumType, quorumHash, protoVote)
}

// MakeQuorumSigns builds signing data for block, state and vote-extensions
// each a sign-id item consist of request-id, raw data, hash of raw and id
func MakeQuorumSigns(
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	protoVote *types.Vote,
) (QuorumSignData, error) {
	// Convert before MakeBlockSignItem: that helper panics rather than returning an
	// error, so all fallible work belongs ahead of it.
	extensions, err := VoteExtensionsFromProto(protoVote.VoteExtensions...)
	if err != nil {
		return QuorumSignData{}, err
	}
	quorumSign := QuorumSignData{
		Block: MakeBlockSignItem(chainID, protoVote, quorumType, quorumHash),
	}
	quorumSign.VoteExtensionSignItems, err =
		extensions.
			Filter(func(ext VoteExtensionIf) bool {
				return ext.IsThresholdRecoverable()
			}).
			SignItems(chainID, quorumType, quorumHash, protoVote.Height, protoVote.Round)
	if err != nil {
		return QuorumSignData{}, err
	}
	return quorumSign, nil
}

// MakeBlockSignItem creates SignItem struct for a block
func MakeBlockSignItem(chainID string, vote *types.Vote, quorumType btcjson.LLMQType, quorumHash []byte) SignItem {
	reqID := BlockRequestID(vote.Height, vote.Round)
	raw, err := vote.SignBytes(chainID)
	if err != nil {
		panic(fmt.Errorf("block sign item: %w", err))
	}
	return NewSignItem(quorumType, quorumHash, reqID, raw)
}

// BlockRequestID returns a block request ID
func BlockRequestID(height int64, round int32) []byte {
	return heightRoundRequestID("dpbvote", height, round)
}

// SignItem represents signing session data (in field SignItem.ID) that will be signed to get threshold signature share.
// Field names are the same as in Dash Core, but the meaning is different.
// See DIP-0007
type SignItem struct {
	LlmqType   btcjson.LLMQType // Quorum type for which this sign item is created
	ID         []byte           // Request ID for quorum signing
	MsgHash    []byte           // Checksum of Raw
	QuorumHash []byte           // Quorum hash for which this sign item is created

	SignHash []byte // Hash of llmqType, quorumHash, id, and msgHash - as provided to crypto sign/verify functions

	Msg []byte // Raw data to be signed, before any transformations; optional
}

// Validate validates prepared data for signing
func (i *SignItem) Validate() error {
	if len(i.ID) != crypto.DefaultHashSize {
		return fmt.Errorf("invalid request ID size: %X", i.ID)
	}
	if len(i.MsgHash) != crypto.DefaultHashSize {
		return fmt.Errorf("invalid hash size %d: %X", len(i.MsgHash), i.MsgHash)
	}
	if len(i.QuorumHash) != crypto.QuorumHashSize {
		return fmt.Errorf("invalid quorum hash size %d: %X", len(i.QuorumHash), i.QuorumHash)
	}
	// Msg is optional
	if len(i.Msg) > 0 {
		if !bytes.Equal(crypto.Checksum(i.Msg), i.MsgHash) {
			return fmt.Errorf("invalid hash %X for raw data: %X", i.MsgHash, i.Msg)
		}
	}
	return nil
}

func (i SignItem) MarshalZerologObject(e *zerolog.Event) {
	e.Hex("msg", i.Msg)
	e.Hex("signRequestID", i.ID)
	e.Hex("signID", i.SignHash)
	e.Hex("msgHash", i.MsgHash)
	e.Hex("quorumHash", i.QuorumHash)
	e.Uint8("llmqType", tmmath.MustConvertUint8(i.LlmqType))

}

// NewSignItem creates a new instance of SignItem with calculating a hash for a raw and creating signID
//
// Arguments:
// - quorumType: quorum type
// - quorumHash: quorum hash
// - reqID: sign request ID
// - msg: raw data to be signed; it will be hashed with crypto.Checksum()
func NewSignItem(quorumType btcjson.LLMQType, quorumHash, reqID, msg []byte) SignItem {
	msgHash := crypto.Checksum(msg) // FIXME: shouldn't we use sha256(sha256(raw)) here?
	item := NewSignItemFromHash(quorumType, quorumHash, reqID, msgHash)
	item.Msg = msg

	return item
}

// Create a new sign item without raw value, using provided hash.
func NewSignItemFromHash(quorumType btcjson.LLMQType, quorumHash, reqID, msgHash []byte) SignItem {
	item := SignItem{
		ID:         reqID,
		MsgHash:    msgHash,
		LlmqType:   quorumType,
		QuorumHash: quorumHash,
		Msg:        nil, // Raw is empty, as we don't have it
	}

	// By default, reverse fields when calculating SignHash
	item.UpdateSignHash(true)

	return item
}

// UpdateSignHash recalculates signHash field
// If reverse is true, then all []byte elements will be reversed before
// calculating signID
func (i *SignItem) UpdateSignHash(reverse bool) {
	if err := i.Validate(); err != nil {
		panic("invalid sign item: " + err.Error())
	}
	llmqType := i.LlmqType

	quorumHash := i.QuorumHash
	requestID := i.ID
	messageHash := i.MsgHash

	if reverse {
		quorumHash = tmbytes.Reverse(quorumHash)
		requestID = tmbytes.Reverse(requestID)
		messageHash = tmbytes.Reverse(messageHash)
	}

	var blsQuorumHash bls.Hash
	copy(blsQuorumHash[:], quorumHash)

	var blsRequestID bls.Hash
	copy(blsRequestID[:], requestID)

	var blsMessageHash bls.Hash
	copy(blsMessageHash[:], messageHash)

	// fmt.Printf("LlmqType: %x + ", llmqType)
	// fmt.Printf("QuorumHash: %x + ", blsQuorumHash)
	// fmt.Printf("RequestID: %x + ", blsRequestID)
	// fmt.Printf("MsgHash: %x\n", blsMessageHash)

	blsSignHash := bls.BuildSignHash(tmmath.MustConvertUint8(llmqType), blsQuorumHash, blsRequestID, blsMessageHash)

	signHash := make([]byte, 32)
	copy(signHash, blsSignHash[:])

	i.SignHash = signHash
}

// VerifySignature verifies signature for a sign item
func (i *SignItem) VerifySignature(pubkey crypto.PubKey, sig []byte) bool {
	return pubkey.VerifySignatureDigest(i.SignHash, sig)
}
