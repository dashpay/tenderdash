package types

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dashevo/dashd-go/btcjson"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/merkle"
	"github.com/tendermint/tendermint/crypto/tmhash"
	tmjson "github.com/tendermint/tendermint/libs/json"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
)

// Evidence represents any provable malicious activity by a validator.
// Verification logic for each evidence is part of the evidence module.
type Evidence interface {
	ABCI() []abci.Evidence // forms individual evidence to be sent to the application
	Bytes() []byte         // bytes which comprise the evidence
	Hash() []byte          // hash of the evidence
	Height() int64         // height of the infraction
	String() string        // string format of the evidence
	Time() time.Time       // time of the infraction
	ValidateBasic() error  // basic consistency check
}

//--------------------------------------------------------------------------------------

// DuplicateVoteEvidence contains evidence of a single validator signing two conflicting votes.
type DuplicateVoteEvidence struct {
	VoteA *Vote `json:"vote_a"`
	VoteB *Vote `json:"vote_b"`

	// abci specific information
	TotalVotingPower int64
	ValidatorPower   int64
	Timestamp        time.Time
}

var _ Evidence = &DuplicateVoteEvidence{}

// NewDuplicateVoteEvidence creates DuplicateVoteEvidence with right ordering given
// two conflicting votes. If one of the votes is nil, evidence returned is nil as well
func NewDuplicateVoteEvidence(vote1, vote2 *Vote, blockTime time.Time, valSet *ValidatorSet) (*DuplicateVoteEvidence, error) {
	var voteA, voteB *Vote
	if vote1 == nil || vote2 == nil {
		return nil, errors.New("missing vote")
	}
	if valSet == nil {
		return nil, errors.New("missing validator set")
	}
	idx, val := valSet.GetByProTxHash(vote1.ValidatorProTxHash)
	if idx == -1 {
		return nil, errors.New("validator not in validator set")
	}

	if strings.Compare(vote1.BlockID.Key(), vote2.BlockID.Key()) == -1 {
		voteA = vote1
		voteB = vote2
	} else {
		voteA = vote2
		voteB = vote1
	}
	return &DuplicateVoteEvidence{
		VoteA:            voteA,
		VoteB:            voteB,
		TotalVotingPower: valSet.TotalVotingPower(),
		ValidatorPower:   val.VotingPower,
		Timestamp:        blockTime,
	}, nil
}

// ABCI returns the application relevant representation of the evidence
func (dve *DuplicateVoteEvidence) ABCI() []abci.Evidence {
	return []abci.Evidence{{
		Type: abci.EvidenceType_DUPLICATE_VOTE,
		Validator: abci.Validator{
			ProTxHash: dve.VoteA.ValidatorProTxHash,
			Power:     dve.ValidatorPower,
		},
		Height:           dve.VoteA.Height,
		Time:             dve.Timestamp,
		TotalVotingPower: dve.TotalVotingPower,
	}}
}

// Bytes returns the proto-encoded evidence as a byte array.
func (dve *DuplicateVoteEvidence) Bytes() []byte {
	pbe := dve.ToProto()
	bz, err := pbe.Marshal()
	if err != nil {
		panic("marshaling duplicate vote evidence to bytes: " + err.Error())
	}

	return bz
}

// Hash returns the hash of the evidence.
func (dve *DuplicateVoteEvidence) Hash() []byte {
	return tmhash.Sum(dve.Bytes())
}

// Height returns the height of the infraction
func (dve *DuplicateVoteEvidence) Height() int64 {
	return dve.VoteA.Height
}

// String returns a string representation of the evidence.
func (dve *DuplicateVoteEvidence) String() string {
	return fmt.Sprintf("DuplicateVoteEvidence{VoteA: %v, VoteB: %v}", dve.VoteA, dve.VoteB)
}

// Time returns the time of the infraction
func (dve *DuplicateVoteEvidence) Time() time.Time {
	return dve.Timestamp
}

// ValidateBasic performs basic validation.
func (dve *DuplicateVoteEvidence) ValidateBasic() error {
	if dve == nil {
		return errors.New("empty duplicate vote evidence")
	}

	if dve.VoteA == nil || dve.VoteB == nil {
		return fmt.Errorf("one or both of the votes are empty %v, %v", dve.VoteA, dve.VoteB)
	}
	if err := dve.VoteA.ValidateBasic(); err != nil {
		return fmt.Errorf("invalid VoteA: %w", err)
	}
	if err := dve.VoteB.ValidateBasic(); err != nil {
		return fmt.Errorf("invalid VoteB: %w", err)
	}
	// Enforce Votes are lexicographically sorted on blockID
	if strings.Compare(dve.VoteA.BlockID.Key(), dve.VoteB.BlockID.Key()) >= 0 {
		return errors.New("duplicate votes in invalid order")
	}
	return nil
}

