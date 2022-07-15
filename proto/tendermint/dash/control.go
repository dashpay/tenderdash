package dash

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/gogo/protobuf/proto"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/types"
)

// Wrap implements the p2p Wrapper interface and wraps a message.
func (m *ControlMessage) Wrap(pb proto.Message) error {
	switch msg := pb.(type) {
	case *ValidatorChallenge:
		m.Sum = &ControlMessage_ValidatorChallenge{ValidatorChallenge: msg}

	case *ValidatorChallengeResponse:
		m.Sum = &ControlMessage_ValidatorChallengeResponse{ValidatorChallengeResponse: msg}

	default:
		return fmt.Errorf("unknown message: %T", msg)
	}

	return nil
}

// Unwrap implements the p2p Wrapper interface and unwraps a wrapped message.
func (m *ControlMessage) Unwrap() (proto.Message, error) {
	switch msg := m.Sum.(type) {
	case *ControlMessage_ValidatorChallenge:
		return m.GetValidatorChallenge(), nil

	case *ControlMessage_ValidatorChallengeResponse:
		return m.GetValidatorChallengeResponse(), nil

	default:
		return nil, fmt.Errorf("unknown message: %T", msg)
	}
}

// Validate validates the message returning an error upon failure.
func (m *ControlMessage) Validate(senderID, recipientID types.NodeID, senderProTxHash, recipientProTxHash types.ProTxHash) error {
	if m == nil {
		return errors.New("message cannot be nil")
	}

	switch msg := m.Sum.(type) {
	case *ControlMessage_ValidatorChallenge:
		return msg.ValidatorChallenge.Validate(senderID, recipientID, senderProTxHash, recipientProTxHash)

	case *ControlMessage_ValidatorChallengeResponse:
		return msg.ValidatorChallengeResponse.Validate()

	default:
		return fmt.Errorf("unknown message type: %T", msg)
	}
}

func NewValidatorChallenge(
	senderNodeID, recipientNodeID types.NodeID,
	senderProTxHash, recipientProTxHash types.ProTxHash,
	senderHeight int64,
	quorumHash crypto.QuorumHash,
) ValidatorChallenge {
	token := make([]byte, 12)
	now := time.Now().UnixNano()
	binary.LittleEndian.PutUint64(token, uint64(now))

	challenge := ValidatorChallenge{
		SenderNodeID:       string(senderNodeID),
		RecipientNodeID:    string(recipientNodeID),
		SenderProTxHash:    senderProTxHash,
		RecipientProTxHash: recipientProTxHash,
		SenderHeight:       senderHeight,
		Token:              token,
		QuorumHash:         quorumHash,
	}

	return challenge
}

// Validate checks if the challenge is valid. It does NOT verify the signature.
// If `token` arg is nil, also token will not be verified.
func (challenge ValidatorChallenge) Validate(
	senderNodeID, recipientNodeID types.NodeID,
	senderProTxHash, recipientProTxHash types.ProTxHash,
) error {
	if !senderNodeID.Equal(types.NodeID(challenge.GetSenderNodeID())) {
		return fmt.Errorf("invalid sender node ID - got: %s, expected: %s", challenge.GetSenderNodeID(), senderNodeID)
	}
	if !recipientNodeID.Equal(types.NodeID(challenge.GetRecipientNodeID())) {
		return fmt.Errorf("invalid recipient node ID - got: %s, expected: %s", challenge.GetRecipientNodeID(), recipientNodeID)
	}

	if !senderProTxHash.Equal(challenge.GetSenderProTxHash()) {
		return fmt.Errorf(
			"invalid sender node proTxHash - got: %s, expected: %s",
			tmbytes.HexBytes(challenge.GetSenderProTxHash()).ShortString(),
			senderProTxHash.ShortString())
	}

	if !recipientProTxHash.Equal(challenge.GetRecipientProTxHash()) {
		return fmt.Errorf(
			"invalid recipient node proTxHash - got: %s, expected: %s",
			tmbytes.HexBytes(challenge.GetRecipientProTxHash()).ShortString(),
			recipientProTxHash.ShortString())
	}

	if challenge.SenderHeight < 0 {
		return fmt.Errorf("invalid sender height: %d", challenge.SenderHeight)
	}

	if len(challenge.QuorumHash) != crypto.QuorumHashSize {
		return fmt.Errorf("invalid quorum hash length: is: %d, expected: %d", len(challenge.QuorumHash), crypto.QuorumHashSize)
	}

	signLen := len(challenge.Signature)
	if signLen != 0 && signLen != bls12381.SignatureSize {
		return fmt.Errorf("invalid challenge signature length - is: %d, should be: %d", signLen, bls12381.SignatureSize)
	}

	return nil
}

// Digest returns digest (checksum/hash) of the challenge, excluding signature field
func (challenge ValidatorChallenge) Digest() (tmbytes.HexBytes, error) {
	challenge.Signature = nil // this should not affect original signature, as we don't pass challenge by ptr
	signBytes, err := proto.Marshal(&challenge)
	if err != nil {
		return nil, fmt.Errorf("cannot prepare challenge bytes to sign: %w", err)
	}
	checksum := crypto.Checksum(signBytes)

	return checksum, nil
}

// Sign signs the challenge with privkey.
func (challenge *ValidatorChallenge) Sign(consensusPrivKey crypto.PrivKey) error {
	signature, err := signChallenge(*challenge, consensusPrivKey)
	if err != nil {
		return err
	}

	challenge.Signature = signature
	return nil
}

// Verify verifies challenge signature.
func (challenge ValidatorChallenge) Verify(pubkey crypto.PubKey) error {
	return verifyChallengeSignature(challenge, challenge.GetSignature(), pubkey)
}

// NewResponse generates a response for a given challenge.
func NewResponse(challenge ValidatorChallenge, privKey crypto.PrivKey) (ValidatorChallengeResponse, error) {
	signature, err := signChallenge(challenge, privKey)
	if err != nil {
		return ValidatorChallengeResponse{}, err
	}
	return ValidatorChallengeResponse{Signature: signature}, nil
}

// Validate checks some basic constraints
func (resp ValidatorChallengeResponse) Validate() error {
	if len(resp.GetSignature()) != bls12381.SignatureSize {
		return fmt.Errorf("invalid signature: %s", tmbytes.HexBytes(resp.GetSignature()))
	}
	return nil
}

// Verify verifies signature
func (resp ValidatorChallengeResponse) Verify(challenge ValidatorChallenge, peerPubkey crypto.PubKey) error {
	return verifyChallengeSignature(challenge, resp.GetSignature(), peerPubkey)
}

func signChallenge(challenge ValidatorChallenge, privkey crypto.PrivKey) (tmbytes.HexBytes, error) {
	digest, err := challenge.Digest()
	if err != nil {
		return nil, err
	}
	signature, err := privkey.SignDigest(digest)
	if err != nil {
		return nil, fmt.Errorf("cannot sign challenge: %w", err)
	}

	return signature, nil
}

func verifyChallengeSignature(challenge ValidatorChallenge, signature tmbytes.HexBytes, key crypto.PubKey) error {
	digest, err := challenge.Digest()
	if err != nil {
		return err
	}
	if !key.VerifySignatureDigest(digest, signature) {
		return fmt.Errorf("challenge signature is invalid: hash=%X, signature=%X, pubkey=%X", digest, signature, key.Bytes())
	}
	return nil
}
