package types

import (
	"errors"
	"fmt"
	"time"

	"github.com/dashpay/dashd-go/btcjson"

	"github.com/dashpay/tenderdash/crypto"
	"github.com/dashpay/tenderdash/internal/libs/protoio"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
)

// ErrProposerHeartbeatInvalidSignature is returned when a heartbeat's signature
// does not verify against the public key share of the proposer it names.
var ErrProposerHeartbeatInvalidSignature = errors.New("invalid proposer heartbeat signature")

// proposerHeartbeatRequestIDPrefix separates heartbeat signatures from vote and
// proposal signatures made with the same quorum key share.
const proposerHeartbeatRequestIDPrefix = "dphb"

// ProposerHeartbeat is sent by the proposer of a round while it waits for
// transactions, to tell validators that it is still there and able to propose.
//
// A heartbeat changes no consensus state. Validators only use it to tell a live
// proposer from a silent one.
type ProposerHeartbeat struct {
	Height            int64            `json:"height,string"`
	Round             int32            `json:"round"`
	Sequence          uint64           `json:"sequence,string"`
	ProposerProTxHash ProTxHash        `json:"proposer_pro_tx_hash"`
	Timestamp         time.Time        `json:"timestamp"`
	Signature         tmbytes.HexBytes `json:"signature"`
}

// ValidateBasic performs checks that need no state.
func (hb *ProposerHeartbeat) ValidateBasic() error {
	if hb == nil {
		return errors.New("nil proposer heartbeat")
	}
	if hb.Height <= 0 {
		return fmt.Errorf("non-positive height %d", hb.Height)
	}
	if hb.Round < 0 {
		return fmt.Errorf("negative round %d", hb.Round)
	}
	if len(hb.ProposerProTxHash) != crypto.ProTxHashSize {
		return fmt.Errorf("proposer pro tx hash has %d bytes, expected %d",
			len(hb.ProposerProTxHash), crypto.ProTxHashSize)
	}
	if len(hb.Signature) == 0 {
		return errors.New("signature is missing")
	}
	if len(hb.Signature) > SignatureSize {
		return fmt.Errorf("signature is too big (max: %d)", SignatureSize)
	}
	return nil
}

// Verify checks the heartbeat's signature against pubKey, the public key share
// of the proposer of the heartbeat's height and round.
func (hb *ProposerHeartbeat) Verify(
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
	pubKey crypto.PubKey,
) error {
	if pubKey == nil {
		return errors.New("no public key to verify the proposer heartbeat with")
	}
	signItem := ProposerHeartbeatSignItem(chainID, hb.ToProto(), quorumType, quorumHash)
	if !pubKey.VerifySignatureDigest(signItem.SignHash, hb.Signature) {
		return ErrProposerHeartbeatInvalidSignature
	}
	return nil
}

func (hb *ProposerHeartbeat) String() string {
	if hb == nil {
		return "nil-ProposerHeartbeat"
	}
	return fmt.Sprintf("ProposerHeartbeat{%d/%d #%d %s @ %s}",
		hb.Height, hb.Round, hb.Sequence, hb.ProposerProTxHash.ShortString(), CanonicalTime(hb.Timestamp))
}

// ToProto converts the heartbeat to its protobuf form.
func (hb *ProposerHeartbeat) ToProto() *tmproto.ProposerHeartbeat {
	if hb == nil {
		return nil
	}
	return &tmproto.ProposerHeartbeat{
		Height:            hb.Height,
		Round:             hb.Round,
		Sequence:          hb.Sequence,
		ProposerProTxHash: hb.ProposerProTxHash,
		Timestamp:         hb.Timestamp,
		Signature:         hb.Signature,
	}
}

// ProposerHeartbeatFromProto converts a protobuf heartbeat and checks it with
// ValidateBasic.
func ProposerHeartbeatFromProto(pb *tmproto.ProposerHeartbeat) (*ProposerHeartbeat, error) {
	if pb == nil {
		return nil, errors.New("nil proposer heartbeat")
	}
	hb := &ProposerHeartbeat{
		Height:            pb.Height,
		Round:             pb.Round,
		Sequence:          pb.Sequence,
		ProposerProTxHash: pb.ProposerProTxHash,
		Timestamp:         pb.Timestamp,
		Signature:         pb.Signature,
	}
	return hb, hb.ValidateBasic()
}

// ProposerHeartbeatSignBytes returns the canonical bytes the proposer signs.
func ProposerHeartbeatSignBytes(chainID string, hb *tmproto.ProposerHeartbeat) []byte {
	canonical := tmproto.CanonicalProposerHeartbeat{
		Type:      tmproto.ProposerHeartbeatType,
		Height:    hb.Height,
		Round:     int64(hb.Round),
		Sequence:  hb.Sequence,
		Timestamp: hb.Timestamp,
		ChainID:   chainID,
	}
	bz, err := protoio.MarshalDelimited(&canonical)
	if err != nil {
		panic(err)
	}
	return bz
}

// ProposerHeartbeatRequestID returns the quorum signing request ID of one
// heartbeat. It includes the sequence, so every heartbeat a proposer sends in
// a run is its own signing request.
//
// A proposer that restarts with its clock gone back can repeat a sequence, and
// so an ID, for a heartbeat with another timestamp. That is harmless: the
// timestamp is in the signed message, so the signature differs, and the signer
// keeps no record of the IDs it signed under, as Dash Core does not for
// `quorum platformsign` without submitting.
func ProposerHeartbeatRequestID(height int64, round int32, sequence uint64) []byte {
	return heightRoundRequestID(proposerHeartbeatRequestIDPrefix, height, round, sequence)
}

// ProposerHeartbeatSignItem returns what the proposer signs with its quorum key
// share for hb.
func ProposerHeartbeatSignItem(
	chainID string,
	hb *tmproto.ProposerHeartbeat,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
) SignItem {
	return NewSignItem(
		quorumType,
		quorumHash,
		ProposerHeartbeatRequestID(hb.Height, hb.Round, hb.Sequence),
		ProposerHeartbeatSignBytes(chainID, hb),
	)
}
