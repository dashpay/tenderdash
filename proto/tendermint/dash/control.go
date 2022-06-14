package dash

import (
	"errors"
	"fmt"

	"github.com/gogo/protobuf/proto"

	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/bls12381"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
)

const (
	ChallengeSize = crypto.HashSize
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
func (m *ControlMessage) Validate() error {
	if m == nil {
		return errors.New("message cannot be nil")
	}

	switch msg := m.Sum.(type) {
	case *ControlMessage_ValidatorChallenge:
		return msg.ValidatorChallenge.Validate()

	case *ControlMessage_ValidatorChallengeResponse:
		return msg.ValidatorChallengeResponse.Validate()

	default:
		return fmt.Errorf("unknown message type: %T", msg)
	}
}

func isValidChallenge(challenge []byte) bool {
	return len(challenge) == ChallengeSize
}

func isValidSignature(signature []byte) bool {
	return len(signature) == bls12381.SignatureSize
}

func (req ValidatorChallenge) Validate() error {
	if !isValidChallenge(req.GetChallenge()) {
		return fmt.Errorf("invalid challenge: %s", tmbytes.HexBytes(req.GetChallenge()))
	}

	if !isValidSignature(req.GetSignature()) {
		return fmt.Errorf("invalid signature: %s", tmbytes.HexBytes(req.GetSignature()))
	}

	return nil
}

// Sign signs the challenge with privkey.
func (req *ValidatorChallenge) Sign(privkey crypto.PrivKey) error {
	if !isValidChallenge(req.GetChallenge()) {
		return fmt.Errorf("invalid challenge: %s", tmbytes.HexBytes(req.Challenge))
	}

	sign, err := privkey.SignDigest(req.GetChallenge())
	if err != nil {
		return fmt.Errorf("cannot sign challenge: %w", err)
	}

	req.Signature = sign
	return nil
}

// Verify verifies challenge with pubkey.
func (req ValidatorChallenge) Verify(pubkey crypto.PubKey) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if !pubkey.VerifySignatureDigest(req.GetChallenge(), req.GetSignature()) {
		return errors.New("challenge signature is invalid")
	}

	return nil
}

// Response generates a response for a given challenge
func (req ValidatorChallenge) Response(privKey crypto.PrivKey) (ValidatorChallengeResponse, error) {
	if err := req.Validate(); err != nil {
		return ValidatorChallengeResponse{}, err
	}

	signature, err := privKey.SignDigest(req.GetChallenge())
	if err != nil {
		return ValidatorChallengeResponse{}, fmt.Errorf("cannot sign challenge: %w", err)
	}

	return ValidatorChallengeResponse{Signature: signature}, nil
}

// Validate checks some basic constraints
func (resp ValidatorChallengeResponse) Validate() error {
	if !isValidSignature(resp.GetSignature()) {
		return fmt.Errorf("invalid signature: %s", tmbytes.HexBytes(resp.GetSignature()))
	}

	return nil
}

// Verify verifies response for a given challenge using pubkey.
func (resp ValidatorChallengeResponse) Verify(challenge []byte, pubkey crypto.PubKey) error {
	if err := resp.Validate(); err != nil {
		return fmt.Errorf("invalid response: %w", err)
	}

	if !pubkey.VerifySignatureDigest(challenge, resp.GetSignature()) {
		return errors.New("challenge signature is invalid")
	}

	return nil
}
