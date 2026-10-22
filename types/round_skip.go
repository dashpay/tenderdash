package types

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/crypto/bls12381"
	"github.com/dashpay/tenderdash/internal/libs/protoio"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

var (
	// ErrRoundSkipVoteInvalidSignature is returned when a skip vote's signature
	// does not verify against the key share of the validator it names.
	ErrRoundSkipVoteInvalidSignature = errors.New("invalid round skip vote signature")
	// ErrRoundSkipCertificateInvalidSignature is returned when a certificate's
	// quorum signature does not verify against the quorum's public key.
	ErrRoundSkipCertificateInvalidSignature = errors.New("invalid round skip certificate signature")
)

// roundSkipRequestIDPrefix separates skip vote signatures from other signatures
// made with the same quorum key share.
const roundSkipRequestIDPrefix = "dpskip"

// RoundSkipVote is a validator's vote to hand a round to the next proposer. A
// validator sends it when the proposer of the round it waits in has stopped
// sending heartbeats.
//
// A skip vote is not a prevote: it locks nothing, and a validator that sent one
// may still vote for the round's proposal if it arrives. Every validator signs
// the same bytes for a height and round, so there is only one skip vote a
// validator can make for a round, and the votes of +2/3 recover into a single
// quorum signature: the RoundSkipCertificate.
type RoundSkipVote struct {
	Height             int64            `json:"height,string"`
	Round              int32            `json:"round"`
	ValidatorProTxHash ProTxHash        `json:"validator_pro_tx_hash"`
	ValidatorIndex     int32            `json:"validator_index"`
	Signature          tmbytes.HexBytes `json:"signature"`
}

// ValidateBasic performs checks that need no state.
func (v *RoundSkipVote) ValidateBasic() error {
	if v == nil {
		return errors.New("nil round skip vote")
	}
	if v.Height <= 0 {
		return fmt.Errorf("non-positive height %d", v.Height)
	}
	if v.Round < 0 {
		return fmt.Errorf("negative round %d", v.Round)
	}
	if len(v.ValidatorProTxHash) != crypto.ProTxHashSize {
		return fmt.Errorf("validator pro tx hash has %d bytes, expected %d",
			len(v.ValidatorProTxHash), crypto.ProTxHashSize)
	}
	if v.ValidatorIndex < 0 {
		return fmt.Errorf("negative validator index %d", v.ValidatorIndex)
	}
	if len(v.Signature) == 0 {
		return errors.New("signature is missing")
	}
	if len(v.Signature) > SignatureSize {
		return fmt.Errorf("signature is too big (max: %d)", SignatureSize)
	}
	return nil
}

// Verify checks the vote's signature against pubKey, the key share of the
// validator the vote names.
func (v *RoundSkipVote) Verify(
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	pubKey crypto.PubKey,
) error {
	if pubKey == nil {
		return errors.New("no public key to verify the round skip vote with")
	}
	signItem := RoundSkipSignItem(chainID, v.Height, v.Round, quorumType, quorumHash)
	if !pubKey.VerifySignatureDigest(signItem.SignHash, v.Signature) {
		return ErrRoundSkipVoteInvalidSignature
	}
	return nil
}

func (v *RoundSkipVote) String() string {
	if v == nil {
		return "nil-RoundSkipVote"
	}
	return fmt.Sprintf("RoundSkipVote{%d/%d %d:%s}",
		v.Height, v.Round, v.ValidatorIndex, v.ValidatorProTxHash.ShortString())
}

// ToProto converts the vote to its protobuf form.
func (v *RoundSkipVote) ToProto() *tmproto.RoundSkipVote {
	if v == nil {
		return nil
	}
	return &tmproto.RoundSkipVote{
		Height:             v.Height,
		Round:              v.Round,
		ValidatorProTxHash: v.ValidatorProTxHash,
		ValidatorIndex:     v.ValidatorIndex,
		Signature:          v.Signature,
	}
}

// RoundSkipVoteFromProto converts a protobuf skip vote and checks it with
// ValidateBasic.
func RoundSkipVoteFromProto(pb *tmproto.RoundSkipVote) (*RoundSkipVote, error) {
	if pb == nil {
		return nil, errors.New("nil round skip vote")
	}
	v := &RoundSkipVote{
		Height:             pb.Height,
		Round:              pb.Round,
		ValidatorProTxHash: pb.ValidatorProTxHash,
		ValidatorIndex:     pb.ValidatorIndex,
		Signature:          pb.Signature,
	}
	return v, v.ValidateBasic()
}

// RoundSkipCertificate proves that validators holding +2/3 of the voting power
// voted to skip a round. Any node that holds one may move past that round.
type RoundSkipCertificate struct {
	Height             int64             `json:"height,string"`
	Round              int32             `json:"round"`
	QuorumHash         crypto.QuorumHash `json:"quorum_hash"`
	ThresholdSignature tmbytes.HexBytes  `json:"threshold_signature"`
}

// ValidateBasic performs checks that need no state.
func (c *RoundSkipCertificate) ValidateBasic() error {
	if c == nil {
		return errors.New("nil round skip certificate")
	}
	if c.Height <= 0 {
		return fmt.Errorf("non-positive height %d", c.Height)
	}
	if c.Round < 0 {
		return fmt.Errorf("negative round %d", c.Round)
	}
	if len(c.QuorumHash) != crypto.QuorumHashSize {
		return fmt.Errorf("quorum hash has %d bytes, expected %d", len(c.QuorumHash), crypto.QuorumHashSize)
	}
	if len(c.ThresholdSignature) != SignatureSize {
		return fmt.Errorf("threshold signature has %d bytes, expected %d", len(c.ThresholdSignature), SignatureSize)
	}
	return nil
}

