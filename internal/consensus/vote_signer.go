package consensus

import (
	"context"
	"time"

	sm "github.com/tendermint/tendermint/internal/state"
	"github.com/tendermint/tendermint/libs/log"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

type VoteSigner struct {
	privValidator privValidator
	logger        log.Logger
	msgInfoQueue  *msgInfoQueue
	wal           WALWriteFlusher
	blockExec     *sm.BlockExecutor
}

// sign the vote and publish on internalMsgQueue
func (cs *VoteSigner) signAddVote(
	ctx context.Context,
	appState *AppState,
	msgType tmproto.SignedMsgType,
	blockID types.BlockID,
) *types.Vote {
	if cs.privValidator.IsZero() { // the node does not have a key
		cs.logger.Error("signAddVote", "err", ErrPrivValidatorNotSet)
		return nil
	}

	// If the node not in the validator set, do nothing.
	if !appState.Validators.HasProTxHash(cs.privValidator.ProTxHash) {
		cs.logger.Debug("do nothing, node is not a part of validator set")
		return nil
	}

	// TODO: pass pubKey to signVote
	start := time.Now()
	vote, err := cs.signVote(ctx, appState, msgType, blockID)
	if err != nil {
		cs.logger.Error("failed signing vote", "height", appState.Height, "round", appState.Round, "vote", vote, "err", err)
		return nil
	}
	_ = cs.msgInfoQueue.send(ctx, &VoteMessage{vote}, "")
	cs.logger.Debug("signed and pushed vote",
		"height", appState.Height,
		"round", appState.Round,
		"vote", vote,
		"quorum_hash", appState.Validators.QuorumHash,
		"took", time.Since(start).String())
	return vote
}

// CONTRACT: cs.privValidator is not nil.
// FIXME: Looks like it is used only in tests, remove and refactor the test.
func (cs *VoteSigner) signVote(
	ctx context.Context,
	appState *AppState,
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
	valIdx, _ := appState.Validators.GetByProTxHash(proTxHash)

	// Since the block has already been validated the block.lastAppHash must be the state.AppHash
	vote := &types.Vote{
		ValidatorProTxHash: proTxHash,
		ValidatorIndex:     valIdx,
		Height:             appState.Height,
		Round:              appState.Round,
		Type:               msgType,
		BlockID:            blockID,
	}

	// If the signedMessageType is for precommit,
	// use our local precommit Timeout as the max wait time for getting a singed commit. The same goes for prevote.
	timeout := time.Second
	if msgType == tmproto.PrecommitType && !vote.BlockID.IsNil() {
		timeout = appState.voteTimeout(appState.Round)
		// if the signedMessage type is for a precommit, add VoteExtension
		exts, err := cs.blockExec.ExtendVote(ctx, vote)
		if err != nil {
			return nil, err
		}
		vote.VoteExtensions = types.NewVoteExtensionsFromABCIExtended(exts)
	}

	v := vote.ToProto()

	ctxto, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err := cs.privValidator.SignVote(ctxto, appState.state.ChainID, appState.state.Validators.QuorumType, appState.state.Validators.QuorumHash,
		v, cs.logger)
	if err != nil {
		return nil, err
	}
	err = vote.PopulateSignsFromProto(v)
	if err != nil {
		return nil, err
	}

	return vote, nil
}
