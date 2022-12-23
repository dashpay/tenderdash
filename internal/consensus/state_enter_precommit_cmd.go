package consensus

import (
	"context"
	"fmt"

	cstypes "github.com/tendermint/tendermint/internal/consensus/types"
	"github.com/tendermint/tendermint/libs/log"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/types"
)

type EnterPrecommitEvent struct {
	Height int64
	Round  int32
}

// EnterPrecommitCommand ...
// Enter: `timeoutPrevote` after any +2/3 prevotes.
// Enter: `timeoutPrecommit` after any +2/3 precommits.
// Enter: +2/3 precomits for block or nil.
// Lock & precommit the ProposalBlock if we have enough prevotes for it (a POL in this round)
// else, precommit nil otherwise.
type EnterPrecommitCommand struct {
	logger         log.Logger
	eventPublisher *EventPublisher
	blockExec      *blockExecutor
	voteSigner     *VoteSigner
}

// Execute ...
// Enter: `timeoutPrevote` after any +2/3 prevotes.
// Enter: `timeoutPrecommit` after any +2/3 precommits.
// Enter: +2/3 precomits for block or nil.
// Lock & precommit the ProposalBlock if we have enough prevotes for it (a POL in this round)
// else, precommit nil otherwise.
func (cs *EnterPrecommitCommand) Execute(ctx context.Context, behavior *Behavior, stateEvent StateEvent) (any, error) {
	event := stateEvent.Data.(EnterPrecommitEvent)
	appState := stateEvent.AppState
	height := event.Height
	round := event.Round
	logger := cs.logger.With("new_height", height, "new_round", round)

	if appState.Height != height || round < appState.Round || (appState.Round == round && cstypes.RoundStepPrecommit <= appState.Step) {
		logger.Debug("entering precommit step with invalid args",
			"height", appState.Height,
			"round", appState.Round,
			"step", appState.Step)
		return nil, nil
	}

	logger.Debug("entering precommit step",
		"height", appState.Height,
		"round", appState.Round,
		"step", appState.Step)

	defer func() {
		// Done enterPrecommit:
		appState.updateRoundStep(round, cstypes.RoundStepPrecommit)
		behavior.newStep(appState.RoundState)
		// TODO PERSIST AppState
	}()

	// check for a polka
	blockID, ok := appState.Votes.Prevotes(round).TwoThirdsMajority()

	// If we don't have a polka, we must precommit nil.
	if !ok {
		if appState.LockedBlock != nil {
			logger.Debug("precommit step; no +2/3 prevotes during enterPrecommit while we are locked; precommitting nil")
		} else {
			logger.Debug("precommit step; no +2/3 prevotes during enterPrecommit; precommitting nil")
		}

		cs.voteSigner.signAddVote(ctx, appState, tmproto.PrecommitType, types.BlockID{})
		return nil, nil
	}

	// At this point +2/3 prevoted for a particular block or nil.
	cs.eventPublisher.PublishPolkaEvent(appState.RoundState)

	// the latest POLRound should be this round.
	polRound, _ := appState.Votes.POLInfo()
	if polRound < round {
		panic(fmt.Sprintf("this POLRound should be %v but got %v", round, polRound))
	}

	// +2/3 prevoted nil. Precommit nil.
	if blockID.IsNil() {
		logger.Debug("precommit step: +2/3 prevoted for nil; precommitting nil")
		cs.voteSigner.signAddVote(ctx, appState, tmproto.PrecommitType, types.BlockID{})
		return nil, nil
	}
	// At this point, +2/3 prevoted for a particular block.

	// If we never received a proposal for this block, we must precommit nil
	if appState.Proposal == nil || appState.ProposalBlock == nil {
		logger.Debug("precommit step; did not receive proposal, precommitting nil")
		cs.voteSigner.signAddVote(ctx, appState, tmproto.PrecommitType, types.BlockID{})
		return nil, nil
	}

	// If the proposal time does not match the block time, precommit nil.
	if !appState.Proposal.Timestamp.Equal(appState.ProposalBlock.Header.Time) {
		logger.Debug("precommit step: proposal timestamp not equal; precommitting nil")
		cs.voteSigner.signAddVote(ctx, appState, tmproto.PrecommitType, types.BlockID{})
		return nil, nil
	}

	// If we're already locked on that block, precommit it, and update the LockedRound
	if appState.LockedBlock.HashesTo(blockID.Hash) {
		logger.Debug("precommit step: +2/3 prevoted locked block; relocking")
		appState.LockedRound = round

		cs.eventPublisher.PublishRelockEvent(appState.RoundState)
		cs.voteSigner.signAddVote(ctx, appState, tmproto.PrecommitType, blockID)
		return nil, nil
	}

	// If greater than 2/3 of the voting power on the network prevoted for
	// the proposed block, update our locked block to this block and issue a
	// precommit vote for it.
	if appState.ProposalBlock.HashesTo(blockID.Hash) {
		logger.Debug("precommit step: +2/3 prevoted proposal block; locking", "hash", blockID.Hash)

		// we got precommit but we didn't process proposal yet
		cs.blockExec.processOrPanic(ctx, appState, round)

		// Validate the block.
		cs.blockExec.validateOrPanic(ctx, appState)

		appState.LockedRound = round
		appState.LockedBlock = appState.ProposalBlock
		appState.LockedBlockParts = appState.ProposalBlockParts

		cs.eventPublisher.PublishLockEvent(appState.RoundState)
		cs.voteSigner.signAddVote(ctx, appState, tmproto.PrecommitType, blockID)
		return nil, nil
	}

	// There was a polka in this round for a block we don't have.
	// Fetch that block, and precommit nil.
	logger.Debug("precommit step: +2/3 prevotes for a block we do not have; voting nil", "block_id", blockID)

	if !appState.ProposalBlockParts.HasHeader(blockID.PartSetHeader) {
		appState.ProposalBlock = nil
		appState.metrics.MarkBlockGossipStarted()
		appState.ProposalBlockParts = types.NewPartSetFromHeader(blockID.PartSetHeader)
	}

	cs.voteSigner.signAddVote(ctx, appState, tmproto.PrecommitType, types.BlockID{})
	return nil, nil
}