// Verify checks that the certificate was signed by the quorum of valSet.
func (c *RoundSkipCertificate) Verify(chainID string, valSet *ValidatorSet) error {
	if valSet == nil || valSet.ThresholdPublicKey == nil {
		return errors.New("no quorum public key to verify the round skip certificate with")
	}
	if !bytes.Equal(c.QuorumHash, valSet.QuorumHash) {
		return fmt.Errorf("round skip certificate signed by quorum %s, expected %s",
			c.QuorumHash.ShortString(), valSet.QuorumHash.ShortString())
	}
	signItem := RoundSkipSignItem(chainID, c.Height, c.Round, valSet.QuorumType, valSet.QuorumHash)
	if !valSet.ThresholdPublicKey.VerifySignatureDigest(signItem.SignHash, c.ThresholdSignature) {
		return ErrRoundSkipCertificateInvalidSignature
	}
	return nil
}

func (c *RoundSkipCertificate) String() string {
	if c == nil {
		return "nil-RoundSkipCertificate"
	}
	return fmt.Sprintf("RoundSkipCertificate{%d/%d quorum:%s}", c.Height, c.Round, c.QuorumHash.ShortString())
}

// ToProto converts the certificate to its protobuf form.
func (c *RoundSkipCertificate) ToProto() *tmproto.RoundSkipCertificate {
	if c == nil {
		return nil
	}
	return &tmproto.RoundSkipCertificate{
		Height:             c.Height,
		Round:              c.Round,
		QuorumHash:         c.QuorumHash,
		ThresholdSignature: c.ThresholdSignature,
	}
}

// RoundSkipCertificateFromProto converts a protobuf certificate and checks it
// with ValidateBasic.
func RoundSkipCertificateFromProto(pb *tmproto.RoundSkipCertificate) (*RoundSkipCertificate, error) {
	if pb == nil {
		return nil, errors.New("nil round skip certificate")
	}
	c := &RoundSkipCertificate{
		Height:             pb.Height,
		Round:              pb.Round,
		QuorumHash:         pb.QuorumHash,
		ThresholdSignature: pb.ThresholdSignature,
	}
	return c, c.ValidateBasic()
}

// RecoverRoundSkipCertificate recovers the quorum signature of valSet from the
// skip votes of its members, which must be for one height and round and must
// already be verified. It fails unless the votes hold the quorum's threshold
// voting power.
func RecoverRoundSkipCertificate(chainID string, valSet *ValidatorSet, votes []*RoundSkipVote) (*RoundSkipCertificate, error) {
	if len(votes) == 0 {
		return nil, errors.New("no round skip votes to recover a certificate from")
	}
	height, round := votes[0].Height, votes[0].Round
	signatures := make([][]byte, 0, len(votes))
	proTxHashes := make([][]byte, 0, len(votes))
	seen := make(map[string]struct{}, len(votes))
	var power int64
	for _, vote := range votes {
		if vote.Height != height || vote.Round != round {
			return nil, fmt.Errorf("round skip votes for %d/%d and %d/%d", height, round, vote.Height, vote.Round)
		}
		key := vote.ValidatorProTxHash.String()
		if _, ok := seen[key]; ok {
			continue
		}
		_, val := valSet.GetByProTxHash(vote.ValidatorProTxHash)
		if val == nil {
			return nil, fmt.Errorf("round skip vote from %s, which is not a validator", vote.ValidatorProTxHash.ShortString())
		}
		seen[key] = struct{}{}
		power += val.VotingPower
		signatures = append(signatures, vote.Signature)
		proTxHashes = append(proTxHashes, vote.ValidatorProTxHash)
	}
	if threshold := valSet.QuorumVotingThresholdPower(); power < threshold {
		return nil, fmt.Errorf("round skip votes hold %d voting power, %d needed", power, threshold)
	}

	signature, err := bls12381.RecoverThresholdSignatureFromShares(signatures, proTxHashes)
	if err != nil {
		return nil, fmt.Errorf("cannot recover round skip certificate signature: %w", err)
	}
	certificate := &RoundSkipCertificate{
		Height:             height,
		Round:              round,
		QuorumHash:         valSet.QuorumHash,
		ThresholdSignature: signature,
	}
	if err := certificate.Verify(chainID, valSet); err != nil {
		return nil, err
	}
	return certificate, nil
}

// RoundSkipSignBytes returns the canonical bytes every validator voting to skip
// a round signs.
func RoundSkipSignBytes(chainID string, height int64, round int32) []byte {
	canonical := tmproto.CanonicalRoundSkip{
		Type:    tmproto.RoundSkipType,
		Height:  height,
		Round:   int64(round),
		ChainID: chainID,
	}
	bz, err := protoio.MarshalDelimited(&canonical)
	if err != nil {
		panic(err)
	}
	return bz
}

// RoundSkipRequestID returns the quorum signing request ID of the skip votes
// for a height and round.
func RoundSkipRequestID(height int64, round int32) []byte {
	return heightRoundRequestID(roundSkipRequestIDPrefix, height, round)
}

// RoundSkipSignItem returns what a validator signs with its quorum key share to
// vote to skip a round.
func RoundSkipSignItem(
	chainID string,
	height int64,
	round int32,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
) SignItem {
	return NewSignItem(quorumType, quorumHash, RoundSkipRequestID(height, round), RoundSkipSignBytes(chainID, height, round))
}
