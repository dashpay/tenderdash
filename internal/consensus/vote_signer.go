package consensus

import (
	"context"
	"time"

	sm "github.com/dashpay/tenderdash/internal/state"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// voteSigner provides the ability to sign and add a vote
type voteSigner struct {
	privValidator privValidator
	logger        log.Logger
	queueSender   queueSender
	wal           WALWriteFlusher
	voteExtender  sm.VoteExtender
}

// signAddVote signs a vote and sends it to internalMsgQueue
// signing a vote is possible only if a validator is a part of validator-set
func (s *voteSigner) signAddVote(
	ctx context.Context,
	stateData *StateData,
	msgType tmproto.SignedMsgType,
	blockID types.BlockID,
) *types.Vote {
	if s.privValidator.IsZero() { // the node does not have a key
		s.logger.Error("private-validator is not set", "error", ErrPrivValidatorNotSet)
		return nil
	}
	// If the node not in the validator set, do nothing.
	if !stateData.Validators.HasProTxHash(s.privValidator.ProTxHash) {
		s.logger.Error("do nothing, node %s is not a part of validator set %+v",
			s.privValidator.ProTxHash.ShortString(), stateData.Validators)
		return nil
	}
	keyVals := []any{"height", stateData.Height, "round", stateData.Round, "quorum_hash", stateData.Validators.QuorumHash}
	// TODO: pass pubKey to signVote
	start := time.Now()
	vote, err := s.signVote(ctx, stateData, msgType, blockID)
	if err != nil {
		s.logger.Error("failed signing vote", append(keyVals, "error", err)...)
		return nil
	}
	err = s.queueSender.send(ctx, &VoteMessage{vote}, "")
	if err != nil {
		keyVals = append(keyVals, "error", err)
	}
	keyVals = append(keyVals, "vote", vote, "took", time.Since(start).String())
	s.logger.Info("signed and pushed vote", keyVals...)
	return vote
}

func (s *voteSigner) signVote(
	ctx context.Context,
	stateData *StateData,
	msgType tmproto.SignedMsgType,
	blockID types.BlockID,
) (*types.Vote, error) {
	// Flush the WAL. Otherwise, we may not recompute the same vote to sign,
	// and the privValidator will refuse to sign anything.
	if err := s.wal.FlushAndSync(); err != nil {
		return nil, err
	}
	if s.privValidator.IsZero() {
		return nil, ErrPrivValidatorNotSet
	}
	proTxHash := s.privValidator.ProTxHash
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
		s.voteExtender.ExtendVote(ctx, vote)
	}

	protoVote := vote.ToProto()

	ctxto, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err := s.privValidator.SignVote(ctxto,
		stateData.state.ChainID,
		stateData.state.Validators.QuorumType,
		stateData.state.Validators.QuorumHash,
		protoVote,
		s.logger,
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

func (s *voteSigner) Subscribe(emitter *eventemitter.EventEmitter) {
	emitter.AddListener(setPrivValidatorEventName, func(obj eventemitter.EventData) error {
		s.privValidator = obj.(privValidator)
		return nil
	})
}
