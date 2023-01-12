package consensus

import (
	"context"
	"time"

	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/log"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

// VoteSigner provides the ability to sign and add a vote
type VoteSigner struct {
	privValidator privValidator
	logger        log.Logger
	queueSender   queueSender
	wal           WALWriteFlusher
	voteExtender  sm.VoteExtender
}

// signAddVote signs a vote and sends it to internalMsgQueue
// signing a vote is possible only if a validator is a part of validator-set
func (cs *VoteSigner) signAddVote(
	ctx context.Context,
	stateData *StateData,
	msgType tmproto.SignedMsgType,
	blockID types.BlockID,
) *types.Vote {
	if cs.privValidator.IsZero() { // the node does not have a key
		cs.logger.Error("private-validator is not set", "error", ErrPrivValidatorNotSet)
		return nil
	}
	// If the node not in the validator set, do nothing.
	if !stateData.Validators.HasProTxHash(cs.privValidator.ProTxHash) {
		cs.logger.Debug("do nothing, node is not a part of validator set")
		return nil
	}
	keyVals := []any{"height", stateData.Height, "round", stateData.Round, "quorum_hash", stateData.Validators.QuorumHash}
	// TODO: pass pubKey to signVote
	start := time.Now()
	vote, err := cs.signVote(ctx, stateData, msgType, blockID)
	if err != nil {
		cs.logger.Error("failed signing vote", append(keyVals, "error", err)...)
		return nil
	}
	err = cs.queueSender.send(ctx, &VoteMessage{vote}, "")
	if err != nil {
		keyVals = append(keyVals, "error", err)
	}
	keyVals = append(keyVals, "vote", vote, "took", time.Since(start).String())
	cs.logger.Debug("signed and pushed vote", keyVals...)
	return vote
}

func (cs *VoteSigner) signVote(
	ctx context.Context,
	stateData *StateData,
	msgType tmproto.SignedMsgType,
	blockID types.BlockID,
) (*types.Vote, error) {
	// Flush the WAL. Otherwise, we may not recompute the same vote to sign,
	// and the privValidator will refuse to sign anything.
	if err := cs.wal.FlushAndSync(); err != nil {
		return nil, err
	}
	if cs.privValidator.IsZero() {
		return nil, ErrPrivValidatorNotSet
	}
	proTxHash := cs.privValidator.ProTxHash
	valIdx, _ := stateData.Validators.GetByProTxHash(proTxHash)
	// Since the block has already been validated the block.lastAppHash must be the state.AppHash
	vote := &types.Vote{
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     valIdx,
		Height:             stateData.Height,
		Round:              stateData.Round,
		Type:               msgType,
		BlockID:            blockID,
	}
	// If the signedMessageType is for precommit,
	// use our local precommit Timeout as the max wait time for getting a singed commit. The same goes for prevote.
	timeout := time.Second
	if msgType == tmproto.PrecommitType && !vote.BlockID.IsNil() {
		timeout = stateData.voteTimeout(stateData.Round)
		// if the signedMessage type is for a precommit, add VoteExtension
		cs.voteExtender.ExtendVote(ctx, vote)
	}

	protoVote := vote.ToProto()

	ctxto, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err := cs.privValidator.SignVote(ctxto,
		stateData.state.ChainID,
		stateData.state.Validators.QuorumType,
		stateData.state.Validators.QuorumHash,
		protoVote,
		cs.logger,
	)
	if err != nil {
		return nil, err
	}
	err = vote.PopulateSignsFromProto(protoVote)
	if err != nil {
		return nil, err
	}
	return vote, nil
}
