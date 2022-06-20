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

// Wrap implements the p2p Wrapper interface and wraps a blockchain message.
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

// Unwrap implements the p2p Wrapper interface and unwraps a wrapped blockchain
// message.
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
func (m *ControlMessage) Validate(nodeID, peerID types.NodeID, nodeProTxHash, peerProTxHash types.ProTxHash, token tmbytes.HexBytes) error {
	if m == nil {
		return errors.New("message cannot be nil")
	}

	switch msg := m.Sum.(type) {
	case *ControlMessage_ValidatorChallenge:
		return msg.ValidatorChallenge.Validate(nodeID, peerID, nodeProTxHash, peerProTxHash, token)

	case *ControlMessage_ValidatorChallengeResponse:
		return msg.ValidatorChallengeResponse.Validate()

	default:
		return fmt.Errorf("unknown message type: %T", msg)
	}
}

func NewValidatorChallenge(
	senderNodeID, recipientNodeID types.NodeID,
	senderProTxHash, recipientProTxHash types.ProTxHash,
) ValidatorChallenge {
	token := make([]byte, 12)
	now := time.Now().UnixNano()
	binary.LittleEndian.PutUint64(token, uint64(now))

	challenge := ValidatorChallenge{
		SenderProtxhash:    senderProTxHash,
		RecipientProtxhash: recipientProTxHash,
		SenderNodeId:       string(senderNodeID),
		RecipientNodeId:    string(recipientNodeID),
		// Token:              token,
	}

	return challenge
}

// Validate checks if the challenge is valid. It does NOT verify the signature.
// If `token` arg is nil, also token will not be verified
func (challenge ValidatorChallenge) Validate(
	senderNodeID, recipientPeerID types.NodeID,
	senderProTxHash, recipientProTxHash types.ProTxHash,
	token tmbytes.HexBytes,
) error {
	if !senderNodeID.Equal(types.NodeID(challenge.GetSenderNodeId())) {
		return fmt.Errorf("invalid sender node ID - got: %s, expected: %s", challenge.GetSenderNodeId(), senderNodeID)
	}
	if !recipientPeerID.Equal(types.NodeID(challenge.GetRecipientNodeId())) {
		return fmt.Errorf("invalid recipient node ID - got: %s, expected: %s", challenge.GetRecipientNodeId(), recipientPeerID)
	}

	if !senderProTxHash.Equal(challenge.GetSenderProtxhash()) {
		return fmt.Errorf(
			"invalid sender node proTxHash - got: %s, expected: %s",
			tmbytes.HexBytes(challenge.GetSenderProtxhash()).ShortString(),
			senderProTxHash.ShortString())
	}

	if !recipientProTxHash.Equal(challenge.GetRecipientProtxhash()) {
		return fmt.Errorf(
			"invalid recipient node proTxHash - got: %s, expected: %s",
			tmbytes.HexBytes(challenge.GetRecipientProtxhash()).ShortString(),
			recipientProTxHash.ShortString())
	}

	if token != nil && !token.Equal(challenge.GetToken()) {
		return fmt.Errorf(
			"invalid token - got: %s, expected: %s",
			tmbytes.HexBytes(challenge.GetToken()).String(),
			token.String())
	}

	signLen := len(challenge.Signature)
	if signLen != 0 && signLen != bls12381.SignatureSize {
		return fmt.Errorf("invalid challenge signature length - is: %d, should be: %d", signLen, bls12381.SignatureSize)
	}

	return nil
}
func (challenge ValidatorChallenge) signBytes() (tmbytes.HexBytes, error) {
	challenge.Signature = nil // this should not affect original signature, as we don't pass challenge by ptr
	signBytes, err := proto.Marshal(&challenge)
	if err != nil {
		return nil, fmt.Errorf("cannot prepare challenge bytes to sign: %w", err)
	}
	checksum := crypto.Checksum(signBytes)
	// fmt.Printf("checksum: %X\n", checksum)
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

// Response generates a response for a given challenge.
func (challenge ValidatorChallenge) Response(privKey crypto.PrivKey) (ValidatorChallengeResponse, error) {
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

func signChallenge(challenge ValidatorChallenge, key crypto.PrivKey) (tmbytes.HexBytes, error) {
	signBytes, err := challenge.signBytes()
	if err != nil {
		return nil, err
	}
	sign, err := key.SignDigest(signBytes)
	if err != nil {
		return nil, fmt.Errorf("cannot sign challenge: %w", err)
	}

	return sign, nil
}

func verifyChallengeSignature(challenge ValidatorChallenge, signature tmbytes.HexBytes, key crypto.PubKey) error {
	signBytes, err := challenge.signBytes()
	if err != nil {
		return err
	}
	if !key.VerifySignatureDigest(signBytes, signature) {
		return fmt.Errorf("challenge signature is invalid: sign bytes %X, signature %X", signBytes, signature)
	}
	return nil
}
