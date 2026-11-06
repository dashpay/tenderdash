package consensus

import (
	"context"
	"errors"
	"fmt"
	"time"

	sm "github.com/dashpay/tenderdash/internal/state"
	tmbytes "github.com/dashpay/tenderdash/libs/bytes"
	"github.com/dashpay/tenderdash/libs/eventemitter"
	"github.com/dashpay/tenderdash/libs/log"
	tmproto "github.com/dashpay/tenderdash/proto/tendermint/types"
	"github.com/dashpay/tenderdash/types"
)

// blockRandomnessApplier applies a block's random beacon through the application; see
// sm.Executor.ApplyBlockRandomness.
type blockRandomnessApplier interface {
	ApplyBlockRandomness(ctx context.Context, block *types.Block, round int32, beacon []byte) (tmbytes.HexBytes, error)
}

// precommitStateKey identifies the block and round a precommit's final state ID is computed for.
type precommitStateKey struct {
	round int32
	hash  string
}

// voteSigner provides the ability to sign and add a vote
type voteSigner struct {
	privValidator privValidator
	logger        log.Logger
	queueSender   queueSender
	wal           WALWriteFlusher
	voteExtender  sm.VoteExtender
	// randomness applies the round's random beacon before a precommit is signed (consensus version 2)
	randomness blockRandomnessApplier

	// finalStateIDs caches, for finalStateHeight, the state ID each non-nil precommit was signed over
	// (consensus version 2), so this node never signs two state IDs for the same block and round:
	// that is duplicate-vote evidence. Only the consensus goroutine signs votes, so it needs no lock.
	finalStateHeight int64
	finalStateIDs    map[precommitStateKey]tmbytes.HexBytes
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
		s.logger.Error("do nothing, node is not a part of validator set",
			"protxhash", s.privValidator.ProTxHash.ShortString(),
			"validators", stateData.Validators)
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
		// From consensus version 2 the precommit signs the state with the block's randomness applied.
		// The application applies it before ExtendVote, so the extensions see that state.
		if stateData.state.ConsensusParams.Version.RandomBeacon() {
			stateID, err := s.finalStateID(ctx, stateData, vote.BlockID)
			if err != nil {
				return nil, err
			}
			vote.BlockID.StateID = stateID
		}
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

// finalStateID returns the state ID a precommit for blockID signs at consensus version 2: the hash of the
// block header's StateID with the app hash the application returns from ApplyBlockRandomness, given the
// random beacon recovered from this round's prevotes for the block. blockID is the prevote majority's,
// with the header's state ID.
//
// The result is cached per block and round, and a cached value is returned without asking the
// application again. Like an ExtendVote failure, an ApplyBlockRandomness failure panics: the
// application cannot go on.
func (s *voteSigner) finalStateID(ctx context.Context, stateData *StateData, blockID types.BlockID) (tmbytes.HexBytes, error) {
	height, round := stateData.Height, stateData.Round
	if s.finalStateHeight != height || s.finalStateIDs == nil {
		s.finalStateHeight = height
		s.finalStateIDs = make(map[precommitStateKey]tmbytes.HexBytes)
	}
	key := precommitStateKey{round: round, hash: string(blockID.Hash)}
	if stateID, ok := s.finalStateIDs[key]; ok {
		return stateID.Copy(), nil
	}

	block := stateData.ProposalBlock
	if !block.HashesTo(blockID.Hash) {
		return nil, fmt.Errorf("cannot apply the randomness of block %X: it is not the proposal block", blockID.Hash)
	}
	prevotes := stateData.Votes.Prevotes(round)
	polka, ok := prevotes.TwoThirdsMajority()
	if !ok || !polka.SameBlock(blockID) {
		return nil, fmt.Errorf("no prevote majority for block %X in round %d", blockID.Hash, round)
	}
	beacon := prevotes.ThresholdBlockSignature()
	if len(beacon) == 0 {
		return nil, errors.New("random beacon of the prevote majority not recovered")
	}
	if s.randomness == nil {
		return nil, errors.New("no application to apply the block's randomness")
	}
	appHash, err := s.randomness.ApplyBlockRandomness(ctx, block, round, beacon)
	if err != nil {
		panic(fmt.Errorf("ApplyBlockRandomness call failed: %w", err))
	}
	stateID := block.StateIDWithAppHash(appHash)
	s.finalStateIDs[key] = stateID.Hash()
	s.logger.Debug("applied block randomness", "height", height, "round", round,
		"block_app_hash", block.AppHash, "app_hash", appHash)
	return s.finalStateIDs[key].Copy(), nil
}

func (s *voteSigner) Subscribe(emitter *eventemitter.EventEmitter) {
	emitter.AddListener(setPrivValidatorEventName, func(obj eventemitter.EventData) error {
		s.privValidator = obj.(privValidator)
		return nil
	})
}
