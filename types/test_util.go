package types

import (
	"fmt"

	"github.com/tendermint/tendermint/crypto/tmhash"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	tmversion "github.com/tendermint/tendermint/proto/tendermint/version"
	"github.com/tendermint/tendermint/version"
)

func MakeCommit(blockID BlockID, stateID StateID, height int64, round int32,
	voteSet *VoteSet, validators []PrivValidator) (*Commit, error) {

	// all sign
	for i := 0; i < len(validators); i++ {
		proTxHash, err := validators[i].GetProTxHash()
		if err != nil {
			return nil, fmt.Errorf("can't get proTxHash: %w", err)
		}
		vote := &Vote{
			ValidatorProTxHash: proTxHash,
			ValidatorIndex:     int32(i),
			Height:             height,
			Round:              round,
			Type:               tmproto.PrecommitType,
			BlockID:            blockID,
		}

		_, err = signAddVote(validators[i], vote, voteSet)
		if err != nil {
			return nil, err
		}
	}

	return voteSet.MakeCommit(), nil
}

// signAddVote signs a vote using StateID configured inside voteSet, and adds it to that voteSet
func signAddVote(privVal PrivValidator, vote *Vote, voteSet *VoteSet) (signed bool, err error) {
	return signAddVoteForStateID(privVal, vote, voteSet, voteSet.stateID)
}

// signAddVoteForStateID signs a vote using specific StateID and adds it to voteSet
func signAddVoteForStateID(privVal PrivValidator, vote *Vote, voteSet *VoteSet,
	stateID StateID) (signed bool, err error) {
	v := vote.ToProto()

	err = privVal.SignVote(voteSet.ChainID(), voteSet.valSet.QuorumType, voteSet.valSet.QuorumHash,
		v, stateID, nil)
	if err != nil {
		return false, err
	}
	vote.BlockSignature = v.BlockSignature
	vote.StateSignature = v.StateSignature
	return voteSet.AddVote(vote)
}

func MakeVote(
	height int64,
	blockID BlockID,
	stateID StateID,
	valSet *ValidatorSet,
	privVal PrivValidator,
	chainID string,
) (*Vote, error) {
	if privVal == nil {
		return nil, fmt.Errorf("privVal must be set")
	}
	proTxHash, err := privVal.GetProTxHash()
	if err != nil {
		return nil, fmt.Errorf("can't get proTxHash: %w", err)
	}
	idx, _ := valSet.GetByProTxHash(proTxHash)
	vote := &Vote{
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     idx,
		Height:             height,
		Round:              0,
		Type:               tmproto.PrecommitType,
		BlockID:            blockID,
	}
	v := vote.ToProto()

	if err := privVal.SignVote(chainID, valSet.QuorumType, valSet.QuorumHash, v, stateID, nil); err != nil {
		return nil, err
	}

	vote.BlockSignature = v.BlockSignature
	vote.StateSignature = v.StateSignature

	return vote, nil
}

// MakeBlock returns a new block with an empty header, except what can be
// computed from itself.
// It populates the same set of fields validated by ValidateBasic.
func MakeBlock(
	height int64,
	coreChainLockedHeight uint32,
	coreChainLock *CoreChainLock,
	txs []Tx,
	lastCommit *Commit,
	evidence []Evidence,
	proposedAppVersion uint64,
) *Block {
	block := &Block{
		Header: Header{
			Version:               tmversion.Consensus{Block: version.BlockProtocol, App: 0},
			Height:                height,
			CoreChainLockedHeight: coreChainLockedHeight,
			ProposedAppVersion:    proposedAppVersion,
		},
		CoreChainLock: coreChainLock,
		Data: Data{
			Txs: txs,
		},
		Evidence:   EvidenceData{Evidence: evidence},
		LastCommit: lastCommit,
	}
	block.fillHeader()
	return block
}

func RandStateID() StateID {
	return StateID{
		Height:      tmrand.Int63(),
		LastAppHash: tmrand.Bytes(tmhash.Size),
	}
}