// ValidateABCI validates the ABCI component of the evidence by checking the
// timestamp, validator power and total voting power.
func (dve *DuplicateVoteEvidence) ValidateABCI(
	val *Validator,
	valSet *ValidatorSet,
	evidenceTime time.Time,
) error {

	if dve.Timestamp != evidenceTime {
		return fmt.Errorf(
			"evidence has a different time to the block it is associated with (%v != %v)",
			dve.Timestamp, evidenceTime)
	}

	if val.VotingPower != dve.ValidatorPower {
		return fmt.Errorf("validator power from evidence and our validator set does not match (%d != %d)",
			dve.ValidatorPower, val.VotingPower)
	}
	if valSet.TotalVotingPower() != dve.TotalVotingPower {
		return fmt.Errorf("total voting power from the evidence and our validator set does not match (%d != %d)",
			dve.TotalVotingPower, valSet.TotalVotingPower())
	}

	return nil
}

// GenerateABCI populates the ABCI component of the evidence. This includes the
// validator power, timestamp and total voting power.
func (dve *DuplicateVoteEvidence) GenerateABCI(
	val *Validator,
	valSet *ValidatorSet,
	evidenceTime time.Time,
) {
	dve.ValidatorPower = val.VotingPower
	dve.TotalVotingPower = valSet.TotalVotingPower()
	dve.Timestamp = evidenceTime
}

// ToProto encodes DuplicateVoteEvidence to protobuf
func (dve *DuplicateVoteEvidence) ToProto() *tmproto.DuplicateVoteEvidence {
	voteB := dve.VoteB.ToProto()
	voteA := dve.VoteA.ToProto()
	tp := tmproto.DuplicateVoteEvidence{
		VoteA:            voteA,
		VoteB:            voteB,
		TotalVotingPower: dve.TotalVotingPower,
		ValidatorPower:   dve.ValidatorPower,
		Timestamp:        dve.Timestamp,
	}
	return &tp
}

// DuplicateVoteEvidenceFromProto decodes protobuf into DuplicateVoteEvidence
func DuplicateVoteEvidenceFromProto(pb *tmproto.DuplicateVoteEvidence) (*DuplicateVoteEvidence, error) {
	if pb == nil {
		return nil, errors.New("nil duplicate vote evidence")
	}

	vA, err := VoteFromProto(pb.VoteA)
	if err != nil {
		return nil, err
	}

	vB, err := VoteFromProto(pb.VoteB)
	if err != nil {
		return nil, err
	}

	dve := &DuplicateVoteEvidence{
		VoteA:            vA,
		VoteB:            vB,
		TotalVotingPower: pb.TotalVotingPower,
		ValidatorPower:   pb.ValidatorPower,
		Timestamp:        pb.Timestamp,
	}

	return dve, dve.ValidateBasic()
}

//------------------------------------------------------------------------------------------

// EvidenceList is a list of Evidence. Evidences is not a word.
type EvidenceList []Evidence

// Hash returns the simple merkle root hash of the EvidenceList.
func (evl EvidenceList) Hash() []byte {
	// These allocations are required because Evidence is not of type Bytes, and
	// golang slices can't be typed cast. This shouldn't be a performance problem since
	// the Evidence size is capped.
	evidenceBzs := make([][]byte, len(evl))
	for i := 0; i < len(evl); i++ {
		// TODO: We should change this to the hash. Using bytes contains some unexported data that
		// may cause different hashes
		evidenceBzs[i] = evl[i].Bytes()
	}
	return merkle.HashFromByteSlices(evidenceBzs)
}

func (evl EvidenceList) String() string {
	s := ""
	for _, e := range evl {
		s += fmt.Sprintf("%s\t\t", e)
	}
	return s
}

// Has returns true if the evidence is in the EvidenceList.
func (evl EvidenceList) Has(evidence Evidence) bool {
	for _, ev := range evl {
		if bytes.Equal(evidence.Hash(), ev.Hash()) {
			return true
		}
	}
	return false
}

//------------------------------------------ PROTO --------------------------------------

// EvidenceToProto is a generalized function for encoding evidence that conforms to the
// evidence interface to protobuf
func EvidenceToProto(evidence Evidence) (*tmproto.Evidence, error) {
	if evidence == nil {
		return nil, errors.New("nil evidence")
	}

	switch evi := evidence.(type) {
	case *DuplicateVoteEvidence:
		pbev := evi.ToProto()
		return &tmproto.Evidence{
			Sum: &tmproto.Evidence_DuplicateVoteEvidence{
				DuplicateVoteEvidence: pbev,
			},
		}, nil

	default:
		return nil, fmt.Errorf("toproto: evidence is not recognized: %T", evi)
	}
}

// EvidenceFromProto is a generalized function for decoding protobuf into the
// evidence interface
func EvidenceFromProto(evidence *tmproto.Evidence) (Evidence, error) {
	if evidence == nil {
		return nil, errors.New("nil evidence")
	}

	switch evi := evidence.Sum.(type) {
	case *tmproto.Evidence_DuplicateVoteEvidence:
		return DuplicateVoteEvidenceFromProto(evi.DuplicateVoteEvidence)
	default:
		return nil, errors.New("evidence is not recognized")
	}
}

func init() {
	tmjson.RegisterType(&DuplicateVoteEvidence{}, "tendermint/DuplicateVoteEvidence")
}

//-------------------------------------------- ERRORS --------------------------------------

// ErrInvalidEvidence wraps a piece of evidence and the error denoting how or why it is invalid.
type ErrInvalidEvidence struct {
	Evidence Evidence
	Reason   error
}

// NewErrInvalidEvidence returns a new EvidenceInvalid with the given err.
func NewErrInvalidEvidence(ev Evidence, err error) *ErrInvalidEvidence {
	return &ErrInvalidEvidence{ev, err}
}

// Error returns a string representation of the error.
func (err *ErrInvalidEvidence) Error() string {
	return fmt.Sprintf("Invalid evidence: %v. Evidence: %v", err.Reason, err.Evidence)
}

// ErrEvidenceOverflow is for when there the amount of evidence exceeds the max bytes.
type ErrEvidenceOverflow struct {
	Max int64
	Got int64
}

// NewErrEvidenceOverflow returns a new ErrEvidenceOverflow where got > max.
func NewErrEvidenceOverflow(max, got int64) *ErrEvidenceOverflow {
	return &ErrEvidenceOverflow{max, got}
}

// Error returns a string representation of the error.
func (err *ErrEvidenceOverflow) Error() string {
	return fmt.Sprintf("Too much evidence: Max %d, got %d", err.Max, err.Got)
}

//-------------------------------------------- MOCKING --------------------------------------

// unstable - use only for testing

// NewMockDuplicateVoteEvidence assumes the round to be 0 and the validator index to be 0
func NewMockDuplicateVoteEvidence(
	height int64,
	time time.Time,
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
) (*DuplicateVoteEvidence, error) {
	val := NewMockPVForQuorum(quorumHash)
	return NewMockDuplicateVoteEvidenceWithValidator(height, time, val, chainID, quorumType, quorumHash)
}

// NewMockDuplicateVoteEvidenceWithValidator assumes voting power to be DefaultDashVotingPower and
// validator to be the only one in the set
// TODO: discuss if this might be moved to some *_test.go file
func NewMockDuplicateVoteEvidenceWithValidator(
	height int64,
	time time.Time,
	pv PrivValidator,
	chainID string,
	quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash,
) (*DuplicateVoteEvidence, error) {
	pubKey, err := pv.GetPubKey(context.Background(), quorumHash)
	if err != nil {
		panic(err)
	}

	stateID := RandStateID().WithHeight(height - 1)

	proTxHash, _ := pv.GetProTxHash(context.Background())
	val := NewValidator(pubKey, DefaultDashVotingPower, proTxHash, "")

	voteA := makeMockVote(height, 0, 0, proTxHash, randBlockID())
	vA := voteA.ToProto()
	_ = pv.SignVote(context.Background(), chainID, quorumType, quorumHash, vA, stateID, nil)
	voteA.BlockSignature = vA.BlockSignature
	voteA.StateSignature = vA.StateSignature
	voteB := makeMockVote(height, 0, 0, proTxHash, randBlockID())
	vB := voteB.ToProto()
	_ = pv.SignVote(context.Background(), chainID, quorumType, quorumHash, vB, stateID, nil)
	voteB.BlockSignature = vB.BlockSignature
	voteB.StateSignature = vB.StateSignature
	return NewDuplicateVoteEvidence(
		voteA,
		voteB,
		time,
		NewValidatorSet([]*Validator{val}, val.PubKey, quorumType, quorumHash, true),
	)
}

// assumes voting power to be DefaultDashVotingPower and validator to be the only one in the set
func NewMockDuplicateVoteEvidenceWithPrivValInValidatorSet(height int64, time time.Time,
	pv PrivValidator, valSet *ValidatorSet, chainID string, quorumType btcjson.LLMQType,
	quorumHash crypto.QuorumHash) (*DuplicateVoteEvidence, error) {
	proTxHash, _ := pv.GetProTxHash(context.Background())

	stateID := RandStateID().WithHeight(height - 1)

	voteA := makeMockVote(height, 0, 0, proTxHash, randBlockID())
	vA := voteA.ToProto()
	_ = pv.SignVote(context.Background(), chainID, quorumType, quorumHash, vA, stateID, nil)
	voteA.BlockSignature = vA.BlockSignature
	voteA.StateSignature = vA.StateSignature
	voteB := makeMockVote(height, 0, 0, proTxHash, randBlockID())
	vB := voteB.ToProto()
	_ = pv.SignVote(context.Background(), chainID, quorumType, quorumHash, vB, stateID, nil)
	voteB.BlockSignature = vB.BlockSignature
	voteB.StateSignature = vB.StateSignature
	return NewDuplicateVoteEvidence(voteA, voteB, time, valSet)
}

func makeMockVote(height int64, round, index int32, proTxHash crypto.ProTxHash,
	blockID BlockID) *Vote {
	return &Vote{
		Type:               tmproto.SignedMsgType(2),
		Height:             height,
		Round:              round,
		BlockID:            blockID,
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     index,
	}
}

func randBlockID() BlockID {
	return BlockID{
		Hash: tmrand.Bytes(tmhash.Size),
		PartSetHeader: PartSetHeader{
			Total: 1,
			Hash:  tmrand.Bytes(tmhash.Size),
		},
	}
}
